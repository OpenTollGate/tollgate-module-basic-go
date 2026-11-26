package lnurlcash

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- test doubles ---------------------------------------------------------

// fakeIssuer is the LUD-03 issuer: it serves the withdrawRequest, records every
// redemption callback it receives, and answers with whatever the test planted.
type fakeIssuer struct {
	srv *httptest.Server

	mu        sync.Mutex
	callbacks int
	// lastQuery is the query string of the last redemption callback, i.e. what
	// the gate actually asked the issuer to do.
	lastQuery string
	// answer decides the redemption answer; nil means "OK".
	answer func(w http.ResponseWriter, r *http.Request)
	// withdraw decides the withdrawRequest answer; nil means a standard one
	// pointing back at this same server.
	withdraw func(w http.ResponseWriter, r *http.Request)
	k1       string
	maxMsat  uint64
}

func newFakeIssuer(t *testing.T, k1 string, maxMsat uint64) *fakeIssuer {
	t.Helper()
	f := &fakeIssuer{k1: k1, maxMsat: maxMsat}
	mux := http.NewServeMux()
	mux.HandleFunc("/withdraw", func(w http.ResponseWriter, r *http.Request) {
		if f.withdraw != nil {
			f.withdraw(w, r)
			return
		}
		writeJSON(t, w, map[string]any{
			"tag":             "withdrawRequest",
			"callback":        f.srv.URL + "/withdraw/cb",
			"k1":              f.k1,
			"maxWithdrawable": f.maxMsat,
			"minWithdrawable": 1000,
		})
	})
	mux.HandleFunc("/withdraw/cb", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.callbacks++
		f.lastQuery = r.URL.RawQuery
		f.mu.Unlock()
		if f.answer != nil {
			f.answer(w, r)
			return
		}
		writeJSON(t, w, map[string]any{"status": "OK"})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIssuer) note() string { return f.srv.URL + "/withdraw?k1=" + f.k1 }
func (f *fakeIssuer) callbackCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.callbacks
}

// fakeGate is the settlement half: the gate's own accepted mint. It hands out a
// NUT-04 quote (invoice) per request and reports the quote's settlement state.
type fakeGate struct {
	mu       sync.Mutex
	quotes   int
	invoices []string
	mint     string
	// quoteErr, when set, makes the invoice request fail. It is how a test reaches
	// the "nothing was promised, the note is still good" state.
	quoteErr error
	// settle decides the state a quote reports. nil means "never paid".
	settle func(quoteID string) (paid bool, granted bool, err error)
}

func (g *fakeGate) RequestRedeemInvoice(macAddress, mintURL string, amountSats uint64) (string, string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.quoteErr != nil {
		return "", "", g.quoteErr
	}
	g.quotes++
	id := fmt.Sprintf("quote-%d", g.quotes)
	inv := fmt.Sprintf("lnbc%dquote%d", amountSats, g.quotes)
	g.invoices = append(g.invoices, inv)
	return id, inv, nil
}

func (g *fakeGate) RedemptionSettled(quoteID, macAddress string) (bool, bool, error) {
	if g.settle != nil {
		return g.settle(quoteID)
	}
	// Default: the mint confirms the quote and the grant is applied. A test
	// that cares about the difference says so.
	return true, true, nil
}

func (g *fakeGate) SettlementMint() (string, error) { return g.mint, nil }
func (g *fakeGate) quoteCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.quotes
}
func (g *fakeGate) invoiceList() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.invoices...)
}

// --- harness --------------------------------------------------------------

const testMint = "https://mint.example"

type harness struct {
	svc    *Redeemer
	issuer *fakeIssuer
	gate   *fakeGate
	dir    string
}

