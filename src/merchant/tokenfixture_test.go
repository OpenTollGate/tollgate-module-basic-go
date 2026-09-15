//go:build testenv && !cdk_wallet

package merchant

// Token fixture helpers — the SINGLE place in the merchant tests that touches a
// concrete Cashu library (gonuts-tollgate). Tests build tokens through these
// helpers so they stay wallet-agnostic; swapping the wallet is then a change to
// this file plus a build-tagged sibling, not to every test.
//
// A future `testenv && cdk_wallet` sibling can build the same tokens via the
// CDK bindings, keeping the tests identical across wallets.

import (
	"testing"

	"github.com/OpenTollGate/gonuts-tollgate/cashu"
)

const fixtureMintURL = "https://testmint.example.com"

// mustV4Token returns a serialized Cashu V4 token ("cashuB…") for amount.
func mustV4Token(t *testing.T, amount uint64, secret string) string {
	t.Helper()
	proofs := cashu.Proofs{{Amount: amount, Id: "00ad", C: "ab", Secret: secret}}
	tok, err := cashu.NewTokenV4(proofs, fixtureMintURL, cashu.Sat, false)
	if err != nil {
		t.Fatalf("NewTokenV4: %v", err)
	}
	s, err := tok.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	return s
}

// mustV3Token returns a serialized Cashu V3 token ("cashuA…") for amount.
func mustV3Token(t *testing.T, amount uint64, secret string) string {
	t.Helper()
	proofs := cashu.Proofs{{Amount: amount, Id: "00ad", C: "ab", Secret: secret}}
	tok, err := cashu.NewTokenV3(proofs, fixtureMintURL, cashu.Sat, false)
	if err != nil {
		t.Fatalf("NewTokenV3: %v", err)
	}
	s, err := tok.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	return s
}
