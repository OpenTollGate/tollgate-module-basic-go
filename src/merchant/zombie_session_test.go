package merchant

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
)

// The ZOMBIE SESSION, merchant side (bench MT3000, pre17, 2026-09-26).
//
// The valve-level contract is in src/valve/zombie_session_test.go. This file
// pins the two things the CUSTOMER-visible symptom depends on, driven through the
// real usage-monitor entry point against a fake ndsctl that answers the way the
// bench did:
//
//   - the session of a client NoDogSplash has FORGOTTEN must be RETIRED (the
//     bench log shows the opposite: "the session is retained and the close is
//     retried", 170+ sweeps of "unusable/unreadable" for a MAC that had left, so
//     /balance and the portal kept reporting a session whose client could not be
//     given access);
//   - the module must CONVERGE: once the address is gone from NoDogSplash, the
//     module must stop touching it and unconfirmed_closes must stop moving. The
//     measured loop climbed 113 -> 193 -> 195 and drove ndsctl at the sweep
//     cadence, which is what wedged its socket and, with it, the authorisation
//     path for NEW purchases.
//
// Addresses here belong to this file only: the valve's gate state is
// package-global for the whole test binary, so every assertion is per-MAC.

const (
	// zombieForgottenMAC carries the metered session whose client has left.
	zombieForgottenMAC = "aa:bb:cc:dd:ee:60"

	// zombieOrphanMAC carries a gate the module holds with no session record.
	zombieOrphanMAC = "aa:bb:cc:dd:ee:61"
)

// TestMonitorRetiresASessionWhoseClientNdsHasForgotten is the bench scenario in
// the metering lane: the counters cannot be read (NoDogSplash answers "{}") and
// the deauth answers "Client <mac> not found." with exit status 1. Before the fix
// the monitor burned its grace window, then closed the gate on every sweep for
// ever: the close never confirmed, the session was never retired, and the
// unconfirmed_closes counter climbed without bound.
func TestMonitorRetiresASessionWhoseClientNdsHasForgotten(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")
	closeGateCleanup(t, zombieForgottenMAC)

	installBytesSession(t, m, zombieForgottenMAC, 1<<40)
	if err := valve.SetDataBaseline(zombieForgottenMAC); err != nil {
		t.Fatalf("SetDataBaseline: %v", err)
	}

	// The customer's device leaves, and NoDogSplash drops its record entirely:
	// `ndsctl json` answers "{}" and `ndsctl deauth` answers
	// "Client <mac> not found." with exit status 1.
	ndsctl.setRegistered(t, false)
	ndsctl.forgetClient(t)

	failuresBefore := valve.GateCloseFailures()

	// The monitor's grace window (30 sweeps) plus the reconciliation's cadence.
	for i := 0; i < 80; i++ {
		m.checkDataUsage()
	}

	if hasSession(m, zombieForgottenMAC) {
		t.Fatal("the session of a client NoDogSplash has forgotten is still tracked: its gate can never be closed (the client is not there to deauthorize) and /balance keeps reporting a session the customer cannot use — the bench kept this state for 170+ sweeps and across test runs")
	}
	if got := valve.GateCloseFailures() - failuresBefore; got != 0 {
		t.Fatalf("unconfirmed_closes grew by %d for a client NoDogSplash does not know: this is a completed close, not a failure", got)
	}

	// Convergence: with the address gone, the module must stop touching it.
	deauthsAfter := deauthsFor(t, ndsctl, zombieForgottenMAC)
	for i := 0; i < 40; i++ {
		m.checkDataUsage()
	}

	if got := deauthsFor(t, ndsctl, zombieForgottenMAC); got != deauthsAfter {
		t.Fatalf("the module kept trying to deauthorize an address NoDogSplash does not know (%d -> %d deauths): that retry storm is what wedged the ndsctl socket on the bench, and a wedged socket is what makes a PAID purchase grant nothing", deauthsAfter, got)
	}
	if got := valve.GateCloseFailures() - failuresBefore; got != 0 {
		t.Fatalf("unconfirmed_closes grew by %d while converging on an address NoDogSplash does not know", got)
	}
}

// TestReconciliationRetiresABindingNdsHasForgotten is the same defect in the lane
// that has no session record: a gate the module still holds for an address
// NoDogSplash has forgotten, which nothing else looks at. The reconciliation has
// the evidence (it probes the client first), so its close must converge and the
// gate must leave the module's bookkeeping.
func TestReconciliationRetiresABindingNdsHasForgotten(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")
	closeGateCleanup(t, zombieOrphanMAC)

	if err := valve.CloseGate(zombieOrphanMAC); err != nil {
		t.Fatalf("CloseGate (precondition): %v", err)
	}
	if err := valve.OpenGate(zombieOrphanMAC); err != nil {
		t.Fatalf("OpenGate (precondition): %v", err)
	}

	// The client is gone from NoDogSplash: the probe says so and the deauth
	// answers "not found".
	stubClientProbe(t, m, clientIsGone)
	ndsctl.forgetClient(t)

	failuresBefore := valve.GateCloseFailures()

	sweep(t, m, staleBindingSweeps)

	if valveTrackedGate(zombieOrphanMAC) {
		t.Fatal("the module still holds a gate for an address NoDogSplash does not know: its close is retried for ever, and the gate is inherited by whoever holds that address next")
	}
	if got := valve.GateCloseFailures() - failuresBefore; got != 0 {
		t.Fatalf("unconfirmed_closes grew by %d for a binding whose client NoDogSplash does not know", got)
	}

	// Convergence: nothing may keep probing or deauthorizing the gone address.
	deauthsAfter := deauthsFor(t, ndsctl, zombieOrphanMAC)
	sweep(t, m, staleBindingSweeps)
	if got := deauthsFor(t, ndsctl, zombieOrphanMAC); got != deauthsAfter {
		t.Fatalf("the module kept deauthorizing an address NoDogSplash does not know (%d -> %d)", deauthsAfter, got)
	}
}

