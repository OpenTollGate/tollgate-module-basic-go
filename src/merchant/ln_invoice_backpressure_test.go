package merchant

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
)

// The merchant-side half of the `/ln-invoice` backpressure contract:
//
//   * a mint answering 429 is *busy*, not *broken* — neither a payment that hits
//     a rate limit nor a probe that sees one may remove the mint from the
//     reachable set, because on a single-mint deployment that empties the set and
//     stops all sales (the revenue DoS);
//   * a mint that stops answering at all still leaves the set, but only after a
//     consecutive-failure threshold rather than one bad probe;
//   * the in-flight quote table is bounded, and reaching the bound evicts an
//     abandoned quote instead of growing;
//   * our own outbound quote traffic toward a mint is self-limited, so the flood
//     can never be what drives the mint to answer 429 in the first place.

const quoteTestMint = "https://mint.example"

// stubQuoteWallet injects only the two wallet calls RequestLightningInvoice and
// its monitor goroutine make. Every other WalletPort method panics through the
// embedded nil interface, so untested wallet interaction cannot pass silently.
type stubQuoteWallet struct {
	tollwallet.WalletPort
	mu       sync.Mutex
	requests int
}

func (w *stubQuoteWallet) RequestMintQuote(amount uint64, mintURL string) (*tollwallet.MintQuote, error) {
	w.mu.Lock()
	w.requests++
	n := w.requests
	w.mu.Unlock()

	return &tollwallet.MintQuote{
		QuoteID: fmt.Sprintf("quote-%d", n),
		Request: fmt.Sprintf("lnbc1stub%d", n),
		State:   tollwallet.StateUnpaid,
		Amount:  amount,
		Expiry:  uint64(time.Now().Add(time.Hour).Unix()),
	}, nil
}

func (w *stubQuoteWallet) GetMintQuoteState(quoteID string) (tollwallet.MintQuoteState, error) {
	return tollwallet.StateUnpaid, nil
}

func (w *stubQuoteWallet) requestCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.requests
}

func quoteTestConfig() *config_manager.Config {
	return &config_manager.Config{
		Metric:   "milliseconds",
		StepSize: 1000,
		AcceptedMints: []config_manager.MintConfig{
			{URL: quoteTestMint, PricePerStep: 1, PriceUnit: "sat"},
		},
	}
}

func newQuoteTestMerchant(t *testing.T, wallet *stubQuoteWallet, tracker *MintHealthTracker) *Merchant {
	t.Helper()

	if tracker == nil {
		tracker = newTestTracker(quoteTestConfig(), nil)
	}
	return &Merchant{
		config:            quoteTestConfig(),
		tollwallet:        wallet,
		mintHealthTracker: tracker,
		lightningQuotes:   map[string]*lightningQuoteRecord{},
	}
}

// --- a) busy is not broken -------------------------------------------------

// rateLimitReceiveWallet answers Receive with a mint rate-limit error, the shape
// wallet/merchant code sees when a mint answers HTTP 429.
type rateLimitReceiveWallet struct {
	tollwallet.WalletPort
	receiveErr error
}

func (w *rateLimitReceiveWallet) DecodeToken(string) (tollwallet.Token, error) {
	return preflightToken{}, nil
}

func (w *rateLimitReceiveWallet) Receive(tollwallet.Token) (uint64, error) {
	return 0, w.receiveErr
}

func (w *rateLimitReceiveWallet) SwapFeeSats(tollwallet.Token) (uint64, error) { return 0, nil }

func reachableTrackerFor(t *testing.T, mintURL string) (*MintHealthTracker, *config_manager.ConfigManager) {
	t.Helper()

	cm, _ := setupTestConfigManager(t)
	cfg := cm.GetConfig()
	cfg.AcceptedMints = []config_manager.MintConfig{{URL: mintURL, PricePerStep: 1, PriceUnit: "sat"}}

	tracker := newTestTracker(cfg, nil)
	// The mint was healthy when the customer started paying; the flood has not
	// happened yet.
	tracker.mu.Lock()
	tracker.reachableMints[mintURL] = true
	tracker.reachableCount = 1
	tracker.hadReachableMint = true
	tracker.mu.Unlock()

	return tracker, cm
}

