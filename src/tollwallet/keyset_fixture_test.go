package tollwallet

import (
	"encoding/json"
	"fmt"

	"github.com/OpenTollGate/gonuts-tollgate/crypto"
)

// The gonuts v0.14.0 forged-keyset-id guard (tollgate #705) enforces NUT-02:
// a mint's advertised keyset id must be the derivation of its published
// keys. The fake-mint fixtures below advertise ids derived through the same
// gonuts API the guard uses, so they register like real mints.

// fixtureAmount1Key is the fixed amount-1 public key shared by the fake-mint
// fixtures: a valid secp256k1 point (the curve generator).
const fixtureAmount1Key = "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"

// mustDeriveKeysetID derives the NUT-02 keyset id for a /v1/keys "keys"
// object (e.g. `{"1":"<pubkey hex>"}`).
func mustDeriveKeysetID(keysJSON string) string {
	keys := crypto.PublicKeys{}
	if err := json.Unmarshal([]byte(keysJSON), &keys); err != nil {
		panic(fmt.Sprintf("derive fixture keyset id: %v", err))
	}
	return crypto.DeriveKeysetId(keys)
}

// fixtureKeysetID is the id the fixed amount-1 key derives to.
var fixtureKeysetID = mustDeriveKeysetID(`{"1":"` + fixtureAmount1Key + `"}`)

// fixtureKeysetsJSON serves a single active "sat" keyset whose id matches
// its keys — what a fake mint must advertise to pass the #705 guard.
func fixtureKeysetsJSON() string {
	return `{"keysets":[{"id":"` + fixtureKeysetID + `","unit":"sat","active":true,` +
		`"keys":{"1":"` + fixtureAmount1Key + `"}}]}`
}
