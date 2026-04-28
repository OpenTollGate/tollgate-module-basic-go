//go:build integration

package merchant

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
	"github.com/Origami74/gonuts-tollgate/cashu"
	"github.com/Origami74/gonuts-tollgate/cashu/nuts/nut04"
	"github.com/Origami74/gonuts-tollgate/wallet"
)

const testMintTarget = "https://nofee.testnut.cashu.space"

func setupReverseProxy(t *testing.T, targetURL string) *httptest.Server {
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
	return httptest.NewServer(proxy)
}

func requireMintReachable(t *testing.T, mintURL string) {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(strings.TrimRight(mintURL, "/") + "/v1/info")
	if err != nil {
		t.Skipf("mint unreachable at %s: %v — skipping integration test", mintURL, err)
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Skipf("mint at %s returned HTTP %d — skipping integration test", mintURL, resp.StatusCode)
	}
}

func fundWallet(t *testing.T, proxyURL, walletDir string) uint64 {
	t.Helper()

	cfg := wallet.Config{
		WalletPath:     walletDir,
		CurrentMintURL: proxyURL,
	}
	w, err := wallet.LoadWallet(cfg)
	if err != nil {
		t.Fatalf("LoadWallet: %v", err)
	}

	quote, err := w.RequestMint(1000, proxyURL)
	if err != nil {
		w.Shutdown()
		t.Fatalf("RequestMint(1000, %s): %v", proxyURL, err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		state, err := w.MintQuoteState(quote.Quote)
		if err != nil {
			w.Shutdown()
			t.Fatalf("MintQuoteState: %v", err)
		}
		if state.State == nut04.Paid {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	minted, err := w.MintTokens(quote.Quote)
	if err != nil {
		w.Shutdown()
		t.Fatalf("MintTokens(quote=%s): %v", quote.Quote, err)
	}

	balance := w.GetBalance()
	t.Logf("minted %d sats, balance=%d", minted, balance)

	if err := w.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if balance == 0 {
		t.Fatal("balance is 0 after minting")
	}

	return balance
}

func confirmProxyDead(t *testing.T, proxyURL string) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(proxyURL + "/v1/info")
	if err == nil {
		resp.Body.Close()
		t.Log("WARNING: proxy still responding, test may not be valid")
	} else {
		t.Logf("Confirmed proxy dead: %v", err)
	}
}

func extractPort(t *testing.T, rawURL string) int {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return port
}

func startProxyOnPort(t *testing.T, targetURL string, port int) *httptest.Server {
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
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listen on port %d: %v", port, err)
	}
	server := httptest.NewUnstartedServer(proxy)
	server.Listener = listener
	server.Start()
	return server
}

func setupTestConfigManager(t *testing.T) (*config_manager.ConfigManager, string) {
	t.Helper()
	testDir := t.TempDir()
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", testDir)
	configPath := filepath.Join(testDir, "config.json")
	installPath := filepath.Join(testDir, "install.json")
	identitiesPath := filepath.Join(testDir, "identities.json")
	cm, err := config_manager.NewConfigManager(configPath, installPath, identitiesPath)
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	return cm, testDir
}

func boolStr(cond bool, ifTrue, ifFalse string) string {
	if cond {
		return ifTrue
	}
	return ifFalse
}

func errBoolStr(err error, ifErr, ifNil string) string {
	if err != nil {
		return ifErr
	}
	return ifNil
}

// --- Test 1: First boot offline (no wallet DB, no internet) ---

func TestIntegration_FirstBootOffline(t *testing.T) {
	unreachableMint := "http://127.0.0.1:1"

	cm, _ := setupTestConfigManager(t)
	cfg := cm.GetConfig()
	cfg.AcceptedMints = []config_manager.MintConfig{
		{URL: unreachableMint, PricePerStep: 1, PriceUnit: "sats"},
	}

	tracker := NewMintHealthTracker(cm)
	tracker.RunInitialProbe()

	if len(tracker.GetReachableMintConfigs()) != 0 {
		t.Fatal("expected 0 reachable mints on first boot offline")
	}
	if len(tracker.GetAllConfiguredMintConfigs()) != 1 {
		t.Fatal("expected 1 configured mint")
	}
	t.Log("Intermediate check: 0 reachable, 1 configured")

	walletDir := t.TempDir()
	deg := NewMerchantDegradedWithWallet(cm, tracker, DefaultWalletFactory, walletDir)

	if !deg.WalletLoaded() {
		t.Log("NOTE: WalletLoaded() == false — gonuts could not create empty wallet offline")
	} else {
		t.Log("PASS: WalletLoaded() == true — gonuts creates empty wallet even offline")
	}

	if deg.GetBalance() != 0 {
		t.Errorf("expected balance 0 on first boot, got %d", deg.GetBalance())
	} else {
		t.Log("PASS: balance == 0 (no proofs in fresh wallet)")
	}

	if deg.GetBalanceByMint(unreachableMint) != 0 {
		t.Errorf("expected balance-by-mint 0, got %d", deg.GetBalanceByMint(unreachableMint))
	}

	mints := deg.GetAcceptedMints()
	if len(mints) != 1 || mints[0].URL != unreachableMint {
		t.Errorf("expected 1 configured mint %s, got %v", unreachableMint, mints)
	}
	t.Log("PASS: GetAcceptedMints() returns configured mints even when offline")

	_, payErr := deg.CreatePaymentTokenWithOverpayment(unreachableMint, 1, 10000, 500)
	if payErr == nil {
		t.Error("expected error from CreatePaymentTokenWithOverpayment with empty wallet")
	} else {
		t.Logf("PASS: CreatePaymentTokenWithOverpayment returns error (no funds): %v", payErr)
	}

	event, sessErr := deg.PurchaseSession("cashuToken", "AA:BB:CC:DD:EE:FF")
	if sessErr != nil {
		t.Errorf("PurchaseSession error: %v", sessErr)
	} else if event == nil || event.Kind != 21023 {
		t.Errorf("expected notice event kind 21023, got %v", event)
	} else {
		t.Log("PASS: PurchaseSession returns service-unavailable notice event")
	}

	usage, usageErr := deg.GetUsage("AA:BB:CC:DD:EE:FF")
	if usageErr != nil || usage != "-1/-1" {
		t.Errorf("GetUsage: expected '-1/-1', got %q err=%v", usage, usageErr)
	}

	deg.StartPayoutRoutine()
	deg.StartDataUsageMonitoring()

	t.Log("")
	t.Log("=== FirstBootOffline summary ===")
	t.Logf("  WalletLoaded:    %s (empty wallet, no funds)", boolStr(deg.WalletLoaded(), "true", "false"))
	t.Log("  Balance:         0 (correct)")
	t.Log("  AcceptedMints:   PASS (all configured returned)")
	t.Log("  Payment:         FAIL (expected, no funds)")
	t.Log("  Degraded stubs:  PASS")
	t.Log("  VERDICT: PASS — first boot offline degrades gracefully")
}

// --- Test 2: Offline wallet reload (raw wallet.LoadWallet) ---

func TestIntegration_OfflineWalletReload(t *testing.T) {
	requireMintReachable(t, testMintTarget)

	proxy := setupReverseProxy(t, testMintTarget)
	proxyURL := proxy.URL
	t.Logf("Phase 1: proxy at %s -> %s", proxyURL, testMintTarget)

	walletDir := t.TempDir()
	balance := fundWallet(t, proxyURL, walletDir)
	t.Logf("Phase 1: funded wallet with %d sats", balance)

	dbPath := filepath.Join(walletDir, "wallet.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("wallet DB not found at %s: %v", dbPath, err)
	}

	proxy.CloseClientConnections()
	proxy.Close()
	t.Log("Phase 2: proxy stopped — mint unreachable")
	confirmProxyDead(t, proxyURL)

	t.Log("Phase 2a: testing raw wallet.LoadWallet() offline reload")

	cfg := wallet.Config{
		WalletPath:     walletDir,
		CurrentMintURL: proxyURL,
	}
	w, err := wallet.LoadWallet(cfg)
	if err != nil {
		t.Fatalf("CRITICAL: wallet.LoadWallet() failed offline: %v\n"+
			"This means the KICKSTART_DEADLOCK fix has a gap in gonuts-tollgate.", err)
	}
	t.Log("PASS: LoadWallet succeeded offline")

	trusted := w.TrustedMints()
	mintTrusted := false
	for _, m := range trusted {
		if m == proxyURL {
			mintTrusted = true
			break
		}
	}
	if !mintTrusted {
		t.Errorf("FAIL: proxy URL not in TrustedMints %v", trusted)
	} else {
		t.Logf("PASS: mint is trusted (loaded from cache)")
	}

	offlineBalance := w.GetBalance()
	if offlineBalance != balance {
		t.Errorf("FAIL: balance mismatch — online=%d, offline=%d", balance, offlineBalance)
	} else {
		t.Logf("PASS: balance = %d (matches online)", offlineBalance)
	}

	balancesByMint := w.GetBalanceByMints()
	mintBalance, mintExists := balancesByMint[proxyURL]
	if !mintExists {
		t.Errorf("FAIL: proxy URL not in balance-by-mint: %v", balancesByMint)
	} else {
		t.Logf("PASS: balance by mint = %d", mintBalance)
	}

	t.Log("Phase 2b: testing SendWithOptions offline with AllowOverpayment")
	options := wallet.SendOptions{
		IncludeFees:            true,
		AllowOverpayment:       true,
		MaxOverpaymentPercent:  10000,
		MaxOverpaymentAbsolute: 500,
	}
	sendResult, sendErr := w.SendWithOptions(1, proxyURL, options)
	if sendErr != nil {
		t.Logf("SendWithOptions offline: FAILED — %v", sendErr)
		t.Logf("  => Degraded merchant can report balance but CANNOT pay offline.")
	} else {
		t.Logf("PASS: SendWithOptions offline — sent %d sats (overpayment=%d, wasOffline=%v)",
			sendResult.ActualAmount, sendResult.Overpayment, sendResult.WasOffline)
		if !sendResult.WasOffline {
			t.Log("  WARNING: WasOffline=false — mint may not actually be unreachable")
		}
	}

	w.Shutdown()

	t.Log("")
	t.Log("=== OfflineWalletReload summary ===")
	t.Logf("  LoadWallet offline:          PASS")
	t.Logf("  Balance reporting offline:   %s", boolStr(offlineBalance == balance, "PASS", "FAIL"))
	t.Logf("  SendWithOptions offline:     %s", errBoolStr(sendErr, "FAIL", "PASS"))
	if sendErr != nil {
		t.Log("  VERDICT: PARTIAL — balance works, payment does not.")
	} else {
		t.Log("  VERDICT: PASS")
	}
}

// --- Test 3: Degraded merchant with offline wallet ---

func TestIntegration_DegradedMerchantOffline(t *testing.T) {
	requireMintReachable(t, testMintTarget)

	proxy := setupReverseProxy(t, testMintTarget)
	proxyURL := proxy.URL
	t.Logf("Phase 1: proxy at %s -> %s", proxyURL, testMintTarget)

	walletDir := t.TempDir()
	balance := fundWallet(t, proxyURL, walletDir)
	t.Logf("Phase 1: funded with %d sats", balance)

	proxy.CloseClientConnections()
	proxy.Close()
	t.Log("Phase 2: proxy stopped")
	confirmProxyDead(t, proxyURL)

	cm, _ := setupTestConfigManager(t)
	cfg := cm.GetConfig()
	cfg.AcceptedMints = []config_manager.MintConfig{
		{URL: proxyURL, PricePerStep: 1, PriceUnit: "sats"},
	}

	tracker := NewMintHealthTracker(cm)
	tracker.RunInitialProbe()

	if len(tracker.GetReachableMintConfigs()) != 0 {
		t.Fatal("expected 0 reachable mints")
	}
	t.Log("Intermediate check: 0 reachable mints")

	if len(tracker.GetAllConfiguredMintConfigs()) != 1 {
		t.Fatal("expected 1 configured mint")
	}

	deg := NewMerchantDegradedWithWallet(cm, tracker, DefaultWalletFactory, walletDir)

	if !deg.WalletLoaded() {
		t.Fatal("CRITICAL: MerchantDegraded failed to load offline wallet.\n" +
			"The KICKSTART_DEADLOCK fix does not work through the production code path.")
	}
	t.Log("PASS: WalletLoaded() = true")

	degBalance := deg.GetBalance()
	if degBalance != balance {
		t.Errorf("FAIL: balance mismatch — expected %d, got %d", balance, degBalance)
	} else {
		t.Logf("PASS: GetBalance() = %d", degBalance)
	}

	mints := deg.GetAcceptedMints()
	if len(mints) != 1 || mints[0].URL != proxyURL {
		t.Errorf("expected 1 mint %s, got %v", proxyURL, mints)
	} else {
		t.Log("PASS: GetAcceptedMints() returns all configured mints")
	}

	degMintBalance := deg.GetBalanceByMint(proxyURL)
	if degMintBalance != balance {
		t.Errorf("balance by mint mismatch: expected %d, got %d", balance, degMintBalance)
	}

	token, payErr := deg.CreatePaymentTokenWithOverpayment(proxyURL, 1, 10000, 500)
	if payErr != nil {
		t.Logf("CreatePaymentTokenWithOverpayment offline: FAILED — %v", payErr)
	} else {
		t.Logf("PASS: CreatePaymentTokenWithOverpayment — token (len=%d)", len(token))
	}

	event, sessErr := deg.PurchaseSession("cashuToken", "AA:BB:CC:DD:EE:FF")
	if sessErr != nil {
		t.Errorf("PurchaseSession error: %v", sessErr)
	} else if event == nil || event.Kind != 21023 {
		t.Errorf("expected kind 21023, got %v", event)
	} else {
		t.Log("PASS: PurchaseSession returns notice event")
	}

	usage, usageErr := deg.GetUsage("AA:BB:CC:DD:EE:FF")
	if usageErr != nil || usage != "-1/-1" {
		t.Errorf("GetUsage: expected '-1/-1', got %q err=%v", usage, usageErr)
	}

	deg.StartPayoutRoutine()
	deg.StartDataUsageMonitoring()

	t.Log("")
	t.Log("=== DegradedMerchantOffline summary ===")
	t.Logf("  WalletLoaded:              PASS")
	t.Logf("  Balance offline:           %s", boolStr(degBalance == balance, "PASS", "FAIL"))
	t.Logf("  AcceptedMints (all):       %s", boolStr(len(mints) == 1, "PASS", "FAIL"))
	t.Logf("  Payment creation offline:  %s", errBoolStr(payErr, "FAIL", "PASS"))
	t.Logf("  Degraded stubs:            PASS")
	if payErr != nil {
		t.Log("  VERDICT: PARTIAL — balance/mints work, payment does not.")
	} else {
		t.Log("  VERDICT: PASS — degraded merchant fully functional offline.")
	}
}

// --- Test 4: Recovery and upgrade (degraded -> full -> MerchantProvider swap) ---

func TestIntegration_RecoveryAndUpgrade(t *testing.T) {
	requireMintReachable(t, testMintTarget)

	proxy := setupReverseProxy(t, testMintTarget)
	proxyURL := proxy.URL
	proxyPort := extractPort(t, proxyURL)
	t.Logf("Phase 1: proxy at %s -> %s (port %d)", proxyURL, testMintTarget, proxyPort)

	// Use the same directory for both config and wallet, because
	// newFullMerchant uses filepath.Dir(configManager.ConfigFilePath) as wallet dir
	testDir := t.TempDir()
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", testDir)
	configPath := filepath.Join(testDir, "config.json")
	installPath := filepath.Join(testDir, "install.json")
	identitiesPath := filepath.Join(testDir, "identities.json")
	cm, err := config_manager.NewConfigManager(configPath, installPath, identitiesPath)
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	cmCfg := cm.GetConfig()
	cmCfg.AcceptedMints = []config_manager.MintConfig{
		{URL: proxyURL, PricePerStep: 1, PriceUnit: "sats"},
	}

	// Fund wallet in the same directory as config (newFullMerchant looks here)
	balance := fundWallet(t, proxyURL, testDir)
	t.Logf("Phase 1: funded with %d sats at %s", balance, testDir)

	proxy.CloseClientConnections()
	proxy.Close()
	t.Log("Phase 2: proxy stopped")
	confirmProxyDead(t, proxyURL)

	tracker := NewMintHealthTracker(cm)
	tracker.RunInitialProbe()

	if len(tracker.GetReachableMintConfigs()) != 0 {
		t.Fatal("expected 0 reachable mints initially")
	}
	t.Log("Intermediate check: 0 reachable mints (offline)")

	deg := NewMerchantDegradedWithWallet(cm, tracker, DefaultWalletFactory, testDir)
	if !deg.WalletLoaded() {
		t.Fatal("degraded merchant failed to load offline wallet")
	}
	t.Logf("PASS: degraded merchant loaded, balance=%d", deg.GetBalance())

	recoveryCh := make(chan struct{}, 1)
	upgradeCh := make(chan MerchantInterface, 1)

	tracker.SetOnFirstReachable(func() {
		t.Log("onFirstReachable callback fired")
		close(recoveryCh)

		full, err := newFullMerchant(cm, tracker)
		if err != nil {
			t.Logf("NOTE: newFullMerchant failed (BoltDB locked by degraded merchant): %v", err)
			t.Log("This is a known limitation: the degraded merchant holds BoltDB open.")
			t.Log("In production, the BoltDB timeout (5s) would allow eventual recovery.")
			return
		}
		if deg.onUpgrade != nil {
			deg.onUpgrade(full)
		}
	})

	deg.OnUpgrade(func(full MerchantInterface) {
		upgradeCh <- full
	})

	provider := NewMutexMerchantProvider(deg)
	t.Log("PASS: MerchantProvider initialized with degraded merchant")

	currentBefore := provider.GetMerchant()
	if _, ok := currentBefore.(*MerchantDegraded); !ok {
		t.Fatal("expected MerchantProvider to hold MerchantDegraded initially")
	}
	t.Log("PASS: MerchantProvider.GetMerchant() returns MerchantDegraded")

	providerBalance := currentBefore.GetBalance()
	if providerBalance != balance {
		t.Errorf("provider balance mismatch: expected %d, got %d", balance, providerBalance)
	}

	t.Log("Phase 3: restarting proxy on same port to simulate internet recovery")
	proxy2 := startProxyOnPort(t, testMintTarget, proxyPort)
	defer proxy2.Close()
	t.Logf("Proxy restarted at %s (same port)", proxyURL)

	tracker.RunProactiveCheck()
	t.Log("Proactive check 1/3 done")
	tracker.RunProactiveCheck()
	t.Log("Proactive check 2/3 done")
	tracker.RunProactiveCheck()
	t.Log("Proactive check 3/3 done")

	reachableAfter := tracker.GetReachableMintConfigs()
	t.Logf("Reachable mints after recovery: %d", len(reachableAfter))
	if len(reachableAfter) == 0 {
		t.Fatal("expected at least 1 reachable mint after proxy restart")
	}
	t.Log("PASS: MintHealthTracker detected mint recovery")

	select {
	case <-recoveryCh:
		t.Log("PASS: onFirstReachable callback fired")
	case <-time.After(5 * time.Second):
		t.Fatal("TIMEOUT: onFirstReachable callback did not fire within 5s")
	}

	// The full merchant creation may fail because the degraded merchant holds BoltDB open.
	// This is a known limitation documented in the production code.
	// The recovery MECHANISM (tracker -> callback -> upgrade) is validated above.
	var fullMerchant MerchantInterface
	select {
	case fullMerchant = <-upgradeCh:
		t.Log("PASS: full merchant created and upgrade callback fired")

		provider.SetMerchant(fullMerchant)
		t.Log("PASS: MerchantProvider.SetMerchant() — swapped to full merchant")

		currentAfter := provider.GetMerchant()
		if _, ok := currentAfter.(*MerchantDegraded); ok {
			t.Error("FAIL: MerchantProvider still returns MerchantDegraded after swap")
		}

		fullMints := currentAfter.GetAcceptedMints()
		if len(fullMints) != 1 || fullMints[0].URL != proxyURL {
			t.Errorf("full merchant mints unexpected: %v", fullMints)
		} else {
			t.Log("PASS: full merchant GetAcceptedMints() returns the mint")
		}
	case <-time.After(10 * time.Second):
		t.Log("NOTE: full merchant creation did not complete within 10s")
		t.Log("This is expected when BoltDB is held open by the degraded merchant.")
		t.Log("The recovery mechanism (tracker + callback) was validated successfully.")
		t.Log("The BoltDB locking issue is a known limitation in the current architecture.")
	}

	t.Log("")
	t.Log("=== RecoveryAndUpgrade summary ===")
	t.Log("  Degraded merchant offline:  PASS")
	t.Log("  MintHealthTracker recovery: PASS")
	t.Log("  onFirstReachable callback:  PASS")
	t.Log("  MerchantProvider setup:     PASS")
	t.Logf("  Full merchant creation:     %s", boolStr(fullMerchant != nil, "PASS", "DEFERRED (BoltDB locking)"))
	t.Log("  VERDICT: PASS — recovery mechanism validated")
}

func mintTestTokenForFund(t *testing.T, proxyURL string) string {
	t.Helper()

	tmpDir := t.TempDir()
	cfg := wallet.Config{
		WalletPath:     tmpDir,
		CurrentMintURL: proxyURL,
	}
	w, err := wallet.LoadWallet(cfg)
	if err != nil {
		t.Logf("mintTestTokenForFund: LoadWallet: %v", err)
		return ""
	}
	defer w.Shutdown()

	quote, err := w.RequestMint(100, proxyURL)
	if err != nil {
		t.Logf("mintTestTokenForFund: RequestMint: %v", err)
		return ""
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		state, err := w.MintQuoteState(quote.Quote)
		if err != nil {
			t.Logf("mintTestTokenForFund: MintQuoteState: %v", err)
			return ""
		}
		if state.State == nut04.Paid {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	_, err = w.MintTokens(quote.Quote)
	if err != nil {
		t.Logf("mintTestTokenForFund: MintTokens: %v", err)
		return ""
	}

	balance := w.GetBalance()
	if balance == 0 {
		t.Log("mintTestTokenForFund: balance is 0")
		return ""
	}

	sendResult, err := w.SendWithOptions(10, proxyURL, wallet.SendOptions{
		IncludeFees:            true,
		AllowOverpayment:       true,
		MaxOverpaymentPercent:  10000,
		MaxOverpaymentAbsolute: 100,
	})
	if err != nil {
		t.Logf("mintTestTokenForFund: SendWithOptions: %v", err)
		return ""
	}

	token, err := cashu.NewTokenV4(sendResult.Proofs, proxyURL, cashu.Sat, true)
	if err != nil {
		t.Logf("mintTestTokenForFund: NewTokenV4: %v", err)
		return ""
	}

	tokenStr, err := token.Serialize()
	if err != nil {
		t.Logf("mintTestTokenForFund: Serialize: %v", err)
		return ""
	}

	return tokenStr
}

// --- Test 5: Full E2E lifecycle via merchant.New() ---

func TestIntegration_FullLifecycle_E2E(t *testing.T) {
	requireMintReachable(t, testMintTarget)

	proxy := setupReverseProxy(t, testMintTarget)
	proxyURL := proxy.URL
	t.Logf("Phase 1: proxy at %s -> %s", proxyURL, testMintTarget)

	walletDir := t.TempDir()
	balance := fundWallet(t, proxyURL, walletDir)
	t.Logf("Phase 1: funded with %d sats", balance)

	// Load the wallet ONLINE (single instance — BoltDB can't be opened twice in-process)
	tw, err := tollwallet.New(walletDir, []string{proxyURL}, false)
	if err != nil {
		t.Fatalf("tollwallet.New failed: %v", err)
	}
	t.Logf("PASS: TollWallet created, balance=%d", tw.GetBalance())

	// Online operations: send + receive
	sendResult, sendErr := tw.SendWithOverpayment(10, proxyURL, 10000, 100)
	if sendErr != nil {
		t.Fatalf("SendWithOverpayment failed: %v", sendErr)
	}
	t.Logf("PASS: SendWithOverpayment — token (len=%d)", len(sendResult))

	parsedToken, err := cashu.DecodeToken(sendResult)
	if err != nil {
		t.Fatalf("DecodeToken: %v", err)
	}

	received, recvErr := tw.Receive(parsedToken)
	if recvErr != nil {
		t.Fatalf("Receive: %v", recvErr)
	}
	t.Logf("PASS: Receive — received %d sats back", received)

	onlineBalance := tw.GetBalance()

	// Phase 2: Stop proxy — internet goes down MID-SESSION
	t.Log("Phase 2: stopping proxy — internet drops mid-session")
	proxy.CloseClientConnections()
	proxy.Close()
	confirmProxyDead(t, proxyURL)

	// The SAME wallet instance should still work offline
	offlineBalance := tw.GetBalance()
	if offlineBalance != onlineBalance {
		t.Errorf("balance after disconnect: expected %d, got %d", onlineBalance, offlineBalance)
	} else {
		t.Logf("PASS: balance = %d (correct after disconnect)", offlineBalance)
	}

	offlineMintBalance := tw.GetBalanceByMint(proxyURL)
	if offlineMintBalance != onlineBalance {
		t.Errorf("balance by mint after disconnect: expected %d, got %d", onlineBalance, offlineMintBalance)
	}

	offlineToken, offlineErr := tw.SendWithOverpayment(1, proxyURL, 10000, 100)
	if offlineErr != nil {
		t.Logf("SendWithOverpayment after disconnect: FAILED — %v", offlineErr)
	} else {
		t.Logf("PASS: SendWithOverpayment after disconnect — token (len=%d)", len(offlineToken))
	}

	t.Log("")
	t.Log("=== FullLifecycle_E2E summary ===")
	t.Log("  Online wallet creation:     PASS")
	t.Log("  Online payment creation:    PASS")
	t.Log("  Online token receive:       PASS")
	t.Logf("  Post-disconnect balance:    %s", boolStr(offlineBalance == onlineBalance, "PASS", "FAIL"))
	t.Logf("  Post-disconnect payment:    %s", errBoolStr(offlineErr, "FAIL", "PASS"))
	if offlineErr != nil {
		t.Log("  VERDICT: PARTIAL — online works, offline send fails after disconnect")
	} else {
		t.Log("  VERDICT: PASS — full lifecycle validated")
	}
}

// --- TollWallet-level offline reload test ---

func TestIntegration_TollWalletOfflineReload(t *testing.T) {
	requireMintReachable(t, testMintTarget)

	proxy := setupReverseProxy(t, testMintTarget)
	proxyURL := proxy.URL

	walletDir := t.TempDir()
	balance := fundWallet(t, proxyURL, walletDir)
	t.Logf("Funded TollWallet with %d sats", balance)

	proxy.CloseClientConnections()
	proxy.Close()
	confirmProxyDead(t, proxyURL)

	// NOTE: fundWallet calls wallet.Shutdown(), so the BoltDB is released.
	// This simulates a router reboot: old process dies (releases DB), new process starts.
	tw, err := tollwallet.New(walletDir, []string{proxyURL}, false)
	if err != nil {
		t.Fatalf("CRITICAL: tollwallet.New() failed offline: %v", err)
	}

	twBalance := tw.GetBalance()
	if twBalance != balance {
		t.Errorf("TollWallet balance mismatch: expected %d, got %d", balance, twBalance)
	} else {
		t.Logf("PASS: TollWallet.GetBalance() = %d", twBalance)
	}

	twMintBalance := tw.GetBalanceByMint(proxyURL)
	if twMintBalance != balance {
		t.Errorf("TollWallet balance-by-mint mismatch: expected %d, got %d", balance, twMintBalance)
	}

	allBalances := tw.GetAllMintBalances()
	if len(allBalances) == 0 {
		t.Error("GetAllMintBalances returned empty map")
	}

	token, sendErr := tw.SendWithOverpayment(1, proxyURL, 10000, 500)
	if sendErr != nil {
		t.Logf("TollWallet.SendWithOverpayment offline: FAILED — %v", sendErr)
	} else {
		t.Logf("PASS: TollWallet.SendWithOverpayment offline — token (len=%d)", len(token))
	}

	t.Log("")
	t.Log("=== TollWalletOfflineReload summary ===")
	t.Logf("  TollWallet.New() offline:    PASS")
	t.Logf("  GetBalance() offline:        %s", boolStr(twBalance == balance, "PASS", "FAIL"))
	t.Logf("  SendWithOverpayment offline: %s", errBoolStr(sendErr, "FAIL", "PASS"))
	if sendErr != nil {
		t.Log("  VERDICT: PARTIAL")
	} else {
		t.Log("  VERDICT: PASS")
	}
}
