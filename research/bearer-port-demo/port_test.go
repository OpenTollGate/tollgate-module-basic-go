package bearerport

import (
	"bytes"
	"errors"
	"testing"
)

// ---------------------------------------------------------------------------
// The executable form of the contract. Every assertion below is a rule the
// generic port must hold for ANY bearer instrument, not a Cashu detail.
// ---------------------------------------------------------------------------

func demoAuthorities() (*CashuAuthority, *FedimintAuthority, *PowAuthority) {
	return NewCashuAuthority("https://mint.example"),
		NewFedimintAuthority("fed1invitefedimintexample"),
		NewPowAuthority("miner-pubkey-01")
}

func demoPort() (*Port, *CashuAuthority, *FedimintAuthority, *PowAuthority) {
	c, f, p := demoAuthorities()
	port := NewPort()
	port.Register(c)
	port.Register(f)
	port.Register(p)
	return port, c, f, p
}

// Rule 1: the port sniffs the encoding family and exposes only primitive,
// backend-neutral facts. Nothing Cashu-specific crosses the seam.
func TestDecodeSniffsBackendForEveryKind(t *testing.T) {
	port, cashu, fedi, pow := demoPort()
	seed := port.Seed()

	blobs := map[string][]byte{}
	for _, a := range []Authority{cashu, fedi, pow} {
		b, err := port.IssueInto(a.Name(), 100)
		if err != nil {
			t.Fatalf("issue into %s: %v", a.Name(), err)
		}
		blobs[a.Name()] = b
	}

	cases := []struct {
		authority Authority
		kind      string
		unit      string
		nullifier string
	}{
		{cashu, "cashuA", "sat", cashu.Nullifier(seed, 0)},
		{fedi, "fed1", "sat", fedi.Nullifier(seed, 0)},
		// A miner's pool never learns a nullifier: it can attest neither
		// "consumed" nor "exists", so the instrument honestly carries none.
		{pow, "pow1", "work", ""},
	}

	for _, tc := range cases {
		in, err := port.Decode(blobs[tc.authority.Name()])
		if err != nil {
			t.Fatalf("%s: decode: %v", tc.authority.Name(), err)
		}
		if in.Kind() != tc.kind {
			t.Errorf("%s: kind = %q, want %q", tc.authority.Name(), in.Kind(), tc.kind)
		}
		if in.Authority() != tc.authority.Name() {
			t.Errorf("%s: authority = %q, want %q", tc.authority.Name(), in.Authority(), tc.authority.Name())
		}
		if in.Unit() != tc.unit {
			t.Errorf("%s: unit = %q, want %q", tc.authority.Name(), in.Unit(), tc.unit)
		}
		if in.Nullifier() != tc.nullifier {
			t.Errorf("%s: nullifier = %q, want %q", tc.authority.Name(), in.Nullifier(), tc.nullifier)
		}
		if bytes.Contains(in.ReissueBlob(), seed) {
			t.Errorf("%s: the port's seed leaked into the blob handed to the authority", tc.authority.Name())
		}
	}
}

