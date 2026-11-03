package tollwallet

// Honest keyset ids for test fixtures.
//
// gonuts-tollgate v0.14.0 (tollgate #705) verifies that a mint's advertised
// keyset id matches the id its published keys actually derive to, and
// refuses mismatches — the forged-keyset-id guard. Test fakes used to
// advertise arbitrary ids over hand-picked keys, which the guard correctly
// rejects; fixtures must publish ids their keys derive to. These helpers
// compute ids with the fork's own DeriveKeyId so fake mints stay honest,
// with every other assertion untouched.

import (
	"encoding/hex"

	"github.com/OpenTollGate/gonuts-tollgate/crypto"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// generatorPointHex is the fixtures' standard amount-1 key: the compressed
// generator point G (any valid point works for the wallet's math; the id
// just has to be derived from whatever key is advertised).
const generatorPointHex = "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"

// deriveHonestKeysetID returns the id the fork's guard derives for keys,
// so a fake mint advertising it is accepted as a real keyset.
func deriveHonestKeysetID(keys map[uint64]string) string {
	pks := make(crypto.PublicKeys, len(keys))
	for amount, hexKey := range keys {
		raw, err := hex.DecodeString(hexKey)
		if err != nil {
			panic("fixture key is not hex: " + err.Error())
		}
		pub, err := secp256k1.ParsePubKey(raw)
		if err != nil {
			panic("fixture key does not parse: " + err.Error())
		}
		pks[amount] = pub
	}
	return crypto.DeriveKeysetId(pks)
}

// honestV1KeysetID is the id for the standard single-amount-1 fixture
// keyset (the generator point) shared by the receive/sentinel/check-state
// fixtures. It replaces the old hand-picked constant, which v0.14.0's
// guard rejects.
var honestV1KeysetID = deriveHonestKeysetID(map[uint64]string{1: generatorPointHex})

// honestKeysetIDFromPriv derives the id for a fixture keyset from a private
// key (fakes that generate a fresh key per run, like newTestMint).
func honestKeysetIDFromPriv(priv *secp256k1.PrivateKey) string {
	return crypto.DeriveKeysetId(crypto.PublicKeys{1: priv.PubKey()})
}
