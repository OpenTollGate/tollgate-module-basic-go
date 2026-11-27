package fips_exit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr/nip19"
)

var testEpoch = time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

// keyFor derives a stable non-zero x-only key from a label, so fixtures are
// reproducible without embedding key material.
func keyFor(label string) PeerKey {
	var k PeerKey
	sum := sha256.Sum256([]byte("fips-exit-admission/" + label))
	copy(k[:], sum[:])
	return k
}

func clientFixtures() (PeerKey, PeerKey, ClientsFile) {
	alice, bob := keyFor("alice"), keyFor("bob")
	return alice, bob, ClientsFile{Entries: []ClientsEntry{
		{Addr: alice.OuterAddress(), Name: "alice"},
		{Addr: bob.OuterAddress(), Name: "bob"},
	}}
}

// --- clients file format -------------------------------------------------

func TestParseClientsFileSkipsAndReportsBadLines(t *testing.T) {
	alice, bob := keyFor("alice"), keyFor("bob")
	aliceAddr, bobAddr := alice.OuterAddress().String(), bob.OuterAddress().String()
	src := strings.Join([]string{
		"# managed by tollgate; do not edit by hand",
		"",
		aliceAddr + " # alice",
		bobAddr,
		"\t " + bobAddr + "   #   bob (duplicate)  ",
		"100.64.0.9 # ipv4 is not a fips identity",
		"fd00::1",
		"2001:db8::1",
		aliceAddr + " trailing garbage",
		"not-an-address",
	}, "\n") + "\n"

	got := ParseClientsFile([]byte(src))
	want := []ClientsEntry{
		{Addr: alice.OuterAddress(), Name: "alice"},
		{Addr: bob.OuterAddress()},
		{Addr: netip.MustParseAddr("fd00::1")},
	}
	if len(got.Entries) != len(want) {
		t.Fatalf("entries = %v, want %v", got.Entries, want)
	}
	for i := range want {
		if got.Entries[i] != want[i] {
			t.Fatalf("entry %d = %+v, want %+v", i, got.Entries[i], want[i])
		}
	}
	// Invalid lines are reported, never fatal, and never silently dropped.
	if len(got.Invalid) != 5 {
		t.Fatalf("invalid = %+v, want 5 reports", got.Invalid)
	}
	if got.Invalid[0].Line != 5 || !strings.Contains(got.Invalid[0].Text, bobAddr) {
		t.Fatalf("first report = %+v, want line 5 with the raw text", got.Invalid[0])
	}
	if !strings.Contains(got.Invalid[0].Reason, "duplicate") {
		t.Fatalf("duplicate reason = %q", got.Invalid[0].Reason)
	}
	if got.Invalid[1].Line != 6 || got.Invalid[3].Line != 9 || got.Invalid[4].Line != 10 {
		t.Fatalf("report lines = %+v, want 6, 9, 10", got.Invalid)
	}
	for _, r := range got.Invalid {
		if r.Reason == "" {
			t.Fatalf("report %+v has no reason", r)
		}
	}
}

func TestParseClientsFileHandlesCRLFAndMissingFinalNewline(t *testing.T) {
	alice, bob := keyFor("alice"), keyFor("bob")
	src := []byte(alice.OuterAddress().String() + " # alice\r\n" + bob.OuterAddress().String())
	got := ParseClientsFile(src)
	if len(got.Entries) != 2 || len(got.Invalid) != 0 {
		t.Fatalf("entries = %+v invalid = %+v", got.Entries, got.Invalid)
	}
	if got.Entries[0].Name != "alice" {
		t.Fatalf("crlf name = %q", got.Entries[0].Name)
	}
	// Rendering canonicalises line endings and always ends with a newline.
	rendered, err := RenderClientsFile(got)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := alice.OuterAddress().String() + " # alice\n" + bob.OuterAddress().String() + "\n"
	if string(rendered) != want {
		t.Fatalf("rendered %q, want %q", rendered, want)
	}
}

func TestRenderClientsFileIsByteExactAndRefusesUnsafeEntries(t *testing.T) {
	alice := keyFor("alice")
	empty, err := RenderClientsFile(ClientsFile{})
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty render = %q, %v", empty, err)
	}
	cases := map[string]ClientsEntry{
		"newline in name":   {Addr: alice.OuterAddress(), Name: "two\nlines"},
		"comment character": {Addr: alice.OuterAddress(), Name: "a # b"},
		"carriage return":   {Addr: alice.OuterAddress(), Name: "a\rb"},
		"ipv4 address":      {Addr: netip.MustParseAddr("192.0.2.1")},
		"non-fd ipv6":       {Addr: netip.MustParseAddr("2001:db8::1")},
		"zero address":      {},
	}
	for name, entry := range cases {
		if _, err := RenderClientsFile(ClientsFile{Entries: []ClientsEntry{entry}}); err == nil {
			t.Fatalf("%s: render must be refused", name)
		}
	}
	if _, err := RenderClientsFile(ClientsFile{Entries: []ClientsEntry{
		{Addr: alice.OuterAddress()}, {Addr: alice.OuterAddress()},
	}}); err == nil {
		t.Fatal("duplicate address must be refused")
	}
}