// Rule 2: FaceValue is an unverified claim. The credited amount is the
// authority's answer, never the instrument's self-description.
func TestAcquireCreditsAuthorityValueNotClaimedFaceValue(t *testing.T) {
	port, cashu, _, _ := demoPort()

	// The mint signs a 100-sat output...
	raw := cashu.Issue([]byte("a-foreign-holders-seed"), 0, 100)

	honest, err := port.Decode(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if honest.FaceValue() != 100 {
		t.Fatalf("honest face value = %d, want 100", honest.FaceValue())
	}

	// ...and the blob's own encoding is edited to claim ten times that.
	forged := bytes.Replace(raw, []byte(":100"), []byte(":1000"), 1)
	lying, err := port.Decode(forged)
	if err != nil {
		t.Fatalf("decode forged: %v", err)
	}
	if lying.FaceValue() != 1000 {
		t.Fatalf("forged face value = %d, want 1000", lying.FaceValue())
	}

	got, err := port.Acquire(lying)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if got != 100 {
		t.Errorf("credited = %d, want 100 (the mint's number, not the claim)", got)
	}
	if bal := port.BalanceByAuthority(cashu.Name()); bal != 100 {
		t.Errorf("balance = %d, want 100", bal)
	}
}

// Rule 3: acquisition is atomic per instrument. A rejected acquisition consumes
// nothing and credits nothing.
func TestAcquireIsAtomicAndDetectsDoubleRedemption(t *testing.T) {
	port, cashu, fedi, _ := demoPort()

	raw := cashu.Issue([]byte("holder-a"), 0, 250)
	if _, err := port.AcquireRaw(raw); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	before := port.BalanceByAuthority(cashu.Name())

	if _, err := port.AcquireRaw(raw); !errors.Is(err, ErrAlreadyConsumed) {
		t.Fatalf("second acquire err = %v, want ErrAlreadyConsumed", err)
	}
	if after := port.BalanceByAuthority(cashu.Name()); after != before {
		t.Errorf("balance moved on a rejected acquire: %d -> %d", before, after)
	}

	// The same rule holds on another instrument kind.
	fediRaw := fedi.Issue([]byte("holder-b"), 0, 250)
	if _, err := port.AcquireRaw(fediRaw); err != nil {
		t.Fatalf("fedi first acquire: %v", err)
	}
	if _, err := port.AcquireRaw(fediRaw); !errors.Is(err, ErrAlreadyConsumed) {
		t.Errorf("fedi second acquire err = %v, want ErrAlreadyConsumed", err)
	}
}

// Rule 4 (the NUT-07 derivation): a state answer is only ever SPENT or UNSPENT
// when the authority actually attested. Anything else is UNKNOWN, and the port
// must fail closed rather than treat silence as "unspent".
func TestUnknownIsNeverReportedAsUnspent(t *testing.T) {
	port, cashu, _, _ := demoPort()

	raw, err := port.IssueInto(cashu.Name(), 50)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	in, err := port.Decode(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	// The authority recorded this output, so it can attest: UNSPENT.
	s, err := port.CheckState(in)
	if err != nil {
		t.Fatalf("CheckState on a freshly issued instrument: %v", err)
	}
	if s != StateUnspent {
		t.Errorf("freshly issued = %v, want StateUnspent", s)
	}

	// A nullifier the authority never learned stays UNKNOWN, never UNSPENT.
	never := NullifierOf([]byte("some-other-holder"), 999, cashu.Name())
	st, err := cashu.CheckStates([]string{never})
	if err != nil {
		t.Fatalf("checkstates: %v", err)
	}
	if st[never] != StateUnknown {
		t.Errorf("never-learned nullifier = %v, want StateUnknown", st[never])
	}

	// An unreachable authority is UNKNOWN too, and the port will not guess.
	cashu.SetOffline(true)
	s, err = port.CheckState(in)
	if !errors.Is(err, ErrStateUnavailable) {
		t.Fatalf("offline CheckState err = %v, want ErrStateUnavailable", err)
	}
	if s != StateUnknown {
		t.Errorf("offline CheckState = %v, want StateUnknown", s)
	}
	cashu.SetOffline(false)

	// Now consume it: the only way the answer becomes SPENT is the authority
	// recording that it was actually redeemed.
	if _, err := port.Acquire(in); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	s, err = port.CheckState(in)
	if err != nil {
		t.Fatalf("CheckState after spend: %v", err)
	}
	if s != StateSpent {
		t.Errorf("consumed instrument = %v, want StateSpent", s)
	}
}

// Rule 5: when the authority has no token-level liveness API, NUT-07 is
// re-derived as an atomic PROBE. A probe is a mutation and must be declared as
// such — the capability matrix is the honest contract.
func TestProbeModeDerivesNut07WhereNoQueryApiExists(t *testing.T) {
	port, _, fedi, _ := demoPort()

	// A note issued to someone else: we hold the bearer instrument but have
	// not claimed it, so we do not yet know whether it is live.
	raw := fedi.Issue([]byte("a-foreign-holders-seed"), 0, 120)
	in, err := port.Decode(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	var fediCap Capability
	for _, c := range port.Capabilities() {
		if c.Authority == fedi.Name() {
			fediCap = c
		}
	}
	if fediCap.StateCheck != CheckModeProbe {
		t.Fatalf("fedi state_check = %q, want %q", fediCap.StateCheck, CheckModeProbe)
	}

	// Probing a live note succeeds AND claims it: that is the documented cost
	// of probe mode, and the reason it has to be declared.
	s, err := port.CheckState(in)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if s != StateUnspent {
		t.Errorf("probe of a live note = %v, want StateUnspent", s)
	}
	if got := port.BalanceByAuthority(fedi.Name()); got != 120 {
		t.Errorf("probe credit = %d, want 120", got)
	}

	// Probing it again reports SPENT — the double-spend property survives.
	s, err = port.CheckState(in)
	if err != nil {
		t.Fatalf("second probe: %v", err)
	}
	if s != StateSpent {
		t.Errorf("second probe = %v, want StateSpent", s)
	}
	if got := port.BalanceByAuthority(fedi.Name()); got != 120 {
		t.Errorf("double credit after second probe: %d, want 120", got)
	}
}

// Rule 6: an authority with NO state API answers UNKNOWN for everything, and
// the port surfaces that as a capability gap rather than a failure.
func TestNoStateApiIsUnknownNotAGuess(t *testing.T) {
	port, _, _, pow := demoPort()

	raw, err := port.IssueInto(pow.Name(), 300)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	in, err := port.Decode(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	s, err := port.CheckState(in)
	if s != StateUnknown {
		t.Errorf("state = %v, want StateUnknown", s)
	}
	if !errors.Is(err, ErrStateUnavailable) {
		t.Errorf("err = %v, want ErrStateUnavailable", err)
	}
}

// Rule 7 (the NUT-07 sharp edge): a PLAIN Cashu mint answers "UNSPENT" for a
// nullifier it has never seen in its life, because /checkstate reports
// membership in the spent set. The port must therefore never treat a NUT-07
// answer as proof of existence — that is what Decode is for, and this test
// pins the distinction that stops a forged note from passing a spendability
// check.
func TestNut07AnswersConsumptionNotExistence(t *testing.T) {
	raw := NewRawCashuAuthority("https://raw-mint.example")
	port := NewPort()
	port.Register(raw)

	// A nullifier this mint has never learned, for an output it never signed.
	neverSeen := NullifierOf([]byte("a-forged-note"), 0, raw.Name())
	states, err := raw.CheckStates([]string{neverSeen})
	if err != nil {
		t.Fatalf("checkstates: %v", err)
	}
	if states[neverSeen] != StateUnspent {
		t.Fatalf("raw mint answered %v, want StateUnspent (that IS the real behaviour)", states[neverSeen])
	}
	// ...and yet the same value is not an instrument the mint ever issued.
	if _, err := port.Decode([]byte("cashuA|deadbeef:" + "5")); !errors.Is(err, ErrNotIssued) {
		t.Errorf("decode of an unsigned output err = %v, want ErrNotIssued", err)
	}

	// A real output, acquired twice: the second attempt is refused by the
	// authority's spend set, which is the question NUT-07 actually answers.
	blob := raw.Issue([]byte("holder"), 0, 40)
	if _, err := port.AcquireRaw(blob); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := port.AcquireRaw(blob); !errors.Is(err, ErrAlreadyConsumed) {
		t.Errorf("second acquire err = %v, want ErrAlreadyConsumed", err)
	}
}

// Rule 8 (the NUT-09 derivation): recovery replays the deterministic derivation
// against the authority's own issuance memory. Local store loss must not lose
// funds — and an output already spent must NOT come back.
func TestRecoverReplaysAgainstAuthorityIssuanceMemory(t *testing.T) {
	port, cashu, _, _ := demoPort()
	seed := port.Seed()

	for i := 0; i < 3; i++ {
		if _, err := port.IssueInto(cashu.Name(), 100); err != nil {
			t.Fatalf("issue %d: %v", i, err)
		}
	}
	if got := port.BalanceByAuthority(cashu.Name()); got != 300 {
		t.Fatalf("balance = %d, want 300", got)
	}

	// Index 1 is consumed out of band (another device, or a crash mid-swap).
	cashu.MarkSpentForTest(CommitmentOf(seed, 1, cashu.Name()))

	port.DestroyStore()
	if got := port.Balance(); got != 0 {
		t.Fatalf("post-destroy balance = %d, want 0", got)
	}

	rec, err := port.Recover(seed)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if got := rec[cashu.Name()]; got != 200 {
		t.Errorf("recovered = %d, want 200 (2 of 3; the spent note must not return)", got)
	}
	if got := port.BalanceByAuthority(cashu.Name()); got != 200 {
		t.Errorf("post-recover balance = %d, want 200", got)
	}
}

// Rule 9: recovery works for every instrument, by whatever memory the authority
// actually keeps. The port does not assume one shape.
func TestRecoverAcrossAllAuthorities(t *testing.T) {
	port, cashu, fedi, pow := demoPort()

	for _, tc := range []struct {
		a      Authority
		amount uint64
	}{{cashu, 100}, {fedi, 200}, {pow, 300}} {
		if _, err := port.IssueInto(tc.a.Name(), tc.amount); err != nil {
			t.Fatalf("issue %s: %v", tc.a.Name(), err)
		}
	}

	port.DestroyStore()
	rec, err := port.Recover(port.Seed())
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	for authority, want := range map[string]uint64{
		cashu.Name(): 100,
		fedi.Name():  200,
		pow.Name():   300,
	} {
		if rec[authority] != want {
			t.Errorf("recovered %s = %d, want %d", authority, rec[authority], want)
		}
	}
}

// Rule 10: the seed never leaves the port's own store — not into the blob the
// authority sees, and not into the decoded instrument — and every path that
// exposes a derivation range persists the counter first.
func TestSeedNeverLeavesPortAndCounterPersistsBeforeExposure(t *testing.T) {
	port, cashu, _, _ := demoPort()
	seed := port.Seed()

	raw, err := port.IssueInto(cashu.Name(), 10)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	in, err := port.Decode(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if bytes.Contains(in.ReissueBlob(), seed) {
		t.Errorf("seed leaked into the blob the authority sees")
	}
	if !bytes.Equal(port.Seed(), seed) {
		t.Errorf("port lost its own seed")
	}
	if got := in.Commitment(); got != CommitmentOf(seed, 0, cashu.Name()) {
		t.Errorf("commitment = %q, want the derived value for counter 0", got)
	}

	// Issuing already advanced the counter (persisted before exposure).
	if got := port.Counter(cashu.Name()); got != 1 {
		t.Errorf("counter = %d, want 1 (reserved before the authority saw it)", got)
	}
	port.PersistCounterBeforeExposure(cashu.Name(), 1, 5)
	if got := port.Counter(cashu.Name()); got != 5 {
		t.Errorf("counter = %d, want 5", got)
	}
	// Monotonic: a lower reservation never rewinds it, because re-deriving an
	// exposed range is the #257/#266/#480 bug class.
	port.PersistCounterBeforeExposure(cashu.Name(), 0, 2)
	if got := port.Counter(cashu.Name()); got != 5 {
		t.Errorf("counter rewound to %d, want 5", got)
	}
}

// Rule 11: one spend path, three backends. The target key is namespaced so a
// federation id and a mint URL can never collide.
func TestSpendIsUniformAcrossBackends(t *testing.T) {
	port, cashu, fedi, pow := demoPort()

	for _, a := range []Authority{cashu, fedi, pow} {
		if _, err := port.IssueInto(a.Name(), 500); err != nil {
			t.Fatalf("issue %s: %v", a.Name(), err)
		}
	}

	for _, a := range []Authority{cashu, fedi, pow} {
		out, err := port.Spend(200, a.Name())
		if err != nil {
			t.Fatalf("spend on %s: %v", a.Name(), err)
		}
		if out == "" {
			t.Errorf("spend on %s returned an empty instrument", a.Name())
		}
		if got := port.BalanceByAuthority(a.Name()); got != 300 {
			t.Errorf("balance on %s = %d, want 300", a.Name(), got)
		}
	}

	// Overdrawing is refused and does not move the balance.
	before := port.BalanceByAuthority(cashu.Name())
	if _, err := port.Spend(10_000, cashu.Name()); !errors.Is(err, ErrInsufficientFunds) {
		t.Errorf("overdraw err = %v, want ErrInsufficientFunds", err)
	}
	if after := port.BalanceByAuthority(cashu.Name()); after != before {
		t.Errorf("overdraw moved the balance: %d -> %d", before, after)
	}
}

// Rule 12: the capability matrix is declared, not discovered later. This is
// exactly what the existing manifests already encode as nut_07/nut_09, made
// backend-neutral.
func TestCapabilityMatrixIsExplicit(t *testing.T) {
	port, cashu, fedi, pow := demoPort()
	caps := map[string]Capability{}
	for _, c := range port.Capabilities() {
		caps[c.Authority] = c
	}

	if got := caps[cashu.Name()]; got.StateCheck != CheckModeQuery || got.Recover != RecoverModeIssuanceLog {
		t.Errorf("cashu caps = %+v", got)
	}
	if got := caps[fedi.Name()]; got.StateCheck != CheckModeProbe || got.Recover != RecoverModeAuthorityBackup {
		t.Errorf("fedi caps = %+v", got)
	}
	if got := caps[pow.Name()]; got.StateCheck != CheckModeNone || got.Recover != RecoverModeAccountLedger {
		t.Errorf("pow caps = %+v", got)
	}
}

// Rule 13: an instrument no registered authority has issued is refused
// outright. The port will not present an unattributable instrument as if it had
// a state.
func TestForeignIssuedInstrumentIsRefused(t *testing.T) {
	port, _, _, _ := demoPort()

	outside := NewCashuAuthority("https://someone-elses-mint.example")
	blob := outside.Issue([]byte("another-holders-seed"), 0, 50)

	if _, err := port.AcquireRaw(blob); !errors.Is(err, ErrNotIssued) {
		t.Errorf("foreign instrument err = %v, want ErrNotIssued", err)
	}
	if got := port.Balance(); got != 0 {
		t.Errorf("foreign instrument credited %d", got)
	}
}

// Rule 14: recovery is scoped to the holder. A wrong seed recovers nothing, so
// it cannot be used to claim another holder's outputs.
func TestRecoveryWithWrongSeedYieldsNothing(t *testing.T) {
	port, cashu, _, _ := demoPort()

	if _, err := port.IssueInto(cashu.Name(), 400); err != nil {
		t.Fatalf("issue: %v", err)
	}
	mine, err := port.Recover(port.Seed())
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if mine[cashu.Name()] != 400 {
		t.Fatalf("own recovery = %d, want 400", mine[cashu.Name()])
	}

	theirs, err := port.Recover([]byte("someone-elses-seed"))
	if err != nil {
		t.Fatalf("recover other: %v", err)
	}
	if got := theirs[cashu.Name()]; got != 0 {
		t.Errorf("wrong seed recovered %d, want 0", got)
	}
	if got := port.BalanceByAuthority(cashu.Name()); got != 0 {
		t.Errorf("wrong seed left a balance of %d", got)
	}
}