func newHarness(t *testing.T, cfg Config) *harness {
	t.Helper()
	issuer := newFakeIssuer(t, "k1secret", 10_000_000) // 10 000 sats
	gate := &fakeGate{mint: testMint}
	dir := t.TempDir()
	store, err := OpenIntentStore(filepath.Join(dir, "lnurlcash-intents.json"))
	if err != nil {
		t.Fatalf("OpenIntentStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	callerSetCeiling := cfg.MaxNoteSats != 0
	if cfg.MaxNoteSats == 0 && !cfg.Enabled {
		cfg = DefaultConfig()
	}
	// The harness note is worth 10,000 sats. Default the ceiling above it, so the
	// tests that are about crash recovery and duplicate presentation are not
	// failing on the (correct) refusal of a note above the ceiling. A caller that
	// wants a ceiling sets MaxNoteSats itself.
	if !callerSetCeiling {
		cfg.MaxNoteSats = 100_000
	}
	// Every harness that does not explicitly ask for the disabled path opts in:
	// the disabled path has its own test.
	cfg.Enabled = true
	svc := NewRedeemer(cfg, gate, store, NewClient(ClientOptions{
		AllowInsecureLoopbackForTests: true,
		Timeout:                       testTimeout,
	}))
	svc.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	return &harness{svc: svc, issuer: issuer, gate: gate, dir: dir}
}

// --- the happy path -------------------------------------------------------

func TestRedeemAsksTheIssuerToPayOurMintInvoice(t *testing.T) {
	h := newHarness(t, Config{})

	res, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff")
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if res.State != StateRedeemed {
		t.Fatalf("state = %q, want %q", res.State, StateRedeemed)
	}
	if res.QuoteID == "" || res.Invoice == "" {
		t.Fatalf("result carries no quote/invoice for the portal to poll: %+v", res)
	}
	if h.issuer.callbackCount() != 1 {
		t.Fatalf("issuer saw %d callbacks, want 1", h.issuer.callbackCount())
	}
	// The invoice in the callback must be the one OUR mint issued — that is the
	// whole settlement argument: the issuer burns the note into our mint.
	if got, want := queryValue(h.issuer.lastQuery, "pr"), res.Invoice; got != want {
		t.Fatalf("callback carried pr=%q, want our mint's invoice %q", got, want)
	}
	if got := queryValue(h.issuer.lastQuery, "k1"); got != "k1secret" {
		t.Fatalf("callback carried k1=%q", got)
	}
}

// A redemption is not a grant. The mint is authoritative for value, so an
// issuer that says OK while the mint never reports the quote paid must leave the
// customer ungranted.
func TestIssuerAcceptedButMintUnpaidGrantsNothing(t *testing.T) {
	h := newHarness(t, Config{})
	h.gate.settle = func(string) (bool, bool, error) { return false, false, nil }

	res, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff")
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if res.Granted {
		t.Fatal("granted access on the issuer's word alone")
	}
	if res.State != StateRedeemed {
		t.Fatalf("state = %q, want %q (issuer accepted, settlement pending)", res.State, StateRedeemed)
	}
}

// --- refusals -------------------------------------------------------------

func TestIssuerRefusalGrantsNothing(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
	}{
		{name: "spent", body: map[string]any{"status": "ERROR", "reason": "this note has already been withdrawn"}},
		{name: "expired", body: map[string]any{"status": "ERROR", "reason": "withdraw link expired"}},
		{name: "unknown k1", body: map[string]any{"status": "ERROR", "reason": "unknown k1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, Config{})
			h.issuer.answer = func(w http.ResponseWriter, r *http.Request) { writeJSON(t, w, tt.body) }
			h.gate.settle = func(string) (bool, bool, error) { return true, true, nil }

			res, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff")
			if err != nil {
				t.Fatalf("Redeem: %v", err)
			}
			if res.Granted || res.QuoteID != "" {
				t.Fatalf("refused note produced a payable quote or a grant: %+v", res)
			}
			if res.State != StateAbandoned {
				t.Fatalf("state = %q, want %q", res.State, StateAbandoned)
			}
		})
	}
}

// A callback that never answers is ambiguous, not a failure: the issuer may have
// paid our invoice. It must not be retried, and a second presentation of the
// same note must not produce a second callback.
func TestCallbackTimeoutIsAmbiguousAndNeverRetried(t *testing.T) {
	h := newHarness(t, Config{RedemptionTimeoutSeconds: 1})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	h.issuer.answer = func(w http.ResponseWriter, r *http.Request) {
		<-release // hold the callback open past the redemption deadline
	}

	res, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff")
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if res.Granted {
		t.Fatalf("an ambiguous redemption produced a grant: %+v", res)
	}
	if res.State != StateAmbiguous {
		t.Fatalf("state = %q, want %q", res.State, StateAmbiguous)
	}

	// Second presentation: refused from the journal, with NO new callback and NO
	// new quote. This is the invariant that makes a timeout survivable.
	before := h.issuer.callbackCount()
	again, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff")
	if err != nil {
		t.Fatalf("second Redeem: %v", err)
	}
	if again.State != StateAmbiguous {
		t.Fatalf("second presentation state = %q, want %q", again.State, StateAmbiguous)
	}
	if h.issuer.callbackCount() != before {
		t.Fatalf("a second presentation re-sent the callback (%d -> %d): a spent-note retry is a blind retry",
			before, h.issuer.callbackCount())
	}
	if h.gate.quoteCount() != 1 {
		t.Fatalf("a second presentation asked the mint for another quote (%d total)", h.gate.quoteCount())
	}
}

// Eight parallel presentations of ONE note must produce one redemption, not
// eight. The journal alone cannot stop this: each request can read "no record"
// before any of them writes one, so without an in-memory claim the gate opens one
// mint quote per request — eight invoices for one note, against a quote table
// capped at three per MAC.
func TestConcurrentPresentationsOfOneNoteRedeemOnce(t *testing.T) {
	h := newHarness(t, Config{})
	h.gate.settle = func(string) (bool, bool, error) { return true, true, nil }

	// Hold the callback open so all eight really do overlap.
	gate := make(chan struct{})
	h.issuer.answer = func(w http.ResponseWriter, r *http.Request) {
		<-gate
		writeJSON(t, w, map[string]any{"status": "OK"})
	}

	const n = 8
	var wg sync.WaitGroup
	results := make([]Result, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff")
		}(i)
	}
	close(start)

	// Let the one real redemption proceed.
	time.Sleep(100 * time.Millisecond)
	close(gate)
	wg.Wait()

	if got := h.gate.quoteCount(); got != 1 {
		t.Fatalf("asked the mint for %d quotes, want 1: concurrent presentations each opened one", got)
	}
	if got := h.issuer.callbackCount(); got != 1 {
		t.Fatalf("sent %d callbacks, want 1", got)
	}

	granted := 0
	for i := 0; i < n; i++ {
		if results[i].Granted {
			granted++
		}
		// The losers must be told something actionable, not left with an empty state.
		if !results[i].Granted && results[i].Message == "" && errs[i] == nil {
			t.Fatalf("loser %d got neither a grant nor a message: %+v", i, results[i])
		}
	}
	if granted != 1 {
		t.Fatalf("%d of %d presentations reported a grant, want exactly 1", granted, n)
	}
}