// --- peer keys ----------------------------------------------------------

func TestLongAndControlCharacterNamesAreRefused(t *testing.T) {
	alice := keyFor("alice")
	if _, err := Grant(State{}, GrantRequest{Key: alice, Name: strings.Repeat("n", 65), Mode: BillingTime,
		GrantedAt: testEpoch, ExpiresAt: testEpoch.Add(time.Hour)}); err == nil {
		t.Fatal("a name longer than 64 bytes must be refused")
	}
	// A control character in the comment must be reported, not written back.
	got := ParseClientsFile([]byte(alice.OuterAddress().String() + " # bad\x00name\n"))
	if len(got.Entries) != 0 || len(got.Invalid) != 1 {
		t.Fatalf("entries = %+v invalid = %+v", got.Entries, got.Invalid)
	}
	if !strings.Contains(got.Invalid[0].Reason, "unsafe") {
		t.Fatalf("reason = %q", got.Invalid[0].Reason)
	}
}

func TestDecodeStateRefusesRevisionAboveState(t *testing.T) {
	keyHex := keyFor("alice").Hex()
	bad := `{"revision":1,"entitlements":[{"key":"` + keyHex + `","name":"alice","mode":"time",` +
		`"granted_at":"2026-03-01T12:00:00Z","expires_at":"2026-03-01T13:00:00Z","revision":2}]}`
	if _, err := DecodeState([]byte(bad)); err == nil {
		t.Fatal("an entitlement newer than its state must be refused")
	}
}

func TestParsePeerKeyAcceptsNpubAndHexOnly(t *testing.T) {
	alice := keyFor("alice")
	hexKey := hex.EncodeToString(alice[:])
	npub, err := nip19.EncodePublicKey(hexKey)
	if err != nil {
		t.Fatalf("encode npub: %v", err)
	}
	for _, in := range []string{hexKey, strings.ToUpper(hexKey), " " + npub + " "} {
		got, err := ParsePeerKey(in)
		if err != nil {
			t.Fatalf("ParsePeerKey(%q): %v", in, err)
		}
		if got != alice {
			t.Fatalf("ParsePeerKey(%q) = %x, want %x", in, got, alice)
		}
	}
	nsec, err := nip19.EncodePrivateKey(hexKey)
	if err != nil {
		t.Fatalf("encode nsec: %v", err)
	}
	for _, in := range []string{"", "   ", hexKey[:62], "zz" + hexKey[2:], nsec, npub[:len(npub)-1],
		strings.ToUpper(npub)} {
		if _, err := ParsePeerKey(in); err == nil {
			t.Fatalf("ParsePeerKey(%q) must fail", in)
		}
	}
	var zero PeerKey
	if !zero.IsZero() {
		t.Fatal("IsZero must report the zero key")
	}
	if _, err := ParsePeerKey(strings.Repeat("0", 64)); err == nil {
		t.Fatal("the all-zero key is not an identity and must be refused")
	}
}

// --- grants -------------------------------------------------------------

func TestGrantRefusesUnusableRequests(t *testing.T) {
	base := GrantRequest{
		Key: keyFor("alice"), Name: "alice", Mode: BillingTime,
		GrantedAt: testEpoch, ExpiresAt: testEpoch.Add(24 * time.Hour),
	}
	if _, err := Grant(State{}, base); err != nil {
		t.Fatalf("baseline grant: %v", err)
	}
	cases := map[string]func(*GrantRequest){
		"zero key":        func(r *GrantRequest) { r.Key = PeerKey{} },
		"inverted window": func(r *GrantRequest) { r.ExpiresAt = r.GrantedAt.Add(-time.Second) },
		"zero width":      func(r *GrantRequest) { r.ExpiresAt = r.GrantedAt },
		"missing grant":   func(r *GrantRequest) { r.GrantedAt = time.Time{} },
		"missing expiry":  func(r *GrantRequest) { r.ExpiresAt = time.Time{} },
		"zero budget":     func(r *GrantRequest) { r.Mode = BillingBytes; r.ByteBudget = 0 },
		"budget in time":  func(r *GrantRequest) { r.ByteBudget = 1 },
		"unknown mode":    func(r *GrantRequest) { r.Mode = "moon" },
		"unsafe name":     func(r *GrantRequest) { r.Name = "a\nb" },
	}
	for name, mutate := range cases {
		req := base
		mutate(&req)
		if _, err := Grant(State{}, req); err == nil {
			t.Fatalf("%s: grant must be refused", name)
		}
	}
	// A duplicate key is a renewal decision, never a second grant.
	if _, err := Grant(mustGrant(t, base), base); err == nil {
		t.Fatal("duplicate key must be refused")
	}
}

func mustGrant(t *testing.T, r GrantRequest) State {
	t.Helper()
	s, err := Grant(State{}, r)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	return s
}

