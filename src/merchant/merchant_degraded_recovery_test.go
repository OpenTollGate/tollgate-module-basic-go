package merchant

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenTollGate/gonuts-tollgate/crypto"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// switchableMintServer serves unreachable (503) until setHealthy(true), then
// answers the endpoints a wallet registration touches. Recovery is simulated
// by flipping the switch and running one proactive check.
type switchableMintServer struct {
	*httptest.Server
	healthy atomic.Bool
}

// switchableMintKeysetID is the NUT-02 ID the wallet derives from the
// stub's fixed amount-1 key. Served verbatim because gonuts v0.14.0
// refuses a keyset whose advertised ID differs from its keys ("forged
// keyset id", tollgate #705) — an invented ID makes wallet construction
// fail and the degraded-start precondition of these tests unreachable.
func switchableMintKeysetID(t *testing.T) string {
	t.Helper()
	raw, err := hex.DecodeString("0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798")
	if err != nil {
		t.Fatalf("decode stub key: %v", err)
	}
	pk, err := secp256k1.ParsePubKey(raw)
	if err != nil {
		t.Fatalf("parse stub key: %v", err)
	}
	return crypto.DeriveKeysetId(crypto.PublicKeys{1: pk})
}

func newSwitchableMintServer(t *testing.T) *switchableMintServer {
	t.Helper()
	s := &switchableMintServer{}
	keysetID := switchableMintKeysetID(t)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.healthy.Load() {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/v1/keysets":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"keysets":[{"id":"`+keysetID+`","unit":"sat","active":true,"input_fee_ppk":0}]}`)
		case "/v1/keys":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"keysets":[{"id":"`+keysetID+`","unit":"sat","active":true,"keys":{"1":"0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"}}]}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{}`)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// TestDegradedWireRecoveryTrigger_RuntimDowngradeUpgrades pins #400: a
// runtime downgrade (full merchant -> degraded after all mints went down)
// must be able to upgrade back once a mint recovers. Before the fix, only
// the startup degraded paths registered the tracker's first-reachable
// callback — the runtime path registered the onUpgrade consumer but nothing
// ever fired it, so the service stayed degraded until manually restarted.
func TestDegradedWireRecoveryTrigger_RuntimDowngradeUpgrades(t *testing.T) {
	srv := newSwitchableMintServer(t)

	cm, testDir := setupTestConfigManager(t)
	cfg := cm.GetConfig()
	cfg.AcceptedMints = simpleMintConfig(srv.URL)

	tracker := newTestTracker(cfg, nil)
	tracker.RunInitialProbe()
	if tracker.IsReachable(srv.URL) {
		t.Fatal("precondition: mint must be unreachable for a degraded start")
	}

	// The runtime-downgrade construction from main.go: a degraded merchant
	// carrying the full merchant's tracker, with only an onUpgrade consumer.
	deg := NewMerchantDegradedWithWallet(cm, tracker, DefaultWalletFactory, testDir)
	upgraded := make(chan MerchantInterface, 1)
	deg.OnUpgrade(func(mi MerchantInterface) { upgraded <- mi })
	deg.WireRecoveryTrigger()

	// Mint comes back; the proactive check fires the first-reachable trigger.
	srv.healthy.Store(true)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		tracker.runProactiveCheck()
		select {
		case mi := <-upgraded:
			if mi == nil {
				t.Fatal("onUpgrade fired with a nil merchant")
			}
			if _, ok := mi.(*Merchant); !ok {
				t.Fatalf("onUpgrade fired with %T, want *Merchant", mi)
			}
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatal("runtime-downgraded merchant never upgraded after mint recovery (#400)")
}
