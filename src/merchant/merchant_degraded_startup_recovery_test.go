package merchant

import (
	"testing"
	"time"
)

// TestDegradedStartupWireRecoveryTrigger_Upgrades pins #10: a merchant that
// STARTS in degraded mode (all mints unreachable at boot, never had a full
// merchant) must also upgrade once a mint recovers. The runtime-downgrade
// path got WireRecoveryTrigger in #400; the startup path (installMerchant in
// main.go) was missed, leaving the tracker's onFirstReachable nil and the
// service stuck degraded forever even after the mint returned.
func TestDegradedStartupWireRecoveryTrigger_Upgrades(t *testing.T) {
	srv := newSwitchableMintServer(t)

	cm, testDir := setupTestConfigManager(t)
	cfg := cm.GetConfig()
	cfg.AcceptedMints = simpleMintConfig(srv.URL)

	tracker := newTestTracker(cfg, nil)
	tracker.RunInitialProbe()
	if tracker.IsReachable(srv.URL) {
		t.Fatal("precondition: mint must be unreachable for a degraded start")
	}

	// The startup construction from installMerchant in main.go: a degraded
	// merchant with an onUpgrade consumer and WireRecoveryTrigger called.
	// Before #10's fix, WireRecoveryTrigger was only on the runtime path.
	deg := NewMerchantDegradedWithWallet(cm, tracker, DefaultWalletFactory, testDir)
	upgraded := make(chan MerchantInterface, 1)
	deg.OnUpgrade(func(mi MerchantInterface) { upgraded <- mi })
	deg.WireRecoveryTrigger()

	// Mint comes back; the aggressive probe fires the first-reachable trigger.
	srv.healthy.Store(true)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		tracker.runAggressiveCheck(nil)
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
	t.Fatal("startup-degraded merchant never upgraded after mint recovery (#10)")
}

// TestDegradedWithoutWireRecoveryTrigger_StaysDegraded pins the anti-flap
// invariant: a degraded merchant WITHOUT the recovery trigger wired (the
// pre-fix state) stays degraded even when the mint returns — proving the fix
// is the wiring, not the probe loop. This also guards against the trigger
// firing spuriously (no upgrade when there's nothing to fire).
func TestDegradedWithoutWireRecoveryTrigger_StaysDegraded(t *testing.T) {
	srv := newSwitchableMintServer(t)

	cm, testDir := setupTestConfigManager(t)
	cfg := cm.GetConfig()
	cfg.AcceptedMints = simpleMintConfig(srv.URL)

	tracker := newTestTracker(cfg, nil)
	tracker.RunInitialProbe()
	if tracker.IsReachable(srv.URL) {
		t.Fatal("precondition: mint must be unreachable for a degraded start")
	}

	// No WireRecoveryTrigger — the pre-#10 state.
	deg := NewMerchantDegradedWithWallet(cm, tracker, DefaultWalletFactory, testDir)
	upgraded := make(chan MerchantInterface, 1)
	deg.OnUpgrade(func(mi MerchantInterface) { upgraded <- mi })
	// Deliberately NOT calling deg.WireRecoveryTrigger()

	srv.healthy.Store(true)
	tracker.runProactiveCheck()

	select {
	case <-upgraded:
		t.Fatal("upgrade fired without WireRecoveryTrigger — the trigger wiring is broken (spurious fire)")
	case <-time.After(500 * time.Millisecond):
		// Correct: stayed degraded because nothing was wired.
	}
}

// TestDegradedStaysDegradedWhileMintDown pins the no-flap invariant: while
// the mint is unreachable, the degraded merchant does not attempt to upgrade
// (no spurious upgrades, no oscillation), even with the trigger properly wired.
func TestDegradedStaysDegradedWhileMintDown(t *testing.T) {
	srv := newSwitchableMintServer(t)
	// Mint stays unhealthy for the entire test.

	cm, testDir := setupTestConfigManager(t)
	cfg := cm.GetConfig()
	cfg.AcceptedMints = simpleMintConfig(srv.URL)

	tracker := newTestTracker(cfg, nil)
	tracker.RunInitialProbe()
	if tracker.IsReachable(srv.URL) {
		t.Fatal("precondition: mint must be unreachable for a degraded start")
	}

	deg := NewMerchantDegradedWithWallet(cm, tracker, DefaultWalletFactory, testDir)
	upgraded := make(chan MerchantInterface, 1)
	deg.OnUpgrade(func(mi MerchantInterface) { upgraded <- mi })
	deg.WireRecoveryTrigger()

	tracker.runProactiveCheck()

	select {
	case <-upgraded:
		t.Fatal("upgrade fired while mint is unreachable — flapping detected")
	case <-time.After(500 * time.Millisecond):
		// Correct: stayed degraded while the mint is down.
	}
}
