package tollwallet

import (
	"errors"
	"testing"
)

// The #719 crash window closes at this seam: an uninitialized wallet (a
// degraded boot, a bare Merchant) must answer the resume with the typed
// not-initialized error rather than a nil-pointer panic — the daemon logs
// it non-fatally and the journaled intents wait for a boot that has a
// wallet.
func TestResumePendingSwapsOnUninitializedWallet(t *testing.T) {
	var tw TollWallet
	_, _, err := tw.ResumePendingSwaps()
	if !errors.Is(err, ErrWalletNotInitialized) {
		t.Fatalf("ResumePendingSwaps on an uninitialized wallet: err = %v, want ErrWalletNotInitialized", err)
	}
}

// GonutsWallet must delegate to the wrapped TollWallet — the port cannot
// silently swallow the resume the daemon's crash recovery depends on.
func TestGonutsWalletDelegatesResume(t *testing.T) {
	gw := &GonutsWallet{inner: &TollWallet{}}
	_, _, err := gw.ResumePendingSwaps()
	if !errors.Is(err, ErrWalletNotInitialized) {
		t.Fatalf("GonutsWallet.ResumePendingSwaps: err = %v, want the wrapped wallet's ErrWalletNotInitialized (delegation broke)", err)
	}
}
