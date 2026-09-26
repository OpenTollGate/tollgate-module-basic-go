package merchant

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
)

// The unmeterable-session escalation's CADENCE (t_83e6ab0f item 2, measured on
// the bench MT3000, pre17, 2026-09-26):
//
//	10:21:09 ERROR: the usage of the bytes session of 02:11:22:33:44:55 has been
//	        unreadable for 39 sweeps ...: the session cannot be metered, so its
//	        gate is closed rather than left open unmetered
//
// A session whose counters the module cannot read is closed after a minute of
// grace, and that is the right fail-safe direction: an unmeasurable session left
// open is unmetered internet. But the sweep runs every 2 s, so once the grace
// window was over the module repeated the escalation — and the close failure
// under it — on EVERY sweep, for as long as NoDogSplash refused the close. That
// is the same unbounded, self-inflicted log storm the close-retry budget was
// added for, one layer up: 60 identical ERROR lines a minute, which is how a
// real escalation stops being readable.
//
// The contract: the force-close is escalated ONCE, and while the session stays
// unmeterable the module reports at most once per
// unmeterableSessionReportInterval, at WARNING. What must NOT change: the
// session stays TRACKED (a failed close is not a close) and the module keeps
// attempting the close, so the state still converges when ndsctl recovers.
func TestUnmeterableSessionEscalationIsBounded(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")

	const macAddress = "aa:bb:cc:dd:ee:7a"
	closeGateCleanup(t, macAddress)
	installBytesSession(t, m, macAddress, 22020096)

	// The state the force-close exists for: the client is still listed, the
	// module's session is tracked and open, and NoDogSplash refuses every
	// deauthorization — so the close can never be confirmed and the session
	// cannot be retired.
	ndsctl.setRegistered(t, true)
	ndsctl.failDeauth(t, true)
	unmeterableSessionReportInterval = time.Hour

	buf := captureMerchantLog(t)

	usageErr := errors.New("client counters unavailable")
	for i := 0; i < usageMonitorGraceSweeps+3; i++ {
		m.closeUnmeterableSession(macAddress, usageErr)
	}

	escalations := linesMentioning(buf.String(), macAddress, "has been unreadable for")
	if len(escalations) != 1 {
		t.Fatalf("the force-close was escalated %d times for one unmeterable session, want exactly 1: %v", len(escalations), escalations)
	}
	if !strings.Contains(escalations[0], "closed rather than left open unmetered") {
		t.Fatalf("the escalation does not say what the module does about the unmeterable session: %q", escalations[0])
	}

	// The same state, for a long stretch. The window is what is measured: the
	// grace window's own per-sweep warnings are bounded by the window itself.
	windowStart := buf.Len()
	for i := 0; i < 60; i++ {
		m.closeUnmeterableSession(macAddress, usageErr)
	}
	window := buf.String()[windowStart:]

	if got := len(linesMentioning(window, macAddress, "has been unreadable for")); got != 0 {
		t.Fatalf("the force-close escalation repeated %d times over 60 further sweeps of the same state: an escalation that repeats every 2 s is not an escalation", got)
	}
	if got := len(linesMentioning(window, macAddress, "could not close the gate")); got != 0 {
		t.Fatalf("the close failure of an unmeterable session was reported %d times over 60 sweeps: it belongs to the bounded escalation, not to every sweep", got)
	}
	if got := len(linesMentioning(window, macAddress, "")); got > 2 {
		t.Fatalf("%d lines were written about one unmeterable session over 60 sweeps; only a change of state may add one", got)
	}

	// Bounded reporting must not become "no enforcement": the session of a client
	// whose counters cannot be read stays TRACKED while its close is unconfirmed,
	// so the record that keeps the gate closable is never dropped.
	if !hasSession(m, macAddress) {
		t.Fatal("the session of a client whose counters cannot be read was retired without a confirmed close: the record that says the gate must be closed is the only thing that keeps it closable")
	}
}