// A payment the mint answers with 429 must leave the reachable set alone: the
// mint is up and telling us to slow down. Treating that as "unreachable" empties
// the set, fires the set-changed callback and downgrades the merchant — one
// rate-limited request stops every sale on the router.
func TestReceiveRateLimitDoesNotRemoveMintFromReachableSet(t *testing.T) {
	const mintURL = "https://preflight-mint.example.com"

	stubPreflightProbe(t, func(string) (valve.ClientState, error) {
		return valve.ClientState{Registered: true}, nil
	})

	tracker, cm := reachableTrackerFor(t, mintURL)
	setChanged := make(chan struct{}, 1)
	tracker.SetOnReachableSetChanged(func() {
		select {
		case setChanged <- struct{}{}:
		default:
		}
	})

	m := &Merchant{
		config:            cm.GetConfig(),
		configManager:     cm,
		mintHealthTracker: tracker,
		lightningQuotes:   map[string]*lightningQuoteRecord{},
		tollwallet: &rateLimitReceiveWallet{
			receiveErr: errors.New("mint https://preflight-mint.example.com returned status 429: too many requests"),
		},
	}

	event, err := m.PurchaseSession("cashuAstub", "AA:BB:CC:DD:EE:FF")
	if err != nil {
		t.Fatalf("PurchaseSession returned a hard error: %v", err)
	}
	if code := noticeCode(t, event); code != "mint-rate-limited" {
		t.Fatalf("notice code = %q, want %q — a 429 must be reported as rate limiting, not as an outage", code, "mint-rate-limited")
	}

	if !tracker.IsReachable(mintURL) {
		t.Fatal("a 429 answer removed the mint from the reachable set: one rate-limited request stops every sale on the router")
	}

	select {
	case <-setChanged:
		t.Fatal("the reachable-set-changed callback fired for a 429: the merchant self-downgrades and stops selling")
	case <-time.After(150 * time.Millisecond):
	}
}

// The counterpart guard: a genuinely dead mint still leaves the reachable set, so
// the 429 exemption above cannot silently disable mint health tracking.
func TestReceiveTransportErrorStillRemovesMintFromReachableSet(t *testing.T) {
	const mintURL = "https://preflight-mint.example.com"

	stubPreflightProbe(t, func(string) (valve.ClientState, error) {
		return valve.ClientState{Registered: true}, nil
	})

	tracker, cm := reachableTrackerFor(t, mintURL)
	m := &Merchant{
		config:            cm.GetConfig(),
		configManager:     cm,
		mintHealthTracker: tracker,
		lightningQuotes:   map[string]*lightningQuoteRecord{},
		tollwallet: &rateLimitReceiveWallet{
			receiveErr: errors.New("dial tcp 203.0.113.7:443: connect: connection refused"),
		},
	}

	if _, err := m.PurchaseSession("cashuAstub", "AA:BB:CC:DD:EE:FF"); err != nil {
		t.Fatalf("PurchaseSession returned a hard error: %v", err)
	}

	if tracker.IsReachable(mintURL) {
		t.Fatal("a transport failure did not remove the mint from the reachable set: mint health tracking is broken")
	}
}

// The probe side of the same rule: a mint answering the keysets probe with 429 is
// reachable (it answered), so the proactive check must not empty the set.
func TestProbeRateLimitKeepsMintReachable(t *testing.T) {
	var throttled bool
	srv := keysetsStatusServer(t, func() int {
		if throttled {
			return http.StatusTooManyRequests
		}
		return http.StatusOK
	})

	tracker := newTestTracker(mintConfigWithURLs(srv), nil)
	tracker.RunInitialProbe()
	if !tracker.IsReachable(srv) {
		t.Fatal("precondition: the mint must be reachable before it starts rate-limiting")
	}

	throttled = true
	tracker.RunProactiveCheck()

	if !tracker.IsReachable(srv) {
		t.Fatal("a 429 probe answer removed the mint from the reachable set: busy is not broken")
	}
}

// A mint that stops answering leaves the reachable set, but only after the
// documented consecutive-failure threshold — one bad probe during a flood must
// not downgrade the merchant.
func TestProactiveCheckRequiresConsecutiveFailuresBeforeDowngrade(t *testing.T) {
	const failureThreshold = 3

	var down bool
	srv := keysetsStatusServer(t, func() int {
		if down {
			// A mint that stops answering at all — not a 429, which is a
			// *reachable* answer (see the test above).
			return http.StatusServiceUnavailable
		}
		return http.StatusOK
	})

	tracker := newTestTracker(mintConfigWithURLs(srv), nil)
	tracker.RunInitialProbe()
	if !tracker.IsReachable(srv) {
		t.Fatal("precondition: the mint must be reachable")
	}

	down = true
	for i := 1; i < failureThreshold; i++ {
		tracker.RunProactiveCheck()
		if !tracker.IsReachable(srv) {
			t.Fatalf("the mint left the reachable set after %d failed probe(s); want %d consecutive failures", i, failureThreshold)
		}
	}

	tracker.RunProactiveCheck()
	if tracker.IsReachable(srv) {
		t.Fatalf("the mint is still reachable after %d consecutive failed probes; a dead mint must eventually leave the set", failureThreshold)
	}
}

// --- c) the quote table is bounded -----------------------------------------

