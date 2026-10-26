package merchant

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/OpenTollGate/gonuts-tollgate/crypto"
	"github.com/OpenTollGate/gonuts-tollgate/wallet"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// The #818 slice at the merchant level: booting against a REACHABLE mint
// that has rotated its keyset set since the wallet last synced must reach
// "=== Merchant ready ===", not the degraded mode the issue shipped
// ("Wallet initialization failed (… Unknown Keyset) — starting in degraded
// mode" with a first-reachable trigger that never fires because the mint
// was reachable all along).
//
// The fake mint is reachable on every keyset endpoint but no longer knows
// the stale keyset id the pre-rotation wallet.db still has as its active
// record — the exact desk-run shape from the issue.

// honestStaleKeysetID derives a second honest id (a keyset this mint never
// served, standing in for one it has retired and dropped).
func honestStaleKeysetID(t *testing.T) string {
	t.Helper()
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}
	return crypto.DeriveKeysetId(crypto.PublicKeys{1: priv.PubKey()})
}

func seedRetiredKeysetWallet(t *testing.T, dir, mintURL, staleID string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir wallet dir: %v", err)
	}
	db, err := wallet.InitStorage(dir)
	if err != nil {
		t.Fatalf("seed InitStorage: %v", err)
	}
	rec := crypto.WalletKeyset{Id: staleID, MintURL: mintURL, Unit: "sat", Active: true}
	if err := db.SaveKeyset(&rec); err != nil {
		t.Fatalf("seed SaveKeyset: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("seed close: %v", err)
	}
}

// TestMerchantReadyUnderKeysetRotation_Issue818 boots the full startup
// path (probe → wallet → advertisement) with a rotated mint and a
// pre-rotation wallet: the merchant must come up READY.
func TestMerchantReadyUnderKeysetRotation_Issue818(t *testing.T) {
	staleID := honestStaleKeysetID(t)
	var keysetsProbes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/keysets":
			keysetsProbes.Add(1)
			w.Header().Set("Content-Type", "application/json")
			// 000f715baf5d4c2e is what DeriveKeysetId derives for this fake's
			// amount-1 key (the generator point) — v0.14.0's #705 guard
			// refuses ids the published keys do not derive to.
			fmt.Fprint(w, `{"keysets":[{"id":"000f715baf5d4c2e","unit":"sat","active":true,"input_fee_ppk":0}]}`)
		case "/v1/keys":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"keysets":[{"id":"000f715baf5d4c2e","unit":"sat","active":true,"keys":{"1":"0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"}}]}`)
		case "/v1/keys/" + staleID:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"detail":"Unknown Keyset","code":12001}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{}`)
		}
	}))
	t.Cleanup(srv.Close)

	cm, testDir := setupTestConfigManager(t)
	// GetConfig returns a defensive copy — the mutation must be persisted
	// and reloaded, or the merchant boots against the factory-default mint
	// (the first version of this test "passed" by reaching coinos.io).
	cfg := cm.GetConfig()
	cfg.AcceptedMints = simpleMintConfig(srv.URL)
	if err := config_manager.SaveConfig(cm.ConfigFilePath, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if err := cm.ReloadConfig(); err != nil {
		t.Fatalf("ReloadConfig: %v", err)
	}

	// The pre-rotation wallet lives where the merchant will look for it:
	// next to the config file (filepath.Dir of ConfigFilePath).
	seedRetiredKeysetWallet(t, testDir, srv.URL, staleID)

	m, err := New(cm)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s, ok := m.(interface{ Shutdown() error }); ok {
		defer func() { _ = s.Shutdown() }()
	}
	if _, degraded := m.(*MerchantDegraded); degraded {
		t.Fatal("merchant degraded at boot under keyset rotation with a reachable mint — the #818 brick")
	}
	full, ok := m.(*Merchant)
	if !ok {
		t.Fatalf("New returned %T, want *Merchant", m)
	}
	if got := full.GetBalance(); got != 0 {
		t.Fatalf("balance: want 0 got %d", got)
	}
	if keysetsProbes.Load() == 0 {
		t.Fatal("the merchant never probed the rotating mint — the test did not exercise it")
	}
}