func TestStateRevisionsIncreaseMonotonically(t *testing.T) {
	req := func(label string) GrantRequest {
		return GrantRequest{
			Key: keyFor(label), Name: label, Mode: BillingTime,
			GrantedAt: testEpoch, ExpiresAt: testEpoch.Add(time.Hour),
		}
	}
	s := mustGrant(t, req("alice"))
	first := s.Revision
	if first == 0 {
		t.Fatal("revision must start non-zero")
	}
	s, err := Grant(s, req("bob"))
	if err != nil {
		t.Fatalf("second grant: %v", err)
	}
	if s.Revision != first+1 {
		t.Fatalf("revision %d, want %d", s.Revision, first+1)
	}
	for _, e := range s.Entitlements {
		if e.Revision == 0 || e.Revision > s.Revision {
			t.Fatalf("entitlement revision %d out of range 1..%d", e.Revision, s.Revision)
		}
	}
}

func TestGrantRefusesInnerIPv4CollisionBetweenDistinctPeers(t *testing.T) {
	// Derive() keeps only 22 identity bits in the inner IPv4 (10 bits of the
	// 100.64.0.0/10 range plus all 8 bits of the first octet's low bits, and
	// 16 bits that are shared with the outer address), so collisions between
	// distinct peers are reachable by construction: search for a real one
	// instead of asserting a property the address format does not have.
	seen := map[netip.Addr]PeerKey{}
	var a, b PeerKey
	found := false
	for i := 0; i < 1<<21 && !found; i++ {
		sum := sha256.Sum256([]byte{byte(i), byte(i >> 8), byte(i >> 16), 0x7f, 0x11})
		var raw [32]byte
		copy(raw[:], sum[:])
		k := PeerKey(raw)
		_, inner := Derive(raw)
		if prev, ok := seen[inner]; ok && prev != k {
			a, b, found = prev, k, true
			break
		}
		seen[inner] = k
	}
	if !found {
		t.Fatal("failed to find an inner IPv4 collision among synthetic keys")
	}
	req := func(k PeerKey) GrantRequest {
		return GrantRequest{Key: k, Mode: BillingTime, GrantedAt: testEpoch, ExpiresAt: testEpoch.Add(time.Hour)}
	}
	s := mustGrant(t, req(a))
	if _, err := Grant(s, req(b)); err == nil {
		t.Fatal("two distinct peers sharing an inner IPv4 must not both be granted")
	}
}

// --- persistence --------------------------------------------------------

func TestEncodeStateIsDeterministicAndDecodesBack(t *testing.T) {
	req := func(label string) GrantRequest {
		return GrantRequest{
			Key: keyFor(label), Name: label, Mode: BillingTime,
			GrantedAt: testEpoch, ExpiresAt: testEpoch.Add(2 * time.Hour),
		}
	}
	a := mustGrant(t, req("alice"))
	b, err := Grant(a, req("bob"))
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	// Identical content in a different slice order must encode identically.
	reversed := State{Revision: b.Revision, Entitlements: []Entitlement{
		b.Entitlements[1], b.Entitlements[0],
	}}
	x, err := EncodeState(b)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	y, err := EncodeState(reversed)
	if err != nil {
		t.Fatalf("encode reversed: %v", err)
	}
	if !bytes.Equal(x, y) {
		t.Fatalf("encoding depends on slice order:\n%s\n%s", x, y)
	}
	if !json.Valid(x) {
		t.Fatalf("encoded state is not JSON: %q", x)
	}
	decoded, err := DecodeState(x)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	again, err := EncodeState(decoded)
	if err != nil {
		t.Fatalf("re-encode: %v", err)
	}
	if !bytes.Equal(x, again) {
		t.Fatalf("decode/encode is not a fixed point:\n%s\n%s", x, again)
	}
	if e, ok := decoded.Entitlement(keyFor("alice")); !ok || e.Name != "alice" {
		t.Fatalf("decoded state lost alice: %+v ok=%v", e, ok)
	}
	if decoded.Revision != b.Revision {
		t.Fatalf("revision = %d, want %d", decoded.Revision, b.Revision)
	}
}

func TestDecodeStateRefusesCorruptOrAmbiguousState(t *testing.T) {
	good := mustGrant(t, GrantRequest{
		Key: keyFor("alice"), Name: "alice", Mode: BillingBytes, ByteBudget: 1024,
		GrantedAt: testEpoch, ExpiresAt: testEpoch.Add(time.Hour),
	})
	encoded, err := EncodeState(good)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !json.Valid(encoded) {
		t.Fatalf("fixture encode is not JSON: %q", encoded)
	}
	if _, err := DecodeState([]byte("{")); err == nil {
		t.Fatal("truncated JSON must be refused")
	}
	if _, err := DecodeState([]byte(`{"revision":1,"entitlements":[],"extra":true}`)); err == nil {
		t.Fatal("unknown fields must be refused, not ignored")
	}
	aliceKey := keyFor("alice")
	keyHex := hex.EncodeToString(aliceKey[:])
	rec := func(name string, rev int) string {
		return `{"key":"` + keyHex + `","name":"` + name + `","mode":"time",` +
			`"granted_at":"2026-03-01T12:00:00Z","expires_at":"2026-03-01T13:00:00Z","revision":` +
			itoa(rev) + `}`
	}
	// Two records for the same key are ambiguous: refusing beats picking one.
	dup := `{"revision":2,"entitlements":[` + rec("alice", 1) + `,` + rec("alice-again", 2) + `]}`
	if _, err := DecodeState([]byte(dup)); err == nil {
		t.Fatal("duplicate entitlement keys must be refused")
	}
	// An entitlement that could never have been granted must not load either.
	bad := `{"revision":1,"entitlements":[{"key":"` + keyHex + `","name":"alice","mode":"bytes",` +
		`"granted_at":"2026-03-01T12:00:00Z","expires_at":"2026-03-01T13:00:00Z","revision":1}]}`
	if _, err := DecodeState([]byte(bad)); err == nil {
		t.Fatal("a byte-billed entitlement with no budget must be refused")
	}
	// Nor may persisted state smuggle in a non-fd address.
	badAddr := `{"revision":1,"entitlements":[],"unmanaged":[{"addr":"2001:db8::1"}]}`
	if _, err := DecodeState([]byte(badAddr)); err == nil {
		t.Fatal("a non-fips unmanaged address must be refused")
	}
}

