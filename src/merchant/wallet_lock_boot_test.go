package merchant

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

// The boot consequence of a wallet-open failure (#504): a database lock held
// by another process fails the boot with an operator-actionable message;
// every other construction failure keeps the degraded-mode behavior. The
// end-to-end refusal itself (flock → bolt.ErrTimeout → the sentinel through
// the fork's wrap chain) is pinned in tollwallet's wallet_lock_test.go —
// these tests pin the merchant-side decision mapping.
func TestWalletLockBootErrorFailsTheBoot(t *testing.T) {
	for _, wrapped := range []error{
		tollwallet.ErrWalletLocked,
		fmt.Errorf("failed to create wallet: %w", tollwallet.ErrWalletLocked),
		fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", tollwallet.ErrWalletLocked)),
	} {
		err := walletLockBootError(wrapped)
		if err == nil {
			t.Fatalf("a held wallet lock must fail the boot, got nil for: %v", wrapped)
		}
		if !errors.Is(err, tollwallet.ErrWalletLocked) {
			t.Fatalf("the boot error must keep the sentinel for callers, got: %v", err)
		}
		if !strings.Contains(err.Error(), "stop the other wallet holder") {
			t.Fatalf("the boot error must tell the operator what to do, got: %v", err)
		}
	}
}

func TestWalletLockBootErrorDegradesForOtherFailures(t *testing.T) {
	for _, other := range []error{
		errors.New("no reachable mints"),
		fmt.Errorf("failed to create wallet: seed unreadable"),
		nil,
	} {
		if err := walletLockBootError(other); err != nil {
			t.Fatalf("only the held-lock class fails the boot; %v must degrade, got %v", other, err)
		}
	}
}
