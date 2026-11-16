package merchant

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

// TestDegradedWalletFactoryRespectsBackendSelection pins the #1157 fix:
// the degraded-mode wallet factory must route through the backend
// selection — under wallet.backend=cdk NO second wallet implementation
// (the in-process gonuts wallet) may ever be constructed, or a
// degrade/upgrade window silently splits operator value into a wallet the
// operator never chose. A dead sidecar socket yields a WALLET-LESS
// degraded merchant (fail closed), never a silent fallback.
func TestDegradedWalletFactoryRespectsBackendSelection(t *testing.T) {
	mints := []string{"https://mint.example/"}

	t.Run("cdk_backend_factory_never_constructs_in_process_wallet", func(t *testing.T) {
		cm := writeWalletSectionConfig(t, `"wallet":{"backend":"cdk","socket":"/nonexistent/w.sock"}`)
		f := walletFactoryForBackend(cm)
		walletDir := filepath.Join(t.TempDir(), "wallet")
		w, err := f(walletDir, mints)
		if err == nil {
			t.Fatalf("dead sidecar socket must fail the factory (wallet-less degraded), got %T", w)
		}
		if w != nil {
			t.Fatalf("factory must return no wallet on failure, got %T", w)
		}
		if !errors.Is(err, tollwallet.ErrSidecarNotConnected) {
			t.Fatalf("factory failure must be the seam's sidecar error, got: %v", err)
		}
		// The in-process wallet's signature side effect must NOT exist: no
		// wallet directory contents created by the legacy constructor.
		if entries, readErr := os.ReadDir(walletDir); readErr == nil && len(entries) > 0 {
			t.Fatalf("legacy wallet artifacts created under backend=cdk: %v", entries)
		}
	})

	t.Run("default_backend_factory_is_the_legacy_path", func(t *testing.T) {
		cm := writeWalletSectionConfig(t, "")
		f := walletFactoryForBackend(cm)
		w, err := f(filepath.Join(t.TempDir(), "wallet"), mints)
		if err != nil {
			t.Fatalf("legacy factory must keep working for the default backend: %v", err)
		}
		if w == nil {
			t.Fatal("default backend must construct the in-process wallet")
		}
		if err := w.Shutdown(); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	})

	t.Run("runtime_downgrade_path_is_wallet_less_on_dead_sidecar", func(t *testing.T) {
		cm := writeWalletSectionConfig(t, `"wallet":{"backend":"cdk","socket":"/nonexistent/w.sock"}`)
		tracker := NewMintHealthTracker(cm)
		deg := NewMerchantDegradedFromFull(cm, tracker)
		if deg.WalletLoaded() {
			t.Fatal("degraded merchant must be wallet-less when the sidecar socket is dead — never a silent second wallet")
		}
	})
}