// The lab escape hatch must be unreachable from ordinary client options: a client
// built the way the daemon builds it in production still refuses a plain-http
// hostname AND a private address. This is the fence on TOLLGATE_ALLOW_PLAINTEXT_ISSUERS
// — the switch exists, but only the explicitly-named option turns it on.
func TestDefaultClientRefusesPlaintextAndPrivateHosts(t *testing.T) {
	// Default options, as every production path builds them.
	c := NewClient(ClientOptions{Timeout: testTimeout})

	if _, err := decodeNote("http://issuer.example/withdraw?k1=abc", forClient(c)); err == nil {
		t.Fatal("the default client accepted a plain-http hostname")
	}

	// And the dial guard still refuses a private address even with the scheme
	// relaxed, unless the lab flag is the thing doing the relaxing.
	lab := NewClient(ClientOptions{Timeout: testTimeout, PlainTextIssuersForTests: true})
	if err := lab.validateURL(mustURL(t, "http://172.28.0.12:8099/withdraw")); err != nil {
		t.Fatalf("the lab client should accept a lab-bridge http issuer: %v", err)
	}
	if err := c.validateURL(mustURL(t, "http://172.28.0.12:8099/withdraw")); err == nil {
		t.Fatal("a production client accepted a private-address http issuer")
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return u
}

// A note is single-use, and the grant lands on ONE device. If a second client
// presents a note that was already redeemed for a different device, telling that
// second client "granted" is a false report: it has no session, and a portal acting
// on it will let the customer sit at a captive portal that never opens. The
// journal knows who the grant was for; this asserts we actually consult it.
func TestSecondClientOnARedeemedNoteIsNotToldItWasGranted(t *testing.T) {
	h := newHarness(t, Config{})
	h.gate.settle = func(string) (bool, bool, error) { return true, true, nil }

	const aliceMAC = "aa:aa:aa:aa:aa:aa"
	const bobMAC = "bb:bb:bb:bb:bb:bb"

	first, err := h.svc.Redeem(context.Background(), h.issuer.note(), aliceMAC)
	if err != nil {
		t.Fatalf("first Redeem: %v", err)
	}
	if !first.Granted {
		t.Fatalf("the first presentation should have been granted: %+v", first)
	}

	// Bob presents the SAME note, from a different device.
	second, err := h.svc.Redeem(context.Background(), h.issuer.note(), bobMAC)
	if err != nil {
		t.Fatalf("second Redeem: %v", err)
	}
	if second.Granted {
		t.Fatalf("client %s was told granted=true for a note redeemed for %s: "+
			"a false grant report sends the customer to a portal that will never open",
			bobMAC, aliceMAC)
	}
	if second.Message == "" {
		t.Fatal("a client that was not granted must be told why")
	}
	// Still only one callback and one quote: the journal decided, not the network.
	if got := h.issuer.callbackCount(); got != 1 {
		t.Fatalf("issuer saw %d callbacks, want 1", got)
	}
	if got := h.gate.quoteCount(); got != 1 {
		t.Fatalf("mint was asked for %d quotes, want 1", got)
	}
}

// The original grantee asking again IS still granted — the answer is idempotent for
// the device that owns the grant, so a portal retry does not look like a failure.
func TestTheOriginalGranteeIsStillToldGrantedOnRePresentation(t *testing.T) {
	h := newHarness(t, Config{})
	h.gate.settle = func(string) (bool, bool, error) { return true, true, nil }

	const mac = "aa:aa:aa:aa:aa:aa"
	if _, err := h.svc.Redeem(context.Background(), h.issuer.note(), mac); err != nil {
		t.Fatalf("first Redeem: %v", err)
	}
	again, err := h.svc.Redeem(context.Background(), h.issuer.note(), mac)
	if err != nil {
		t.Fatalf("second Redeem: %v", err)
	}
	if !again.Granted {
		t.Fatalf("the device that owns the grant was told granted=false: %+v", again)
	}
}

// The ceiling must be enforced against the amount we are actually going to INVOICE.
//
// The pending-resume path keeps the JOURNALLED amount, because a second fetch of the
// issuer's claim may say anything. That is correct on its own — but it means the
// ceiling cannot be checked only against the live claim: if the issuer now claims
// LESS than it did, the live check passes and the code then invoices the larger
// journalled amount. The customer's 50,000-sat note gets melted against a ceiling the
// operator set to 1,000, which is precisely the partial-burn class this gate exists
// to prevent.
func TestCeilingIsEnforcedAgainstTheAmountWeInvoiceNotTheLiveClaim(t *testing.T) {
	h := newHarness(t, Config{MaxNoteSats: 100_000}) // note is 10,000 sats
	h.gate.settle = func(string) (bool, bool, error) { return true, true, nil }

	// First attempt: the invoice request fails, leaving a PENDING record journalled at
	// the note's full 10,000 sats. Nothing was promised to the issuer.
	h.gate.quoteErr = fmt.Errorf("mint refused to quote for now")
	if _, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff"); err == nil {
		t.Fatal("expected the first attempt to fail at the invoice step")
	}

	// The issuer now claims the note is worth only 500 sats (it may say anything), and
	// the operator has lowered the ceiling to 1,000. The LIVE claim passes the ceiling
	// check; the journalled amount does not.
	h.gate.quoteErr = nil
	h.svc.cfg.MaxNoteSats = 1_000
	h.issuer.mu.Lock()
	h.issuer.maxMsat = 500_000 // 500 sats
	h.issuer.mu.Unlock()

	res, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff")
	if err == nil {
		t.Fatal("a journalled amount above the ceiling should be refused, however small the live claim")
	}
	if res.Granted {
		t.Fatal("granted a note whose journalled amount is above the current ceiling")
	}
	if n := len(h.gate.invoiceList()); n != 0 {
		t.Fatalf("an invoice was requested for an amount above the ceiling: %v", h.gate.invoiceList())
	}
	if got := h.issuer.callbackCount(); got != 0 {
		t.Fatalf("the issuer was asked to melt a note above the ceiling (%d callbacks)", got)
	}
}

