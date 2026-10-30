package tollwallet

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OpenTollGate/gonuts-tollgate/cashu"
	"github.com/OpenTollGate/gonuts-tollgate/crypto"
	"github.com/OpenTollGate/gonuts-tollgate/wallet"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

func testKeys(t *testing.T) crypto.PublicKeys {
	t.Helper()
	keys := make(crypto.PublicKeys, 4)
	for i := uint64(1); i <= 8; i *= 2 {
		priv, err := secp256k1.GeneratePrivateKey()
		if err != nil {
			t.Fatalf("generate key: %v", err)
		}
		keys[i] = priv.PubKey()
	}
	return keys
}

func healthyMint(t *testing.T, keys crypto.PublicKeys, id string) *httptest.Server {
	t.Helper()
	keysJSON, err := json.Marshal(keys)
	if err != nil {
		t.Fatalf("marshal keys: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/keysets":
			fmt.Fprintf(w, `{"keysets":[{"id":%q,"unit":"sat","active":true,"input_fee_ppk":0}]}`, id)
		case "/v1/keys", "/v1/keys/" + id:
			fmt.Fprintf(w, `{"keysets":[{"id":%q,"unit":"sat","active":true,"input_fee_ppk":0,"keys":%s}]}`, id, keysJSON)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// eofMint accepts the connection and closes it without a response, which is
// what a down/crashed/restarting mint looks like (io.EOF).
func eofMint(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("response writer does not support hijacking")
			return
		}
		conn, _, err := hj.Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestNewToleratesUnreachableStoredMint is the E750 regression: a wallet DB
// still holding a keyset and proof for a mint that is unreachable with EOF
// must not fail initialization. The reachable mint must be registered and the
// wallet usable; the unreachable record is retained for later recovery.
func TestNewToleratesUnreachableStoredMint(t *testing.T) {
	healthyKeys := testKeys(t)
	healthyID := crypto.DeriveKeysetId(healthyKeys)
	healthy := healthyMint(t, healthyKeys, healthyID)

	dead := eofMint(t)
	deadKeys := testKeys(t)
	deadID := crypto.DeriveKeysetId(deadKeys)

	dir := t.TempDir()
	db, err := wallet.InitStorage(dir)
	if err != nil {
		t.Fatalf("seed InitStorage: %v", err)
	}
	if err := db.SaveKeyset(&crypto.WalletKeyset{Id: deadID, MintURL: dead.URL, Unit: "sat", Active: true}); err != nil {
		t.Fatalf("seed SaveKeyset: %v", err)
	}
	if err := db.IncrementKeysetCounter(deadID, 2); err != nil {
		t.Fatalf("seed counter: %v", err)
	}
	if err := db.SaveProofs(cashu.Proofs{{Amount: 1, Secret: "offline-proof", C: "02" + deadID[:16], Id: deadID}}); err != nil {
		t.Fatalf("seed SaveProofs: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("seed close: %v", err)
	}

	tw, err := New(dir, []string{healthy.URL, dead.URL}, false)
	if err != nil {
		t.Fatalf("New must tolerate an unreachable stored mint, got: %v", err)
	}
	if tw == nil || tw.wallet == nil {
		t.Fatal("wallet not initialized")
	}
	if !tw.registeredMints[normalizeMintURL(healthy.URL)] {
		t.Fatal("healthy mint was not registered")
	}
}
