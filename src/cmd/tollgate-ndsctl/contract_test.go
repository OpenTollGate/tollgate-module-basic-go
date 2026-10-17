package main

// contract_test.go — the CLI surface and the exact JSON/exit-status contract
// that src/valve depends on (LINUX-HOST-5).
//
// The tests drive the REAL entry point (run), never a re-implementation, so an
// exit status or a byte of stdout that changes under the module is caught here
// rather than on a router. The presence files and the state file are ordinary
// files in a t.TempDir(); only the paths differ from production.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// The golden JSON fixture
// ---------------------------------------------------------------------------

// goldenNow / goldenAdded put the golden record's `duration` at 67 s and keep
// every value in it a literal, so the fixture cannot drift with the clock.
const (
	goldenMAC   = "a8:a0:92:a5:39:7a"
	goldenIP    = "192.168.1.124"
	goldenAdded = int64(1790419500)
	goldenNow   = int64(1790419567)
	goldenDown  = uint64(2367)
	goldenUp    = uint64(64)
	goldenToken = "d393b184"
)

// goldenStats is the record the shim must render for the fixture's client.
func goldenStats() ClientStats {
	return statsFor(goldenMAC, Session{
		IP:         goldenIP,
		Added:      goldenAdded,
		Active:     goldenAdded,
		Downloaded: goldenDown,
		Uploaded:   goldenUp,
		Token:      goldenToken,
	}, 1, goldenNow)
}

func goldenBytes(t *testing.T) []byte {
	t.Helper()
	rendered, err := formatStats(goldenStats())
	if err != nil {
		t.Fatalf("render golden record: %v", err)
	}
	return []byte(rendered)
}

// TestGoldenClientJSON pins the byte-exact single-client record — member order,
// two-decimal speeds and all — against the committed fixture. If the wire form
// changes, this fails before the module ever sees it.
func TestGoldenClientJSON(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "golden-client.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got := goldenBytes(t)
	if !bytes.Equal(got, want) {
		t.Fatalf("the single-client record does not match the golden fixture\n got: %s\nwant: %s", got, want)
	}
}

// TestGoldenFixtureMutationFails proves the golden comparison is discriminating:
// mutating a single value in the fixture must make the same comparison the
// golden test performs FAIL. A fixture no mutation can break would be
// decoration, not a guard.
func TestGoldenFixtureMutationFails(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "golden-client.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got := goldenBytes(t)

	mutations := map[string][]byte{
		"a changed counter (KiB vs bytes would look like this)": bytes.Replace(want, []byte(`"downloaded":2367`), []byte(`"downloaded":2368`), 1),
		"a changed state":                     bytes.Replace(want, []byte(`"state":"Authenticated"`), []byte(`"state":"Preauthenticated"`), 1),
		"a dropped member (the exact schema)": bytes.Replace(want, []byte(`,"avg_up_speed":0.00`), nil, 1),
	}
	for name, mutated := range mutations {
		if bytes.Equal(mutated, want) {
			t.Fatalf("%s: the mutation did not change the fixture (fix the mutation)", name)
		}
		if bytes.Equal(mutated, got) {
			t.Fatalf("%s: a mutated fixture still equals the rendered record — the golden comparison does not discriminate", name)
		}
	}
}

// TestClientStatsSchemaIsExact pins the member SET and ORDER. JSON object order
// is not semantically significant to the module's unmarshal, but the card names
// the order explicitly, and pinning it keeps the shim's bytes comparable to the
// fixture.
func TestClientStatsSchemaIsExact(t *testing.T) {
	want := []string{
		"id", "ip", "mac", "added", "active", "duration", "token", "state",
		"downloaded", "uploaded", "avg_down_speed", "avg_up_speed",
	}
	got := orderedKeys(t, goldenBytes(t))
	if len(got) != len(want) {
		t.Fatalf("the record carries %d members, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("member %d is %q, want %q (full order: %v)", i, got[i], want[i], got)
		}
	}

	// And the same, as a set, through the module's own decoder shape: a record
	// must survive encoding/json round-tripping of every field.
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(goldenBytes(t), &decoded); err != nil {
		t.Fatalf("the record is not valid JSON: %v", err)
	}
	if len(decoded) != len(want) {
		t.Fatalf("the record has %d distinct members, want %d", len(decoded), len(want))
	}
	for _, name := range want {
		if _, ok := decoded[name]; !ok {
			t.Fatalf("the record is missing the %q member", name)
		}
	}
}

