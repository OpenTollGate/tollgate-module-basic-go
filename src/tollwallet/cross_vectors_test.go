package tollwallet

import (
	"encoding/hex"
	"testing"

	"github.com/OpenTollGate/gonuts-tollgate/crypto"
)

// NUT-00 hash_to_curve cross-implementation vectors (canonical set lives in
// the workspace as cashu-cross-vectors.json). The same secrets must produce
// byte-identical Ys in gonuts (btcec), cashu-core-lite (k256), and the
// coincurve Python port — Y is the value NUT-07 checkstate keys on, so any
// divergence silently reports spent proofs as unspent. The third secret is a
// valid hex string that MUST be hashed as its 64 ASCII characters verbatim;
// hashing the decoded bytes instead yields a plausible-but-wrong Y — a
// divergence that shipped in two independent implementations to date.
func TestHashToCurveCrossVectors(t *testing.T) {
	vectors := []struct {
		secret string
		yHex   string
	}{
		{"test-secret-01", "0279110ffdbbaccf1f96e0641dd8794fb206e8f95eb52c0fa001487b070cb5f7b1"},
		{"a", "029794c59a5d9b910a18e50e10623c864b77c7edf4552f8652b0c85d30ac0498f0"},
		{"deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "0244d4bdec44e84725e2b6d9d7a2896df8bc27b482e84e0cb2144272d318375bc3"},
	}
	for _, v := range vectors {
		y, err := crypto.HashToCurve([]byte(v.secret))
		if err != nil {
			t.Fatalf("HashToCurve(%q): %v", v.secret, err)
		}
		got := hex.EncodeToString(y.SerializeCompressed())
		if got != v.yHex {
			t.Errorf("HashToCurve(%q) = %s, want %s (cross-implementation divergence: gonuts vs k256/coincurve)", v.secret, got, v.yHex)
		}
	}
}
