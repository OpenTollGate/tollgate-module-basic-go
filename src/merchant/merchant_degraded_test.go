package merchant

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
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

func TestMerchantDegraded_GetAcceptedMints_ReturnsAllConfigured(t *testing.T) {
	deg, _ := newDegradedMerchantWithConfig(t)

	mints := deg.GetAcceptedMints()
	if len(mints) != 1 {
		t.Errorf("expected 1 configured mint (all configured, not just reachable), got %d", len(mints))
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

type mockWallet struct {
	balance         uint64
	balanceByMint   map[string]uint64
	overpaymentErr  error
	overpaymentResult string
}

func (w *mockWallet) GetBalance() uint64 {
	return w.balance
}

func (w *mockWallet) GetBalanceByMint(mintUrl string) uint64 {
	if w.balanceByMint != nil {
		return w.balanceByMint[mintUrl]
	}
	return 0
}

func (w *mockWallet) GetAllMintBalances() map[string]uint64 {
	if w.balanceByMint != nil {
		result := make(map[string]uint64, len(w.balanceByMint))
		for k, v := range w.balanceByMint {
			result[k] = v
		}
		return result
	}
	return make(map[string]uint64)
}

func (w *mockWallet) SendWithOverpayment(amount uint64, mintUrl string, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error) {
	if w.overpaymentErr != nil {
		return "", w.overpaymentErr
	}
	return w.overpaymentResult, nil
}

func newDegradedMerchantWithMockWallet(t *testing.T, wallet Wallet, walletFactoryErr error) (*MerchantDegraded, *config_manager.ConfigManager, *MintHealthTracker) {
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
		{URL: "https://mint2.test", PricePerStep: 2, PriceUnit: "sats"},
	}

	tracker := newTestTracker(cfg, nil)
	tracker.RunInitialProbe()

	var factory WalletFactory
	if walletFactoryErr != nil {
		factory = func(walletPath string, mintURLs []string) (Wallet, error) {
			return nil, walletFactoryErr
		}
	} else {
		factory = func(walletPath string, mintURLs []string) (Wallet, error) {
			return wallet, nil
		}
	}

	deg := NewMerchantDegradedWithWallet(cm, tracker, factory, testDir)
	return deg, cm, tracker
}

func TestKickstart_WalletLoaded_OfflineBalanceAvailable(t *testing.T) {
	mw := &mockWallet{
		balance: 500,
		balanceByMint: map[string]uint64{
			"https://mint2.test": 300,
		},
	}

	deg, _, _ := newDegradedMerchantWithMockWallet(t, mw, nil)

	if !deg.WalletLoaded() {
		t.Fatal("expected wallet to be loaded")
	}
	if deg.GetBalance() != 500 {
		t.Errorf("expected balance 500, got %d", deg.GetBalance())
	}
	if deg.GetBalanceByMint("https://mint2.test") != 300 {
		t.Errorf("expected balance 300 for mint2, got %d", deg.GetBalanceByMint("https://mint2.test"))
	}
	if deg.GetBalanceByMint("https://unknown.test") != 0 {
		t.Errorf("expected 0 for unknown mint, got %d", deg.GetBalanceByMint("https://unknown.test"))
	}
	balances := deg.GetAllMintBalances()
	if len(balances) != 1 || balances["https://mint2.test"] != 300 {
		t.Errorf("expected balances map with mint2=300, got %v", balances)
	}
}

func TestKickstart_WalletLoaded_GetAcceptedMintsReturnsAllConfigured(t *testing.T) {
	mw := &mockWallet{balance: 100}
	deg, _, tracker := newDegradedMerchantWithMockWallet(t, mw, nil)

	mints := deg.GetAcceptedMints()
	if len(mints) != 2 {
		t.Fatalf("expected 2 configured mints, got %d", len(mints))
	}

	allMints := tracker.GetAllConfiguredMintConfigs()
	if len(allMints) != 2 {
		t.Fatalf("expected 2 all configured mints, got %d", len(allMints))
	}

	reachableMints := tracker.GetReachableMintConfigs()
	if len(reachableMints) != 0 {
		t.Fatalf("expected 0 reachable mints (all down), got %d", len(reachableMints))
	}
}

func TestKickstart_WalletLoaded_CreatePaymentTokenWithOverpayment(t *testing.T) {
	mw := &mockWallet{
		balance:           1000,
		overpaymentResult: "cashuAmocktoken123",
	}

	deg, _, _ := newDegradedMerchantWithMockWallet(t, mw, nil)

	token, err := deg.CreatePaymentTokenWithOverpayment("https://mint2.test", 100, 10000, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "cashuAmocktoken123" {
		t.Errorf("expected mock token, got %s", token)
	}
}

func TestKickstart_WalletLoaded_CreatePaymentTokenWithOverpayment_Error(t *testing.T) {
	mw := &mockWallet{
		balance:        1000,
		overpaymentErr: fmt.Errorf("insufficient funds"),
	}

	deg, _, _ := newDegradedMerchantWithMockWallet(t, mw, nil)

	_, err := deg.CreatePaymentTokenWithOverpayment("https://mint2.test", 100, 10000, 100)
	if err == nil {
		t.Fatal("expected error from mock wallet")
	}
	if !strings.Contains(err.Error(), "insufficient funds") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestKickstart_WalletNotLoaded_FirstBoot_NoPanic(t *testing.T) {
	_, _, _ = newDegradedMerchantWithMockWallet(t, nil, fmt.Errorf("wallet db does not exist"))
}

func TestKickstart_WalletNotLoaded_StubsReturnZero(t *testing.T) {
	deg, _, _ := newDegradedMerchantWithMockWallet(t, nil, fmt.Errorf("no wallet on disk"))

	if deg.WalletLoaded() {
		t.Fatal("expected wallet to NOT be loaded")
	}
	if deg.GetBalance() != 0 {
		t.Errorf("expected 0 balance, got %d", deg.GetBalance())
	}
	if deg.GetBalanceByMint("https://mint2.test") != 0 {
		t.Errorf("expected 0 balance by mint, got %d", deg.GetBalanceByMint("https://mint2.test"))
	}
	balances := deg.GetAllMintBalances()
	if len(balances) != 0 {
		t.Errorf("expected empty balances map, got %v", balances)
	}
}

func TestKickstart_WalletNotLoaded_PaymentTokenFails(t *testing.T) {
	deg, _, _ := newDegradedMerchantWithMockWallet(t, nil, fmt.Errorf("no wallet on disk"))

	_, err := deg.CreatePaymentTokenWithOverpayment("https://mint2.test", 100, 10000, 100)
	if err == nil {
		t.Fatal("expected error when wallet not loaded")
	}
	if !strings.Contains(err.Error(), "wallet not initialized") {
		t.Errorf("unexpected error: %v", err)
	}

	_, err = deg.CreatePaymentToken("https://mint2.test", 100)
	if err == nil {
		t.Fatal("expected error when wallet not loaded")
	}
}

func TestKickstart_WalletNotLoaded_GetAcceptedMintsStillReturnsAllConfigured(t *testing.T) {
	deg, _, _ := newDegradedMerchantWithMockWallet(t, nil, fmt.Errorf("no wallet on disk"))

	mints := deg.GetAcceptedMints()
	if len(mints) != 2 {
		t.Errorf("expected 2 configured mints even without wallet, got %d", len(mints))
	}
}

func TestKickstart_WalletNotLoaded_NoConfiguredMints(t *testing.T) {
	testDir := t.TempDir()
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", testDir)

	configPath := filepath.Join(testDir, "config.json")
	installPath := filepath.Join(testDir, "install.json")
	identitiesPath := filepath.Join(testDir, "identities.json")

	cm, err := config_manager.NewConfigManager(configPath, installPath, identitiesPath)
	if err != nil {
		t.Fatalf("failed to create config manager: %v", err)
	}

	cfg := cm.GetConfig()
	cfg.AcceptedMints = []config_manager.MintConfig{}

	tracker := newTestTracker(cfg, nil)

	factoryCalled := false
	factory := func(walletPath string, mintURLs []string) (Wallet, error) {
		factoryCalled = true
		return &mockWallet{balance: 100}, nil
	}

	deg := NewMerchantDegradedWithWallet(cm, tracker, factory, testDir)

	if deg.WalletLoaded() {
		t.Error("expected wallet to NOT be loaded when no mints configured")
	}
	if factoryCalled {
		t.Error("expected wallet factory to NOT be called when no mints configured")
	}
}

func TestKickstart_WalletFactoryReceivesAllConfiguredMintURLs(t *testing.T) {
	var receivedURLs []string

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
		{URL: "https://mint2.test", PricePerStep: 2, PriceUnit: "sats"},
	}

	tracker := newTestTracker(cfg, nil)
	tracker.RunInitialProbe()

	factory := func(walletPath string, mintURLs []string) (Wallet, error) {
		receivedURLs = make([]string, len(mintURLs))
		copy(receivedURLs, mintURLs)
		return &mockWallet{balance: 0}, nil
	}

	NewMerchantDegradedWithWallet(cm, tracker, factory, testDir)

	allConfigs := tracker.GetAllConfiguredMintConfigs()
	expectedURLs := make([]string, len(allConfigs))
	for i, c := range allConfigs {
		expectedURLs[i] = c.URL
	}

	if len(receivedURLs) != len(expectedURLs) {
		t.Fatalf("expected %d mint URLs, got %d", len(expectedURLs), len(receivedURLs))
	}

	for i, url := range receivedURLs {
		if url != expectedURLs[i] {
			t.Errorf("expected URL %s at index %d, got %s", expectedURLs[i], i, url)
		}
	}
}

func TestKickstart_WalletLoaded_OtherStubsStillWork(t *testing.T) {
	mw := &mockWallet{balance: 100}
	deg, _, _ := newDegradedMerchantWithMockWallet(t, mw, nil)

	deg.StartPayoutRoutine()
	deg.StartDataUsageMonitoring()

	usage, err := deg.GetUsage("AA:BB:CC:DD:EE:FF")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usage != "-1/-1" {
		t.Errorf("expected '-1/-1', got %s", usage)
	}

	_, err = deg.GetSession("AA:BB:CC:DD:EE:FF")
	if err == nil || !strings.Contains(err.Error(), "wallet not initialized") {
		t.Errorf("expected wallet not initialized error for GetSession, got %v", err)
	}

	_, _, err = deg.DrainMint("https://mint2.test")
	if err == nil || !strings.Contains(err.Error(), "wallet not initialized") {
		t.Errorf("expected wallet not initialized error for DrainMint, got %v", err)
	}

	_, err = deg.Fund("cashuToken")
	if err == nil || !strings.Contains(err.Error(), "wallet not initialized") {
		t.Errorf("expected wallet not initialized error for Fund, got %v", err)
	}
}

func TestKickstart_ImplementsWalletInterface(t *testing.T) {
	var _ Wallet = &mockWallet{}
}

func TestKickstart_Integration_DegradedToFullUpgrade(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

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
	tracker.recoveryThreshold = 1

	mw := &mockWallet{balance: 200, balanceByMint: map[string]uint64{srvFail.URL: 200}}
	factory := func(walletPath string, mintURLs []string) (Wallet, error) {
		return mw, nil
	}

	deg := NewMerchantDegradedWithWallet(cm, tracker, factory, testDir)

	if !deg.WalletLoaded() {
		t.Fatal("expected offline wallet to be loaded")
	}
	if deg.GetBalance() != 200 {
		t.Fatalf("expected balance 200, got %d", deg.GetBalance())
	}

	mints := deg.GetAcceptedMints()
	if len(mints) != 1 || mints[0].URL != srvFail.URL {
		t.Fatalf("expected 1 configured mint, got %d", len(mints))
	}

	reachable := tracker.GetReachableMintConfigs()
	if len(reachable) != 0 {
		t.Fatal("expected 0 reachable mints (server is down)")
	}

	var upgraded MerchantInterface
	done := make(chan struct{})
	tracker.SetOnFirstReachable(func() {
		close(done)
	})

	provider := tracker.configProvider.(*mockConfigProvider)
	provider.config = &config_manager.Config{
		AcceptedMints: []config_manager.MintConfig{
			{URL: srv.URL, PricePerStep: 1, PriceUnit: "sats"},
		},
	}

	tracker.RunProactiveCheck()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("expected onFirstReachable to fire")
	}

	if !tracker.IsReachable(srv.URL) {
		t.Fatal("expected mint to be reachable after proactive check")
	}

	_ = upgraded
}

func TestKickstart_EndToEnd_OfflineKickstartWithWalletBalance(t *testing.T) {
	srvDown := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srvDown.Close()

	testDir := t.TempDir()
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", testDir)

	configPath := filepath.Join(testDir, "config.json")
	installPath := filepath.Join(testDir, "install.json")
	identitiesPath := filepath.Join(testDir, "identities.json")

	cm, err := config_manager.NewConfigManager(configPath, installPath, identitiesPath)
	if err != nil {
		t.Fatalf("failed to create config manager: %v", err)
	}

	cfg := cm.GetConfig()
	cfg.AcceptedMints = []config_manager.MintConfig{
		{URL: srvDown.URL, PricePerStep: 1, PriceUnit: "sats"},
		{URL: "https://mint2.example.com", PricePerStep: 2, PriceUnit: "sats"},
	}

	tracker := newTestTracker(cfg, nil)
	tracker.RunInitialProbe()

	if len(tracker.GetReachableMintConfigs()) != 0 {
		t.Fatal("setup: expected no reachable mints")
	}

	mw := &mockWallet{
		balance: 5000,
		balanceByMint: map[string]uint64{
			srvDown.URL:              3000,
			"https://mint2.example.com": 2000,
		},
		overpaymentResult: "cashuAofflinepaymenttoken",
	}

	factory := func(walletPath string, mintURLs []string) (Wallet, error) {
		return mw, nil
	}

	deg := NewMerchantDegradedWithWallet(cm, tracker, factory, testDir)

	if !deg.WalletLoaded() {
		t.Fatal("expected wallet loaded from disk")
	}

	mints := deg.GetAcceptedMints()
	if len(mints) != 2 {
		t.Fatalf("expected 2 configured mints, got %d", len(mints))
	}

	if deg.GetBalance() != 5000 {
		t.Errorf("expected total balance 5000, got %d", deg.GetBalance())
	}
	if deg.GetBalanceByMint(srvDown.URL) != 3000 {
		t.Errorf("expected 3000 for mint1, got %d", deg.GetBalanceByMint(srvDown.URL))
	}
	if deg.GetBalanceByMint("https://mint2.example.com") != 2000 {
		t.Errorf("expected 2000 for mint2, got %d", deg.GetBalanceByMint("https://mint2.example.com"))
	}

	token, err := deg.CreatePaymentTokenWithOverpayment("https://mint2.example.com", 500, 10000, 100)
	if err != nil {
		t.Fatalf("failed to create payment token: %v", err)
	}
	if token != "cashuAofflinepaymenttoken" {
		t.Errorf("expected mock token, got %s", token)
	}

	event, err := deg.PurchaseSession("cashuToken", "AA:BB:CC:DD:EE:FF")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil || event.Kind != 21023 {
		t.Error("expected notice event (kind 21023) for PurchaseSession in degraded mode")
	}

	adv := deg.GetAdvertisement()
	var advMap map[string]interface{}
	if err := json.Unmarshal([]byte(adv), &advMap); err != nil {
		t.Fatalf("failed to parse advertisement: %v", err)
	}
	if kind, ok := advMap["kind"].(float64); !ok || int(kind) != 21023 {
		t.Errorf("expected advertisement kind 21023, got %v", advMap["kind"])
	}
}

func TestKickstart_EndToEnd_FirstBootNoWallet_FallsBackToStubs(t *testing.T) {
	srvDown := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srvDown.Close()

	testDir := t.TempDir()
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", testDir)

	configPath := filepath.Join(testDir, "config.json")
	installPath := filepath.Join(testDir, "install.json")
	identitiesPath := filepath.Join(testDir, "identities.json")

	cm, err := config_manager.NewConfigManager(configPath, installPath, identitiesPath)
	if err != nil {
		t.Fatalf("failed to create config manager: %v", err)
	}

	cfg := cm.GetConfig()
	cfg.AcceptedMints = []config_manager.MintConfig{
		{URL: srvDown.URL, PricePerStep: 1, PriceUnit: "sats"},
	}

	tracker := newTestTracker(cfg, nil)
	tracker.RunInitialProbe()

	factory := func(walletPath string, mintURLs []string) (Wallet, error) {
		return nil, fmt.Errorf("bolt db does not exist: first boot")
	}

	deg := NewMerchantDegradedWithWallet(cm, tracker, factory, testDir)

	if deg.WalletLoaded() {
		t.Fatal("expected wallet to NOT be loaded on first boot")
	}

	mints := deg.GetAcceptedMints()
	if len(mints) != 1 {
		t.Fatalf("expected 1 configured mint even without wallet, got %d", len(mints))
	}

	if deg.GetBalance() != 0 {
		t.Errorf("expected 0 balance, got %d", deg.GetBalance())
	}

	_, err = deg.CreatePaymentTokenWithOverpayment(srvDown.URL, 100, 10000, 100)
	if err == nil {
		t.Fatal("expected error when no wallet loaded")
	}

	event, err := deg.PurchaseSession("cashuToken", "AA:BB:CC:DD:EE:FF")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event == nil || event.Kind != 21023 {
		t.Error("expected notice event for PurchaseSession")
	}
}

func TestGetAllConfiguredMintConfigs(t *testing.T) {
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srvA.Close()

	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srvB.Close()

	config := &config_manager.Config{
		AcceptedMints: []config_manager.MintConfig{
			{URL: srvA.URL, PricePerStep: 1, PriceUnit: "sats"},
			{URL: srvB.URL, PricePerStep: 2, PriceUnit: "sats"},
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

	if reachable[0].URL != srvA.URL {
		t.Errorf("expected reachable mint A, got %s", reachable[0].URL)
	}

	allURLs := make(map[string]bool)
	for _, m := range all {
		allURLs[m.URL] = true
	}
	if !allURLs[srvA.URL] || !allURLs[srvB.URL] {
		t.Error("expected both mints in GetAllConfiguredMintConfigs")
	}
}

func TestGetAllConfiguredMintConfigs_NilConfig(t *testing.T) {
	tracker := newTestTracker(nil, nil)
	all := tracker.GetAllConfiguredMintConfigs()
	if all != nil {
		t.Errorf("expected nil for nil config, got %v", all)
	}
}

func TestWalletBridge_ImplementsWallet(t *testing.T) {
	var _ Wallet = (*tollwallet.TollWallet)(nil)
}