// A note's journal key must be its IDENTITY, not its spelling. Two spellings of one
// note that differ only in host case, a default port, or a harmless extra query
// parameter are the SAME note to the issuer — and the issuer will burn it once. If
// they hash differently they become two journal records, and "a second presentation
// never grants twice" stops being true: an attacker only has to change the spelling
// to get a second grant out of an issuer that pays on each presentation.
func TestOneNoteHasOneJournalKeyAcrossURLSpellings(t *testing.T) {
	base := "https://issuer.example/withdraw?k1=abc123"
	spellings := []string{
		base,
		"https://ISSUER.example/withdraw?k1=abc123",         // host case
		"https://Issuer.Example/withdraw?k1=abc123",         // host case again
		"https://issuer.example:443/withdraw?k1=abc123",     // default port
		"https://issuer.example/withdraw?k1=abc123&x=1",     // extra param
		"https://issuer.example/withdraw?x=1&k1=abc123",     // param order
		"https://issuer.example/withdraw?k1=abc123&x=1&y=2", // more extra params
	}

	keys := map[string]string{}
	for _, s := range spellings {
		n, err := decodeNote(s, relaxScheme{})
		if err != nil {
			t.Fatalf("decodeNote(%q): %v", s, err)
		}
		fp := Fingerprint(n)
		if prev, seen := keys[fp]; seen {
			// Same key for two spellings is the goal; remember the first spelling.
			_ = prev
			continue
		}
		keys[fp] = s
	}
	if len(keys) != 1 {
		for fp, s := range keys {
			t.Logf("  key %s <- %s", fp[:16], s)
		}
		t.Fatalf("%d distinct journal keys for one note across spellings, want 1: "+
			"changing the spelling would buy a second redemption", len(keys))
	}

	// And the behaviour, not just the key: redeem under one spelling, then present the
	// same note with a harmless extra query parameter. The journal must answer, with no
	// second callback and no second invoice.
	issuer := newFakeIssuer(t, "k1secret", 10_000_000)
	h := newHarness(t, Config{})
	if _, err := h.svc.Redeem(context.Background(), issuer.note(), "aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatalf("first Redeem: %v", err)
	}
	beforeCallbacks := issuer.callbackCount()
	beforeQuotes := h.gate.quoteCount()

	respelled := issuer.note() + "&utm_source=wallet"
	if _, err := h.svc.Redeem(context.Background(), respelled, "aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatalf("second Redeem: %v", err)
	}
	if got := issuer.callbackCount(); got != beforeCallbacks {
		t.Fatalf("a re-spelled note produced a second callback (%d -> %d): the journal did not decide",
			beforeCallbacks, got)
	}
	if got := h.gate.quoteCount(); got != beforeQuotes {
		t.Fatalf("a re-spelled note opened a second mint quote (%d -> %d)", beforeQuotes, got)
	}
}

