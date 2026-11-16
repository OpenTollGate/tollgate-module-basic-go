package merchant

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

// waitFor polls cond until it returns true or the timeout elapses. It is the
// test-only replacement for fixed time.Sleep calls that wait on async
// callbacks (which fire via `go cb()` in the production code). A fixed sleep
// is a flake source under -race / CI load: the goroutine may not be scheduled
// within the sleep window. Polling removes that nondeterminism.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
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

func newUnreachableServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	return srv
}

type degradedSetup struct {
	CM      *config_manager.ConfigManager
	Tracker *MintHealthTracker
	TestDir string
	Server  *httptest.Server
}

func newDegradedSetup(t *testing.T, mints []config_manager.MintConfig) *degradedSetup {
	t.Helper()
	testDir := t.TempDir()
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", testDir)
	configPath := filepath.Join(testDir, "config.json")
	installPath := filepath.Join(testDir, "install.json")
	identitiesPath := filepath.Join(testDir, "identities.json")
	// Accepted mints are persisted in the config FILE, not the defensive
	// copy GetConfig returns: mutating the copy silently left the manager
	// on factory defaults (zero mints), so every degraded-mode wallet test
	// failed early at "no reachable mints" and never exercised the
	// storage/mmap error paths it exists to pin (#1158).
	if mints != nil {
		cfg := config_manager.Config{AcceptedMints: mints}
		raw, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("marshal mints config: %v", err)
		}
		if err := os.WriteFile(configPath, raw, 0o644); err != nil {
			t.Fatalf("write mints config: %v", err)
		}
	}
	cm, err := config_manager.NewConfigManager(configPath, installPath, identitiesPath)
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	tracker := newTestTracker(cm.GetConfig(), nil)
	tracker.RunInitialProbe()
	return &degradedSetup{CM: cm, Tracker: tracker, TestDir: testDir}
}

func newDegradedSetupWithServer(t *testing.T, extraMints []config_manager.MintConfig) (*degradedSetup, *httptest.Server) {
	t.Helper()
	srv := newUnreachableServer(t)
	mints := []config_manager.MintConfig{
		{URL: srv.URL, PricePerStep: 1, PriceUnit: "sat"},
	}
	mints = append(mints, extraMints...)
	ds := newDegradedSetup(t, mints)
	ds.Server = srv
	return ds, srv
}

func (ds *degradedSetup) Degraded() *MerchantDegraded {
	return &MerchantDegraded{
		configManager:     ds.CM,
		mintHealthTracker: ds.Tracker,
	}
}

func (ds *degradedSetup) DegradedWithWallet(wallet Wallet, walletErr error) *MerchantDegraded {
	var factory WalletFactory
	if walletErr != nil {
		factory = func(walletPath string, mintURLs []string) (Wallet, error) {
			return nil, walletErr
		}
	} else {
		factory = func(walletPath string, mintURLs []string) (Wallet, error) {
			return wallet, nil
		}
	}
	return NewMerchantDegradedWithWallet(ds.CM, ds.Tracker, factory, ds.TestDir)
}

func (ds *degradedSetup) DegradedWithCustomFactory(factory WalletFactory) *MerchantDegraded {
	return NewMerchantDegradedWithWallet(ds.CM, ds.Tracker, factory, ds.TestDir)
}

func simpleMintConfig(url string) []config_manager.MintConfig {
	return []config_manager.MintConfig{
		{URL: url, PricePerStep: 1, PriceUnit: "sat"},
	}
}

func walletFactory(wallet Wallet, err error) WalletFactory {
	if err != nil {
		return func(walletPath string, mintURLs []string) (Wallet, error) {
			return nil, err
		}
	}
	return func(walletPath string, mintURLs []string) (Wallet, error) {
		return wallet, nil
	}
}

func fmtWalletErr(format string, args ...interface{}) error {
	return fmt.Errorf(format, args...)
}
