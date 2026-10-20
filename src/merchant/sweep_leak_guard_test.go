package merchant

// The suite-level usage-sweep leak guard (#733) and the Shutdown ordering it
// protects.
//
// A usage sweep that outlives its merchant is not idle garbage: on its
// client-list cadence it execs `ndsctl` through the process PATH — which
// t.Setenv repoints at whichever later test's fake is live — closes gates of
// clients its dead session map never knew, mutates the valve's package-global
// close-streak state, and reports through the standard logger into whatever
// capture holds. That is the flake family behind "merchant suite green
// serialized, flakes under heavy concurrent load": on an idle box the leaked
// pass usually fires between tests; under load the suite stretches and the
// pass lands inside a later test's window (deterministic repro:
// `deauths = 2, want 1` in ~19 s, nds-flake-hunt lane, 2026-10-20).
//
// The guard makes reintroducing a leak a deterministic red instead of a
// load-luck flake: if any sweep is still live when the test binary finishes,
// the run FAILS here, naming how many. Run with TOLLGATE_TEST_SWEEP_STACKS=1
// to also capture where each live sweep was constructed, so the failing run
// names the test that leaked.

import (
	"fmt"
	"os"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if live := usageSweepLive.Load(); live > 0 {
		fmt.Fprintf(os.Stderr, "\nLEAK: %d usage sweep(s) still live at test exit — a sweep that outlives its merchant will exec ndsctl and report into whichever later test's fixtures are live (#733; see sweep_leak_guard_test.go). Some test constructed a full merchant (or received one via an upgrade) without draining it: add drainBackgroundWorkers / m.stopDataUsageMonitoring, or m.Shutdown() the merchant.\n", live)
		usageSweepBorn.Range(func(k, v any) bool {
			fmt.Fprintf(os.Stderr, "=== LEAKED SWEEP #%d born at (run with TOLLGATE_TEST_SWEEP_STACKS=1 to keep these):\n%s\n", k.(int32), v.(string))
			return true
		})
		os.Exit(1)
	}
	if payout := payoutLive.Load(); payout > 0 {
		fmt.Fprintf(os.Stderr, "\nLEAK: %d payout routine goroutine(s) still live at test exit — a payout routine that outlives its merchant keeps ticker-driven wallet work running on a merchant nobody owns (#733 follow-up; see sweep_leak_guard_test.go). Some test constructed a full merchant (or received one via an upgrade) without m.Shutdown()-ing or draining it.\n", payout)
		os.Exit(1)
	}
	if janitors := janitorLive.Load(); janitors > 0 {
		fmt.Fprintf(os.Stderr, "\nLEAK: %d lightning-quote janitor goroutine(s) still live at test exit — same class as a leaked payout routine (#733 follow-up; see sweep_leak_guard_test.go).\n", janitors)
		os.Exit(1)
	}
	os.Exit(code)
}

// workersStillArmed reports whether any of the merchant's timer-driven
// background work is still armed (usage sweep, payout routine, quote
// janitor), read under each worker's own lock.
func (m *Merchant) workersStillArmed() bool {
	m.monitorMu.Lock()
	sweep := m.monitorOn || m.monitorDone != nil
	m.monitorMu.Unlock()

	m.payoutMu.Lock()
	payout := m.payoutOn || m.payoutStop != nil
	m.payoutMu.Unlock()

	m.janitorMu.Lock()
	janitor := m.janitorOn || m.janitorStop != nil || m.janitorDone != nil
	m.janitorMu.Unlock()

	return sweep || payout || janitor
}

// orderingWitnessWallet fails its test if any of the merchant's timer-driven
// background work is still armed when the wallet is shut down: (*Merchant).
// Shutdown must stop the usage sweep, the payout routine and the quote
// janitor BEFORE the wallet — each stop also waits out the pass that is in
// flight, because a sweep metering sessions or a payout moving wallet money
// against a wallet that is already closing underneath it is exactly the
// window the ordering exists to keep shut.
type orderingWitnessWallet struct {
	tollwallet.WalletPort
	t *testing.T
	m *Merchant
}

func (w *orderingWitnessWallet) Shutdown() error {
	if w.m.workersStillArmed() {
		w.t.Error("(*Merchant).Shutdown shut the wallet down while background work (usage sweep / payout routine / quote janitor) was still armed: every worker must be stopped (and its in-flight pass waited out) BEFORE the wallet closes — work running against a closing wallet is the race the ordering exists to prevent (#733)")
	}
	return nil
}

// TestShutdownStopsEveryBackgroundWorkerBeforeTheWallet pins the ordering
// guarantee of Shutdown: all three timer-driven workers (usage sweep, payout
// routine, quote janitor) are retired before the wallet closes, and stay
// retired afterwards. The witness wallet observes the armed state from inside
// wallet.Shutdown, so the assertion is about the exact instant the ordering
// question asks about, not a before/after timing inference.
func TestShutdownStopsEveryBackgroundWorkerBeforeTheWallet(t *testing.T) {
	w := &orderingWitnessWallet{t: t}
	m := &Merchant{
		config: &config_manager.Config{
			Metric: "milliseconds",
			AcceptedMints: []config_manager.MintConfig{
				{URL: "https://witness-mint.example.com", PricePerStep: 1, PriceUnit: "sat"},
			},
		},
		customerSessions: make(map[string]*CustomerSession),
		expiredSessions:  make(map[string]int64),
		tollwallet:       w,
	}
	w.m = m

	// The mint health tracker is shared infrastructure (a runtime-downgraded
	// merchant keeps using it to upgrade back), so Shutdown deliberately does
	// not stop it — this test must not leak its proactive checks either.
	tracker := newTestTracker(m.config, nil)
	t.Cleanup(tracker.Stop)
	m.mintHealthTracker = tracker

	m.startUsageSweep()
	m.StartPayoutRoutine()
	m.startLightningQuoteJanitor()

	if !m.workersStillArmed() {
		t.Fatal("precondition: the workers must be armed before Shutdown is called")
	}

	if err := m.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if m.workersStillArmed() {
		t.Error("Shutdown returned with background work still armed")
	}
}
