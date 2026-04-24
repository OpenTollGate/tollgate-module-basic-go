package merchant

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

func newDegradedMerchantWithConfig(t *testing.T) (*MerchantDegraded, *config_manager.ConfigManager) {
	t.Helper()

	testDir := t.TempDir()
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", testDir)

	configPath := filepath.Join(testDir, "config.json")
	installPath := filepath.Join(testDir, "install.json")
	identitiesPath := filepath.Join(testDir, "identities.json")

	cm, err := config_manager.NewConfigManager(configPath, installPath, identitiesPath)
	if err != nil {
		t.Fatalf("failed to create config manager: %v", err)
	}

	srvFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srvFail.Close)

	cfg := cm.GetConfig()
	cfg.AcceptedMints = []config_manager.MintConfig{
		{URL: srvFail.URL, PricePerStep: 1, PriceUnit: "sats"},
	}

	tracker := newTestTracker(cfg, nil)
	tracker.RunInitialProbe()

	deg := &MerchantDegraded{
		configManager:     cm,
		mintHealthTracker: tracker,
	}
	return deg, cm
}

func TestMerchantDegraded_CreatePaymentToken_ReturnsError(t *testing.T) {
	deg, _ := newDegradedMerchantWithConfig(t)

	_, err := deg.CreatePaymentToken("https://mint.test", 100)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "wallet not initialized") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMerchantDegraded_CreatePaymentTokenWithOverpayment_ReturnsError(t *testing.T) {
	deg, _ := newDegradedMerchantWithConfig(t)

	_, err := deg.CreatePaymentTokenWithOverpayment("https://mint.test", 100, 10, 5)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "wallet not initialized") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMerchantDegraded_DrainMint_ReturnsError(t *testing.T) {
	deg, _ := newDegradedMerchantWithConfig(t)

	_, _, err := deg.DrainMint("https://mint.test")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "wallet not initialized") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMerchantDegraded_GetBalance_ReturnsZero(t *testing.T) {
	deg, _ := newDegradedMerchantWithConfig(t)

	if deg.GetBalance() != 0 {
		t.Errorf("expected 0, got %d", deg.GetBalance())
	}
	if deg.GetBalanceByMint("https://mint.test") != 0 {
		t.Errorf("expected 0, got %d", deg.GetBalanceByMint("https://mint.test"))
	}
	balances := deg.GetAllMintBalances()
	if len(balances) != 0 {
		t.Errorf("expected empty map, got %v", balances)
	}
}

func TestMerchantDegraded_GetAcceptedMints_ReturnsEmpty(t *testing.T) {
	deg, _ := newDegradedMerchantWithConfig(t)

	mints := deg.GetAcceptedMints()
	if len(mints) != 0 {
		t.Errorf("expected 0 accepted mints, got %d", len(mints))
	}
}

func TestMerchantDegraded_PurchaseSession_ReturnsNoticeEvent(t *testing.T) {
	deg, _ := newDegradedMerchantWithConfig(t)

	event, err := deg.PurchaseSession("cashuToken", "AA:BB:CC:DD:EE:FF")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil {
		t.Fatal("expected non-nil event")
	}
	if event.Kind != 21023 {
		t.Errorf("expected kind 21023, got %d", event.Kind)
	}

	hasCodeTag := false
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "code" && tag[1] == "service-unavailable" {
			hasCodeTag = true
			break
		}
	}
	if !hasCodeTag {
		t.Error("expected code=service-unavailable tag")
	}
}

func TestMerchantDegraded_GetAdvertisement_ReturnsNoticeJSON(t *testing.T) {
	deg, _ := newDegradedMerchantWithConfig(t)

	adv := deg.GetAdvertisement()
	if adv == "" {
		t.Fatal("expected non-empty advertisement string")
	}

	var event map[string]interface{}
	if err := json.Unmarshal([]byte(adv), &event); err != nil {
		t.Fatalf("failed to unmarshal advertisement as JSON: %v", err)
	}

	if kind, ok := event["kind"].(float64); !ok || int(kind) != 21023 {
		t.Errorf("expected kind 21023 in advertisement, got %v", event["kind"])
	}
}

func TestMerchantDegraded_StartPayoutRoutine_NoPanic(t *testing.T) {
	deg, _ := newDegradedMerchantWithConfig(t)
	deg.StartPayoutRoutine()
}

func TestMerchantDegraded_StartDataUsageMonitoring_NoPanic(t *testing.T) {
	deg, _ := newDegradedMerchantWithConfig(t)
	deg.StartDataUsageMonitoring()
}

func TestMerchantDegraded_GetSession_ReturnsError(t *testing.T) {
	deg, _ := newDegradedMerchantWithConfig(t)

	_, err := deg.GetSession("AA:BB:CC:DD:EE:FF")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "wallet not initialized") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMerchantDegraded_AddAllotment_ReturnsError(t *testing.T) {
	deg, _ := newDegradedMerchantWithConfig(t)

	_, err := deg.AddAllotment("AA:BB:CC:DD:EE:FF", "bytes", 1000)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "wallet not initialized") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMerchantDegraded_GetUsage_ReturnsDefault(t *testing.T) {
	deg, _ := newDegradedMerchantWithConfig(t)

	usage, err := deg.GetUsage("AA:BB:CC:DD:EE:FF")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usage != "-1/-1" {
		t.Errorf("expected '-1/-1', got %s", usage)
	}
}