func TestSaveStateUsesTheInjectedAtomicStore(t *testing.T) {
	st := memStore{}
	a := mustGrant(t, GrantRequest{
		Key: keyFor("alice"), Name: "alice", Mode: BillingBytes, ByteBudget: 4096,
		GrantedAt: testEpoch, ExpiresAt: testEpoch.Add(time.Hour),
	})
	if err := SaveState(&st, a); err != nil {
		t.Fatalf("save: %v", err)
	}
	if st.writes != 1 {
		t.Fatalf("writes = %d, want exactly one atomic write per save", st.writes)
	}
	if !json.Valid(st.data) {
		t.Fatalf("stored payload is not JSON: %q", st.data)
	}
	loaded, err := LoadState(&st)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Revision != a.Revision {
		t.Fatalf("loaded revision %d, want %d", loaded.Revision, a.Revision)
	}
	failing := memStore{failWrite: errors.New("disk full")}
	if err := SaveState(&failing, a); err == nil {
		t.Fatal("write failure must be returned")
	}
}

func TestLoadStateWithoutPersistedFileGrantsNothing(t *testing.T) {
	st, err := LoadState(&memStore{})
	if err != nil {
		t.Fatalf("absent state must not be an error: %v", err)
	}
	if st.Revision != 0 || len(st.Entitlements) != 0 {
		t.Fatalf("absent state = %+v, want empty", st)
	}
	if v := VerdictFor(st, testEpoch, keyFor("alice"), 0); v != Renew {
		t.Fatalf("absent state verdict = %q, want renew", v)
	}
	if _, err := LoadState(&memStore{data: []byte("nope")}); err == nil {
		t.Fatal("corrupt persisted state must be reported, not ignored")
	}
	if _, err := LoadState(&memStore{failRead: errors.New("eio")}); err == nil {
		t.Fatal("read failure must be reported")
	}
}

// --- decisions ----------------------------------------------------------

func TestVerdictForIsPureAndExpiryAware(t *testing.T) {
	alice := keyFor("alice")
	st := mustGrant(t, GrantRequest{Key: alice, Mode: BillingTime,
		GrantedAt: testEpoch, ExpiresAt: testEpoch.Add(time.Hour)})
	if v := VerdictFor(st, testEpoch.Add(59*time.Minute), alice, 0); v != Keep {
		t.Fatalf("verdict = %q, want keep", v)
	}
	// Expiry is inclusive: at the expiry instant the grant is gone.
	if v := VerdictFor(st, testEpoch.Add(time.Hour), alice, 0); v != RevokeExpired {
		t.Fatalf("verdict at the expiry instant = %q, want revoke-expired", v)
	}
	if v := VerdictFor(st, testEpoch, keyFor("nobody"), 0); v != Renew {
		t.Fatalf("verdict for an unknown key = %q, want renew", v)
	}
	bytesMode := mustGrant(t, GrantRequest{Key: alice, Mode: BillingBytes, ByteBudget: 10,
		GrantedAt: testEpoch, ExpiresAt: testEpoch.Add(time.Hour)})
	if v := VerdictFor(bytesMode, testEpoch, alice, 9); v != Keep {
		t.Fatalf("verdict = %q, want keep", v)
	}
	if v := VerdictFor(bytesMode, testEpoch, alice, 10); v != RevokeExhausted {
		t.Fatalf("verdict = %q, want revoke-exhausted", v)
	}
}

