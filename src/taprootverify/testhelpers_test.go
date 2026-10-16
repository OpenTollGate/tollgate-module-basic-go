package taprootverify

import (
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
)

// schnorrSerialize returns the 32-byte x-only serialization of a public key.
// Kept as a tiny helper so the tests read as "compare x-only keys", which is
// what a scriptPubKey actually contains.
func schnorrSerialize(k *btcec.PublicKey) []byte {
	return schnorr.SerializePubKey(k)
}

// wrongRootForThree deliberately implements the INCORRECT odd-node rule, so the
// test suite can prove our MerkleRoot does not quietly degrade into it. This
// exists only for tests; it is not exported as production API on purpose.
//
// The bug it models: at each level, if the count is odd, duplicate the last
// element and then continue -- but WITHOUT BIP-341's promotion semantics, and
// more importantly by pairing positionally rather than by the spec's ordering.
// Several taproot libraries have shipped this variant; for 1, 2 and 4 leaves it
// agrees with BIP-341, and at 3 leaves it silently produces a different (still
// valid-looking) output key.
func wrongRootForThree(scripts [][]byte) []byte {
	level := make([][]byte, 0, len(scripts))
	for _, s := range scripts {
		h := txscript.NewBaseTapLeaf(s).TapHash()
		level = append(level, h[:])
	}
	// positional pairing, duplicating the last element when odd
	for len(level) > 1 {
		if len(level)%2 == 1 {
			level = append(level, level[len(level)-1])
		}
		next := make([][]byte, 0, len(level)/2)
		for i := 0; i < len(level); i += 2 {
			// concatenate in POSITIONAL order, not lexicographic order --
			// this is the specific defect the test guards against
			a, b := level[i], level[i+1]
			h := chainhash.TaggedHash(chainhash.TagTapBranch, a, b)
			next = append(next, h[:])
		}
		level = next
	}
	return level[0]
}
