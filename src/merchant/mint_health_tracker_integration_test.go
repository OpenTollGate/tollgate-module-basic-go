//go:build integration

package merchant

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These tests validate the Lightning capability probe end-to-end against a
// LIVE testnut mint (https://nofee.testnut.cashu.space), which auto-settles
// invoices — so the 1-sat NUT-04 probe exercises the real backing Lightning
// node without spending sats. They cover the three scenarios from the task's
// physical-router-test-automation plan, lifted to an integration level:
//
//  1. mint with working Lightning     -> Lightning detected & advertised
//  2. mint reachable, LN backend down  -> reachable, Lightning NOT advertised
//  3. LN backend comes back online     -> Lightning re-enabled after recovery
//
// The httptest-backed unit tests in mint_health_tracker_ln_test.go cannot prove
// that probeLightningCapability's request shape, JSON body and response parsing
// actually work against a real Cashu NUT-04 implementation — only a live mint
// can. Every test calls requireMintReachable first and skips (does not fail)
// when the mint is unreachable, so the suite is safe to run in offline CI.
//
// Rate-limit note: the shared public testnut mint throttles the NUT-04 quote
// endpoint (HTTP 429) under repeated probing. SupportsLN-true assertions that
// touch the live LN endpoint therefore classify any mismatch with a single
// diagnostic probe and skip on a 429 (transient) rather than report a false
// failure — see supportsLNEquals. SupportsLN-false and IsReachable assertions
// are deterministic (they either hit a local proxy or /v1/info, neither of
// which is rate-limited) and stay strict.

// lnBreakingProxy forwards non-Lightning requests (notably /v1/info) to the
// real upstream mint while returning 503 on /v1/mint/quote/bolt11 whenever
// lnDown is set. This reproduces the production failure mode the LN capability
// probe exists to catch: a mint whose own /v1/info is healthy but whose backing
// Lightning node (e.g. coinos.io) is unreachable. Flipping lnDown back to 0
// restores LN forwarding, modelling the backend coming back online.
func lnBreakingProxy(t *testing.T, targetURL string, lnDown *atomic.Int32) *httptest.Server {
	t.Helper()
	target, err := url.Parse(targetURL)
	if err != nil {
		t.Fatalf("parse target URL: %v", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		director(req)
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/mint/quote/bolt11" && lnDown.Load() == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":503,"error":"Lightning backend unavailable"}`))
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// lnQuoteStatus performs a single diagnostic 1-sat NUT-04 mint-quote request
// against mintURL (the same request probeLightningCapability sends) and returns
// the HTTP status code (0 on transport error) and the bolt11 invoice returned
// ("" if none). It is used ONLY to classify a SupportsLN mismatch as transient
// (rate-limit / network) versus a real regression, so the happy path never
// spends an extra request.
func lnQuoteStatus(t *testing.T, mintURL string) (status int, invoice string) {
	t.Helper()
	client := &http.Client{Timeout: 15 * time.Second}
	endpoint := strings.TrimRight(mintURL, "/") + lnQuoteEndpoint
	resp, err := client.Post(endpoint, "application/json", strings.NewReader(`{"amount":1,"unit":"sat"}`))
	if err != nil {
		return 0, ""
	}
	defer resp.Body.Close()
	status = resp.StatusCode
	var q struct {
		Request string `json:"request"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&q)
	return status, q.Request
}

// supportsLNEquals asserts tracker.SupportsLN(mintURL) == want. On a mismatch it
// classifies the cause with one diagnostic probe: a rate-limit (HTTP 429) or
// transport failure (status 0) from the shared testnut mint skips the test
// (transient, not a regression); any other outcome is a real failure and fails
// the test. The diagnostic runs ONLY on mismatch, so the happy path is free.
func supportsLNEquals(t *testing.T, tracker *MintHealthTracker, mintURL string, want bool) {
	t.Helper()
	if got := tracker.SupportsLN(mintURL); got == want {
		return
	}
	got := tracker.SupportsLN(mintURL)
	status, invoice := lnQuoteStatus(t, mintURL)
	if status == http.StatusTooManyRequests || status == 0 {
		t.Skipf("SupportsLN=%v want %v, but testnut LN endpoint is rate-limited/unreachable "+
			"(diagnostic HTTP %d) — transient, skipping", got, want, status)
	}
	t.Fatalf("SupportsLN=%v, want %v (diagnostic LN probe: HTTP %d, invoice_len=%d) — "+
		"not a rate-limit, real failure", got, want, status, len(invoice))
}

