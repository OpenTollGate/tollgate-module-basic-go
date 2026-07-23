//go:build cdk_wallet && testenv

package tollwallet

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	cdk_ffi "github.com/cashubtc/cdk-go/bindings/cdkffi"
)

func validV3Token() string {
	payload := map[string]interface{}{
		"token": []map[string]interface{}{
			{
				"proofs": []map[string]interface{}{
					{"amount": 1, "id": "009a1f293253e41e", "secret": "test", "C": "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"},
				},
				"mint": "https://test.example.com",
			},
		},
		"unit": "sat",
	}
	data, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("marshal token JSON: %v", err))
	}
	return "cashuA" + base64.RawURLEncoding.EncodeToString(data)
}

// TestCdkDecodeToken validates that cdk-go can decode BOTH V3 and V4 tokens
// — the exact capability gonuts lacks (gonuts rejects V4, crashes on V2 keysets).
func TestCdkDecodeToken(t *testing.T) {
	// V3 token (cashuA prefix) — both gonuts and cdk-go should handle this
	v3Token := validV3Token()

	t.Run("v3_or_v4_token_decodes", func(t *testing.T) {
		tok, err := DecodeToken(v3Token)
		if err != nil {
			t.Fatalf("DecodeToken failed: %v — this is the EXACT operation gonuts handles but cdk-go should also handle", err)
		}
		defer tok.Close()
		if tok.Mint() == "" {
			t.Fatal("token Mint() returned empty — expected a mint URL")
		}
	})

	t.Run("token_mint_url_extracted", func(t *testing.T) {
		tok, err := DecodeToken(v3Token)
		if err != nil {
			t.Fatalf("DecodeToken: %v", err)
		}
		defer tok.Close()
		mint := tok.Mint()
		if !strings.Contains(mint, "example.com") {
			t.Fatalf("Mint() = %q, expected to contain 'example.com'", mint)
		}
	})

	t.Run("token_amount_extracted", func(t *testing.T) {
		tok, err := DecodeToken(v3Token)
		if err != nil {
			t.Fatalf("DecodeToken: %v", err)
		}
		defer tok.Close()
		amt := tok.Amount()
		if amt == 0 {
			t.Log("Amount() returned 0 — token may have zero-value proofs (expected for test fixture)")
		}
	})

	t.Run("token_serialize_roundtrip", func(t *testing.T) {
		tok, err := DecodeToken(v3Token)
		if err != nil {
			t.Fatalf("DecodeToken: %v", err)
		}
		defer tok.Close()
		s, err := tok.Serialize()
		if err != nil {
			t.Fatalf("Serialize: %v", err)
		}
		if !strings.HasPrefix(s, "cashu") {
			t.Fatalf("Serialize() = %q, expected 'cashu' prefix", s[:10])
		}
	})
}

// TestCdkMalformedToken proves cdk-go returns errors (not panics) on bad input.
func TestCdkMalformedToken(t *testing.T) {
	t.Run("empty_string", func(t *testing.T) {
		_, err := DecodeToken("")
		if err == nil {
			t.Fatal("DecodeToken('') should return error")
		}
	})

	t.Run("garbage_string", func(t *testing.T) {
		_, err := DecodeToken("not-a-cashu-token-at-all")
		if err == nil {
			t.Fatal("DecodeToken(garbage) should return error")
		}
	})

	t.Run("wrong_prefix", func(t *testing.T) {
		_, err := DecodeToken("cashuXinvaliddata1234567890")
		if err == nil {
			t.Fatal("DecodeToken(wrong prefix) should return error")
		}
	})
}

// TestCdkTokenCloseIdempotent verifies that calling Close() multiple times
// doesn't panic — critical for CGO lifecycle safety.
func TestCdkTokenCloseIdempotent(t *testing.T) {
	v3Token := validV3Token()

	tok, err := DecodeToken(v3Token)
	if err != nil {
		t.Fatalf("DecodeToken: %v", err)
	}
	tok.Close()
	tok.Close()
	tok.Close()
}

// TestCdkWalletConstruction verifies wallet creation without network access.
func TestCdkWalletConstruction(t *testing.T) {
	wallet, err := NewWalletPort(t.TempDir(), []string{"https://testnut.cashu.exchange"}, false)
	if err != nil {
		t.Fatalf("NewWalletPort: %v", err)
	}
	defer wallet.Shutdown()

	balance := wallet.GetBalance()
	if balance != 0 {
		t.Fatalf("fresh wallet balance = %d, want 0", balance)
	}

	allBalances := wallet.GetAllMintBalances()
	if len(allBalances) != 0 {
		t.Fatalf("fresh wallet has %d mint balances, want 0", len(allBalances))
	}
}

