package merchant

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

func reachableServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func unreachableServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestStop_TerminatesProactiveChecks(t *testing.T) {
	srv := reachableServer(t)
	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.RunInitialProbe()

	tracker.StartProactiveChecks()
	tracker.Stop()

	time.Sleep(100 * time.Millisecond)
}

func TestStop_Idempotent(t *testing.T) {
	tracker := newTestTracker(mintConfigWithURLs("https://mint.test"), nil)
	tracker.Stop()
	tracker.Stop()
	tracker.Stop()
}

func TestStop_WhenNotStarted(t *testing.T) {
	tracker := newTestTracker(mintConfigWithURLs("https://mint.test"), nil)
	tracker.Stop()
}

func TestStartProactiveChecks_Idempotent(t *testing.T) {
	srv := reachableServer(t)
	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.RunInitialProbe()

	tracker.StartProactiveChecks()
	tracker.StartProactiveChecks()

	tracker.Stop()
}

func TestSetOnFirstReachableForDegraded_FiredOnce(t *testing.T) {
	srv := reachableServer(t)
	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.RunInitialProbe()

	var called int32
	tracker.SetOnFirstReachableForDegraded(func() {
		atomic.AddInt32(&called, 1)
	})

	tracker.RunProactiveCheck()

	if atomic.LoadInt32(&called) != 0 {
		t.Errorf("expected no callback when already reachable from initial probe, got %d", atomic.LoadInt32(&called))
	}
}

func TestSetOnFirstReachableForDegraded_FiresOnRecovery(t *testing.T) {
	srv := unreachableServer(t)
	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.RunInitialProbe()

	if tracker.IsReachable(srv.URL) {
		t.Fatal("mint should be unreachable initially")
	}

	var called int32
	tracker.SetOnFirstReachableForDegraded(func() {
		atomic.AddInt32(&called, 1)
	})

	reachableSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer reachableSrv.Close()

	cfg := &config_manager.Config{
		AcceptedMints: []config_manager.MintConfig{
			{URL: reachableSrv.URL, PricePerStep: 1, PriceUnit: "sat"},
		},
	}
	tracker.configProvider = &mockConfigProvider{config: cfg}

	for i := 0; i < int(tracker.recoveryThreshold); i++ {
		tracker.RunProactiveCheck()
	}

	time.Sleep(50 * time.Millisecond)

	if atomic.LoadInt32(&called) != 1 {
		t.Errorf("expected callback to fire once on recovery, got %d", atomic.LoadInt32(&called))
	}
}

func TestSetOnFirstReachableForDegraded_NotFiredOnSecondRecovery(t *testing.T) {
	srv := unreachableServer(t)
	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.RunInitialProbe()

	if tracker.IsReachable(srv.URL) {
		t.Fatal("mint should be unreachable initially")
	}

	var called int32
	tracker.SetOnFirstReachableForDegraded(func() {
		atomic.AddInt32(&called, 1)
	})

	reachableSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer reachableSrv.Close()

	cfg := &config_manager.Config{
		AcceptedMints: []config_manager.MintConfig{
			{URL: reachableSrv.URL, PricePerStep: 1, PriceUnit: "sat"},
		},
	}
	tracker.configProvider = &mockConfigProvider{config: cfg}

	for i := 0; i < int(tracker.recoveryThreshold); i++ {
		tracker.RunProactiveCheck()
	}

	time.Sleep(50 * time.Millisecond)

	if atomic.LoadInt32(&called) != 1 {
		t.Fatalf("expected first callback to fire, got %d", atomic.LoadInt32(&called))
	}

	atomic.StoreInt32(&called, 0)

	tracker.RunProactiveCheck()
	tracker.RunProactiveCheck()

	time.Sleep(50 * time.Millisecond)

	if atomic.LoadInt32(&called) != 0 {
		t.Errorf("expected no second callback after first recovery, got %d", atomic.LoadInt32(&called))
	}
}

func TestGetAllConfiguredMintConfigs_ReturnsAll(t *testing.T) {
	srvA := reachableServer(t)
	srvB := unreachableServer(t)

	config := &config_manager.Config{
		AcceptedMints: []config_manager.MintConfig{
			{URL: srvA.URL, PricePerStep: 1, PriceUnit: "sat"},
			{URL: srvB.URL, PricePerStep: 2, PriceUnit: "sat"},
		},
	}

	tracker := newTestTracker(config, nil)
	tracker.RunInitialProbe()

	all := tracker.GetAllConfiguredMintConfigs()
	if len(all) != 2 {
		t.Fatalf("expected 2 configured mints, got %d", len(all))
	}

	reachable := tracker.GetReachableMintConfigs()
	if len(reachable) != 1 {
		t.Fatalf("expected 1 reachable mint, got %d", len(reachable))
	}
}

func TestGetAllConfiguredMintConfigs_NilConfig(t *testing.T) {
	tracker := newTestTracker(nil, nil)
	all := tracker.GetAllConfiguredMintConfigs()
	if all != nil {
		t.Errorf("expected nil for nil config, got %v", all)
	}
}

func TestRunInitialProbe_NilConfig_NoPanic(t *testing.T) {
	tracker := newTestTracker(nil, nil)
	tracker.RunInitialProbe()
}

func TestProbeMint_TrailingSlashTrimmed(t *testing.T) {
	var requestedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL+"/"), nil)
	tracker.RunInitialProbe()

	if requestedPath != "/v1/info" {
		t.Errorf("expected /v1/info, got %s", requestedPath)
	}

	if !tracker.IsReachable(srv.URL + "/") {
		t.Error("mint with trailing slash should be reachable")
	}
}

func TestMerchant_GetAcceptedMints_ReturnsOnlyReachable(t *testing.T) {
	srvA := reachableServer(t)
	srvB := unreachableServer(t)

	config := &config_manager.Config{
		AcceptedMints: []config_manager.MintConfig{
			{URL: srvA.URL, PricePerStep: 1, PriceUnit: "sat"},
			{URL: srvB.URL, PricePerStep: 2, PriceUnit: "sat"},
		},
	}

	tracker := newTestTracker(config, nil)
	tracker.RunInitialProbe()

	mints := tracker.GetReachableMintConfigs()
	if len(mints) != 1 {
		t.Fatalf("expected 1 reachable mint, got %d", len(mints))
	}
	if mints[0].URL != srvA.URL {
		t.Errorf("expected %s, got %s", srvA.URL, mints[0].URL)
	}
}

func TestMerchant_SetOnReachableSetChanged_DelegatesToTracker(t *testing.T) {
	srv := reachableServer(t)
	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.RunInitialProbe()

	var called bool
	tracker.SetOnReachableSetChanged(func() {
		called = true
	})

	if tracker.onReachableSetChanged == nil {
		t.Fatal("expected callback to be set on tracker")
	}

	tracker.onReachableSetChanged()
	if !called {
		t.Error("expected delegated callback to fire")
	}
}

func TestMerchant_GetMintHealthTracker_ReturnsTracker(t *testing.T) {
	srv := reachableServer(t)
	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.RunInitialProbe()

	m := &Merchant{
		config:            mintConfigWithURLs(srv.URL),
		mintHealthTracker: tracker,
	}

	returned := m.GetMintHealthTracker()
	if returned != tracker {
		t.Error("GetMintHealthTracker did not return the same tracker instance")
	}
}