// TestCounterUnitsAreKilobytes states the one unit that, got wrong, over-charges
// every metered session by 1024x: the record's counters are KiB.
func TestCounterUnitsAreKilobytes(t *testing.T) {
	stats := goldenStats()
	if stats.Downloaded != goldenDown || stats.Uploaded != goldenUp {
		t.Fatalf("the record must carry the backend's KiB counters verbatim, got down=%d up=%d", stats.Downloaded, stats.Uploaded)
	}
	if !strings.Contains(string(goldenBytes(t)), `"downloaded":2367`) {
		t.Fatal("the rendered record must carry the KiB count, not bytes")
	}
}

// TestKnownVerbsAreExactlyThree pins the surface: auth, deauth, json.
func TestKnownVerbsAreExactlyThree(t *testing.T) {
	want := map[string]bool{verbAuth: true, verbDeauth: true, verbJSON: true}
	if len(knownVerbs) != 3 {
		t.Fatalf("the shim must accept exactly three verbs, has %v", knownVerbs)
	}
	for _, v := range knownVerbs {
		if !want[v] {
			t.Fatalf("unexpected verb %q in the surface", v)
		}
	}
}

// ---------------------------------------------------------------------------
// Exit-status contract (live-shaped runs through run)
// ---------------------------------------------------------------------------

// harness is a shim whose whole environment is a temp dir.
type harness struct {
	t          *testing.T
	leasesPath string
	arpPath    string
	statePath  string
	now        int64
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	h := &harness{
		t:          t,
		leasesPath: filepath.Join(dir, "dhcp.leases"),
		arpPath:    filepath.Join(dir, "arp"),
		statePath:  filepath.Join(dir, "run", "ndsctl-state.json"),
		now:        goldenNow,
	}
	h.write(h.leasesPath, "")
	h.write(h.arpPath, procArpHeader)
	return h
}

const procArpHeader = "IP address       HW type     Flags       HW address            Mask     Device\n"

func (h *harness) write(path, body string) {
	h.t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		h.t.Fatalf("write %s: %v", path, err)
	}
}

// addLease makes the host know a MAC through the DHCP lease file.
func (h *harness) addLease(mac, ip string) {
	h.write(h.leasesPath, "1790500000 "+mac+" "+ip+" host *\n")
}

// addArp makes the host know a MAC through the neighbour table only.
func (h *harness) addArp(mac, ip string) {
	h.write(h.arpPath, procArpHeader+ip+"    0x1         0x2         "+mac+"     *        br-tg\n")
}

func (h *harness) cfg() config {
	return config{
		leasesPath: h.leasesPath,
		arpPath:    h.arpPath,
		statePath:  h.statePath,
		now:        func() time.Time { return time.Unix(h.now, 0) },
	}
}