// TestUnmeterableSessionIsStillClosedWhenNdsctlRecovers: throttling the reporting
// must not stop the module from ATTEMPTING the close, and the session must still
// be retired the moment NoDogSplash confirms it.
func TestUnmeterableSessionIsStillClosedWhenNdsctlRecovers(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")

	const macAddress = "aa:bb:cc:dd:ee:7c"
	closeGateCleanup(t, macAddress)
	installBytesSession(t, m, macAddress, 22020096)

	ndsctl.setRegistered(t, true)
	ndsctl.failDeauth(t, true)

	usageErr := errors.New("client counters unavailable")
	for i := 0; i < usageMonitorGraceSweeps+2; i++ {
		m.closeUnmeterableSession(macAddress, usageErr)
	}
	if !hasSession(m, macAddress) {
		t.Fatal("precondition: the session must still be tracked while the close is unconfirmed")
	}

	ndsctl.failDeauth(t, false)
	m.closeUnmeterableSession(macAddress, usageErr)

	if hasSession(m, macAddress) {
		t.Fatal("the session was not retired after NoDogSplash confirmed the close: bounded reporting must not stop the module from closing a gate it cannot meter")
	}
}

// captureMerchantLog redirects the standard logger the merchant writes with into
// a buffer, so a test can assert what an operator would see.
func captureMerchantLog(t *testing.T) *bytes.Buffer {
	t.Helper()

	buffer := &bytes.Buffer{}
	previousOut, previousFlags := log.Writer(), log.Flags()
	log.SetOutput(buffer)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previousOut)
		log.SetFlags(previousFlags)
	})
	return buffer
}

// linesMentioning returns the lines of logs that name macAddress and contain
// fragment (an empty fragment matches any line naming the address).
func linesMentioning(logs, macAddress, fragment string) []string {
	matches := []string{}
	for _, line := range strings.Split(logs, "\n") {
		if !strings.Contains(line, macAddress) {
			continue
		}
		if fragment != "" && !strings.Contains(line, fragment) {
			continue
		}
		matches = append(matches, line)
	}
	return matches
}

// TestUnmeterableSessionEscalationStillExposesTheAbandonedClose keeps the other
// half of the contract honest: throttling must not silence the state a human has
// to act on. The valve's abandonment of a close whose budget is spent is a CHANGE
// of state, so it is reported at once even with the repeats throttled — and the
// operation the operator has to perform is still named.
func TestUnmeterableSessionEscalationStillExposesTheAbandonedClose(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")

	const macAddress = "aa:bb:cc:dd:ee:7b"
	closeGateCleanup(t, macAddress)

	ndsctl.setRegistered(t, true)
	ndsctl.failDeauth(t, true)

	buf := captureMerchantLog(t)

	abandonedBefore := valve.GateClosesAbandoned()

	usageErr := errors.New("client counters unavailable")
	for i := 0; i < usageMonitorGraceSweeps+closeAttemptBudget+2; i++ {
		m.closeUnmeterableSession(macAddress, usageErr)
	}

	// The valve's own budget is the authority on the abandoned close, and it must
	// still be reached (throttling the merchant's reporting must not stop the
	// module from closing the gate).
	if got := valve.GateClosesAbandoned() - abandonedBefore; got != 1 {
		t.Fatalf("the close of one stuck gate was abandoned %d times, want exactly 1: throttling must not stop the module from spending its close budget", got)
	}

	all := linesMentioning(buf.String(), macAddress, "")
	if len(all) == 0 {
		t.Fatalf("nothing was logged about %s, so an operator cannot see the unmeterable session at all", macAddress)
	}

	// The abandonment itself is reported (it is a change of state), and it does
	// not claim the close is still retried.
	abandoned := linesMentioning(buf.String(), macAddress, "has stopped re-attempting this close")
	if len(abandoned) == 0 {
		t.Fatalf("the abandoned close was never reported for %s: an operator reading the log cannot tell that the module has stopped driving ndsctl for this gate. Lines: %v", macAddress, all)
	}
	if strings.Contains(all[len(all)-1], "the close is retried") {
		t.Fatalf("the last line about %s still claims the close is retried while its budget is spent: %q", macAddress, all[len(all)-1])
	}
}

// closeAttemptBudget mirrors the valve's per-gate close budget so this test can
// drive past it without exporting the constant. It is a bound, not an exact
// value: the test only needs to be past the budget.
const closeAttemptBudget = 8