// The lab flag must not become an arbitrary-host dialler. It exists so a fake issuer
// on the lab bridge is reachable; it must NOT reach loopback (this router's own admin
// API) or the wider LAN. Review round 3 measured that a blanket skip did exactly that:
// with only PlainTextIssuersForTests set, a note pointing at 127.0.0.1 was dialled.
func TestLabFlagDoesNotReachLoopbackOrTheWiderLAN(t *testing.T) {
	// The lab bridge itself is allowed — that is the entire purpose of the flag.
	if !isLabBridgeAddress(mustParseIP(t, "172.28.0.12")) {
		t.Fatal("the lab bridge subnet should be reachable under the lab flag")
	}

	// Every other interesting address is NOT the lab bridge, so the lab flag does not
	// exempt it and the ordinary dial guard decides — which refuses all of these.
	for _, addr := range []string{
		"127.0.0.1",    // loopback: the router's own admin surface
		"::1",          // loopback v6
		"192.168.1.1",  // the LAN
		"10.0.0.1",     // private
		"100.64.0.1",   // CGNAT — the range Go's IsPrivate misses
		"169.254.1.1",  // link-local
		"172.29.0.1",   // private, but NOT the lab bridge
		"172.28.0.300", // malformed, parses to nil -> must not be a lab address
	} {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue // malformed literals cannot be dialled at all
		}
		if isLabBridgeAddress(ip) {
			t.Fatalf("%s is treated as the lab bridge; the flag would allow it", addr)
		}
	}

	// And the behaviour, through the real dial guard: a lab client must still refuse a
	// hostname that resolves to LOOPBACK. This is the exact case round 3 measured as
	// reachable — the router's own admin API, from a note.
	//
	// `localhost` and not a made-up name: the guard runs AFTER resolution, so a name that
	// fails to resolve would pass this assertion for the wrong reason and prove nothing.
	dial := labDialFunc(t)
	for _, host := range []string{"localhost:8099", "127.0.0.1:8099"} {
		if _, err := dial(context.Background(), "tcp", host); err == nil {
			t.Fatalf("the lab flag let a note dial %s: that is loopback, i.e. this router's own admin API", host)
		} else if !strings.Contains(err.Error(), "blocked address") {
			t.Fatalf("dialling %s failed for the wrong reason (%v): the ADDRESS guard did not refuse it", host, err)
		}
	}

	// The lab bridge, by contrast, must be dialable in principle (it is where the fake
	// issuer lives) — assert the guard does not refuse it by CLASS.
	if blockedAddress(mustParseIP(t, "172.28.0.12")) && !isLabBridgeAddress(mustParseIP(t, "172.28.0.12")) {
		t.Fatal("the lab bridge address is blocked and not exempted: the lane could never run")
	}
}

// labDialFunc extracts the client's real DialContext so the guard's decision can be
// exercised without any network traffic leaving the box.
func labDialFunc(t *testing.T) func(context.Context, string, string) (net.Conn, error) {
	t.Helper()
	c := NewClient(ClientOptions{Timeout: testTimeout, PlainTextIssuersForTests: true})
	tr, ok := c.http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", c.http.Transport)
	}
	return tr.DialContext
}

func mustParseIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("net.ParseIP(%q) = nil", s)
	}
	return ip
}

// The message a customer sees must match WHO caused the refusal. Telling them the
// issuer failed when the operator's ceiling is the reason sends them back to retry a
// note this gate will never accept, and makes the operator look like the victim of a bad
// issuer rather than the author of the limit.
func TestRefusalMessageBlamesTheRightParty(t *testing.T) {
	h := newHarness(t, Config{MaxNoteSats: 1_000}) // harness note is 10,000 sats

	res, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff")
	if err == nil {
		t.Fatal("expected the ceiling refusal")
	}
	if !errors.Is(err, ErrNoteAboveCeiling) {
		t.Fatalf("error = %v, want ErrNoteAboveCeiling", err)
	}
	if strings.Contains(strings.ToLower(res.Message), "issuer") {
		t.Fatalf("an operator-caused refusal blames the issuer: %q", res.Message)
	}
	if res.Message == "" {
		t.Fatal("a refusal must say something")
	}

	// And the opposite: a genuine issuer failure must still be attributed to the issuer.
	h2 := newHarness(t, Config{})
	h2.issuer.withdraw = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}
	res2, _ := h2.svc.Redeem(context.Background(), h2.issuer.note(), "aa:bb:cc:dd:ee:ff")
	if !strings.Contains(strings.ToLower(res2.Message), "issuer") {
		t.Fatalf("an issuer-caused refusal does not mention the issuer: %q", res2.Message)
	}
}

// --- duplicate presentation ----------------------------------------------

func TestDuplicatePresentationGrantsOnce(t *testing.T) {
	h := newHarness(t, Config{})
	h.gate.settle = func(string) (bool, bool, error) { return true, true, nil }

	first, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff")
	if err != nil {
		t.Fatalf("first Redeem: %v", err)
	}
	if first.State != StateRedeemed {
		t.Fatalf("first state = %q, want %q", first.State, StateRedeemed)
	}

	for i := 0; i < 3; i++ {
		again, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff")
		if err != nil {
			t.Fatalf("repeat Redeem %d: %v", i, err)
		}
		if again.State != StateRedeemed {
			t.Fatalf("repeat %d state = %q, want the recorded %q", i, again.State, StateRedeemed)
		}
		if again.QuoteID != first.QuoteID {
			t.Fatalf("repeat %d produced a new quote %q (first %q) — one note, one redemption",
				i, again.QuoteID, first.QuoteID)
		}
	}
	if h.issuer.callbackCount() != 1 {
		t.Fatalf("issuer saw %d callbacks for one note, want 1", h.issuer.callbackCount())
	}
	if h.gate.quoteCount() != 1 {
		t.Fatalf("mint saw %d quote requests for one note, want 1", h.gate.quoteCount())
	}
}