func (h *harness) run(args ...string) (int, string, string) {
	h.t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, h.cfg(), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// TestAuthUnknownMACExitsNonZero is the retry-loop contract: a MAC the host does
// not know must fail, or authorizeMAC's 5x/400 ms retry never converges.
func TestAuthUnknownMACExitsNonZero(t *testing.T) {
	h := newHarness(t)
	code, _, stderr := h.run("auth", "aa:bb:cc:dd:ee:01")
	if code == 0 {
		t.Fatalf("auth of a MAC with no lease and no ARP entry must exit non-zero, got %d (stdout only)", code)
	}
	if !strings.Contains(stderr, "not on the host") {
		t.Fatalf("the failure must say why, got stderr %q", stderr)
	}
}

// TestJSONUnknownMACExitsNonZero is the same presence gate for the read path.
func TestJSONUnknownMACExitsNonZero(t *testing.T) {
	h := newHarness(t)
	code, stdout, _ := h.run("json", "aa:bb:cc:dd:ee:02")
	if code == 0 {
		t.Fatalf("json of a MAC with no lease and no ARP entry must exit non-zero, got %d", code)
	}
	if strings.Contains(stdout, emptyObject) {
		t.Fatalf("a non-zero exit must not also print the empty object, got %q", stdout)
	}
}

// TestJSONKnownButUnauthorizedPrintsEmptyObject pins the other half of the
// contract: a MAC the host knows but the shim has not authorized is `{}` with
// exit 0 — the definitive not-authenticated answer, not a read failure.
func TestJSONKnownButUnauthorizedPrintsEmptyObject(t *testing.T) {
	h := newHarness(t)
	h.addLease("aa:bb:cc:dd:ee:03", "192.168.1.50")
	code, stdout, stderr := h.run("json", "aa:bb:cc:dd:ee:03")
	if code != 0 {
		t.Fatalf("json of a known-but-unauthorized MAC must exit 0, got %d (%s)", code, stderr)
	}
	if stdout != emptyObject+"\n" {
		t.Fatalf("json of a known-but-unauthorized MAC must print %q, got %q", emptyObject, stdout)
	}
}

// TestARPPresenceCountsToo: a statically-addressed client has no DHCP lease; the
// neighbour table alone must satisfy the presence gate.
func TestARPPresenceCountsToo(t *testing.T) {
	h := newHarness(t)
	h.addArp("aa:bb:cc:dd:ee:04", "192.168.1.51")
	code, stdout, stderr := h.run("json", "aa:bb:cc:dd:ee:04")
	if code != 0 {
		t.Fatalf("json of an ARP-known MAC must exit 0, got %d (%s)", code, stderr)
	}
	if stdout != emptyObject+"\n" {
		t.Fatalf("an ARP-known but unauthorized MAC must answer %q, got %q", emptyObject, stdout)
	}
}

// TestAuthThenJSONRoundTrip walks the two-verb flow the module actually runs:
// authorize a present client, then read its record back.
func TestAuthThenJSONRoundTrip(t *testing.T) {
	h := newHarness(t)
	mac := "aa:bb:cc:dd:ee:05"
	h.addLease(mac, "192.168.1.52")

	if code, _, stderr := h.run("auth", mac); code != 0 {
		t.Fatalf("auth of a present MAC must exit 0, got %d (%s)", code, stderr)
	}

	code, stdout, stderr := h.run("json", mac)
	if code != 0 {
		t.Fatalf("json after auth must exit 0, got %d (%s)", code, stderr)
	}
	var got ClientStats
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("json after auth must be one client record, got %q: %v", stdout, err)
	}
	if got.MAC != mac {
		t.Fatalf("the record's mac is %q, want %q", got.MAC, mac)
	}
	if got.IP != "192.168.1.52" {
		t.Fatalf("the record's ip is %q, want the lease address 192.168.1.52", got.IP)
	}
	if got.State != stateAuthenticated {
		t.Fatalf("the record's state is %q, want %q", got.State, stateAuthenticated)
	}
	if got.Added != goldenNow || got.Duration != 0 {
		t.Fatalf("a just-authorized session must carry added=now and duration=0, got added=%d duration=%d", got.Added, got.Duration)
	}
	if got.Token == "" {
		t.Fatal("the record must carry a token")
	}
}

// TestAuthIsIdempotentAndKeepsTheClock: re-authorizing a live session must not
// reset `added` (the metering clock) — a customer mid-session must not have the
// session they paid for rewind.
func TestAuthIsIdempotentAndKeepsTheClock(t *testing.T) {
	h := newHarness(t)
	mac := "aa:bb:cc:dd:ee:06"
	h.addLease(mac, "192.168.1.53")

	if code, _, _ := h.run("auth", mac); code != 0 {
		t.Fatal("first auth must exit 0")
	}
	first := readRecord(t, h, mac)

	h.now += 30
	if code, _, _ := h.run("auth", mac); code != 0 {
		t.Fatal("second auth must exit 0 (idempotent)")
	}
	second := readRecord(t, h, mac)

	if second.Added != first.Added {
		t.Fatalf("re-auth moved added from %d to %d: the metering clock must not reset", first.Added, second.Added)
	}
	if second.Duration <= first.Duration {
		t.Fatalf("duration must grow across a re-auth, got %d then %d", first.Duration, second.Duration)
	}
}

// TestDeauthRemovesTheSession: after a confirmed deauth, the shim holds no
// client, so json answers `{}`.
func TestDeauthRemovesTheSession(t *testing.T) {
	h := newHarness(t)
	mac := "aa:bb:cc:dd:ee:07"
	h.addLease(mac, "192.168.1.54")
	if code, _, _ := h.run("auth", mac); code != 0 {
		t.Fatal("auth must exit 0")
	}
	if code, _, stderr := h.run("deauth", mac); code != 0 {
		t.Fatalf("deauth of an authorized client must exit 0, got %d (%s)", code, stderr)
	}
	code, stdout, _ := h.run("json", mac)
	if code != 0 || stdout != emptyObject+"\n" {
		t.Fatalf("json after deauth must be %q exit 0, got %q exit %d", emptyObject, stdout, code)
	}
}

