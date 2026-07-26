package merchant

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

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
	cm, testDir := setupTestConfigManager(t)
	cfg := cm.GetConfig()
	if mints != nil {
		cfg.AcceptedMints = mints
	}
	tracker := newTestTracker(cfg, nil)
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