// valveTrackedGate reports whether the module's own bookkeeping still holds a
// gate for macAddress.
func valveTrackedGate(macAddress string) bool {
	for _, tracked := range valve.TrackedGates() {
		if tracked == macAddress {
			return true
		}
	}
	return false
}

// zombieAbandonedMAC carries the gate whose close ndsctl keeps refusing, so the
// close budget runs out and the close is ABANDONED rather than retried.
const zombieAbandonedMAC = "aa:bb:cc:dd:ee:62"

// TestAbandonedCloseIsNotLoggedAsRetried pins the OPERATOR-FACING claim of the
// abandoned state, not just the state itself.
//
// The valve's budget (closeAttemptBudget) stops the sweep machinery from driving
// ndsctl about a gate whose close has gone unconfirmed eight times, and it says
// so once, in its own UNRESOLVED line. The merchant's failure log line used to
// assert, unconditionally, that "the session is retained and the close is
// retried" — a claim the code does NOT honour once the budget is spent, and the
// same class of false operator claim as the "may still hold open, unmetered
// access" wording this release removes. A reviewer reading it would chase a
// retry that never happens.
//
// The test drives the merchant's own unmeterable-session entry point against a
// real valve whose deauth keeps failing while NoDogSplash still lists the client
// (so the read-only probe cannot complete the close either: the client is there,
// the enforcement layer just will not do it) until the budget is spent, then
// reads the LOG ITSELF.
func TestAbandonedCloseIsNotLoggedAsRetried(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")
	closeGateCleanup(t, zombieAbandonedMAC)

	// The client is still listed, but NoDogSplash refuses every deauthorization:
	// the state the budget exists for. Not `forgetClient`: there the client is
	// gone and the close completes at once, which is the other contract.
	ndsctl.setRegistered(t, true)
	ndsctl.failDeauth(t, true)

	abandonedBefore := valve.GateClosesAbandoned()
	failuresBefore := valve.GateCloseFailures()

	// Capture the standard logger the way the production code writes (the
	// testenv-tagged helper lives behind a build tag and this file is not, so
	// the capture is done here: the untagged lane must still vet and compile).
	buf := &bytes.Buffer{}
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})

	// Past the grace window, then past the close budget (8 unconfirmed attempts).
	for i := 0; i < usageMonitorGraceSweeps+12; i++ {
		m.closeUnmeterableSession(zombieAbandonedMAC, errors.New("client counters unavailable"))
	}

	logs := buf.String()
	lines := []string{}
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, zombieAbandonedMAC) {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		t.Fatalf("the merchant never logged about %s, so nothing was driven", zombieAbandonedMAC)
	}

	last := lines[len(lines)-1]
	if !strings.Contains(last, "has stopped re-attempting this close") {
		t.Fatalf("the last line about %s does not report the abandoned close: %q — an operator reading it cannot tell that the module has stopped driving ndsctl for this gate", zombieAbandonedMAC, last)
	}
	if strings.Contains(last, "the close is retried") {
		t.Fatalf("the last line about %s still claims \"the close is retried\" while the close budget is spent: %q — that is a false claim about the module's own behaviour, the class of wording this release removes", zombieAbandonedMAC, last)
	}

	if got := valve.GateClosesAbandoned() - abandonedBefore; got != 1 {
		t.Fatalf("expected exactly one abandoned close for %s, got %d: an abandoned gate must be escalated once, not once per sweep", zombieAbandonedMAC, got)
	}
	if got := valve.GateCloseFailures() - failuresBefore; got > closeBudgetProbeLimit {
		t.Fatalf("unconfirmed_closes grew by %d for one stuck gate, past the close budget: it must be bounded per gate, not monotonic (the bench measured 113 -> 193 -> 195 -> 2141)", got)
	}

	// Convergence: with the budget spent, the module must stop driving ndsctl.
	deauthsAfter := deauthsFor(t, ndsctl, zombieAbandonedMAC)
	for i := 0; i < 20; i++ {
		m.closeUnmeterableSession(zombieAbandonedMAC, errors.New("client counters unavailable"))
	}
	if got := deauthsFor(t, ndsctl, zombieAbandonedMAC); got != deauthsAfter {
		t.Fatalf("the module kept driving ndsctl for a gate whose close budget is spent (%d -> %d deauths): that storm is what wedged the socket on the bench", deauthsAfter, got)
	}

	ndsctl.failDeauth(t, false)
}

// closeBudgetProbeLimit bounds how much unconfirmed_closes may grow for ONE
// stuck gate in the test above: the close budget, and each budgeted attempt may
// spend the valve's internal deauth retries before it is counted.
const closeBudgetProbeLimit = 8 * 4
