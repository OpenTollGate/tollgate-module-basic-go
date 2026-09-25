//go:build !cdk_wallet

package merchant

// Token fixture helpers — the SINGLE place in the merchant tests that touches a
// concrete Cashu library (gonuts-tollgate). Tests build tokens through these
// helpers so they stay wallet-agnostic; swapping the wallet is then a change to
// this file plus a build-tagged sibling, not to every test.
//
// The build constraint is `!cdk_wallet` only — deliberately no `testenv`. This
// file is pure fixture construction: it builds tokens in memory and touches
// nothing in the environment, so there was never an environment reason to gate
// it. There is a CI reason not to: .github/workflows/test.yml runs this module
// with `./... -v -count=1 -race` and no tags, so a `testenv`-tagged file — and
// every test that uses it — is compiled out of that lane and silently never
// runs. An absence, not a failure.
// TestNoMerchantTestFileIsGatedOnTestenv keeps any file in this package from
// re-gaining that gate.
//
// The `!cdk_wallet` exclusion is deliberate but the cross-wallet parity it
// hints at is NOT enforced today: a future `cdk_wallet` sibling could build the
// same tokens via the CDK bindings, but until it exists (post-RC, with the
// wallet swap in #395), running
//
//	go test -tags cdk_wallet ./merchant/
//
// silently drops this file and every test using it from the build and
// still exits 0 — an absence, not a failure. Enforcing parity needs a
// lane that runs both tag combinations and fails when the cdk_wallet side
// is empty; that lands with the sibling, not before.

import (
	"testing"

	"github.com/OpenTollGate/gonuts-tollgate/cashu"
)

const fixtureMintURL = "https://testmint.example.com"

// mustV4Token returns a serialized Cashu V4 token ("cashuB…") for amount.
func mustV4Token(t *testing.T, amount uint64, secret string) string {
	t.Helper()
	proofs := cashu.Proofs{{Amount: amount, Id: "00ad", C: "ab", Secret: secret}} // pragma: allowlist secret
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
	proofs := cashu.Proofs{{Amount: amount, Id: "00ad", C: "ab", Secret: secret}} // pragma: allowlist secret
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
