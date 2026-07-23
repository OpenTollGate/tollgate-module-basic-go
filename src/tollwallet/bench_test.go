//go:build testenv

package tollwallet

import (
	"testing"

	"github.com/Origami74/gonuts-tollgate/cashu"
)

// BenchmarkDecodeToken measures the overhead of the GonutsWallet adapter's
// DecodeToken path compared to direct cashu.DecodeToken. The adapter wraps
// the result in a gonutsToken struct, so the benchmark captures interface
// dispatch + wrapper allocation cost.
//
// This is the first benchmark in the repo. Pattern established here can be
// extended to Receive/Send when a real wallet fixture is available.

// mustBuildTestToken constructs a valid V4 cashu token string for benchmarking.
func mustBuildTestToken(b *testing.B) string {
	b.Helper()
	proofs := cashu.Proofs{
		{Amount: 1, Id: "009a1f293253e41e", C: "0224f1c4c564230ad3d96c5033efdc425582397a5a7691d600202732edc6d4b1ec", Secret: "test"},
	}
	token, err := cashu.NewTokenV4(proofs, "https://testmint.example.com", cashu.Sat, false)
	if err != nil {
		b.Fatalf("NewTokenV4: %v", err)
	}
	s, err := token.Serialize()
	if err != nil {
		b.Fatalf("Serialize: %v", err)
	}
	return s
}

// BenchmarkDirectCashuDecodeToken benchmarks the raw gonuts cashu.DecodeToken
// without the adapter wrapper. This is the baseline.
func BenchmarkDirectCashuDecodeToken(b *testing.B) {
	tokenStr := mustBuildTestToken(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := cashu.DecodeToken(tokenStr)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPackageLevelDecodeToken benchmarks the package-level tollwallet.DecodeToken
// function (wraps cashu.DecodeToken, returns Token interface). Measures the
// gonutsToken wrapper allocation overhead.
func BenchmarkPackageLevelDecodeToken(b *testing.B) {
	tokenStr := mustBuildTestToken(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t, err := DecodeToken(tokenStr)
		if err != nil {
			b.Fatal(err)
		}
		t.Close()
	}
}

// BenchmarkTokenMintAccess measures the cost of calling Mint() through the
// Token interface vs direct cashu.Token method call.
func BenchmarkTokenMintAccess(b *testing.B) {
	tokenStr := mustBuildTestToken(b)
	tok, err := DecodeToken(tokenStr)
	if err != nil {
		b.Fatal(err)
	}
	defer tok.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tok.Mint()
	}
}

// BenchmarkTokenAmountAccess measures the cost of calling Amount() through
// the Token interface.
func BenchmarkTokenAmountAccess(b *testing.B) {
	tokenStr := mustBuildTestToken(b)
	tok, err := DecodeToken(tokenStr)
	if err != nil {
		b.Fatal(err)
	}
	defer tok.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tok.Amount()
	}
}
