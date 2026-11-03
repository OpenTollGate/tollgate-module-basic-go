package merchant

import (
	"fmt"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// The stuck-degraded slice of the storage-degraded family: when the wallet
// is the broken half (mints reachable, wallet init failing — the #583/#1035
// storage class, and any persistent wallet error), the degraded merchant
// must (a) say WHY it is degraded instead of the hardcoded — and false —
// "no reachable mints", and (b) keep retrying wallet init on a bounded,
// backing-off cadence, because the only other recovery path (the tracker's
// first-reachable trigger) is a ONE-SHOT latch: it fires once, the attempt
// fails, and nothing ever fires again. Before this slice, one failed
// upgrade attempt meant degraded for life until a manual restart.

// healingWalletMint is reachable on /v1/keysets (the health probe passes,
// the tracker's reachable set is non-empty) but its key-serving endpoints
// answer garbage for the first brokenKeysFor /v1/keys requests — exactly
// the "mints fine, wallet broken" split — and serve honest keys afterwards.
type healingWalletMint struct {
	*httptest.Server
	keysCalls atomic.Int32
}

func newHealingWalletMint(t *testing.T, brokenKeysFor int32) *healingWalletMint {
	t.Helper()
	m := &healingWalletMint{}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/keysets":
			// Honest id for the generator-point amount-1 key (the id
			// DeriveKeysetId yields — v0.14.0's #705 guard requires ids
			// the advertised keys derive to).
			fmt.Fprint(w, `{"keysets":[{"id":"000f715baf5d4c2e","unit":"sat","active":true,"input_fee_ppk":0}]}`)
		case "/v1/keys":
			if m.keysCalls.Add(1) <= brokenKeysFor {
				fmt.Fprint(w, `{"keysets":[]}`) // reachable mint, unusable keys
				return
			}
			fmt.Fprint(w, `{"keysets":[{"id":"000f715baf5d4c2e","unit":"sat","active":true,"keys":{"1":"0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"}}]}`)
		case "/v1/keys/000f715baf5d4c2e":
			// 400, not 5xx: parse() treats 400 as a terminal NUT-19 error —
			// a 502 here would be RETRIABLE in the wallet client and cost
			// ~8 s of internal backoff per wallet attempt, blowing the
			// test's timing contract for no added realism.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"detail":"keyset backend unavailable","code":10000}`)
		default:
			fmt.Fprint(w, `{}`)
		}
	}))
	t.Cleanup(m.Close)
	return m
}

func shrinkDegradedWalletRetry(t *testing.T) {
	t.Helper()
	origInitial, origMax := degradedWalletRetryInitial, degradedWalletRetryMax
	degradedWalletRetryInitial, degradedWalletRetryMax = 25*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() {
		degradedWalletRetryInitial, degradedWalletRetryMax = origInitial, origMax
	})
}

// The full production path: merchant.New() boots with a reachable-but-
// wallet-broken mint, degrades for the honest reason, and the wallet retry
// loop — not the one-shot first-reachable latch — delivers the upgrade once
// the mint's key serving heals. The proactive loop (5 min) never runs in
// the test window, so the trigger cannot be what heals it.
func TestDegradedWalletRetryHealsBrokenWallet(t *testing.T) {
	shrinkDegradedWalletRetry(t)
	// Broken long enough to fail New()'s init AND at least one retry
	// attempt; heals only the retry loop can reach.
	mint := newHealingWalletMint(t, 4)

	cm, _ := setupTestConfigManager(t)
	saveAndReloadConfig(t, cm, simpleMintConfig(mint.URL))

	m, err := New(cm)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	deg, ok := m.(*MerchantDegraded)
	if !ok {
		t.Fatalf("precondition: New must degrade while the wallet is broken, got %T", m)
	}
	t.Cleanup(func() { _ = deg.Shutdown() })

	upgraded := make(chan MerchantInterface, 1)
	deg.OnUpgrade(func(mi MerchantInterface) { upgraded <- mi })

	select {
	case mi := <-upgraded:
		if _, ok := mi.(*Merchant); !ok {
			t.Fatalf("onUpgrade fired with %T, want *Merchant", mi)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stuck degraded with a reachable mint after the wallet healed — the retry loop did not fire (one-shot latch class)")
	}

	// Exactly one upgrade: the latch plus the stopped loop must not double-
	// fire after success.
	select {
	case mi := <-upgraded:
		t.Fatalf("second onUpgrade fired (%T) after a successful upgrade", mi)
	case <-time.After(300 * time.Millisecond):
	}
}

// The honest-reason contract: with every mint reachable and the WALLET
// broken, no degraded surface may claim "no reachable mints" — that
// misdirection is what cost the #583/#1035 triage days.
func TestDegradedReasonIsHonestWhenWalletFailsWithReachableMints(t *testing.T) {
	shrinkDegradedWalletRetry(t)
	mint := newHealingWalletMint(t, 1<<30) // never heals

	cm, _ := setupTestConfigManager(t)
	saveAndReloadConfig(t, cm, simpleMintConfig(mint.URL))

	m, err := New(cm)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	deg, ok := m.(*MerchantDegraded)
	if !ok {
		t.Fatalf("precondition: New must degrade while the wallet is broken, got %T", m)
	}
	t.Cleanup(func() { _ = deg.Shutdown() })

	if got := deg.DegradeReason(); got == "no reachable mints" || got == "" {
		t.Fatalf("degrade reason must name the wallet failure, got %q", got)
	}
	if _, _, err := deg.DrainMint(mint.URL); err == nil || err.Error() == "wallet not initialized: no reachable mints" {
		t.Fatalf("degraded stubs must surface the honest reason, got %v", err)
	}
}

// The other class keeps its meaning: degrading because NO mint is
// reachable still says so, and the wallet retry loop must stay silent
// while the reachable set is empty (that class belongs to the
// first-reachable trigger; retrying wallet init with zero reachable mints
// is noise).
func TestDegradedWalletRetryQuietWhileNoMintReachable(t *testing.T) {
	shrinkDegradedWalletRetry(t)
	srv := newUnreachableServer(t)

	cm, _ := setupTestConfigManager(t)
	saveAndReloadConfig(t, cm, simpleMintConfig(srv.URL))

	m, err := New(cm)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	deg, ok := m.(*MerchantDegraded)
	if !ok {
		t.Fatalf("precondition: New must degrade with no reachable mints, got %T", m)
	}
	t.Cleanup(func() { _ = deg.Shutdown() })

	if got := deg.DegradeReason(); got != "no reachable mints" {
		t.Fatalf("reason for the unreachable class must stay %q, got %q", "no reachable mints", got)
	}

	upgraded := make(chan MerchantInterface, 1)
	deg.OnUpgrade(func(mi MerchantInterface) { upgraded <- mi })

	select {
	case mi := <-upgraded:
		t.Fatalf("retry loop ran with an empty reachable set (%T) — the trigger owns that class", mi)
	case <-time.After(400 * time.Millisecond):
	}
}

// saveAndReloadConfig persists a config mutation: GetConfig returns a
// defensive copy, so mutating it in place never reaches the merchant (the
// #1077 merchant test booted against coinos.io this way before this
// helper existed).
func saveAndReloadConfig(t *testing.T, cm *config_manager.ConfigManager, mints []config_manager.MintConfig) {
	t.Helper()
	cfg := cm.GetConfig()
	cfg.AcceptedMints = mints
	if err := config_manager.SaveConfig(cm.ConfigFilePath, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if err := cm.ReloadConfig(); err != nil {
		t.Fatalf("ReloadConfig: %v", err)
	}
}
