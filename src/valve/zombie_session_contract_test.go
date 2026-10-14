package valve

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// The machinery the zombie-session fix adds, in its own file: the close BUDGET,
// the abandonment escalation, the recovery path the reconciliation uses, and the
// second confirmation channel. The contract tests that fail on the previous
// behaviour are in zombie_session_test.go; these exercise symbols that only exist
// after the change.
//
// Addresses here belong to this file only.

const (
	// budgetMAC is driven until its close budget is spent.
	budgetMAC = "aa:bb:cc:dd:ee:52"

	// recoveryMAC is abandoned and then closed under fresh evidence.
	recoveryMAC = "aa:bb:cc:dd:ee:53"

	// absenceMAC has a deauth that fails for a reason that says nothing about the
	// client, while NoDogSplash's own client view says the client is not there.
	absenceMAC = "aa:bb:cc:dd:ee:54"
)

// driveCloseBudget calls CloseGate until the gate's budget is spent, which the
// log announces once.
func driveCloseBudget(t *testing.T, macAddress string) {
	t.Helper()

	for i := 0; i < 3*closeAttemptBudget; i++ {
		_ = CloseGate(macAddress)
		gatesMutex.Lock()
		abandoned := closeStreaks[macAddress] != nil && closeStreaks[macAddress].abandoned
		gatesMutex.Unlock()
		if abandoned {
			return
		}
	}
	t.Fatal("the close budget was never spent: this test needs an abandoned gate")
}

// TestSpentCloseBudgetStopsTheModuleAndSaysSoOnce is the bound itself: the
// module must stop driving ndsctl about a close it cannot confirm, must keep the
// gate tracked, must report the abandonment as ErrGateCloseAbandoned so callers
// can tell it apart from an ordinary failure, and must escalate it exactly once.
func TestSpentCloseBudgetStopsTheModuleAndSaysSoOnce(t *testing.T) {
	ndsctl := setUpZombieGateTest(t)
	ndsctl.wedgeSocket()
	log := captureValveLogAt(t, logrus.ErrorLevel)

	if err := OpenGateUntil(budgetMAC, time.Now().Unix()+3600); err != nil {
		t.Fatalf("OpenGateUntil: %v", err)
	}

	abandonedBefore := GateClosesAbandoned()
	driveCloseBudget(t, budgetMAC)

	if got := GateClosesAbandoned() - abandonedBefore; got != 1 {
		t.Fatalf("GateClosesAbandoned grew by %d, want exactly 1: one stuck gate is one abandoned close", got)
	}
	if !gateTracked(budgetMAC) {
		t.Fatal("an abandoned close dropped the gate from tracking: the record that says the client must be closed is gone")
	}

	attemptsAtBudget := ndsctl.attemptsFor(budgetMAC)
	failuresAtBudget := GateCloseFailures()

	err := CloseGate(budgetMAC)
	if err == nil {
		t.Fatal("CloseGate reported success for a gate whose close was never confirmed")
	}
	if !errors.Is(err, ErrGateCloseAbandoned) {
		t.Fatalf("CloseGate error = %v, want it to wrap ErrGateCloseAbandoned so a caller can tell an abandoned close from a failing one", err)
	}
	if got := ndsctl.attemptsFor(budgetMAC); got != attemptsAtBudget {
		t.Fatalf("ndsctl deauth calls grew from %d to %d after the budget was spent: the module is still driving the interface the close depends on", attemptsAtBudget, got)
	}
	if got := GateCloseFailures(); got != failuresAtBudget {
		t.Fatalf("unconfirmed_closes grew from %d to %d after the budget was spent", failuresAtBudget, got)
	}
	if got := strings.Count(log.String(), "UNRESOLVED"); got != 1 {
		t.Fatalf("the abandonment was escalated %d times, want exactly 1", got)
	}
}

// TestReconcileGateCloseRecoversAnAbandonedGate is the recovery path: a gate the
// module's own machinery has given up on must still be closable when the
// reconciliation establishes, with ndsctl, that NoDogSplash does not know the
// client. Without it the abandoned gate would be unclosable — the zombie session
// again, one state further along.
func TestReconcileGateCloseRecoversAnAbandonedGate(t *testing.T) {
	ndsctl := setUpZombieGateTest(t)
	ndsctl.wedgeSocket()
	log := captureValveLogAt(t, logrus.InfoLevel)

	if err := OpenGateUntil(recoveryMAC, time.Now().Unix()+3600); err != nil {
		t.Fatalf("OpenGateUntil: %v", err)
	}
	driveCloseBudget(t, recoveryMAC)

	if !gateTracked(recoveryMAC) {
		t.Fatal("precondition: the gate must still be tracked after its budget was spent")
	}

	// The client leaves and NoDogSplash drops its record: the reconciliation's
	// probe and its close now both see a client that is not there.
	ndsctl.forgetClient()
	ndsctl.setRegistered(false)

	if err := ReconcileGateClose(recoveryMAC); err != nil {
		t.Fatalf("ReconcileGateClose = %v for a client NoDogSplash does not know: a gate whose budget is spent must not become unclosable", err)
	}
	if gateTracked(recoveryMAC) {
		t.Fatal("the reconciliation's close was confirmed but the gate is still tracked")
	}
	if !strings.Contains(log.String(), "already gone") {
		t.Fatalf("the retirement of an abandoned gate whose client is gone was not logged: log = %q", log.String())
	}
}

