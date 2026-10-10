package merchant

import (
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

// installCloudLabNdsctlSeam stages the one fake ndsctl this repository ships
// (tests/cloud-lab/fake-ndsctl.sh — "the cloud-lab seam, not a second one", in
// the happy-path harness's words) as `ndsctl` on this test's PATH, for a
// merchant test that drives the real valve and needs a gate to actually open.
// Without it the suite's colour depends on the host: on any machine with no
// ndsctl binary every gate open fails, the owed path answers grant-pending,
// and the guard suite's grant assertion fails (#726, #770). The seam's
// contract is the cloud lab's — auth/deauth always succeed, `json` answers an
// Authenticated client with readable counters — which is exactly what a grant
// needs. The auth/deauth log is pointed at the test's own TempDir via
// NDSCTL_LOG, so concurrent suites never share /tmp/ndsctl.log.
func installCloudLabNdsctlSeam(t *testing.T) string {
	t.Helper()

	seam, err := filepath.Abs(filepath.Join("..", "..", "tests", "cloud-lab", "fake-ndsctl.sh"))
	if err != nil {
		t.Fatalf("resolve the cloud-lab ndsctl seam: %v", err)
	}
	if info, statErr := os.Stat(seam); statErr != nil || info.IsDir() {
		t.Fatalf("the cloud-lab ndsctl seam is missing (the happy-path harness depends on the same file): %s", seam)
	}
	body, err := os.ReadFile(seam)
	if err != nil {
		t.Fatalf("read the cloud-lab ndsctl seam: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ndsctl"), body, 0o755); err != nil {
		t.Fatalf("stage the cloud-lab ndsctl seam: %v", err)
	}

	logPath := filepath.Join(dir, "ndsctl.log")
	t.Setenv("NDSCTL_LOG", logPath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
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
