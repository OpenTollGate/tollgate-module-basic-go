//go:build cdk_wallet

package tollwallet

// Empirical probe for PR #1040 phase 3: can the cdk-go FFI wallet exist in a
// CUSTOM unit ("mb") against a live cdk-mintd that has an mb keyset?
// Run: TOLLGATE_CDK_MB_PROBE=1 go test -tags cdk_wallet -run TestProbeCdkCustomUnitWallet ./tollwallet/ -v
// Skipped without the env var (it needs the bench mint on 127.0.0.1:3348).

import (
	"fmt"
	"os"
	"strings"
	"testing"

	cdk_ffi "github.com/cashubtc/cdk-go/bindings/cdkffi"
)

func TestProbeCdkCustomUnitWallet(t *testing.T) {
	if os.Getenv("TOLLGATE_CDK_MB_PROBE") == "" {
		t.Skip("probe disabled: set TOLLGATE_CDK_MB_PROBE=1 with the bench mb-mint on 127.0.0.1:3348")
	}
	dir := t.TempDir()
	store := cdk_ffi.WalletStoreSqlite{Path: dir + "/w.sqlite"}
	w, err := cdk_ffi.NewWallet(
		"http://127.0.0.1:3348",
		cdk_ffi.CurrencyUnitCustom{Unit: "mb"},
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		store,
		cdk_ffi.WalletConfig{},
	)
	if err != nil {
		t.Fatalf("NewWallet with CurrencyUnitCustom{mb}: %v", err)
	}
	defer w.Destroy()
	// The mint's mb keyset must be fetchable by this wallet.
	ks, err := w.ActiveKeyset()
	if err != nil {
		t.Fatalf("ActiveKeyset on mb wallet: %v", err)
	}
	t.Logf("mb wallet sees keyset %s unit=%v", ks.Id, ks.Unit)
	if !strings.Contains(fmt.Sprintf("%v", ks.Unit), "mb") {
		t.Errorf("keyset unit = %q, want mb", ks.Unit)
	}
}
