package merchant

import (
	"fmt"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

// stubFeeWallet stubs DecodeToken and SwapFeeSats; Receive must not be reached
// when the token is below the fee.
type stubFeeWallet struct {
	tollwallet.WalletPort
	fee uint64
}

func (w *stubFeeWallet) DecodeToken(string) (tollwallet.Token, error) { return panicToken{}, nil }
func (w *stubFeeWallet) SwapFeeSats(tollwallet.Token) (uint64, error) { return w.fee, nil }
func (w *stubFeeWallet) Receive(tollwallet.Token) (uint64, error) {
	return 0, fmt.Errorf("Receive must not be called when the token is below the fee")
}

func TestPurchaseSession_BelowSwapFee(t *testing.T) {
	cm, _ := setupTestConfigManager(t)
	m := &Merchant{
		tollwallet:    &stubFeeWallet{fee: 1},
		configManager: cm,
	}

	ev, err := m.PurchaseSession("cashuAstub", "AA:BB:CC:DD:EE:FF")
	if err != nil {
		t.Fatalf("PurchaseSession: %v", err)
	}
	if ev.Kind != 21023 {
		t.Fatalf("expected a notice event (kind 21023), got kind %d", ev.Kind)
	}

	code := ""
	for _, tag := range ev.Tags {
		if len(tag) >= 2 && tag[0] == "code" {
			code = tag[1]
		}
	}
	if code != "payment-error-below-swap-fee" {
		t.Errorf("code = %q, want payment-error-below-swap-fee (tags: %v)", code, ev.Tags)
	}
}

func TestIsBelowSwapFeeError(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"could not swap proofs: token amount 1 is below the mint's swap fees (1): nothing to swap", true},
		{"could not swap proofs: no outputs provided", true},
		{"token already spent", false},
		{"short keyset ID 0118 not found in mint keysets", false},
	}
	for _, c := range cases {
		if got := isBelowSwapFeeError(fmt.Errorf("%s", c.msg)); got != c.want {
			t.Errorf("isBelowSwapFeeError(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

func TestIsMintUnreachableError(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"could not resolve short keyset IDs: short keyset ID 0118 not found in mint keysets", true},
		{"could not get active keyset: dial tcp: connection refused", true},
		{"Get \"https://mint/v1/keysets\": no such host", true},
		{"token already spent", false},
		{"could not swap proofs: no outputs provided", false},
	}
	for _, c := range cases {
		if got := isMintUnreachableError(fmt.Errorf("%s", c.msg)); got != c.want {
			t.Errorf("isMintUnreachableError(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

func TestIsExpiredKeysetError(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		// The live cdk-mintd 0.17.6 refusal pinned by the rotation lane (#440).
		{"could not swap proofs: Keyset has expired", true},
		{"swap: keyset 0118... has expired", true},
		{"Keyset Has Expired", true},
		// Not expiry: resolution failures stay unreachable-class.
		{"could not resolve short keyset IDs: short keyset ID 0118 not found in mint keysets", false},
		{"token already spent", false},
		{"could not swap proofs: no outputs provided", false},
	}
	for _, c := range cases {
		if got := isExpiredKeysetError(fmt.Errorf("%s", c.msg)); got != c.want {
			t.Errorf("isExpiredKeysetError(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

func TestExpiredKeysetNotClassifiedAsUnreachable(t *testing.T) {
	// "could not swap proofs: Keyset has expired" contains both "keyset" and
	// "could not" — the pre-#440 isMintUnreachableError matched it and told
	// the customer the mint was down when their token was permanently dead.
	msg := "could not swap proofs: Keyset has expired"
	if isMintUnreachableError(fmt.Errorf("%s", msg)) {
		t.Errorf("isMintUnreachableError(%q) = true, want false (#440)", msg)
	}
	if !isExpiredKeysetError(fmt.Errorf("%s", msg)) {
		t.Errorf("isExpiredKeysetError(%q) = false, want true", msg)
	}
}

// wedgedFeeWallet reproduces #525: SwapFeeSats never returns (a wedged or
// partitioned mint — docker pause — parks the fee precheck on the client's
// retry ladder for minutes). Receive answers normally: the payment lane must
// not stall just because the fee cannot be determined.
type wedgedFeeWallet struct {
	tollwallet.WalletPort
	receiveCalled chan struct{}
}

func (w *wedgedFeeWallet) DecodeToken(string) (tollwallet.Token, error) { return panicToken{}, nil }
func (w *wedgedFeeWallet) SwapFeeSats(tollwallet.Token) (uint64, error) {
	select {} // park forever, like a mint that accepts nothing
}
func (w *wedgedFeeWallet) Receive(tollwallet.Token) (uint64, error) {
	close(w.receiveCalled)
	return 0, fmt.Errorf("token already spent")
}

func TestPurchaseSession_FeePrecheckIsTimeBounded(t *testing.T) {
	cm, _ := setupTestConfigManager(t)
	w := &wedgedFeeWallet{receiveCalled: make(chan struct{})}
	m := &Merchant{
		tollwallet:        w,
		configManager:     cm,
		mintHealthTracker: newTestTracker(cm.GetConfig(), nil),
	}

	done := make(chan struct{})
	var err error
	go func() {
		_, err = m.PurchaseSession("cashuAstub", "AA:BB:CC:DD:EE:FF")
		close(done)
	}()

	select {
	case <-done:
		// PurchaseSession returned without waiting for the wedged precheck.
	case <-time.After(8 * time.Second):
		t.Fatal("PurchaseSession stalled on a wedged fee precheck (#525): still blocked after 8s")
	}
	select {
	case <-w.receiveCalled:
	default:
		t.Fatal("the payment lane never reached Receive past the wedged precheck")
	}
	_ = err // the classification of Receive's error is not this test's subject
}