// A different spelling of the same note (surrounding whitespace) is the same
// note: the fingerprint is taken over the canonical form, so a customer who
// pastes it twice with a stray newline cannot be granted twice.
func TestNoteFingerprintIgnoresSurroundingWhitespace(t *testing.T) {
	h := newHarness(t, Config{})
	if _, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatalf("first Redeem: %v", err)
	}
	if _, err := h.svc.Redeem(context.Background(), "  "+h.issuer.note()+"\n", "aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatalf("second Redeem: %v", err)
	}
	if h.issuer.callbackCount() != 1 {
		t.Fatalf("whitespace variant of one note produced %d callbacks, want 1", h.issuer.callbackCount())
	}
}

// --- crash between intent and callback ------------------------------------

// The point of no return is the callback. An intent that is durable but whose
// callback was never issued is recoverable: nothing was sent, the note is
// untouched, and the attempt may proceed exactly once.
func TestCrashBeforeCallbackIsRecoverable(t *testing.T) {
	h := newHarness(t, Config{})

	// Plant the state a crash between "intent durable" and "callback sent" leaves
	// behind, exactly as the store writes it.
	note, err := decodeNote(h.issuer.note(), relaxScheme{allowLoopback: true})
	if err != nil {
		t.Fatalf("DecodeNote: %v", err)
	}
	fp := h.svc.Fingerprint(note)
	if _, err := h.svc.store.Begin(fp, Intent{
		MacAddress: "aa:bb:cc:dd:ee:ff",
		MintURL:    testMint,
		AmountSats: 1000,
		IssuerHost: "issuer.example",
	}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	// A real crash closes the process; reopening the store is what a restart
	// does. The pending record must survive and must not read as a redemption.
	if err := h.svc.store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	store, err := OpenIntentStore(filepath.Join(h.dir, "lnurlcash-intents.json"))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	h.svc.store = store

	res, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff")
	if err != nil {
		t.Fatalf("Redeem after recovery: %v", err)
	}
	if res.State != StateRedeemed {
		t.Fatalf("state after recovery = %q, want %q — a pending intent means nothing was sent, so the attempt may proceed",
			res.State, StateRedeemed)
	}
	if h.issuer.callbackCount() != 1 {
		t.Fatalf("issuer saw %d callbacks, want exactly 1 (the crash must not double-send, nor block)", h.issuer.callbackCount())
	}
}

// A crash AFTER the callback was issued is a different matter: the issuer may
// have paid. That record can never be retried, and it must never grant on a
// guess either.
func TestCrashAfterCallbackIsNeverRetried(t *testing.T) {
	h := newHarness(t, Config{})
	note, err := decodeNote(h.issuer.note(), relaxScheme{allowLoopback: true})
	if err != nil {
		t.Fatalf("DecodeNote: %v", err)
	}
	fp := h.svc.Fingerprint(note)
	if _, err := h.svc.store.Begin(fp, Intent{
		MacAddress: "aa:bb:cc:dd:ee:ff",
		MintURL:    testMint,
		AmountSats: 1000,
		IssuerHost: "issuer.example",
	}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := h.svc.store.MarkSubmitted(fp, "quote-crashed"); err != nil {
		t.Fatalf("MarkSubmitted: %v", err)
	}

	h.gate.settle = func(string) (bool, bool, error) { return false, false, nil }

	res, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff")
	if err != nil {
		t.Fatalf("Redeem after crash: %v", err)
	}
	if h.issuer.callbackCount() != 0 {
		t.Fatalf("re-sent a callback for an already-submitted redemption (%d)", h.issuer.callbackCount())
	}
	if res.Granted {
		t.Fatal("granted from a submitted-but-unresolved record")
	}
	if res.State != StateAmbiguous {
		t.Fatalf("state = %q, want %q (reconcile, never retry)", res.State, StateAmbiguous)
	}
}

// --- SSRF ----------------------------------------------------------------

func TestCallbackHostMustMatchTheNoteHost(t *testing.T) {
	h := newHarness(t, Config{})
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the gate dialled a host the note did not name: %s", r.URL)
		writeJSON(t, w, map[string]any{"status": "OK"})
	}))
	t.Cleanup(elsewhere.Close)

	h.issuer.withdraw = func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"tag":             "withdrawRequest",
			"callback":        elsewhere.URL + "/cb",
			"k1":              "k1secret",
			"maxWithdrawable": 10_000_000,
		})
	}

	res, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff")
	if err == nil {
		t.Fatalf("accepted a callback on a foreign host: %+v", res)
	}
	if !strings.Contains(err.Error(), "host") {
		t.Fatalf("error = %q, want a host mismatch refusal", err)
	}
	if h.gate.quoteCount() != 0 {
		t.Fatalf("asked the mint for a quote before validating the callback host (%d quotes)", h.gate.quoteCount())
	}
}

