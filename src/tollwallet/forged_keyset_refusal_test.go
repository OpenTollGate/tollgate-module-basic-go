package tollwallet

// Planted-drift verdict for the v0.14.0 forged-keyset-id guard (tollgate
// #705): the honest fixtures in honest_keyset_test.go prove fake mints with
// DERIVED ids are accepted; this file plants the DRIFT — a mint that
// advertises an id its published keys do NOT derive to — and requires the
// real registration seam to REFUSE it. Per the repo's fence doctrine, a
// guard nobody has seen reject is decoration: this is the rejection proof,
// through New → registerMint → AddMint, not a unit-level call into the fork.

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// TestRegisterMintRefusesForgedKeysetID plants the #705 drift and requires
// the refusal: a fresh random key with its honestly derived id REPLACED by
// an arbitrary V1-shaped hex string. Wallet creation must fail naming the
// forgery — the exact upstream refusal the fixture-honesty pass adapts to.
func TestRegisterMintRefusesForgedKeysetID(t *testing.T) {
	const forgedID = "00deadbeefdeadbe" // hex-decodable, V1-shaped, WRONG

	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}
	pubKeyHex := hex.EncodeToString(priv.PubKey().SerializeCompressed())

	mux := http.NewServeMux()
	serve := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"keysets":[{"id":"`+forgedID+`","unit":"sat","keys":{"1":"`+pubKeyHex+`"},"active":true,"input_fee_ppk":0}]}`)
	}
	mux.HandleFunc("/v1/keys", serve)
	mux.HandleFunc("/v1/keysets", serve)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	_, err = New(t.TempDir(), []string{server.URL}, false)
	if err == nil {
		t.Fatalf("wallet creation must REFUSE a mint whose advertised keyset id (%s) does not match its published keys — the #705 guard did not bite", forgedID)
	}
	if want := "forged keyset id"; !strings.Contains(strings.ToLower(err.Error()), want) {
		t.Fatalf("refusal must name the forgery (want substring %q), got: %v", want, err)
	}
}