// The table holds at most maxActiveQuotesGlobal records, and filling it evicts an
// abandoned unpaid quote rather than growing — while never dropping a quote that
// is being processed or has already been granted access.
func TestQuoteTableEvictsInsteadOfGrowingWithoutBound(t *testing.T) {
	const globalCap = 128

	wallet := &stubQuoteWallet{}
	m := newQuoteTestMerchant(t, wallet, nil)

	abandoned := time.Now().Add(-10 * time.Minute)
	for i := 0; i < globalCap; i++ {
		m.lightningQuotes[fmt.Sprintf("abandoned-%d", i)] = &lightningQuoteRecord{
			MacAddress: fmt.Sprintf("aa:bb:cc:00:%02x:%02x", i/256, i%256),
			CreatedAt:  abandoned,
			Expiry:     uint64(time.Now().Add(time.Hour).Unix()),
		}
	}
	// Two records the eviction must never pick.
	m.lightningQuotes["in-flight"] = &lightningQuoteRecord{
		MacAddress: "aa:bb:cc:ff:00:01",
		CreatedAt:  abandoned,
		Processing: true,
	}
	m.lightningQuotes["paid"] = &lightningQuoteRecord{
		MacAddress:     "aa:bb:cc:ff:00:02",
		CreatedAt:      abandoned,
		SessionGranted: true,
		CompletedAt:    time.Now(),
	}

	if _, err := m.RequestLightningInvoice("de:ad:be:ef:00:01", quoteTestMint, 1); err != nil {
		t.Fatalf("quote creation against a full table = %v, want nil — an abandoned quote must be evicted to make room", err)
	}

	if got := len(m.lightningQuotes); got > globalCap {
		t.Fatalf("quote table holds %d records, want <= %d — the abuse path must not grow memory without bound", got, globalCap)
	}
	if _, ok := m.lightningQuotes["in-flight"]; !ok {
		t.Error("a quote being processed was evicted")
	}
	if _, ok := m.lightningQuotes["paid"]; !ok {
		t.Error("a quote that already granted access was evicted")
	}
	if wallet.requestCount() != 1 {
		t.Fatalf("mint quote requests = %d, want 1", wallet.requestCount())
	}
}

// One client cannot hold an unbounded number of in-flight quotes: the fourth
// active quote is refused, and the refusal never reaches the mint.
func TestQuoteTableCapsActiveQuotesPerClient(t *testing.T) {
	const mac = "8c:16:45:0d:6f:c5"

	wallet := &stubQuoteWallet{}
	m := newQuoteTestMerchant(t, wallet, nil)

	for i := 1; i <= 3; i++ {
		if _, err := m.RequestLightningInvoice(mac, quoteTestMint, 1); err != nil {
			t.Fatalf("quote %d: %v", i, err)
		}
	}

	_, err := m.RequestLightningInvoice(mac, quoteTestMint, 1)
	if err == nil {
		t.Fatal("a 4th active quote for one client was accepted: the quote table is fillable from a single MAC")
	}
	if !errors.Is(err, ErrTooManyQuotes) {
		t.Fatalf("error = %v, want ErrTooManyQuotes", err)
	}
	if got := len(m.lightningQuotes); got != 3 {
		t.Fatalf("quote table holds %d records for one client, want 3", got)
	}
	if got := wallet.requestCount(); got != 3 {
		t.Fatalf("mint quote requests = %d, want 3 — a refused quote must not reach the mint", got)
	}
}

// --- our own outbound traffic toward the mint ------------------------------

// The quote path is self-limited, so a burst of quote creations is refused
// locally instead of driving the mint into answering 429 (which is what turns a
// flood into a health signal the merchant then misreads).
func TestOutboundMintQuoteTrafficIsSelfLimited(t *testing.T) {
	wallet := &stubQuoteWallet{}
	m := newQuoteTestMerchant(t, wallet, nil)

	admitted, refused, otherErr := 0, 0, 0
	for i := 0; i < 30; i++ {
		_, err := m.RequestLightningInvoice(fmt.Sprintf("aa:bb:cc:00:00:%02x", i), quoteTestMint, 1)
		switch {
		case err == nil:
			admitted++
		case errors.Is(err, ErrMintBusyLocal):
			refused++
		default:
			otherErr++
			t.Logf("unexpected error: %v", err)
		}
	}

	if admitted > 6 {
		t.Fatalf("admitted %d quote requests in one burst, want at most the outbound burst (5)", admitted)
	}
	if refused == 0 {
		t.Fatal("no quote request was refused locally: our outbound traffic is unbounded, so the mint's own rate limit is our only backpressure")
	}
	if otherErr != 0 {
		t.Fatalf("%d request(s) failed for an unexpected reason", otherErr)
	}
	if got := wallet.requestCount(); got != admitted {
		t.Fatalf("mint saw %d quote requests but %d were admitted: the local refusal must come before the mint round trip", got, admitted)
	}
}

// keysetsStatusServer answers the keysets probe with the HTTP status status()
// returns: 200 carries a valid NUT-01 keysets body, 429 carries a Retry-After
// (as a real rate limiter sends one), anything else is an empty response with
// that status. It returns the server URL.
func keysetsStatusServer(t *testing.T, status func() int) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := status()
		if code == http.StatusOK {
			writeKeysetsOK(w)
			return
		}
		if code == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "1")
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}