// --- amount, cap, and pricing -------------------------------------------

func TestRedeemableAmountIsCappedByTheNoteNotTruncatedByTheCeiling(t *testing.T) {
	// The note's value is what we invoice. The operator's ceiling decides whether
	// the note is ACCEPTABLE, never how much of it we swallow: LUD-03 has no
	// partial withdraw, so melting a 100,000-sat note into a 1,000-sat invoice
	// destroys 99,000 sats of the customer's money. That is why the ceiling is a
	// refusal threshold here and not a clamp.
	issuer := newFakeIssuer(t, "k1secret", 2_000_000) // 2,000 sat note
	gate := &fakeGate{mint: testMint}
	store, err := OpenIntentStore(filepath.Join(t.TempDir(), "i.json"))
	if err != nil {
		t.Fatalf("OpenIntentStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := NewRedeemer(Config{Enabled: true, MaxNoteSats: 10_000, RedemptionTimeoutSeconds: 5},
		gate, store, NewClient(ClientOptions{AllowInsecureLoopbackForTests: true, Timeout: testTimeout}))

	res, err := svc.Redeem(context.Background(), issuer.note(), "aa:bb:cc:dd:ee:ff")
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if len(gate.invoiceList()) != 1 || gate.invoiceList()[0] != "lnbc2000quote1" {
		t.Fatalf("mint was asked for %v, want the note's full 2000 sats", gate.invoiceList())
	}
	if res.AmountSats != 2000 {
		t.Fatalf("result amount = %d sats, want 2000", res.AmountSats)
	}
}

// A note worth MORE than the operator accepts must be refused BEFORE any network
// call, because the alternative is burning it for less than it holds.
func TestNoteAboveTheCeilingIsRefusedBeforeAnyNetworkCall(t *testing.T) {
	issuer := newFakeIssuer(t, "k1secret", 100_000_000) // 100,000 sat
	gate := &fakeGate{mint: testMint}
	store, err := OpenIntentStore(filepath.Join(t.TempDir(), "i.json"))
	if err != nil {
		t.Fatalf("OpenIntentStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := NewRedeemer(Config{Enabled: true, MaxNoteSats: 1_000, RedemptionTimeoutSeconds: 5},
		gate, store, NewClient(ClientOptions{AllowInsecureLoopbackForTests: true, Timeout: testTimeout}))

	res, err := svc.Redeem(context.Background(), issuer.note(), "aa:bb:cc:dd:ee:ff")
	if err == nil {
		t.Fatal("a note above the ceiling should be refused with a reason")
	}
	if len(gate.invoiceList()) != 0 {
		t.Fatalf("an invoice was requested for a note we refuse: %v — that burns the note", gate.invoiceList())
	}
	if issuer.callbackCount() != 0 {
		t.Fatal("the issuer was asked to melt a note we will not honour")
	}
	if res.Granted {
		t.Fatal("granted on a note above the ceiling")
	}
	if res.State != StateAbandoned {
		t.Fatalf("state=%s, want abandoned (nothing was promised)", res.State)
	}
	if res.Message == "" {
		t.Fatal("a refused note must tell the customer why, or they will retry forever")
	}
}

// A note worth less than one satoshi cannot buy anything, and asking for a
// zero-value invoice is not a request any mint should receive.
// The withdraw path is where a transport error is RETURNED (the callback path
// turns one into ambiguity), so it is where the note URL — and its `?k1=` — would
// reach the log through refuse(). An unreachable issuer must be reported without
// quoting the URL it failed to reach.
func TestUnreachableIssuerDoesNotLeakTheNoteInItsError(t *testing.T) {
	var buf strings.Builder
	restore := captureLogs(&buf)
	defer restore()

	h := newHarness(t, Config{})

	// A host that cannot resolve: the failure is transport-level, and net/http's
	// error text would quote the full note URL, k1 included.
	note := "https://issuer.invalid/withdraw?k1=" + h.issuer.k1
	res, err := h.svc.Redeem(context.Background(), note, "aa:bb:cc:dd:ee:ff")
	if err == nil {
		t.Fatal("an unreachable issuer should surface a refusal")
	}
	if strings.Contains(err.Error(), "k1secret") {
		t.Fatalf("the error carries the note secret: %q", err.Error())
	}
	if strings.Contains(res.Message, "k1secret") {
		t.Fatalf("the refusal message carries the note secret: %q", res.Message)
	}
	if strings.Contains(buf.String(), "k1secret") {
		t.Fatalf("the log carries the note secret:\n%s", buf.String())
	}
	// Non-vacuous: the refusal really did happen for a transport reason.
	if !errors.Is(err, ErrIssuerUnreachable) && !errors.Is(err, ErrIssuerTimeout) {
		t.Fatalf("expected a transport sentinel, got %v", err)
	}
}

func TestDustNoteIsRefusedBeforeAnyNetworkCall(t *testing.T) {
	issuer := newFakeIssuer(t, "k1secret", 999)
	gate := &fakeGate{mint: testMint}
	store, err := OpenIntentStore(filepath.Join(t.TempDir(), "i.json"))
	if err != nil {
		t.Fatalf("OpenIntentStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := NewRedeemer(DefaultConfig(), gate, store,
		NewClient(ClientOptions{AllowInsecureLoopbackForTests: true, Timeout: testTimeout}))

	if _, err := svc.Redeem(context.Background(), issuer.note(), "aa:bb:cc:dd:ee:ff"); err == nil {
		t.Fatal("accepted a sub-satoshi note")
	}
	if gate.quoteCount() != 0 {
		t.Fatalf("asked the mint for a zero-value quote (%d requests)", gate.quoteCount())
	}
	if issuer.callbackCount() != 0 {
		t.Fatalf("sent a callback for a dust note (%d)", issuer.callbackCount())
	}
}

// --- the note secret never reaches a log or a response --------------------

func TestRedeemNeverLeaksTheNoteSecret(t *testing.T) {
	// Capture the package's log output (the redeemer logs through the injected
	// logger, which defaults to the standard one).
	var buf strings.Builder
	restore := captureLogs(&buf)
	defer restore()

	h := newHarness(t, Config{})
	if _, err := h.svc.Redeem(context.Background(), h.issuer.note(), "aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if strings.Contains(buf.String(), "k1secret") {
		t.Fatalf("the note secret appears in the log:\n%s", buf.String())
	}

	// And in the refusal path, which is the one an operator pastes into a ticket.
	h2 := newHarness(t, Config{})
	h2.issuer.answer = func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"status": "ERROR", "reason": "spent"})
	}
	res, err := h2.svc.Redeem(context.Background(), h2.issuer.note(), "aa:bb:cc:dd:ee:ff")
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if strings.Contains(res.Message, "k1secret") {
		t.Fatalf("the refusal message carries the note secret: %q", res.Message)
	}

	// And the TIMEOUT path, which was the leak the reviewer found: net/http's
	// *url.Error quotes the request URL, and the callback URL carries ?k1=<secret>.
	// A refusal logged by an operator pasting the error must not carry it either.
	var tbuf strings.Builder
	trestore := captureLogs(&tbuf)
	defer trestore()

	h3 := newHarness(t, Config{RedemptionTimeoutSeconds: 1})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	h3.issuer.answer = func(w http.ResponseWriter, r *http.Request) {
		<-release // hold the callback past the deadline so the client times out
	}
	tres, terr := h3.svc.Redeem(context.Background(), h3.issuer.note(), "aa:bb:cc:dd:ee:ff")
	if terr != nil {
		// The timeout is recorded as ambiguity, not surfaced as an error, but if it
		// ever is surfaced, its text must be clean too.
		if strings.Contains(terr.Error(), "k1secret") {
			t.Fatalf("the timeout error carries the note secret: %q", terr.Error())
		}
	}
	if strings.Contains(tres.Message, "k1secret") {
		t.Fatalf("the timeout message carries the note secret: %q", tres.Message)
	}
	if strings.Contains(tbuf.String(), "k1secret") {
		t.Fatalf("the timeout log carries the note secret:\n%s", tbuf.String())
	}
	// The callback URL is the thing that carries the secret; make sure the log
	// did see this path at all, so the assertion above is not vacuous.
	if h3.issuer.callbackCount() == 0 {
		t.Fatal("no callback was attempted: the timeout-path assertions above are vacuous")
	}
}