// TestReconcileGateCloseDoesNotEscalateOrAdvanceTheBudget: the reconciliation is
// allowed to re-attempt a spent close because it brings evidence, but that must
// not turn into the unbounded counter growth the bound exists to stop. A caller
// that keeps bringing the same evidence and keeps failing must not be able to
// move unconfirmed_closes.
func TestReconcileGateCloseDoesNotEscalateOrAdvanceTheBudget(t *testing.T) {
	ndsctl := setUpZombieGateTest(t)
	ndsctl.wedgeSocket() // nothing about the client is ever verified

	if err := OpenGateUntil(recoveryMAC, time.Now().Unix()+3600); err != nil {
		t.Fatalf("OpenGateUntil: %v", err)
	}
	driveCloseBudget(t, recoveryMAC)

	failuresBefore := GateCloseFailures()
	abandonedBefore := GateClosesAbandoned()

	for i := 0; i < 5; i++ {
		if err := ReconcileGateClose(recoveryMAC); err == nil {
			t.Fatal("ReconcileGateClose reported success although the deauthorization never succeeded and the client is still listed")
		}
	}

	if got := GateCloseFailures(); got != failuresBefore {
		t.Fatalf("unconfirmed_closes grew by %d across reconciliation passes that brought no new evidence: the counter must be bounded", got-failuresBefore)
	}
	if got := GateClosesAbandoned(); got != abandonedBefore {
		t.Fatalf("the abandonment counter grew by %d although the gate was already abandoned", got-abandonedBefore)
	}
	if !gateTracked(recoveryMAC) {
		t.Fatal("a failed reconciliation close dropped the gate: the record that says the client must be closed is gone")
	}
}

// TestANewGateGenerationGetsAFreshCloseBudget: the budget belongs to a gate
// GENERATION, not to a MAC. A client who pays again must get the full budget —
// otherwise a customer whose previous session ended badly would start one close
// attempt away from being abandoned.
func TestANewGateGenerationGetsAFreshCloseBudget(t *testing.T) {
	ndsctl := setUpZombieGateTest(t)
	ndsctl.wedgeSocket()

	if err := OpenGateUntil(budgetMAC, time.Now().Unix()+3600); err != nil {
		t.Fatalf("OpenGateUntil: %v", err)
	}
	driveCloseBudget(t, budgetMAC)

	attemptsBefore := ndsctl.attemptsFor(budgetMAC)

	// The customer pays again: the gate is extended, which is a new generation.
	if err := OpenGateUntil(budgetMAC, time.Now().Unix()+7200); err != nil {
		t.Fatalf("OpenGateUntil (extension): %v", err)
	}

	if err := CloseGate(budgetMAC); !errors.Is(err, ErrGateCloseAbandoned) {
		if got := ndsctl.attemptsFor(budgetMAC); got <= attemptsBefore {
			t.Fatalf("ndsctl deauth calls = %d, want more than %d: a new gate generation with a stale spent budget would be unclosable", got, attemptsBefore)
		}
	}
}

// TestAbsenceFromNdsCompletesACloseWhenTheDeauthOnlyErrored: ndsctl's exit status
// is one way to confirm a close, not the only one. When the deauthorization
// fails for a reason that says nothing about the client, the second channel — a
// definitive "NoDogSplash has no record for this MAC" — must still complete it,
// because a client NDS does not hold cannot be holding the gate open.
func TestAbsenceFromNdsCompletesACloseWhenTheDeauthOnlyErrored(t *testing.T) {
	ndsctl := setUpZombieGateTest(t)
	ndsctl.wedgeSocket()
	ndsctl.setRegistered(false)
	log := captureValveLogAt(t, logrus.InfoLevel)

	if err := OpenGateUntil(absenceMAC, time.Now().Unix()+3600); err != nil {
		t.Fatalf("OpenGateUntil: %v", err)
	}

	if err := CloseGate(absenceMAC); err != nil {
		t.Fatalf("CloseGate = %v although NoDogSplash reports no client record for this MAC: the client holds nothing to close", err)
	}
	if gateTracked(absenceMAC) {
		t.Fatal("the gate of a client NoDogSplash has no record of is still tracked")
	}
	if !strings.Contains(log.String(), "already gone") {
		t.Fatalf("the completed close was not logged with the verified state: log = %q", log.String())
	}
}

// TestAFailedProbeNeverCompletesAClose: the second channel must be narrow. A
// probe that cannot answer is not evidence that the client is gone, and turning
// it into one would retire the gate of a paying customer whose ndsctl merely
// hiccuped.
func TestAFailedProbeNeverCompletesAClose(t *testing.T) {
	origRunNdsctl := runNdsctl
	t.Cleanup(func() { runNdsctl = origRunNdsctl })

	// deauth fails; `ndsctl json` fails too (a wedged socket answers nothing).
	runNdsctl = func(args ...string) (string, error) {
		return "", errors.New("exit status 1")
	}
	gatesMutex.Lock()
	delete(openGates, absenceMAC)
	delete(closeStreaks, absenceMAC)
	gatesMutex.Unlock()

	// OpenGateUntil needs a working auth, so arm the gate first with the zombie
	// stub and then swap the seam.
	zombie := setUpZombieGateTest(t)
	zombie.wedgeSocket()
	if err := OpenGateUntil(absenceMAC, time.Now().Unix()+3600); err != nil {
		t.Fatalf("OpenGateUntil: %v", err)
	}
	runNdsctl = func(args ...string) (string, error) {
		return "", errors.New("exit status 1")
	}

	if err := CloseGate(absenceMAC); err == nil {
		t.Fatal("CloseGate reported success although neither the deauthorization nor the client probe could be answered: an unanswerable probe is not evidence that the client is gone")
	}
	if !gateTracked(absenceMAC) {
		t.Fatal("the gate was dropped after a close that nothing confirmed")
	}
}
