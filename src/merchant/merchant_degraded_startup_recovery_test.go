package merchant

import (
	"testing"
	"time"
)

// TestDegradedStartupWireRecoveryTrigger_Upgrades pins #10: a merchant that
// STARTS in degraded mode (all mints unreachable at boot, never had a full
// merchant) must also upgrade once a mint recovers. The runtime-downgrade path
// got WireRecoveryTrigger in #400; the startup path (installMerchant in
// main.go) was missed, leaving the tracker's onFirstReachable nil and the
// service stuck degraded forever even after the mint returned.
//
// This test goes through WireStartupDegraded — the SAME method main.go's
// startup path calls. It previously re-implemented the sequence inline
// (OnUpgrade then WireRecoveryTrigger), which meant deleting
// WireRecoveryTrigger() from installMerchant left this test green: it was
// exercising the library, not the startup wiring it claimed to pin.
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

	// The startup construction from installMerchant in main.go, via the same
	// entry point it uses.
	deg := NewMerchantDegradedWithWallet(cm, tracker, DefaultWalletFactory, testDir)
	upgraded := make(chan MerchantInterface, 1)
	deg.WireStartupDegraded(func(mi MerchantInterface) { upgraded <- mi })

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
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatal("service stayed degraded after the mint recovered — the startup path did not wire the recovery trigger (#10)")
}

// The complement, and the reason the two steps must stay a pair: wiring the
// trigger while the mint is still down must NOT upgrade. A test that only
// asserted "eventually upgrades" would pass if the trigger fired
// unconditionally, so this pins the other direction.
func TestDegradedStartupWiringDoesNotUpgradeWhileTheMintIsDown(t *testing.T) {
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
	deg.WireStartupDegraded(func(mi MerchantInterface) { upgraded <- mi })

	tracker.runProactiveCheck()

	select {
	case <-upgraded:
		t.Fatal("upgrade fired while the mint is unreachable — the startup wiring must not upgrade unconditionally")
	case <-time.After(500 * time.Millisecond):
		// Correct: stayed degraded while the mint is down.
	}
}

// A merchant that is NOT degraded must not get the degraded wiring at all —
// the type assertion in installMerchant decides this.
func TestWireStartupDegradedIsDegradedOnly(t *testing.T) {
	srv := newSwitchableMintServer(t)
	srv.healthy.Store(true)

	cm, _ := setupTestConfigManager(t)
	cfg := cm.GetConfig()
	cfg.AcceptedMints = simpleMintConfig(srv.URL)

	full, err := New(cm)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s, ok := full.(interface{ Shutdown() error }); ok {
		defer func() { _ = s.Shutdown() }()
	}

	// The startup path's type switch: only *MerchantDegraded takes the
	// degraded wiring. With the mint up, New must not hand back a degraded
	// merchant — otherwise installMerchant would wire the degraded pair onto
	// a service that has a working wallet.
	if deg, ok := full.(*MerchantDegraded); ok {
		t.Fatalf("New returned a degraded merchant (%T) with the mint healthy; installMerchant would wire the degraded pair", deg)
	}
}
