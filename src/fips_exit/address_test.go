package fips_exit

import (
	"encoding/hex"
	"testing"
)

// Expected addresses are the phone-generated vectors in fr34aky/fips2go
// v0.11.0 exit-node/test_exitctl.py, not calculated by the code under test.
// Keys were decoded by that tag's exitctl.npub_to_pubkey.
func TestDeriveCompatibilityVectors(t *testing.T) {
	vectors := []struct{ key, outer, inner string }{
		{"ca30594c62c8c442dd9bb0f3be2eb8201f4980613afa5817051d927edab879d2", "fd78:56c9:7bcd:d690:d024:a45f:3fdc:e14c", "100.120.86.201"},
		{"41f6a22a076579db41297baae07f3499d8e1985281254596091f7870f47e8f03", "fdf1:5704:54a4:8240:e6bc:df72:56d2:39e7", "100.113.87.4"},
		{"8755edda940455d27c8f8b2778c63354ea0ebd153eb5cf70c83b3f5546c76adb", "fd96:74a1:b794:6dba:39dc:e316:c82:f7ba", "100.86.116.161"},
	}
	for _, v := range vectors {
		t.Run(v.key, func(t *testing.T) {
			decoded, err := hex.DecodeString(v.key)
			if err != nil || len(decoded) != 32 {
				t.Fatalf("invalid fixture key: %v", err)
			}
			var key [32]byte
			copy(key[:], decoded)
			outer, inner := Derive(key)
			if !outer.Is6() || outer.String() != v.outer {
				t.Errorf("outer = %v, want %s", outer, v.outer)
			}
			if !inner.Is4() || inner.String() != v.inner {
				t.Errorf("inner = %v, want %s", inner, v.inner)
			}
		})
	}
}