// --- config gate ---------------------------------------------------------

func TestDisabledConfigRefusesEverything(t *testing.T) {
	issuer := newFakeIssuer(t, "k1secret", 10_000_000)
	gate := &fakeGate{mint: testMint}
	store, err := OpenIntentStore(filepath.Join(t.TempDir(), "i.json"))
	if err != nil {
		t.Fatalf("OpenIntentStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := NewRedeemer(DefaultConfig(), gate, store,
		NewClient(ClientOptions{AllowInsecureLoopbackForTests: true, Timeout: testTimeout}))

	if svc.Enabled() {
		t.Fatal("DefaultConfig enables the payment method; it must ship disabled")
	}
	if _, err := svc.Redeem(context.Background(), issuer.note(), "aa:bb:cc:dd:ee:ff"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("Redeem with the feature off = %v, want ErrDisabled", err)
	}
	if issuer.callbackCount() != 0 || gate.quoteCount() != 0 {
		t.Fatal("a disabled redeemer touched the network")
	}
}

// --- helpers -------------------------------------------------------------

func queryValue(rawQuery, key string) string {
	for _, pair := range strings.Split(rawQuery, "&") {
		if k, v, ok := strings.Cut(pair, "="); ok && k == key {
			return v
		}
	}
	return ""
}