func TestApplyVerdicts(t *testing.T) {
	alice, bob := keyFor("alice"), keyFor("bob")
	carol := keyFor("carol")
	now := testEpoch.Add(time.Hour)
	st := mustGrant(t, GrantRequest{Key: alice, Name: "alice", Mode: BillingTime,
		GrantedAt: testEpoch, ExpiresAt: now.Add(time.Hour)})
	st, err := Grant(st, GrantRequest{Key: bob, Name: "bob", Mode: BillingTime,
		GrantedAt: testEpoch, ExpiresAt: now.Add(-time.Nanosecond)})
	if err != nil {
		t.Fatalf("grant bob: %v", err)
	}
	applied, err := Apply(st, now, []Observation{{Key: alice}, {Key: bob}, {Key: carol}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := map[PeerKey]Verdict{}
	for _, o := range applied.Outcomes {
		if _, dup := got[o.Key]; dup {
			t.Fatalf("duplicate outcome for %x", o.Key)
		}
		got[o.Key] = o.Verdict
	}
	want := map[PeerKey]Verdict{alice: Keep, bob: RevokeExpired, carol: Renew}
	if len(got) != len(want) {
		t.Fatalf("outcomes = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("verdict for %x = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
	if _, ok := applied.State.Entitlement(bob); ok {
		t.Fatal("an expired entitlement must not survive in state")
	}
	rendered := string(applied.Rendered)
	if strings.Contains(rendered, bob.OuterAddress().String()) {
		t.Fatalf("expired peer still present in clients file: %q", rendered)
	}
	if !strings.Contains(rendered, alice.OuterAddress().String()+" # alice\n") {
		t.Fatalf("live peer missing or misrendered in clients file: %q", rendered)
	}
	// Changed is the delta against the file last known to this state, so only
	// alice's line appears (bob's line was never written).
	if len(applied.Changed) != 1 || applied.Changed[0] != alice.OuterAddress() {
		t.Fatalf("changed = %v, want just alice's address", applied.Changed)
	}
}

func TestApplyByteBudgetExhaustionAndRenewal(t *testing.T) {
	dave := keyFor("dave")
	now := testEpoch.Add(time.Minute)
	st := mustGrant(t, GrantRequest{Key: dave, Name: "dave", Mode: BillingBytes, ByteBudget: 1000,
		GrantedAt: testEpoch, ExpiresAt: now.Add(time.Hour)})

	applied, err := Apply(st, now, []Observation{{Key: dave, BytesUsed: 999}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if v := verdictOf(t, applied, dave); v != Keep {
		t.Fatalf("verdict = %q, want keep", v)
	}
	if e, ok := applied.State.Entitlement(dave); !ok || e.BytesUsed != 999 {
		t.Fatalf("bytes_used = %+v, want 999", e)
	}

	applied, err = Apply(applied.State, now, []Observation{{Key: dave, BytesUsed: 1000}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if v := verdictOf(t, applied, dave); v != RevokeExhausted {
		t.Fatalf("verdict = %q, want revoke-exhausted", v)
	}
	if _, ok := applied.State.Entitlement(dave); ok {
		t.Fatal("exhausted entitlement must be dropped")
	}
	if len(applied.Rendered) != 0 {
		t.Fatalf("exhausted peer still in clients file: %q", applied.Rendered)
	}

	// A later pass sees an unentitled peer: the answer is a new sale, not a
	// silent re-admission.
	applied, err = Apply(applied.State, now, []Observation{{Key: dave, BytesUsed: 1000}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if v := verdictOf(t, applied, dave); v != Renew {
		t.Fatalf("verdict after exhaustion = %q, want renew", v)
	}
}

func TestApplyNeverInventsByteCounters(t *testing.T) {
	erin := keyFor("erin")
	now := testEpoch.Add(time.Minute)
	timeMode := mustGrant(t, GrantRequest{Key: erin, Name: "erin", Mode: BillingTime,
		GrantedAt: testEpoch, ExpiresAt: now.Add(time.Hour)})
	applied, err := Apply(timeMode, now, []Observation{{Key: erin, BytesUsed: 5000}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if e, _ := applied.State.Entitlement(erin); e.BytesUsed != 0 {
		t.Fatalf("time-billed peer recorded bytes_used = %d, want 0", e.BytesUsed)
	}

	// Byte mode with no observation at all accrues nothing.
	byteMode := mustGrant(t, GrantRequest{Key: erin, Name: "erin", Mode: BillingBytes, ByteBudget: 10,
		GrantedAt: testEpoch, ExpiresAt: now.Add(time.Hour)})
	applied, err = Apply(byteMode, now, nil)
	if err != nil {
		t.Fatalf("apply with no observations: %v", err)
	}
	if e, _ := applied.State.Entitlement(erin); e.BytesUsed != 0 {
		t.Fatalf("bytes_used = %d without an observation, want 0", e.BytesUsed)
	}
	// The verdict covers the grant on record, not presence: no counter was
	// invented, so the grant is simply still live.
	if v := verdictOf(t, applied, erin); v != Keep {
		t.Fatalf("verdict = %q, want keep", v)
	}

	// Counters are monotonic: a lower reported value never rewinds usage.
	applied2, err := Apply(applied.State, now, []Observation{{Key: erin, BytesUsed: 7}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	applied3, err := Apply(applied2.State, now, []Observation{{Key: erin, BytesUsed: 3}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if e, _ := applied3.State.Entitlement(erin); e.BytesUsed != 7 {
		t.Fatalf("bytes_used rewound to %d, want 7", e.BytesUsed)
	}
	if _, err := Apply(applied3.State, now, []Observation{{Key: erin, Invalid: "counter reset"}}); err == nil {
		t.Fatal("an unusable observation must be refused, not silently accepted")
	}
}

func TestApplyDoesNotChurnWhenNothingChanged(t *testing.T) {
	alice, bob := keyFor("alice"), keyFor("bob")
	now := testEpoch.Add(30 * time.Minute)
	st := State{}
	for _, k := range []PeerKey{alice, bob} {
		var err error
		st, err = Grant(st, GrantRequest{Key: k, Mode: BillingTime,
			GrantedAt: testEpoch, ExpiresAt: now.Add(time.Hour)})
		if err != nil {
			t.Fatalf("grant: %v", err)
		}
	}
	first, err := Apply(st, now, []Observation{{Key: alice}, {Key: bob}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(first.Changed) != 2 {
		t.Fatalf("first apply changed = %v, want the two initial addresses", first.Changed)
	}
	second, err := Apply(first.State, now, []Observation{{Key: alice}, {Key: bob}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(second.Changed) != 0 {
		t.Fatalf("unchanged peers reported as changed: %v", second.Changed)
	}
	if !bytes.Equal(first.Rendered, second.Rendered) {
		t.Fatalf("clients file churned:\n%q\n%q", first.Rendered, second.Rendered)
	}
	if second.State.Revision != first.State.Revision {
		t.Fatalf("revision bumped on a no-op apply: %d -> %d", first.State.Revision, second.State.Revision)
	}
	if err := SaveState(&memStore{}, second.State); err != nil {
		t.Fatalf("save no-op state: %v", err)
	}
}

func TestApplyReportsRenamesWithoutReorderingTheFile(t *testing.T) {
	alice, bob := keyFor("alice"), keyFor("bob")
	now := testEpoch.Add(30 * time.Minute)
	st := State{}
	for _, k := range []struct {
		key  PeerKey
		name string
	}{{alice, "alice"}, {bob, "bob"}} {
		var err error
		st, err = Grant(st, GrantRequest{Key: k.key, Name: k.name, Mode: BillingTime,
			GrantedAt: testEpoch, ExpiresAt: now.Add(time.Hour)})
		if err != nil {
			t.Fatalf("grant: %v", err)
		}
	}
	first, err := Apply(st, now, []Observation{{Key: alice}, {Key: bob}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Rename bob in place on the state produced by that apply.
	renamed := first.State
	found := false
	for i := range renamed.Entitlements {
		if renamed.Entitlements[i].Key == bob {
			renamed.Entitlements[i].Name = "bob-renamed"
			found = true
		}
	}
	if !found {
		t.Fatal("fixture lost bob")
	}
	applied, err := Apply(renamed, now, []Observation{{Key: alice}, {Key: bob}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(applied.Changed) != 1 || applied.Changed[0] != bob.OuterAddress() {
		t.Fatalf("changed = %v, want just bob's address", applied.Changed)
	}
	if !strings.Contains(string(applied.Rendered), "# bob-renamed") {
		t.Fatalf("rename missing from clients file: %q", applied.Rendered)
	}
	// A rename must not reorder already-stable lines.
	if got, want := addrOrder(applied.Rendered), addrOrder(first.Rendered); got != want {
		t.Fatalf("file reordered after a rename: %q, want %q", got, want)
	}
	reparsed := ParseClientsFile(applied.Rendered)
	if len(reparsed.Invalid) != 0 || len(reparsed.Entries) != 2 {
		t.Fatalf("rendered file does not reparse: %+v", reparsed)
	}
	if applied.State.Revision <= first.State.Revision {
		t.Fatal("a content change must bump the state revision")
	}
}

// --- startup reconciliation ---------------------------------------------

func TestReconcilePreservesOperatorLinesAndDropsStaleGrants(t *testing.T) {
	alice, bob := keyFor("alice"), keyFor("bob")
	now := testEpoch.Add(2 * time.Hour)
	manual := keyFor("manual-operator-add")
	st := mustGrant(t, GrantRequest{Key: alice, Name: "alice", Mode: BillingTime,
		GrantedAt: testEpoch, ExpiresAt: now.Add(time.Hour)})
	st, err := Grant(st, GrantRequest{Key: bob, Name: "bob", Mode: BillingTime,
		GrantedAt: testEpoch, ExpiresAt: now.Add(-time.Hour)})
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	file := ClientsFile{Entries: []ClientsEntry{
		{Addr: bob.OuterAddress(), Name: "bob"},
		{Addr: manual.OuterAddress(), Name: "hand-added"},
	}}

	got, out, report, err := Reconcile(st, now, file)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, ok := got.Entitlement(bob); ok {
		t.Fatal("expired grant was resurrected by reconcile")
	}
	if !containsAddr(out.Entries, alice.OuterAddress()) {
		t.Fatal("live grant missing after reconcile")
	}
	if containsAddr(out.Entries, bob.OuterAddress()) {
		t.Fatal("stale grant's file line survived reconcile")
	}
	if !containsAddr(out.Entries, manual.OuterAddress()) {
		t.Fatal("operator-added line was not preserved")
	}
	// Only the operator's line is a grant candidate; ours is already held.
	if len(report.NeedsGrant) != 1 || report.NeedsGrant[0].Addr != manual.OuterAddress() {
		t.Fatalf("needs-grant = %v, want the unmanaged line", report.NeedsGrant)
	}
	if len(got.Unmanaged) != 1 || got.Unmanaged[0].Addr != manual.OuterAddress() {
		t.Fatalf("unmanaged = %+v", got.Unmanaged)
	}
	applied, err := Apply(got, now, []Observation{{Key: alice}})
	if err != nil {
		t.Fatalf("apply after reconcile: %v", err)
	}
	if !containsAddr(applied.File.Entries, manual.OuterAddress()) {
		t.Fatalf("apply dropped the operator-added line: %+v", applied.File.Entries)
	}
}

func TestReconcileReportsRevokedAndDropsByteExhausted(t *testing.T) {
	alice := keyFor("alice")
	now := testEpoch.Add(2 * time.Hour)
	st := mustGrant(t, GrantRequest{Key: alice, Name: "alice", Mode: BillingBytes, ByteBudget: 100,
		GrantedAt: testEpoch, ExpiresAt: now.Add(-time.Minute)})
	sold := keyFor("sold-out")
	st, err := Grant(st, GrantRequest{Key: sold, Name: "sold-out", Mode: BillingBytes, ByteBudget: 10,
		GrantedAt: testEpoch, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	for i := range st.Entitlements {
		if st.Entitlements[i].Key == sold {
			st.Entitlements[i].BytesUsed = 10
		}
	}
	file := ClientsFile{Entries: []ClientsEntry{
		{Addr: alice.OuterAddress(), Name: "alice"},
		{Addr: sold.OuterAddress(), Name: "sold-out"},
	}}
	got, out, report, err := Reconcile(st, now, file)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(report.Revoked) != 2 {
		t.Fatalf("revoked = %v, want both addresses", report.Revoked)
	}
	if len(got.Entitlements) != 0 || len(out.Entries) != 0 {
		t.Fatalf("state = %+v file = %+v, want both empty", got.Entitlements, out.Entries)
	}
	if len(report.NeedsGrant) != 0 {
		t.Fatalf("revoked lines must not become grant candidates: %v", report.NeedsGrant)
	}
	if got.Revision <= st.Revision {
		t.Fatalf("revision %d did not advance past %d", got.Revision, st.Revision)
	}
}

func TestStateRoundTripCarriesUnmanagedAndSnapshot(t *testing.T) {
	_, _, file := clientFixtures()
	st, out, _, err := Reconcile(State{}, testEpoch, file)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(st.Unmanaged) != 2 || len(st.Snapshot) != 2 {
		t.Fatalf("state = %+v, want both lines carried", st)
	}
	encoded, err := EncodeState(st)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := DecodeState(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !sameEntries(decoded.Unmanaged, st.Unmanaged) || !sameEntries(decoded.Snapshot, st.Snapshot) {
		t.Fatalf("round-trip lost operator state: %+v", decoded)
	}
	if !sameEntries(decoded.Snapshot, out.Entries) {
		t.Fatalf("snapshot = %+v, want %+v", decoded.Snapshot, out.Entries)
	}
	// Persisted operator state is never an entitlement: a reload still admits
	// nobody on the strength of those lines alone.
	for _, e := range decoded.Unmanaged {
		if _, ok := decoded.Entitlement(keyFor("alice")); ok {
			t.Fatalf("unexpected entitlement for %s", e.Addr)
		}
	}
}

func TestEncodeStateRefusesRecordsItWouldNotGrant(t *testing.T) {
	good := Entitlement{Key: keyFor("alice"), Name: "alice", Mode: BillingTime,
		GrantedAt: testEpoch, ExpiresAt: testEpoch.Add(time.Hour), Revision: 1}
	cases := map[string]State{
		"revision above state": {Revision: 0, Entitlements: []Entitlement{good}},
		"zero key":             {Revision: 1, Entitlements: []Entitlement{{Mode: BillingTime, GrantedAt: testEpoch, ExpiresAt: testEpoch.Add(time.Hour), Revision: 1}}},
		"inverted window":      {Revision: 1, Entitlements: []Entitlement{{Key: keyFor("a"), Mode: BillingTime, GrantedAt: testEpoch, ExpiresAt: testEpoch, Revision: 1}}},
		"unset granted":        {Revision: 1, Entitlements: []Entitlement{{Key: keyFor("a"), Mode: BillingTime, ExpiresAt: testEpoch, Revision: 1}}},
		"time with budget":     {Revision: 1, Entitlements: []Entitlement{{Key: keyFor("a"), Mode: BillingTime, GrantedAt: testEpoch, ExpiresAt: testEpoch.Add(time.Hour), ByteBudget: 5, Revision: 1}}},
		"bytes over budget":    {Revision: 1, Entitlements: []Entitlement{{Key: keyFor("a"), Mode: BillingBytes, GrantedAt: testEpoch, ExpiresAt: testEpoch.Add(time.Hour), ByteBudget: 5, BytesUsed: 6, Revision: 1}}},
		"unsafe name":          {Revision: 1, Entitlements: []Entitlement{{Key: keyFor("a"), Name: "a#b", Mode: BillingTime, GrantedAt: testEpoch, ExpiresAt: testEpoch.Add(time.Hour), Revision: 1}}},
		"non-fd snapshot":      {Revision: 1, Snapshot: []ClientsEntry{{Addr: netip.MustParseAddr("2001:db8::1")}}},
	}
	for name, s := range cases {
		if _, err := EncodeState(s); err == nil {
			t.Fatalf("%s: encode must be refused", name)
		}
		if err := SaveState(&memStore{}, s); err == nil {
			t.Fatalf("%s: save must be refused", name)
		}
	}
}

func TestApplyRefusesUnusableObservations(t *testing.T) {
	alice := keyFor("alice")
	now := testEpoch.Add(time.Minute)
	st := mustGrant(t, GrantRequest{Key: alice, Mode: BillingTime,
		GrantedAt: testEpoch, ExpiresAt: now.Add(time.Hour)})
	if _, err := Apply(st, now, []Observation{{Key: alice}, {Key: alice}}); err == nil {
		t.Fatal("the same peer observed twice must be refused")
	}
	if _, err := Apply(st, now, []Observation{{}}); err == nil {
		t.Fatal("an observation with no key must be refused")
	}
}

func TestParsePeerKeyRefusesNonHexPayload(t *testing.T) {
	if _, err := ParsePeerKey("npub1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"); err == nil {
		t.Fatal("an npub whose payload is not a 32-byte key must fail")
	}
	if _, err := ParsePeerKey(strings.Repeat("g", 64)); err == nil {
		t.Fatal("64 non-hex characters must fail")
	}
	if _, err := ParsePeerKey("note1" + strings.Repeat("q", 58)); err == nil {
		t.Fatal("a note id is not a peer key")
	}
}

func TestReconcileAfterRestartNeverGrantsFreeAccess(t *testing.T) {
	_, _, file := clientFixtures()
	st, out, report, err := Reconcile(State{}, testEpoch, file)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(st.Entitlements) != 0 || st.Revision != 0 {
		t.Fatalf("reconcile invented entitlements: %+v", st)
	}
	if len(report.NeedsGrant) != len(file.Entries) {
		t.Fatalf("needs-grant = %d, want %d", len(report.NeedsGrant), len(file.Entries))
	}
	for i, e := range report.NeedsGrant {
		if e.Addr != file.Entries[i].Addr {
			t.Fatalf("needs-grant %d = %s, want %s", i, e.Addr, file.Entries[i].Addr)
		}
	}
	// The observed lines survive, unadmitted and unowned.
	if len(out.Entries) != len(file.Entries) {
		t.Fatalf("operator state dropped: %+v", out.Entries)
	}
	for _, e := range out.Entries {
		if !containsAddr(st.Unmanaged, e.Addr) {
			t.Fatalf("line %s not carried as unmanaged", e.Addr)
		}
	}
}

func TestReconcileRefusesConflictingAndInvalidInput(t *testing.T) {
	alice, bob := keyFor("alice"), keyFor("bob")
	now := testEpoch.Add(time.Hour)
	st := mustGrant(t, GrantRequest{Key: alice, Name: "alice", Mode: BillingTime,
		GrantedAt: testEpoch, ExpiresAt: now.Add(time.Hour)})
	// A line carrying a managed address under another name is a conflict, not a
	// rename: the durable record is authoritative.
	file := ClientsFile{Entries: []ClientsEntry{{Addr: alice.OuterAddress(), Name: "spoofed"}}}
	_, out, report, err := Reconcile(st, now, file)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if out.Entries[0].Name != "alice" {
		t.Fatalf("name = %q, want the durable record's name", out.Entries[0].Name)
	}
	if len(report.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want one report", report.Conflicts)
	}
	if _, _, _, err := Reconcile(st, now, ClientsFile{Entries: []ClientsEntry{
		{Addr: bob.OuterAddress()}, {Addr: bob.OuterAddress()},
	}}); err == nil {
		t.Fatal("a duplicate address in the observed file must be refused")
	}
	if _, _, _, err := Reconcile(st, now, ClientsFile{Entries: []ClientsEntry{
		{Addr: netip.MustParseAddr("2001:db8::1")},
	}}); err == nil {
		t.Fatal("a non-fips observed address must be refused")
	}
}

// --- helpers ------------------------------------------------------------

func containsAddr(entries []ClientsEntry, addr netip.Addr) bool {
	for _, e := range entries {
		if e.Addr == addr {
			return true
		}
	}
	return false
}

func verdictOf(t *testing.T, a Applied, k PeerKey) Verdict {
	t.Helper()
	for _, o := range a.Outcomes {
		if o.Key == k {
			return o.Verdict
		}
	}
	return ""
}

// addrOrder is the sequence of addresses in a rendered clients file.
func addrOrder(rendered []byte) string {
	var out []string
	for _, e := range ParseClientsFile(rendered).Entries {
		out = append(out, e.Addr.String())
	}
	return strings.Join(out, ",")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// memStore is the test double for the injected persistence seam. The pure layer
// reaches storage only through this interface, so no disk is touched here.
type memStore struct {
	data      []byte
	writes    int
	failWrite error
	failRead  error
}

func (m *memStore) Read() ([]byte, error) {
	if m.failRead != nil {
		return nil, m.failRead
	}
	if m.data == nil {
		return nil, ErrNoState
	}
	return append([]byte(nil), m.data...), nil
}

func (m *memStore) WriteAtomic(data []byte) error {
	if m.failWrite != nil {
		return m.failWrite
	}
	m.writes++
	m.data = append([]byte(nil), data...)
	return nil
}
