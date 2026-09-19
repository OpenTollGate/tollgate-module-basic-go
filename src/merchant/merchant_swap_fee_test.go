package merchant

import (
	"fmt"
	"testing"

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
