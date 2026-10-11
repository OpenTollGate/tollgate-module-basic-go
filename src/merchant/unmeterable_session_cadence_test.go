package merchant

import (
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
	restoreReportInterval := setUnmeterableReportInterval(time.Hour)
	t.Cleanup(restoreReportInterval)

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
// a buffer, so a test can assert what an operator would see. The buffer is the
// concurrency-safe syncLogs: a goroutine left over from an earlier test (a
// MintHealthTracker probe still winding down) may write through the swapped
// logger while this test reads the capture, and an unsynchronized buffer made
// that a data race (the intermittent "race detected during execution of test"
// from the #619 review).
func captureMerchantLog(t *testing.T) *syncLogs {
	t.Helper()
	return captureSyncLogs(t)
}

// TestCaptureMerchantLogIsSafeToReadWhileGoroutinesLog pins the contract that
// made `go test -race -tags testenv ./...` fail intermittently with "race
// detected during execution of test" during the #619 review: a MintHealthTracker
// goroutine left over from an earlier test (an aggressive-retry probe still in
// flight when its tracker was stopped) writes through the standard logger that
// THIS test swapped in, while the test reads the capture. A capture the package
// hands out must therefore be safe to read while other goroutines may still be
// writing it — the same contract captureSyncLogs already honours.
func TestCaptureMerchantLogIsSafeToReadWhileGoroutinesLog(t *testing.T) {
	buf := captureMerchantLog(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			log.Printf("stray goroutine line %d", i)
		}
	}()

	// Read the capture while the stray writer is still logging: under -race,
	// an unsynchronized buffer here is the reported data race.
	for i := 0; i < 500; i++ {
		_ = buf.String()
	}
	<-done

	if !strings.Contains(buf.String(), "stray goroutine line 499") {
		t.Fatal("the capture did not observe the stray writer's lines")
	}
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
	// The gate cleanup below must be able to reconcile the abandoned streak
	// this test spends, which needs a working deauth: lift the failure before
	// that cleanup runs (cleanups are LIFO, so this registers last, runs
	// first).
	t.Cleanup(func() { ndsctl.failDeauth(t, false) })

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

// setUnmeterableReportInterval shrinks the repeat interval for one test and
// returns the restore, so this file's tuning of a package-level var cannot
// silence or accelerate the reporting of a later test.
func setUnmeterableReportInterval(interval time.Duration) func() {
	previous := unmeterableSessionReportInterval
	unmeterableSessionReportInterval = interval
	return func() { unmeterableSessionReportInterval = previous }
}

// TestUnmeterableEpisodeEscalatesAgainAfterItsSessionIsRetired pins that the
// escalation is written once per EPISODE and not once per MAC for the life of
// the process.
//
// The bookkeeping belongs to the session that could not be metered, so a
// CONFIRMED force-close must forget it. If it does not, the flag stays set for
// that address and a later, distinct unmeterable session starts in the throttled
// branch: no ERROR escalation at all (only the "the state changed" WARNING, or
// nothing), and — because the unchanged `sweeps` counter continues from the
// previous episode — no grace window of its own either, so the new session is
// force-closed on its first unreadable sweep instead of after a minute. That is
// the "a bound whose reset cannot fire in normal operation" shape: the address is
// live again, the state machine is not.
func TestUnmeterableEpisodeEscalatesAgainAfterItsSessionIsRetired(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")

	const macAddress = "aa:bb:cc:dd:ee:7d"
	closeGateCleanup(t, macAddress)

	restoreReportInterval := setUnmeterableReportInterval(time.Hour)
	t.Cleanup(restoreReportInterval)

	usageErr := errors.New("client counters unavailable")

	// Episode one: the counters are unreadable and NoDogSplash refuses the
	// close, so the force-close escalates once and the session stays tracked.
	installBytesSession(t, m, macAddress, 22020096)
	ndsctl.setRegistered(t, true)
	ndsctl.failDeauth(t, true)
	for i := 0; i < usageMonitorGraceSweeps+1; i++ {
		m.closeUnmeterableSession(macAddress, usageErr)
	}

	// NoDogSplash answers again: the close is confirmed and the session is
	// retired.
	ndsctl.failDeauth(t, false)
	m.closeUnmeterableSession(macAddress, usageErr)
	if hasSession(m, macAddress) {
		t.Fatal("precondition: a confirmed force-close must retire the session")
	}

	// Episode two, same address, a new paid session: the counters are unreadable
	// again and the close is refused again.
	installBytesSession(t, m, macAddress, 22020096)
	ndsctl.failDeauth(t, true)

	buf := captureMerchantLog(t)
	for i := 0; i < usageMonitorGraceSweeps+1; i++ {
		m.closeUnmeterableSession(macAddress, usageErr)
	}

	escalations := linesMentioning(buf.String(), macAddress, "has been unreadable for")
	if len(escalations) != 1 {
		t.Fatalf("a second, distinct unmeterable episode on the same address was escalated %d times, want exactly 1: the bookkeeping of the retired session must not silence the new one (and must not carry its sweep count into it either)\nlines: %v", len(escalations), linesMentioning(buf.String(), macAddress, ""))
	}
}
