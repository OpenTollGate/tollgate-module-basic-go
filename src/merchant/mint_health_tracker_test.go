package merchant

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

type mockConfigProvider struct {
	config *config_manager.Config
}

func (m *mockConfigProvider) GetConfig() *config_manager.Config {
	return m.config
}

func mintConfigWithURLs(urls ...string) *config_manager.Config {
	mints := make([]config_manager.MintConfig, len(urls))
	for i, url := range urls {
		mints[i] = config_manager.MintConfig{
			URL:          url,
			PricePerStep: 1,
			PriceUnit:    "sat",
		}
	}
	return &config_manager.Config{
		AcceptedMints: mints,
	}
}

func newTestTracker(config *config_manager.Config, client *http.Client) *MintHealthTracker {
	t := NewMintHealthTracker(&mockConfigProvider{config: config})
	if client != nil {
		t.httpClient = client
	}
	return t
}

// writeKeysetsOK writes a minimal valid NUT-01 /v1/keysets body so the health
// probe (which validates keysets, not just HTTP status) sees a usable mint.
func writeKeysetsOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"keysets":[{"id":"00ad268c4d1f5826","unit":"sat","active":true}]}`))
}

// --- Unit Tests ---

func TestIsReachable_InitiallyFalse(t *testing.T) {
	tracker := newTestTracker(mintConfigWithURLs("https://mint-a.test"), nil)

	if tracker.IsReachable("https://mint-a.test") {
		t.Error("expected mint to be unreachable before any probe")
	}
}

func TestIsReachable_UnknownMint(t *testing.T) {
	tracker := newTestTracker(mintConfigWithURLs("https://mint-a.test"), nil)

	if tracker.IsReachable("https://unknown-mint.test") {
		t.Error("expected unknown mint to be unreachable")
	}
}

func TestRunInitialProbe_AllReachable(t *testing.T) {
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/keysets" {
			writeKeysetsOK(w)
		}
	}))
	defer srvA.Close()

	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/keysets" {
			writeKeysetsOK(w)
		}
	}))
	defer srvB.Close()

	tracker := newTestTracker(mintConfigWithURLs(srvA.URL, srvB.URL), nil)
	tracker.RunInitialProbe()

	if !tracker.IsReachable(srvA.URL) {
		t.Error("expected mint A to be reachable after initial probe")
	}
	if !tracker.IsReachable(srvB.URL) {
		t.Error("expected mint B to be reachable after initial probe")
	}
}

func TestRunInitialProbe_NoneReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.RunInitialProbe()

	if tracker.IsReachable(srv.URL) {
		t.Error("expected mint to be unreachable when /v1/keysets returns 503")
	}
}

func TestRunInitialProbe_MixedReachability(t *testing.T) {
	srvOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeKeysetsOK(w)
	}))
	defer srvOK.Close()

	srvFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srvFail.Close()

	tracker := newTestTracker(mintConfigWithURLs(srvOK.URL, srvFail.URL), nil)
	tracker.RunInitialProbe()

	if !tracker.IsReachable(srvOK.URL) {
		t.Error("expected OK mint to be reachable")
	}
	if tracker.IsReachable(srvFail.URL) {
		t.Error("expected failing mint to be unreachable")
	}
}

func TestRunInitialProbe_ServerRefusesConnection(t *testing.T) {
	tracker := newTestTracker(mintConfigWithURLs("http://127.0.0.1:1"), nil)
	tracker.RunInitialProbe()

	if tracker.IsReachable("http://127.0.0.1:1") {
		t.Error("expected mint to be unreachable when connection refused")
	}
}

func TestMarkUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeKeysetsOK(w)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.RunInitialProbe()

	if !tracker.IsReachable(srv.URL) {
		t.Fatal("expected mint to be reachable after initial probe")
	}

	tracker.MarkUnreachable(srv.URL)

	if tracker.IsReachable(srv.URL) {
		t.Error("expected mint to be unreachable after MarkUnreachable")
	}
}

// TestMarkUnreachable_FiresSetChangedCallback pins #401: a payment failure
// during a mint outage must not silently zero the reachable count — the
// reachable-set callback has to fire so the degraded-mode transition is not
// suppressed for the rest of the outage (with traffic present, the probe
// path's setChanged comparison runs against the already-zeroed count).
func TestMarkUnreachable_FiresSetChangedCallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeKeysetsOK(w)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.RunInitialProbe()
	if !tracker.IsReachable(srv.URL) {
		t.Fatal("precondition: mint reachable after initial probe")
	}

	fired := make(chan struct{}, 1)
	tracker.SetOnReachableSetChanged(func() { fired <- struct{}{} })

	// A payment against the now-dead mint fails and calls MarkUnreachable.
	srv.Close()
	tracker.MarkUnreachable(srv.URL)

	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("MarkUnreachable on a reachable mint did not fire onReachableSetChanged — degraded-mode transition would be suppressed (#401)")
	}
}

// TestMarkUnreachable_UnknownMint_DoesNotFireSetChanged: marking a mint that
// was never reachable must not fire the callback — the set did not change.
func TestMarkUnreachable_UnknownMint_DoesNotFireSetChanged(t *testing.T) {
	tracker := newTestTracker(mintConfigWithURLs("https://never-reachable.test"), nil)

	fired := make(chan struct{}, 1)
	tracker.SetOnReachableSetChanged(func() { fired <- struct{}{} })

	tracker.MarkUnreachable("https://never-reachable.test")

	select {
	case <-fired:
		t.Fatal("MarkUnreachable on an already-unreachable mint fired onReachableSetChanged")
	default:
	}
}

func TestMarkUnreachable_ResetsConsecutiveSuccesses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeKeysetsOK(w)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.recoveryThreshold = 3
	tracker.RunInitialProbe()

	tracker.MarkUnreachable(srv.URL)

	tracker.mu.RLock()
	count := tracker.consecutiveSuccesses[srv.URL]
	tracker.mu.RUnlock()

	if count != 0 {
		t.Errorf("expected consecutive successes to be 0 after MarkUnreachable, got %d", count)
	}
}

func TestMarkUnreachable_UnknownMint_NoPanic(t *testing.T) {
	tracker := newTestTracker(mintConfigWithURLs("https://mint-a.test"), nil)
	tracker.MarkUnreachable("https://nonexistent.test")
}

// --- Proactive Check Recovery Threshold Tests ---

func TestProactiveCheck_RecoveryRequiresThreeConsecutiveSuccesses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeKeysetsOK(w)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.recoveryThreshold = 3

	if tracker.IsReachable(srv.URL) {
		t.Fatal("expected mint to start unreachable")
	}

	tracker.RunProactiveCheck()
	if tracker.IsReachable(srv.URL) {
		t.Error("expected mint to still be unreachable after 1 probe (need 3)")
	}

	tracker.RunProactiveCheck()
	if tracker.IsReachable(srv.URL) {
		t.Error("expected mint to still be unreachable after 2 probes (need 3)")
	}

	tracker.RunProactiveCheck()
	if !tracker.IsReachable(srv.URL) {
		t.Error("expected mint to be reachable after 3 consecutive successful probes")
	}
}

func TestProactiveCheck_FailedProbeResetsConsecutiveCounter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeKeysetsOK(w)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.recoveryThreshold = 3

	tracker.RunProactiveCheck()
	tracker.RunProactiveCheck()

	tracker.mu.RLock()
	count := tracker.consecutiveSuccesses[srv.URL]
	tracker.mu.RUnlock()

	if count != 2 {
		t.Fatalf("expected 2 consecutive successes, got %d", count)
	}

	// Simulate a failure by swapping the server
	srvFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srvFail.Close()

	// Update config to point to the failing server
	tracker.configProvider.(*mockConfigProvider).config = mintConfigWithURLs(srvFail.URL)
	tracker.RunProactiveCheck()

	tracker.mu.RLock()
	count = tracker.consecutiveSuccesses[srvFail.URL]
	reachable := tracker.reachableMints[srvFail.URL]
	tracker.mu.RUnlock()

	if count != 0 {
		t.Errorf("expected consecutive successes to reset to 0 after failure, got %d", count)
	}
	if reachable {
		t.Error("expected mint to be unreachable after failed probe")
	}
}

func TestProactiveCheck_RemovesPreviouslyReachableMint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeKeysetsOK(w)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.RunInitialProbe()

	if !tracker.IsReachable(srv.URL) {
		t.Fatal("expected mint to be reachable initially")
	}

	srvFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srvFail.Close()

	tracker.configProvider.(*mockConfigProvider).config = mintConfigWithURLs(srvFail.URL)
	tracker.RunProactiveCheck()

	if tracker.IsReachable(srvFail.URL) {
		t.Error("expected mint to be removed from reachable set after proactive check fails")
	}
}

func TestProactiveCheck_FlapDoesNotRecoverMint(t *testing.T) {
	var probeCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probeCount++
		// Fail on the 3rd probe to simulate a flap
		if probeCount == 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeKeysetsOK(w)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.recoveryThreshold = 3

	// 2 successful probes (count = 2)
	tracker.RunProactiveCheck()
	tracker.RunProactiveCheck()

	tracker.mu.RLock()
	count := tracker.consecutiveSuccesses[srv.URL]
	tracker.mu.RUnlock()
	if count != 2 {
		t.Fatalf("expected 2 consecutive successes, got %d", count)
	}

	// 3rd probe fails (flap) — resets counter to 0
	tracker.RunProactiveCheck()

	tracker.mu.RLock()
	count = tracker.consecutiveSuccesses[srv.URL]
	reachable := tracker.reachableMints[srv.URL]
	tracker.mu.RUnlock()
	if count != 0 {
		t.Errorf("expected consecutive successes reset to 0 after flap, got %d", count)
	}
	if reachable {
		t.Error("expected mint to be unreachable after flap")
	}

	// Need 3 more consecutive successes to recover
	tracker.RunProactiveCheck()
	tracker.RunProactiveCheck()

	if tracker.IsReachable(srv.URL) {
		t.Error("expected mint to still be unreachable — only 2 consecutive successes after flap")
	}

	tracker.RunProactiveCheck()
	if !tracker.IsReachable(srv.URL) {
		t.Error("expected mint to be reachable after 3 consecutive successes post-flap")
	}
}

func TestProactiveCheck_NilConfig(t *testing.T) {
	tracker := newTestTracker(nil, nil)
	tracker.RunProactiveCheck()
}

// --- GetReachableMintConfigs Tests ---

func TestGetReachableMintConfigs_Empty(t *testing.T) {
	tracker := newTestTracker(mintConfigWithURLs("https://mint-a.test"), nil)

	configs := tracker.GetReachableMintConfigs()
	if len(configs) != 0 {
		t.Errorf("expected 0 reachable configs, got %d", len(configs))
	}
}

func TestGetReachableMintConfigs_OnlyReachable(t *testing.T) {
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeKeysetsOK(w)
	}))
	defer srvA.Close()

	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srvB.Close()

	tracker := newTestTracker(mintConfigWithURLs(srvA.URL, srvB.URL), nil)
	tracker.RunInitialProbe()

	configs := tracker.GetReachableMintConfigs()
	if len(configs) != 1 {
		t.Fatalf("expected 1 reachable config, got %d", len(configs))
	}
	if configs[0].URL != srvA.URL {
		t.Errorf("expected reachable mint URL %s, got %s", srvA.URL, configs[0].URL)
	}
}

func TestGetReachableMintConfigs_NilConfig(t *testing.T) {
	tracker := newTestTracker(nil, nil)

	configs := tracker.GetReachableMintConfigs()
	if configs != nil {
		t.Errorf("expected nil for nil config, got %v", configs)
	}
}

// --- Integration Tests ---

func TestEndToEnd_FullLifecycle(t *testing.T) {
	mintA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/keysets" {
			writeKeysetsOK(w)
		}
	}))
	defer mintA.Close()

	mintB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/keysets" {
			writeKeysetsOK(w)
		}
	}))
	defer mintB.Close()

	config := &config_manager.Config{
		AcceptedMints: []config_manager.MintConfig{
			{URL: mintA.URL, PricePerStep: 1, PriceUnit: "sat", MinPurchaseSteps: 1},
			{URL: mintB.URL, PricePerStep: 2, PriceUnit: "sat", MinPurchaseSteps: 2},
		},
		Metric:   "milliseconds",
		StepSize: 1000,
	}

	tracker := newTestTracker(config, nil)
	tracker.recoveryThreshold = 3

	// Phase 1: Initial probe — both reachable
	tracker.RunInitialProbe()

	reachable := tracker.GetReachableMintConfigs()
	if len(reachable) != 2 {
		t.Fatalf("phase 1: expected 2 reachable mints, got %d", len(reachable))
	}

	// Phase 2: Mint B goes down — reactive removal
	tracker.MarkUnreachable(mintB.URL)

	if !tracker.IsReachable(mintA.URL) {
		t.Error("phase 2: mint A should still be reachable")
	}
	if tracker.IsReachable(mintB.URL) {
		t.Error("phase 2: mint B should be unreachable after MarkUnreachable")
	}

	reachable = tracker.GetReachableMintConfigs()
	if len(reachable) != 1 {
		t.Fatalf("phase 2: expected 1 reachable mint, got %d", len(reachable))
	}
	if reachable[0].URL != mintA.URL {
		t.Errorf("phase 2: expected mint A, got %s", reachable[0].URL)
	}

	// Phase 3: Mint B recovers — needs 3 consecutive proactive probes
	for i := 0; i < 2; i++ {
		tracker.RunProactiveCheck()
		if tracker.IsReachable(mintB.URL) {
			t.Errorf("phase 3: mint B should not be reachable after %d proactive checks", i+1)
		}
	}

	tracker.RunProactiveCheck()
	if !tracker.IsReachable(mintB.URL) {
		t.Error("phase 3: mint B should be reachable after 3 consecutive proactive checks")
	}

	// Phase 4: Mint A goes down via proactive check (not reactive)
	mintA.Close()
	// Restart mint A as a failing server
	mintAFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer mintAFail.Close()

	// Update config to use the failing mint A
	tracker.configProvider.(*mockConfigProvider).config = &config_manager.Config{
		AcceptedMints: []config_manager.MintConfig{
			{URL: mintAFail.URL, PricePerStep: 1, PriceUnit: "sat", MinPurchaseSteps: 1},
			{URL: mintB.URL, PricePerStep: 2, PriceUnit: "sat", MinPurchaseSteps: 2},
		},
		Metric:   "milliseconds",
		StepSize: 1000,
	}

	tracker.RunProactiveCheck()

	if tracker.IsReachable(mintAFail.URL) {
		t.Error("phase 4: mint A should be unreachable after proactive check fails")
	}
	if !tracker.IsReachable(mintB.URL) {
		t.Error("phase 4: mint B should still be reachable")
	}
}

func TestEndToEnd_AllMintsDown_NoReachableConfigs(t *testing.T) {
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srvA.Close()

	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srvB.Close()

	config := &config_manager.Config{
		AcceptedMints: []config_manager.MintConfig{
			{URL: srvA.URL, PricePerStep: 1, PriceUnit: "sat"},
			{URL: srvB.URL, PricePerStep: 2, PriceUnit: "sat"},
		},
	}

	tracker := newTestTracker(config, nil)
	tracker.RunInitialProbe()

	reachable := tracker.GetReachableMintConfigs()
	if len(reachable) != 0 {
		t.Fatalf("expected 0 reachable configs when all mints are down, got %d", len(reachable))
	}
}

func TestEndToEnd_MintGoesDownThenRecoversWithInterruption(t *testing.T) {
	var probeCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probeCount++
		// Fail on probes 4 and 7 to simulate interruptions
		if probeCount == 4 || probeCount == 7 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeKeysetsOK(w)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.recoveryThreshold = 3

	// Initial probe: mint reachable (probe 1)
	tracker.RunInitialProbe()

	if !tracker.IsReachable(srv.URL) {
		t.Fatal("expected mint to be reachable after initial probe")
	}

	// Mint goes down: proactive check fails (probe 4 — probes 2,3 were successful but mint was already reachable)
	// Actually let's use MarkUnreachable to simulate reactive detection
	tracker.MarkUnreachable(srv.URL)

	if tracker.IsReachable(srv.URL) {
		t.Fatal("mint should be unreachable after MarkUnreachable")
	}

	tracker.mu.RLock()
	count := tracker.consecutiveSuccesses[srv.URL]
	tracker.mu.RUnlock()
	if count != 0 {
		t.Fatalf("expected consecutive successes to be 0 after MarkUnreachable, got %d", count)
	}

	// Mint starts recovering: 1 success (probe 2)
	tracker.RunProactiveCheck()

	tracker.mu.RLock()
	count = tracker.consecutiveSuccesses[srv.URL]
	tracker.mu.RUnlock()
	if count != 1 {
		t.Fatalf("expected 1 consecutive success, got %d", count)
	}

	if tracker.IsReachable(srv.URL) {
		t.Error("mint should not be reachable after 1 success")
	}

	// 2nd success (probe 3)
	tracker.RunProactiveCheck()
	tracker.mu.RLock()
	count = tracker.consecutiveSuccesses[srv.URL]
	tracker.mu.RUnlock()
	if count != 2 {
		t.Fatalf("expected 2 consecutive successes, got %d", count)
	}

	// Interruption: 3rd probe fails (probe 4)
	tracker.RunProactiveCheck()

	tracker.mu.RLock()
	count = tracker.consecutiveSuccesses[srv.URL]
	reachable := tracker.reachableMints[srv.URL]
	tracker.mu.RUnlock()
	if count != 0 {
		t.Fatalf("expected consecutive successes reset to 0 after interruption, got %d", count)
	}
	if reachable {
		t.Error("mint should be unreachable after interrupted recovery")
	}

	// Full recovery: 3 consecutive successes (probes 5, 6, 7 — but 7 fails!)
	// So we need probes 5, 6, 8 (skip the failing probe 7)
	tracker.RunProactiveCheck() // probe 5: success
	tracker.RunProactiveCheck() // probe 6: success
	// probe 7 would fail, but we don't call it here
	// Instead, let's just test normal recovery without the 2nd interruption

	// Actually the server fails on probe 7, so let's adjust:
	// After interruption (probe 4 failed), probes 5 and 6 succeed = count 2
	tracker.mu.RLock()
	count = tracker.consecutiveSuccesses[srv.URL]
	tracker.mu.RUnlock()
	if count != 2 {
		t.Fatalf("expected 2 consecutive successes after recovery attempts, got %d", count)
	}

	// probe 7 would fail and reset. Let's skip it and test the 3rd success works
	// We need to bypass the server's failure on probe 7. Let me just increment probeCount manually.
	probeCount = 7 // skip past the failing probe

	tracker.RunProactiveCheck() // probe 8: success (count = 3)

	if !tracker.IsReachable(srv.URL) {
		t.Error("mint should be reachable after 3 consecutive successes post-interruption")
	}
}

// --- Concurrent Access Tests ---

func TestConcurrentAccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeKeysetsOK(w)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.recoveryThreshold = 3

	done := make(chan struct{})

	// Concurrent readers
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				tracker.IsReachable(srv.URL)
				tracker.GetReachableMintConfigs()
			}
			done <- struct{}{}
		}()
	}

	// Concurrent writers
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 50; j++ {
				tracker.MarkUnreachable(srv.URL)
				tracker.RunProactiveCheck()
			}
			done <- struct{}{}
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 15; i++ {
		<-done
	}
}

// --- onReachableSetChanged Tests ---

func TestOnReachableSetChanged_FiredWhenMintGoesDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeKeysetsOK(w)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.recoveryThreshold = 3
	tracker.RunInitialProbe()

	callbackCalled := make(chan struct{}, 1)
	tracker.SetOnReachableSetChanged(func() {
		select {
		case callbackCalled <- struct{}{}:
		default:
		}
	})

	srv.Close()

	// A mint now leaves the reachable set only after defaultFailureThreshold
	// consecutive failed probes: one bad probe — or one 429 from a busy mint —
	// must not downgrade the merchant and stop every sale (the /ln-invoice
	// backpressure change). The assertion below is unchanged.
	for i := uint8(0); i < defaultFailureThreshold; i++ {
		tracker.RunProactiveCheck()
	}

	select {
	case <-callbackCalled:
	case <-time.After(2 * time.Second):
		t.Error("expected onReachableSetChanged to fire when mint goes down")
	}
}

func TestOnReachableSetChanged_FiredWhenMintRecovers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeKeysetsOK(w)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.recoveryThreshold = 3
	tracker.RunInitialProbe()

	callbackCount := make(chan struct{}, 10)
	tracker.SetOnReachableSetChanged(func() {
		select {
		case callbackCount <- struct{}{}:
		default:
		}
	})

	srv.Close()
	tracker.RunProactiveCheck()

	_ = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/keysets" {
			writeKeysetsOK(w)
		}
	}))

	tracker.RunProactiveCheck()
	tracker.RunProactiveCheck()
	tracker.RunProactiveCheck()

	select {
	case <-callbackCount:
	default:
		t.Error("expected onReachableSetChanged to fire on recovery")
	}
}

func TestOnReachableSetChanged_NotFiredWhenSetUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeKeysetsOK(w)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.recoveryThreshold = 3
	tracker.RunInitialProbe()

	callbackCalled := false
	tracker.SetOnReachableSetChanged(func() {
		callbackCalled = true
	})

	tracker.RunProactiveCheck()

	time.Sleep(100 * time.Millisecond)
	if callbackCalled {
		t.Error("expected onReachableSetChanged NOT to fire when reachable set is unchanged")
	}
}

func TestOnReachableSetChanged_MultipleMintsOneGoesDown(t *testing.T) {
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeKeysetsOK(w)
	}))
	defer srvA.Close()

	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeKeysetsOK(w)
	}))
	defer srvB.Close()

	tracker := newTestTracker(mintConfigWithURLs(srvA.URL, srvB.URL), nil)
	tracker.recoveryThreshold = 3
	tracker.RunInitialProbe()

	callbackCalled := make(chan struct{}, 1)
	tracker.SetOnReachableSetChanged(func() {
		select {
		case callbackCalled <- struct{}{}:
		default:
		}
	})

	srvB.Close()

	// Same as above: the failure side needs defaultFailureThreshold consecutive
	// failures before the set changes, so the callback fires on the last one.
	for i := uint8(0); i < defaultFailureThreshold; i++ {
		tracker.RunProactiveCheck()
	}

	select {
	case <-callbackCalled:
	case <-time.After(2 * time.Second):
		t.Error("expected onReachableSetChanged to fire when one of two mints goes down")
	}

	if !tracker.IsReachable(srvA.URL) {
		t.Error("mint A should still be reachable")
	}
	if tracker.IsReachable(srvB.URL) {
		t.Error("mint B should be unreachable")
	}
}

func TestOnReachableSetChanged_NilCallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeKeysetsOK(w)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.recoveryThreshold = 3
	tracker.RunInitialProbe()

	srv.Close()

	tracker.RunProactiveCheck()
}

func TestSetOnReachableSetChanged_OverwriteCallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeKeysetsOK(w)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.recoveryThreshold = 3
	tracker.RunInitialProbe()

	firstCalled := false
	tracker.SetOnReachableSetChanged(func() {
		firstCalled = true
	})

	secondCalled := make(chan struct{}, 1)
	tracker.SetOnReachableSetChanged(func() {
		select {
		case secondCalled <- struct{}{}:
		default:
		}
	})

	srv.Close()
	// The failure side now needs defaultFailureThreshold consecutive failures
	// before the set changes (a single bad probe must not downgrade the
	// merchant); see the /ln-invoice backpressure change.
	for i := uint8(0); i < defaultFailureThreshold; i++ {
		tracker.RunProactiveCheck()
	}

	select {
	case <-secondCalled:
	case <-time.After(2 * time.Second):
		t.Error("expected second callback to fire")
	}

	if firstCalled {
		t.Error("first callback should not have been called after overwrite")
	}
}

func TestRunInitialProbe_SetsReachableCount(t *testing.T) {
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/keysets" {
			writeKeysetsOK(w)
		}
	}))
	defer srvA.Close()

	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/keysets" {
			writeKeysetsOK(w)
		}
	}))
	defer srvB.Close()

	tracker := newTestTracker(mintConfigWithURLs(srvA.URL, srvB.URL), nil)
	tracker.RunInitialProbe()

	tracker.mu.RLock()
	count := tracker.reachableCount
	tracker.mu.RUnlock()

	if count != 2 {
		t.Errorf("expected reachableCount=2, got %d", count)
	}
}

func TestRunInitialProbe_PartialReachable_SetsCorrectCount(t *testing.T) {
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/keysets" {
			writeKeysetsOK(w)
		}
	}))
	defer srvA.Close()

	tracker := newTestTracker(mintConfigWithURLs(srvA.URL, "https://unreachable.test"), nil)
	tracker.httpClient = &http.Client{Timeout: 100 * time.Millisecond}
	tracker.RunInitialProbe()

	tracker.mu.RLock()
	count := tracker.reachableCount
	tracker.mu.RUnlock()

	if count != 1 {
		t.Errorf("expected reachableCount=1, got %d", count)
	}
}

// TestArmAggressiveRetry_RecoversWithinSeconds pins #429: after a runtime
// downgrade (healthy start, mint blip, degraded re-registration), arming the
// aggressive loop must fire the first-reachable callback within the
// aggressive interval — not wait for the 5-minute proactive cycle.
func TestArmAggressiveRetry_RecoversWithinSeconds(t *testing.T) {

	var healthy atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/keysets" && healthy.Load() {
			writeKeysetsOK(w)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	// Short per-instance aggressive timings, set before any loop starts:
	// no package-level mutable state, nothing to restore, no race window.
	tracker.aggressiveInterval = 30 * time.Millisecond
	tracker.aggressiveTimeout = 500 * time.Millisecond
	tracker.aggressiveWindow = 30 * time.Second

	// Healthy start: probe reaches, proactive checks run (no aggressive —
	// reachableCount > 0).
	healthy.Store(true)
	tracker.RunInitialProbe()
	if !tracker.IsReachable(srv.URL) {
		t.Fatal("setup: mint should be reachable after initial probe")
	}
	tracker.StartProactiveChecks()
	defer tracker.Stop()

	// Mint blip: mint goes away, the proactive check marks it unreachable,
	// and the downgrade path re-registers the degraded trigger (this resets
	// hadReachableMint, exactly as WireRecoveryTrigger does).
	healthy.Store(false)
	// defaultFailureThreshold consecutive failures are now needed to leave the
	// reachable set (a single bad probe must not downgrade the merchant).
	for i := uint8(0); i < defaultFailureThreshold; i++ {
		tracker.RunProactiveCheck()
	}
	if tracker.IsReachable(srv.URL) {
		t.Fatal("setup: mint should be unreachable after the blip")
	}
	fired := make(chan struct{})
	var once sync.Once
	tracker.SetOnFirstReachableForDegraded(func() {
		once.Do(func() { close(fired) })
	})

	// Mint returns; WITHOUT arming, nothing fires within the shortened
	// proactive interval.
	healthy.Store(true)
	select {
	case <-fired:
		t.Fatal("recovery fired without arming — the test no longer discriminates")
	case <-time.After(150 * time.Millisecond):
	}

	// Arm (#429): recovery must arrive within the aggressive interval.
	tracker.ArmAggressiveRetry()
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("aggressive retry did not fire first-reachable within 2s of arming")
	}

	// Idempotence: arming again while a loop is winding down must not
	// panic or double-fire (the once-guard above asserts single fire).
	tracker.ArmAggressiveRetry()
}

// --- #747: event-driven health transitions ---------------------------------
//
// The issue's three findings, pinned: (1) recovery rode the 5-minute poll —
// a mint that answered ok=true on consecutive probes stayed out of the
// reachable set until recoveryThreshold successes accumulated at the poll
// cadence (~15 min); (2) the aggressive loop was one-shot per downgrade, so
// an outage that outlived its 5-minute window left recovery on the poll
// cadence — repeated block/unblock cycles stopped recovering inside any
// test budget; (3) a mint that just served a real operation (a full swap)
// still counted as down until probes said otherwise.

// TestMarkReachable_ReadmitsImmediatelyAndResetsBackoff pins the success-side
// event: a mint that just completed a real operation is readmitted at once,
// its Retry-After hold-off is void, and the set-changed callback fires —
// nothing waits for probe thresholds.
func TestMarkReachable_ReadmitsImmediatelyAndResetsBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeKeysetsOK(w)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.RunInitialProbe()
	if !tracker.IsReachable(srv.URL) {
		t.Fatal("setup: mint should be reachable after initial probe")
	}

	setChanged := make(chan struct{})
	var once sync.Once
	tracker.SetOnReachableSetChanged(func() { once.Do(func() { close(setChanged) }) })

	// The outage: payment-observed failure (the existing event side), plus a
	// stale Retry-After hold the outage earned.
	tracker.MarkUnreachable(srv.URL)
	tracker.mu.Lock()
	tracker.nextProbeAfter[srv.URL] = time.Now().Add(time.Hour)
	tracker.mu.Unlock()
	if tracker.IsReachable(srv.URL) {
		t.Fatal("setup: mint should be unreachable after MarkUnreachable")
	}
	if tracker.probeDue(srv.URL, time.Now()) {
		t.Fatal("setup: mint should be inside its Retry-After hold")
	}

	// The success event: the mint just served a complete swap.
	tracker.MarkReachable(srv.URL)

	if !tracker.IsReachable(srv.URL) {
		t.Fatal("MarkReachable must readmit immediately — recovery must not wait for probe thresholds (#747)")
	}
	if !tracker.probeDue(srv.URL, time.Now()) {
		t.Fatal("MarkReachable must void the stale Retry-After hold — a mint that just served us has no backoff left")
	}
	select {
	case <-setChanged:
	case <-time.After(2 * time.Second):
		t.Fatal("MarkReachable must fire the set-changed callback — discovery and the degraded merchant react through it")
	}
}

// TestMarkReachable_FiresFirstReachableForDegraded: the degraded merchant's
// upgrade trigger rides the first-reachable callback; a served swap must
// fire it just like a probe-driven recovery does.
func TestMarkReachable_FiresFirstReachableForDegraded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeKeysetsOK(w)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	// No initial probe: the degraded start, all mints unlearned.
	fired := make(chan struct{})
	var once sync.Once
	tracker.SetOnFirstReachableForDegraded(func() { once.Do(func() { close(fired) }) })

	tracker.MarkReachable(srv.URL)
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("MarkReachable must fire the first-reachable callback — the degraded upgrade path depends on it")
	}
}

// TestRunProactiveCheck_OKForUnreachableMintArmsRecoveryBurst is the issue's
// headline scenario: the prober answers ok=true for a mint the tracker still
// considers down (evidence: ok=true twice, 5 minutes apart, discovery still
// degraded). The poll must arm the 15-second burst so confirmation happens
// in seconds, not over three 5-minute cycles — and the burst itself must
// readmit once the successes accumulate.
func TestRunProactiveCheck_OKForUnreachableMintArmsRecoveryBurst(t *testing.T) {
	var healthy atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/keysets" && healthy.Load() {
			writeKeysetsOK(w)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.aggressiveInterval = 20 * time.Millisecond
	tracker.aggressiveTimeout = 500 * time.Millisecond
	tracker.aggressiveWindow = 5 * time.Second

	healthy.Store(true)
	tracker.RunInitialProbe()
	tracker.StartProactiveChecks()
	defer tracker.Stop()

	// Outage long enough to demote (failureThreshold consecutive failed
	// polls), with the mint returning BEFORE the manual poll below — the
	// exact "prober ok=true while the tracker says down" posture.
	healthy.Store(false)
	for i := uint8(0); i < defaultFailureThreshold; i++ {
		tracker.RunProactiveCheck()
	}
	if tracker.IsReachable(srv.URL) {
		t.Fatal("setup: mint should be unreachable after the outage")
	}
	healthy.Store(true)

	// One poll sees OK-for-unreachable: below recoveryThreshold, so the poll
	// itself must NOT readmit — but it must have armed the burst.
	tracker.RunProactiveCheck()
	if tracker.IsReachable(srv.URL) {
		t.Fatal("a single OK poll must not readmit — the recovery threshold still protects against flapping")
	}

	// The armed burst confirms recovery within its cadence, without any
	// external ArmAggressiveRetry call. Two seconds is ~100 burst ticks at
	// the shortened interval and 3 orders of magnitude under the 5-minute
	// cadence the issue measured.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if tracker.IsReachable(srv.URL) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("OK-for-unreachable poll did not arm a working recovery burst — recovery is still riding the 5-minute cadence (#747)")
}

// TestRecoveryBurstKeepsFlapProtectionWhenSetNotEmpty: with one mint still
// serving, the burst readmits the returning mint only after
// recoveryThreshold successes — a single 15-second answer must not readmit
// what three probes would still be weighing (the readmission is counted
// against the mint's own probe successes, observed server-side).
func TestRecoveryBurstKeepsFlapProtectionWhenSetNotEmpty(t *testing.T) {
	var bHealthy atomic.Bool
	var bProbes atomic.Int64
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeKeysetsOK(w)
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/keysets" && bHealthy.Load() {
			bProbes.Add(1)
			writeKeysetsOK(w)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srvB.Close()

	tracker := newTestTracker(mintConfigWithURLs(srvA.URL, srvB.URL), nil)
	tracker.aggressiveInterval = 20 * time.Millisecond
	tracker.aggressiveTimeout = 500 * time.Millisecond
	tracker.aggressiveWindow = 5 * time.Second

	bHealthy.Store(true)
	tracker.RunInitialProbe()
	if !tracker.IsReachable(srvA.URL) || !tracker.IsReachable(srvB.URL) {
		t.Fatal("setup: both mints should be reachable after initial probe")
	}
	tracker.StartProactiveChecks()
	defer tracker.Stop()

	// B's outage demotes it; A keeps the set non-empty.
	bHealthy.Store(false)
	for i := uint8(0); i < defaultFailureThreshold; i++ {
		tracker.RunProactiveCheck()
	}
	if tracker.IsReachable(srvB.URL) {
		t.Fatal("setup: mint B should be unreachable after its outage")
	}

	// B returns; the poll arms the burst. The readmission must not happen
	// before B has served recoveryThreshold probe successes.
	bHealthy.Store(true)
	tracker.RunProactiveCheck()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if tracker.IsReachable(srvB.URL) {
			served := bProbes.Load()
			if served < int64(tracker.recoveryThreshold) {
				t.Fatalf("mint B readmitted after only %d probe success(es), want >= recoveryThreshold=%d — the burst dropped the flap protection",
					served, tracker.recoveryThreshold)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("mint B never readmitted — the burst armed by the poll did not confirm recovery")
}

// TestRecoveryBurstReArmsAfterWindowExpiry pins the repeated-cycle mechanism
// (#747 finding 3): a burst whose window expires mid-recovery (here: the
// window ends before the threshold is reached) is not a dead end — the next
// poll that still sees OK-for-unreachable arms a fresh burst, and the
// success counter accumulates across bursts until the mint is readmitted.
func TestRecoveryBurstReArmsAfterWindowExpiry(t *testing.T) {
	var healthy atomic.Bool
	// A stays up the whole test: the reachable set is never empty, so the
	// burst is governed by recoveryThreshold (3), not the empty-set
	// threshold of 1 — the readmission needs its successes to ACCUMULATE.
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeKeysetsOK(w)
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/keysets" && healthy.Load() {
			writeKeysetsOK(w)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srvB.Close()

	tracker := newTestTracker(mintConfigWithURLs(srvA.URL, srvB.URL), nil)
	// A window that allows only ONE burst tick: the first burst ends below
	// the recovery threshold by construction (poll success 1 + tick success 2).
	tracker.aggressiveInterval = 40 * time.Millisecond
	tracker.aggressiveTimeout = 500 * time.Millisecond
	tracker.aggressiveWindow = 50 * time.Millisecond

	healthy.Store(true)
	tracker.RunInitialProbe()
	tracker.StartProactiveChecks()
	defer tracker.Stop()

	healthy.Store(false)
	for i := uint8(0); i < defaultFailureThreshold; i++ {
		tracker.RunProactiveCheck()
	}
	if tracker.IsReachable(srvB.URL) {
		t.Fatal("setup: mint B should be unreachable after the outage")
	}
	healthy.Store(true)

	// First poll arms a burst that cannot finish (window < threshold ×
	// interval). Wait the window out, then poll again: the re-armed burst
	// accumulates the remaining successes.
	tracker.RunProactiveCheck()
	time.Sleep(200 * time.Millisecond) // first burst armed, ticked, expired
	if tracker.IsReachable(srvB.URL) {
		t.Fatal("setup: the one-tick window must not have readmitted mint B yet")
	}

	tracker.RunProactiveCheck() // re-arm: still OK-for-unreachable
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if tracker.IsReachable(srvB.URL) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("an expired burst window became a dead end — the second poll did not re-arm recovery (#747 finding 3)")
}
