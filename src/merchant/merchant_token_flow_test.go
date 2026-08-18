//go:build testenv && !cdk_wallet

package merchant

import (
	"fmt"
	"strings"
	"testing"

	"github.com/OpenTollGate/gonuts-tollgate/cashu"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

// TestTokenFlowCharacterization pins the observable behavior of the token-receive
// flow in merchant.go. These tests document what the code DOES today, serving as
// a safety net for the Wave 4 refactor that will swap direct cashu.DecodeToken/
// DecodeTokenV4 calls for WalletPort methods.
//
// NOTE: Merchant.tollwallet is a concrete struct value (not an interface), so
// happy-path and already-spent tests cannot be written pre-refactor — the wallet
// cannot be stubbed. Those cases are SKIP'd with documentation. They become
// writable after Wave 4 introduces the WalletPort interface.
// stubReceiveWallet injects only Receive; the embedded interface panics
// on any other call, so untested wallet interaction cannot pass silently.
type stubReceiveWallet struct {
	tollwallet.WalletPort
	receive func(tollwallet.Token) (uint64, error)
}

func (s *stubReceiveWallet) Receive(t tollwallet.Token) (uint64, error) {
	return s.receive(t)
}

func TestTokenFlowCharacterization(t *testing.T) {
	t.Run("empty_token_string", func(t *testing.T) {
		// Fund("") hits the length guard at merchant.go:1073.
		m := &Merchant{}
		amount, err := m.Fund("")
		if err == nil {
			t.Fatal("Fund(\"\") should return error")
		}
		if amount != 0 {
			t.Fatalf("Fund(\"\") amount = %d, want 0", amount)
		}
		if !strings.Contains(err.Error(), "token too short") {
			t.Fatalf("Fund(\"\") error = %q, want substring 'token too short'", err.Error())
		}
	})

	t.Run("malformed_token_string", func(t *testing.T) {
		// Fund with a string >= 10 chars that doesn't start with "cashuB".
		// DecodeTokenV4 checks the prefix at cashu.go and returns an error.
		m := &Merchant{}
		_, err := m.Fund("not-a-valid-cashu-token-format!")
		if err == nil {
			t.Fatal("Fund(malformed) should return error")
		}
		if !strings.Contains(err.Error(), "invalid cashu token format") {
			t.Fatalf("Fund(malformed) error = %q, want substring 'invalid cashu token format'", err.Error())
		}
	})

	t.Run("malformed_v4_prefix", func(t *testing.T) {
		// Fund with "cashuA" prefix (V3) — Fund uses DecodeTokenV4 which
		// expects "cashuB" prefix. This should fail with a decode error.
		m := &Merchant{}
		_, err := m.Fund("cashuAinvalidbase64data")
		if err == nil {
			t.Fatal("Fund(cashuA...) should return error (Fund uses V4 decode)")
		}
		if !strings.Contains(err.Error(), "invalid cashu token format") {
			t.Fatalf("Fund(cashuA...) error = %q, want 'invalid cashu token format'", err.Error())
		}
	})

	t.Run("wallet_receive_error", func(t *testing.T) {
		// Fund with a valid V4 token but a zero-value TollWallet.
		// The zero-value wallet has nil acceptedMints, so Receive returns
		// "Token rejected" error. Fund wraps this as "failed to receive token".
		m := &Merchant{}

		// Construct a valid V4 token using the cashu library.
		proofs := cashu.Proofs{
			{Amount: 1, Id: "00ad", C: "ab", Secret: "test-secret"},
		}
		token, err := cashu.NewTokenV4(proofs, "https://testmint.example.com", cashu.Sat, false)
		if err != nil {
			t.Fatalf("NewTokenV4: %v", err)
		}
		tokenStr, err := token.Serialize()
		if err != nil {
			t.Fatalf("Serialize: %v", err)
		}

		_, err = m.Fund(tokenStr)
		if err == nil {
			t.Fatal("Fund(valid token, zero wallet) should return error")
		}
		// The zero-value wallet rejects the token because no mints are accepted.
		if !strings.Contains(err.Error(), "failed to receive token") {
			t.Fatalf("Fund(valid token, zero wallet) error = %q, want 'failed to receive token'", err.Error())
		}
	})

	t.Run("happy_path_v3_token", func(t *testing.T) {
		proofs := cashu.Proofs{
			{Amount: 2, Id: "00ad", C: "ab", Secret: "stub-secret-v3"},
		}
		tok, err := cashu.NewTokenV3(proofs, "https://testmint.example.com", cashu.Sat, false)
		if err != nil {
			t.Fatalf("NewTokenV3: %v", err)
		}
		tokenStr, err := tok.Serialize()
		if err != nil {
			t.Fatalf("Serialize: %v", err)
		}

		m := &Merchant{tollwallet: &stubReceiveWallet{
			receive: func(tollwallet.Token) (uint64, error) { return 2, nil },
		}}
		amount, err := m.Fund(tokenStr)
		if err != nil {
			t.Fatalf("Fund(v3 token) error = %v, want nil", err)
		}
		if amount != 2 {
			t.Fatalf("Fund(v3 token) amount = %d, want 2", amount)
		}
	})

	t.Run("happy_path_v4_token", func(t *testing.T) {
		proofs := cashu.Proofs{
			{Amount: 4, Id: "00ad", C: "ab", Secret: "stub-secret-v4"},
		}
		tok, err := cashu.NewTokenV4(proofs, "https://testmint.example.com", cashu.Sat, false)
		if err != nil {
			t.Fatalf("NewTokenV4: %v", err)
		}
		tokenStr, err := tok.Serialize()
		if err != nil {
			t.Fatalf("Serialize: %v", err)
		}

		m := &Merchant{tollwallet: &stubReceiveWallet{
			receive: func(tollwallet.Token) (uint64, error) { return 4, nil },
		}}
		amount, err := m.Fund(tokenStr)
		if err != nil {
			t.Fatalf("Fund(v4 token) error = %v, want nil", err)
		}
		if amount != 4 {
			t.Fatalf("Fund(v4 token) amount = %d, want 4", amount)
		}
	})

	t.Run("already_spent_token", func(t *testing.T) {
		proofs := cashu.Proofs{
			{Amount: 1, Id: "00ad", C: "ab", Secret: "stub-secret-spent"},
		}
		tok, err := cashu.NewTokenV4(proofs, "https://testmint.example.com", cashu.Sat, false)
		if err != nil {
			t.Fatalf("NewTokenV4: %v", err)
		}
		tokenStr, err := tok.Serialize()
		if err != nil {
			t.Fatalf("Serialize: %v", err)
		}

		spentErr := fmt.Errorf("token already spent: proofs are used")
		m := &Merchant{tollwallet: &stubReceiveWallet{
			receive: func(tollwallet.Token) (uint64, error) { return 0, spentErr },
		}}
		amount, err := m.Fund(tokenStr)
		if err == nil {
			t.Fatal("Fund(spent token) should return error")
		}
		if amount != 0 {
			t.Fatalf("Fund(spent token) amount = %d, want 0", amount)
		}
		if !strings.Contains(err.Error(), "failed to receive token") {
			t.Fatalf("Fund(spent token) error = %q, want 'failed to receive token' wrapper", err.Error())
		}
		if !strings.Contains(err.Error(), "already spent") {
			t.Fatalf("Fund(spent token) error = %q, want underlying 'already spent' preserved", err.Error())
		}
	})
}

// TestFund_CreditsPostSwapFeeAmount_Fork63 pins the fee-crediting contract
// at the Fund seam (fork issue #63: "5-sat token receives as 4 sats"):
// Fund returns exactly what wallet.Receive reports — the post-swap-fee
// amount, with no face-value re-crediting. The allotment arithmetic over
// these post-fee amounts is table-tested on #342.
func TestFund_CreditsPostSwapFeeAmount_Fork63(t *testing.T) {
	proofs := cashu.Proofs{
		{Amount: 5, Id: "00ad", C: "ab", Secret: "fork63-five-sat"},
	}
	tok, err := cashu.NewTokenV4(proofs, "https://testmint.example.com", cashu.Sat, false)
	if err != nil {
		t.Fatalf("NewTokenV4: %v", err)
	}
	tokenStr, err := tok.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	m := &Merchant{tollwallet: &stubReceiveWallet{
		receive: func(tollwallet.Token) (uint64, error) {
			return 4, nil // wallet reports 1-sat swap fee deducted
		},
	}}
	amount, err := m.Fund(tokenStr)
	if err != nil {
		t.Fatalf("Fund(fork63 token) error = %v, want nil", err)
	}
	if amount != 4 {
		t.Fatalf("Fund(fork63 token) credited %d, want 4 (post-fee; face value is 5)", amount)
	}
}