// TestDeauthUnauthorizedClientSaysNotFound: the client is on the host but not
// authorized — exactly the measured ndsctl answer "Client <mac> not found."
// with exit 1, which the module reads as an already-closed gate.
func TestDeauthUnauthorizedClientSaysNotFound(t *testing.T) {
	h := newHarness(t)
	mac := "aa:bb:cc:dd:ee:08"
	h.addLease(mac, "192.168.1.55")
	code, stdout, _ := h.run("deauth", mac)
	if code == 0 {
		t.Fatalf("deauth of an unauthorized client must exit non-zero (the measured ndsctl behaviour), got %d", code)
	}
	if !strings.Contains(strings.ToLower(stdout), "not found") || !strings.Contains(stdout, mac) {
		t.Fatalf("the answer must be the phrase deauthorizeMAC recognises (\"Client <mac> not found.\"), got %q", stdout)
	}
}

// TestDeauthUnknownMACExitsNonZero: presence gate on the deauth path too.
func TestDeauthUnknownMACExitsNonZero(t *testing.T) {
	h := newHarness(t)
	if code, _, _ := h.run("deauth", "aa:bb:cc:dd:ee:09"); code == 0 {
		t.Fatal("deauth of a MAC with no lease and no ARP entry must exit non-zero")
	}
}

// TestBogusVerbExitsNonZero: a typo is never a quiet success.
func TestBogusVerbExitsNonZero(t *testing.T) {
	h := newHarness(t)
	code, _, stderr := h.run("authorise", "aa:bb:cc:dd:ee:0a")
	if code == 0 {
		t.Fatal("an unknown verb must exit non-zero")
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Fatalf("an unknown verb must be named as such, got %q", stderr)
	}
}

// TestMissingArgumentExitsNonZero: auth/deauth without a MAC is a usage error.
func TestMissingArgumentExitsNonZero(t *testing.T) {
	h := newHarness(t)
	for _, verb := range []string{verbAuth, verbDeauth} {
		if code, _, _ := h.run(verb); code == 0 {
			t.Fatalf("%s with no MAC must exit non-zero", verb)
		}
	}
}

// TestNoArgumentsExitsNonZero: invoked bare, the shim prints usage and fails.
func TestNoArgumentsExitsNonZero(t *testing.T) {
	h := newHarness(t)
	code, _, stderr := h.run()
	if code == 0 {
		t.Fatal("ndsctl with no verb must exit non-zero")
	}
	if !strings.Contains(stderr, "usage:") {
		t.Fatalf("the usage error must print usage, got %q", stderr)
	}
}

// ---------------------------------------------------------------------------
// The client list (`ndsctl json`, no argument), which nds_clients.go parses.
// ---------------------------------------------------------------------------

func TestJSONListShape(t *testing.T) {
	h := newHarness(t)
	first, second := "aa:bb:cc:dd:ee:0b", "aa:bb:cc:dd:ee:0c"
	h.addLease(first, "192.168.1.60")
	h.write(h.leasesPath, "1790500000 "+first+" 192.168.1.60 host *\n1790500000 "+second+" 192.168.1.61 host *\n")

	if code, _, stderr := h.run("auth", first); code != 0 {
		t.Fatalf("auth first: %d (%s)", code, stderr)
	}
	if code, _, stderr := h.run("auth", second); code != 0 {
		t.Fatalf("auth second: %d (%s)", code, stderr)
	}

	code, stdout, stderr := h.run("json")
	if code != 0 {
		t.Fatalf("the client list must exit 0, got %d (%s)", code, stderr)
	}

	// Decode the way nds_clients.go does.
	var list struct {
		ClientLength int                       `json:"client_length"`
		Clients      map[string]map[string]any `json:"clients"`
	}
	if err := json.Unmarshal([]byte(stdout), &list); err != nil {
		t.Fatalf("the client list is not the shape nds_clients.go parses: %v (%q)", err, stdout)
	}
	if list.ClientLength != 2 || len(list.Clients) != 2 {
		t.Fatalf("the list must report exactly the two authorized clients, got client_length=%d clients=%d", list.ClientLength, len(list.Clients))
	}
	for _, mac := range []string{first, second} {
		entry, ok := list.Clients[mac]
		if !ok {
			t.Fatalf("the list is missing %s", mac)
		}
		if entry["mac"] != mac {
			t.Fatalf("the record for %s must repeat its own mac, got %v", mac, entry["mac"])
		}
		if entry["state"] != stateAuthenticated {
			t.Fatalf("the record for %s must be Authenticated, got %v", mac, entry["state"])
		}
	}
}