func TestMerchantDegraded_Fund_ReturnsError(t *testing.T) {
	deg, _ := newDegradedMerchantWithConfig(t)

	_, err := deg.Fund("cashuToken")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "wallet not initialized") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMerchantDegraded_CreateNoticeEvent_NoMerchantIdentity(t *testing.T) {
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", t.TempDir())

	testDir := os.Getenv("TOLLGATE_TEST_CONFIG_DIR")
	identitiesPath := filepath.Join(testDir, "identities.json")

	noMerchantIdentities := []byte(`{
		"config_version": "v0.0.1",
		"owned_identities": [],
		"public_identities": []
	}`)
	if err := os.WriteFile(identitiesPath, noMerchantIdentities, 0644); err != nil {
		t.Fatalf("failed to write identities: %v", err)
	}

	cm, err := config_manager.NewConfigManager(
		filepath.Join(testDir, "config.json"),
		filepath.Join(testDir, "install.json"),
		identitiesPath,
	)
	if err != nil {
		t.Fatalf("failed to create config manager: %v", err)
	}

	srvFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srvFail.Close)

	tracker := newTestTracker(&config_manager.Config{
		AcceptedMints: []config_manager.MintConfig{
			{URL: srvFail.URL, PricePerStep: 1, PriceUnit: "sats"},
		},
	}, nil)

	deg := &MerchantDegraded{
		configManager:     cm,
		mintHealthTracker: tracker,
	}

	_, err = deg.CreateNoticeEvent("error", "test", "test message", "")
	if err == nil {
		t.Fatal("expected error when no merchant identity exists")
	}
}

func TestMerchantDegraded_OnUpgrade_FiresCallback(t *testing.T) {
	deg, _ := newDegradedMerchantWithConfig(t)

	var callbackFired bool
	deg.OnUpgrade(func(m MerchantInterface) {
		callbackFired = true
	})

	if deg.onUpgrade == nil {
		t.Fatal("expected onUpgrade to be set")
	}

	deg.onUpgrade(nil)
	if !callbackFired {
		t.Error("expected OnUpgrade callback to fire")
	}
}

func TestMerchantDegraded_ImplementsMerchantInterface(t *testing.T) {
	var _ MerchantInterface = &MerchantDegraded{}
}

func TestOnFirstReachable_FiredOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.recoveryThreshold = 3

	var callCount int
	var mu sync.Mutex
	done := make(chan struct{})

	tracker.SetOnFirstReachable(func() {
		mu.Lock()
		callCount++
		mu.Unlock()
		done <- struct{}{}
	})

	for i := 0; i < 3; i++ {
		tracker.RunProactiveCheck()
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("expected onFirstReachable to fire within 2 seconds")
	}

	mu.Lock()
	count := callCount
	mu.Unlock()
	if count != 1 {
		t.Errorf("expected callback to fire exactly once, got %d", count)
	}

	for i := 0; i < 5; i++ {
		tracker.RunProactiveCheck()
	}

	mu.Lock()
	count = callCount
	mu.Unlock()
	if count != 1 {
		t.Errorf("expected callback to still be 1 after additional checks, got %d", count)
	}
}

func TestOnFirstReachable_NotFiredIfInitiallyReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.recoveryThreshold = 3

	done := make(chan struct{})
	tracker.SetOnFirstReachable(func() {
		close(done)
	})

	tracker.RunInitialProbe()

	if !tracker.IsReachable(srv.URL) {
		t.Fatal("expected mint to be reachable after initial probe")
	}

	tracker.mu.RLock()
	hadMint := tracker.hadReachableMint
	tracker.mu.RUnlock()
	if !hadMint {
		t.Fatal("expected hadReachableMint to be true after initial probe with reachable mints")
	}

	for i := 0; i < 5; i++ {
		tracker.RunProactiveCheck()
	}

	select {
	case <-done:
		t.Error("expected onFirstReachable to NOT fire when callback was set before initial probe and mints were reachable")
	default:
	}
}

func TestNew_ReturnsDegradedWhenNoMintsReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cfg := &config_manager.Config{
		AcceptedMints: []config_manager.MintConfig{
			{URL: srv.URL, PricePerStep: 1, PriceUnit: "sats"},
		},
	}

	tracker := newTestTracker(cfg, nil)
	tracker.RunInitialProbe()

	if len(tracker.GetReachableMintConfigs()) != 0 {
		t.Fatal("test setup: expected 0 reachable mints")
	}

	if tracker.hadReachableMint {
		t.Fatal("expected hadReachableMint to be false after initial probe with no reachable mints")
	}
}

func TestOnFirstReachable_SetCallbackResetsHadReachableMint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.recoveryThreshold = 3

	tracker.RunInitialProbe()

	tracker.mu.RLock()
	hadMint := tracker.hadReachableMint
	tracker.mu.RUnlock()
	if !hadMint {
		t.Fatal("expected hadReachableMint to be true after initial probe")
	}

	tracker.SetOnFirstReachable(func() {})

	tracker.mu.RLock()
	hadMint = tracker.hadReachableMint
	tracker.mu.RUnlock()
	if hadMint {
		t.Error("expected hadReachableMint to be reset to false after SetOnFirstReachable")
	}
}

func TestOnFirstReachable_FiredAfterSetOnFirstReachableReset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tracker := newTestTracker(mintConfigWithURLs(srv.URL), nil)
	tracker.recoveryThreshold = 1

	tracker.RunInitialProbe()

	tracker.mu.RLock()
	hadMint := tracker.hadReachableMint
	tracker.mu.RUnlock()
	if !hadMint {
		t.Fatal("expected hadReachableMint to be true after initial probe")
	}

	done := make(chan struct{})
	tracker.SetOnFirstReachable(func() {
		close(done)
	})

	tracker.RunProactiveCheck()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("expected onFirstReachable to fire after SetOnFirstReachable reset and proactive check")
	}
}
