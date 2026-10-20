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
		fmt.Fprintf(os.Stderr, "\nLEAK: %d usage sweep(s) still live at test exit — a sweep that outlives its merchant will exec ndsctl and report into whichever later test's fixtures are live (#733; see sweep_leak_guard_test.go). Some test constructed a full merchant (or received one via an upgrade) without draining it: add drainUsageSweep / m.stopDataUsageMonitoring, or m.Shutdown() the merchant.\n", live)
		usageSweepBorn.Range(func(k, v any) bool {
			fmt.Fprintf(os.Stderr, "=== LEAKED SWEEP #%d born at (run with TOLLGATE_TEST_SWEEP_STACKS=1 to keep these):\n%s\n", k.(int32), v.(string))
			return true
		})
		os.Exit(1)
	}
	os.Exit(code)
}

// orderingWitnessWallet fails its test if the merchant's usage sweep is still
// armed when the wallet is shut down: (*Merchant).Shutdown must stop the
// sweep BEFORE the wallet, because stopDataUsageMonitoring also waits out the
// sweep pass that is in flight — a sweep metering sessions against a wallet
// that is already closing underneath it is exactly the window the ordering
// exists to keep shut.
type orderingWitnessWallet struct {
	tollwallet.WalletPort
	t *testing.T
	m *Merchant
}

func (w *orderingWitnessWallet) Shutdown() error {
	if w.sweepStillArmed() {
		w.t.Error("(*Merchant).Shutdown shut the wallet down while the usage sweep was still armed: the sweep must be stopped (and its in-flight pass waited out) BEFORE the wallet closes — a metering sweep against a closing wallet is the race the ordering exists to prevent (#733)")
	}
	return nil
}

// sweepStillArmed reads the sweep's armed state under monitorMu, so a witness
// can observe it from inside wallet.Shutdown race-free.
func (w *orderingWitnessWallet) sweepStillArmed() bool {
	w.m.monitorMu.Lock()
	defer w.m.monitorMu.Unlock()
	return w.m.monitorOn || w.m.monitorDone != nil
}

// TestShutdownStopsTheSweepBeforeTheWallet pins the ordering guarantee of the
// #887 Shutdown: stopDataUsageMonitoring runs to completion first, then the
// wallet closes. The witness wallet observes the sweep's armed state from
// inside wallet.Shutdown, so the assertion is about the exact instant the
// ordering question asks about, not a before/after timing inference.
func TestShutdownStopsTheSweepBeforeTheWallet(t *testing.T) {
	w := &orderingWitnessWallet{t: t}
	m := &Merchant{
		config:           &config_manager.Config{Metric: "milliseconds"},
		customerSessions: make(map[string]*CustomerSession),
		expiredSessions:  make(map[string]int64),
		tollwallet:       w,
	}
	w.m = m

	m.startUsageSweep()

	if !w.sweepStillArmed() {
		t.Fatal("precondition: the sweep must be armed before Shutdown is called")
	}

	if err := m.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if w.sweepStillArmed() {
		t.Error("Shutdown returned with the usage sweep still armed")
	}
}
