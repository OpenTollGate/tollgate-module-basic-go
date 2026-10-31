package merchant

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

// writeBackendConfig writes a minimal valid config with an optional extra
// top-level fragment (the wallet section) and returns a ConfigManager.
func writeBackendConfig(t *testing.T, extra string) *config_manager.ConfigManager {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	installPath := filepath.Join(dir, "install.json")
	identitiesPath := filepath.Join(dir, "identities.json")

	body := `{"config_version":"v0.0.10","log_level":"info","accepted_mints":[{"url":"https://mint.example/","min_balance":64,"balance_tolerance_percent":10,"payout_interval_seconds":60,"min_payout_amount":128,"price_per_step":1,"price_unit":"sat","purchase_min_steps":1}],"step_size":22020096,"metric":"bytes","show_setup":true`
	if extra != "" {
		body += "," + extra
	}
	body += "}"
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cm, err := config_manager.NewConfigManager(configPath, installPath, identitiesPath)
	if err != nil {
		t.Fatal(err)
	}
	return cm
}

// TestDegradedWalletFactoryRespectsBackendSelection pins the #1157 fix: the
// degraded-mode wallet factory must route through the backend selection —
// under wallet.backend=cdk NO second wallet implementation (the in-process
// gonuts wallet) may ever be constructed, or a degrade/upgrade window
// silently splits operator value into a wallet the operator never chose.
// A dead sidecar socket yields a WALLET-LESS degraded merchant (fail
// closed), never a silent fallback.
func TestDegradedWalletFactoryRespectsBackendSelection(t *testing.T) {
	mints := []string{"https://mint.example/"}

	t.Run("cdk_backend_factory_never_constructs_in_process_wallet", func(t *testing.T) {
		cm := writeBackendConfig(t, `"wallet":{"backend":"cdk","socket":"/nonexistent/w.sock"}`)
		f := walletFactoryForBackend(cm)
		walletDir := filepath.Join(t.TempDir(), "wallet")
		w, err := f(walletDir, mints)
		if err == nil {
			t.Fatalf("dead sidecar socket must fail the factory (wallet-less degraded), got %T", w)
		}
		if w != nil {
			t.Fatalf("factory must return no wallet on failure, got %T", w)
		}
		// The in-process wallet's signature side effect must NOT exist: no
		// wallet directory contents created by the legacy constructor.
		if entries, readErr := os.ReadDir(walletDir); readErr == nil && len(entries) > 0 {
			t.Fatalf("legacy wallet artifacts created under backend=cdk: %v", entries)
		}
	})

	t.Run("default_backend_factory_is_the_legacy_path", func(t *testing.T) {
		cm := writeBackendConfig(t, "")
		f := walletFactoryForBackend(cm)
		w, err := f(filepath.Join(t.TempDir(), "wallet"), mints)
		if err != nil {
			t.Fatalf("legacy factory must keep working for the default backend: %v", err)
		}
		if w == nil {
			t.Fatal("default backend must construct the in-process wallet")
		}
	})

	t.Run("runtime_downgrade_path_is_wallet_less_on_dead_sidecar", func(t *testing.T) {
		cm := writeBackendConfig(t, `"wallet":{"backend":"cdk","socket":"/nonexistent/w.sock"}`)
		tracker := NewMintHealthTracker(cm)
		deg := NewMerchantDegradedFromFull(cm, tracker)
		if deg.WalletLoaded() {
			t.Fatal("degraded merchant must be wallet-less when the sidecar socket is dead — never a silent second wallet")
		}
	})
}