func TestJSONListEmptyIsTheEmptyObject(t *testing.T) {
	h := newHarness(t)
	h.addLease("aa:bb:cc:dd:ee:0d", "192.168.1.62") // known to the host, not authorized
	code, stdout, _ := h.run("json")
	if code != 0 {
		t.Fatalf("an empty client list must exit 0, got %d", code)
	}
	if stdout != emptyObject+"\n" {
		t.Fatalf("an empty client list must be %q, got %q", emptyObject, stdout)
	}
}

// ---------------------------------------------------------------------------
// Host knowledge parsing
// ---------------------------------------------------------------------------

func TestParseDhcpLeases(t *testing.T) {
	data := "1790500000 AA:BB:CC:DD:EE:10 192.168.1.70 host *\n" +
		"1790600000 aa:bb:cc:dd:ee:10 192.168.1.71 host *\n" + // longer expiry wins
		"1790500000 aa:bb:cc:dd:ee:11 192.168.1.72 host *\n"

	ip, ok := parseDhcpLeases(data, "aa:bb:cc:dd:ee:10")
	if !ok || ip != "192.168.1.71" {
		t.Fatalf("a case-insensitive match with the longest expiry must win, got %q ok=%v", ip, ok)
	}
	if ip, ok := parseDhcpLeases(data, "aa:bb:cc:dd:ee:11"); !ok || ip != "192.168.1.72" {
		t.Fatalf("a plain match must be found, got %q ok=%v", ip, ok)
	}
	if _, ok := parseDhcpLeases(data, "aa:bb:cc:dd:ee:12"); ok {
		t.Fatal("a MAC absent from the lease file must not be found")
	}
	if _, ok := parseDhcpLeases("", "aa:bb:cc:dd:ee:10"); ok {
		t.Fatal("an empty lease file must yield nothing")
	}
}

func TestParseProcArp(t *testing.T) {
	header := procArpHeader
	data := header +
		"192.168.1.80    0x1         0x2         AA:BB:CC:DD:EE:20     *        br-tg\n" +
		"192.168.1.81    0x1         0x0         aa:bb:cc:dd:ee:21     *        br-tg\n" + // incomplete
		"192.168.1.82    0x1         0x2         00:00:00:00:00:00     *        br-tg\n" // no address

	if ip, ok := parseProcArp(data, "aa:bb:cc:dd:ee:20"); !ok || ip != "192.168.1.80" {
		t.Fatalf("a case-insensitive ARP match must be found, got %q ok=%v", ip, ok)
	}
	if _, ok := parseProcArp(data, "aa:bb:cc:dd:ee:21"); ok {
		t.Fatal("an INCOMPLETE (flags 0x0) ARP entry must not count as presence")
	}
	if _, ok := parseProcArp(data, "00:00:00:00:00:00"); ok {
		t.Fatal("the all-zero ARP address must not count as a client")
	}
	if _, ok := parseProcArp(header, "aa:bb:cc:dd:ee:20"); ok {
		t.Fatal("a header-only ARP table must yield nothing")
	}
}

func TestTokenIsStableAndCaseInsensitive(t *testing.T) {
	if tokenFor("AA:BB:CC:DD:EE:30") != tokenFor("aa:bb:cc:dd:ee:30") {
		t.Fatal("the token must not depend on the MAC's case")
	}
	if tokenFor("aa:bb:cc:dd:ee:30") == tokenFor("aa:bb:cc:dd:ee:31") {
		t.Fatal("two different MACs must not share a token")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// readRecord reads one client record back through the real CLI.
func readRecord(t *testing.T, h *harness, mac string) ClientStats {
	t.Helper()
	code, stdout, stderr := h.run("json", mac)
	if code != 0 {
		t.Fatalf("json %s: exit %d (%s)", mac, code, stderr)
	}
	var got ClientStats
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("decode record for %s: %v (%q)", mac, err, stdout)
	}
	return got
}

// orderedKeys walks a JSON object's members in document order.
func orderedKeys(t *testing.T, data []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("read first token: %v", err)
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		t.Fatalf("the record is not a JSON object: %q", data)
	}
	var keys []string
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			t.Fatalf("read member name: %v", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			t.Fatalf("member name is not a string: %v", keyToken)
		}
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("read value of %q: %v", key, err)
		}
	}
	return keys
}