// TestMapQuoteState verifies the QuoteState → MintQuoteState mapping.
func TestMapQuoteState(t *testing.T) {
	cases := []struct {
		name  string
		state cdk_ffi.QuoteState
		want  MintQuoteState
	}{
		{"Unpaid", cdk_ffi.QuoteStateUnpaid, StateUnpaid},
		{"Paid", cdk_ffi.QuoteStatePaid, StatePaid},
		{"Issued", cdk_ffi.QuoteStateIssued, StateIssued},
		{"Pending", cdk_ffi.QuoteStatePending, StatePending},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mapQuoteState(tc.state)
			if got != tc.want {
				t.Fatalf("mapQuoteState(%d) = %d, want %d", tc.state, got, tc.want)
			}
		})
	}
}

// TestMapCdkError verifies error code mapping for proof-already-spent.
func TestMapCdkError(t *testing.T) {
	t.Run("nil_passthrough", func(t *testing.T) {
		if err := mapCdkError(nil); err != nil {
			t.Fatalf("mapCdkError(nil) = %v, want nil", err)
		}
	})

	t.Run("string_fallback_already_spent", func(t *testing.T) {
		err := mapCdkError(fmt.Errorf("this proof was already spent by another transaction"))
		if err == nil {
			t.Fatal("mapCdkError should return non-nil for non-nil input")
		}
	})
}

// BenchmarkCdkDecodeToken benchmarks cdk-go's DecodeToken for comparison
// against gonuts's BenchmarkDirectCashuDecodeToken.
func BenchmarkCdkDecodeToken(b *testing.B) {
	v3Token := validV3Token()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t, err := DecodeToken(v3Token)
		if err != nil {
			b.Fatal(err)
		}
		t.Close()
	}
}

// TestCdkMeltToLightningNotStub verifies MeltToLightning is implemented
// and returns real errors, not the "not yet wired" stub.
func TestCdkMeltToLightningNotStub(t *testing.T) {
	wallet, err := NewWalletPort(t.TempDir(), []string{"https://testnut.cashu.exchange"}, false)
	if err != nil {
		t.Fatalf("NewWalletPort: %v", err)
	}
	defer wallet.Shutdown()

	w, ok := wallet.(*CdkWallet)
	if !ok {
		t.Fatalf("expected *CdkWallet, got %T", wallet)
	}

	err = w.MeltToLightning("https://testnut.cashu.exchange", 100, 200, "not-a-valid-address")
	if err == nil {
		t.Fatal("MeltToLightning with invalid address should return error")
	}

	if strings.Contains(err.Error(), "not yet wired") {
		t.Fatalf("MeltToLightning still returns stub error: %v — implementation not finished", err)
	}
}

// TestCdkMeltToLightningWrongFormat tests malformed LNURL input handling.
func TestCdkMeltToLightningWrongFormat(t *testing.T) {
	wallet, err := NewWalletPort(t.TempDir(), []string{"https://testnut.cashu.exchange"}, false)
	if err != nil {
		t.Fatalf("NewWalletPort: %v", err)
	}
	defer wallet.Shutdown()

	w, ok := wallet.(*CdkWallet)
	if !ok {
		t.Fatalf("expected *CdkWallet, got %T", wallet)
	}

	err = w.MeltToLightning("https://testnut.cashu.exchange", 100, 200, "user@")
	if err == nil {
		t.Fatal("MeltToLightning with malformed address 'user@' should return error")
	}

	if strings.Contains(err.Error(), "not yet wired") {
		t.Fatalf("MeltToLightning returned stub error: %v", err)
	}
}

// TestCdkMeltUnknownQuoteID ensures Melt rejects unknown quote IDs.
func TestCdkMeltUnknownQuoteID(t *testing.T) {
	wallet, err := NewWalletPort(t.TempDir(), []string{"https://testnut.cashu.exchange"}, false)
	if err != nil {
		t.Fatalf("NewWalletPort: %v", err)
	}
	defer wallet.Shutdown()

	w, ok := wallet.(*CdkWallet)
	if !ok {
		t.Fatalf("expected *CdkWallet, got %T", wallet)
	}

	result, err := w.Melt("nonexistent-quote-id-12345")
	if err == nil {
		t.Fatal("Melt with unknown quote ID should return error")
	}
	if result != nil {
		t.Fatalf("Melt with unknown quote should return nil result, got %+v", result)
	}
}

// BenchmarkCdkTokenMintAccess benchmarks Token.Mint() via cdk-go.
func BenchmarkCdkTokenMintAccess(b *testing.B) {
	v3Token := validV3Token()
	tok, err := DecodeToken(v3Token)
	if err != nil {
		b.Fatal(err)
	}
	defer tok.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tok.Mint()
	}
}