// TestSupportsLNEquals_SkipsOnRateLimit deterministically verifies the
// resilience contract OFFLINE: when SupportsLN mismatches but the diagnostic
// probe hits a rate-limit (429), supportsLNEquals must SKIP rather than FAIL.
// This proves the rate-limit handling without waiting for the live testnut mint
// to throttle us. (got=false, want=true differ, so the helper cannot return
// early — t.Run returning true therefore implies it took the Skipf branch.)
func TestSupportsLNEquals_SkipsOnRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/info" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"detail":"Rate limit exceeded."}`))
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.RunInitialProbe() // reachable=true; supportsLN=false (429 is non-2xx)

	ok := t.Run("classify", func(st *testing.T) {
		supportsLNEquals(st, tracker, srv.URL, true) // want true, got false -> diagnose -> 429 -> skip
	})
	if !ok {
		t.Fatal("expected supportsLNEquals to SKIP on HTTP 429, but the subtest failed instead")
	}
}

// Scenario 1: a live testnut mint whose Lightning backend is up. RunInitialProbe
// must report it both reachable AND Lightning-capable. This is the operator's
// core ask: a real end-to-end 1-sat probe that confirms the mint can issue a
// bolt11 invoice, proving the probe works against a real Cashu mint.
func TestRunInitialProbe_TestnutMint_DetectsLightningSupport(t *testing.T) {
	requireMintReachable(t, testMintTarget)

	tracker := newTestTracker(mintConfigWithURLs(testMintTarget), nil)
	tracker.RunInitialProbe()

	if !tracker.IsReachable(testMintTarget) {
		t.Fatal("expected live testnut mint to be reachable after RunInitialProbe")
	}
	// Touches the live LN endpoint — skip (not fail) if testnut is rate-limiting.
	supportsLNEquals(t, tracker, testMintTarget, true)
	t.Logf("testnut mint %s: reachable=true, supports_ln=true", testMintTarget)
}

// Scenario 2: the production root cause, reproduced at integration level. A
// mint whose /v1/info is healthy (proxied from the real testnut mint) but whose
// Lightning endpoint is down (the proxy returns 503 on the NUT-04 quote path).
// The probe must keep the mint reachable while withholding Lightning support —
// the graceful-degradation behaviour that prevents silent Lightning failures at
// purchase time. Deterministic: the LN probe hits the local proxy, not testnut.
func TestRunInitialProbe_TestnutMint_LNBackendDown_ReachableButNotLN(t *testing.T) {
	requireMintReachable(t, testMintTarget)

	var lnDown atomic.Int32
	lnDown.Store(1)
	proxy := lnBreakingProxy(t, testMintTarget, &lnDown)

	tracker := newTestTracker(mintConfigWithURLs(proxy.URL), nil)
	tracker.RunInitialProbe()

	if !tracker.IsReachable(proxy.URL) {
		t.Fatal("expected mint to remain reachable (its /v1/info is proxied from a healthy mint)")
	}
	if tracker.SupportsLN(proxy.URL) {
		t.Fatal("expected SupportsLN=false when the LN endpoint returns 503 (backend down)")
	}
	t.Logf("proxy mint %s: reachable=true, supports_ln=false (LN backend down)", proxy.URL)
}

// Scenario 3: the "mint comes back online -> Lightning re-enabled" case from
// the task's test plan, driven against the real testnut mint behind an
// LN-breaking proxy. Lightning must degrade while the backend is down, then
// re-advertise only after lnRecoveryThreshold consecutive successful proactive
// probes once the backend is restored.
func TestProactiveCheck_TestnutMint_LNRecoversAfterBackendRestored(t *testing.T) {
	requireMintReachable(t, testMintTarget)

	var lnDown atomic.Int32
	lnDown.Store(1)
	proxy := lnBreakingProxy(t, testMintTarget, &lnDown)

	tracker := newTestTracker(mintConfigWithURLs(proxy.URL), nil)

	// Initial probe with LN down -> degraded (reachable, but no Lightning).
	tracker.RunInitialProbe()
	if !tracker.IsReachable(proxy.URL) {
		t.Fatal("expected mint to be reachable through the proxy")
	}
	if tracker.SupportsLN(proxy.URL) {
		t.Fatal("expected SupportsLN=false initially (LN backend down)")
	}

	// Restore the LN backend: the proxy now forwards /v1/mint/quote/bolt11 to
	// the live testnut mint, so the recovery probes exercise the real LN node.
	lnDown.Store(0)

	// It takes lnRecoveryThreshold consecutive successful proactive probes to
	// re-advertise Lightning. Before the threshold is reached it must stay off;
	// at the threshold it must flip on. The final (true) assertion touches the
	// live LN endpoint, so it skips rather than fails under a testnut rate-limit.
	for i := uint8(1); i <= lnRecoveryThreshold; i++ {
		tracker.RunProactiveCheck()
		supportsLNEquals(t, tracker, proxy.URL, i >= lnRecoveryThreshold)
	}
}
