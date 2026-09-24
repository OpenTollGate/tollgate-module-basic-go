package merchant

import (
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
)

// The usage monitor's contract (C1-2b, C1-2c; pre-release security audit
// 2026-09-23).
//
// A bytes session's gate is enforced ONLY by this monitor, so a session the
// monitor stops looking at is a session that keeps an open gate for as long as
// the process lives. The monitor must never do that:
//
//   - a gate whose close FAILED is not closed: the session stays tracked so the
//     next sweep retries, and it is retired only once the close is confirmed;
//   - a bytes session whose metering baseline was never established must have
//     one established (and re-established), not be skipped forever.
//
// The tests drive the real checkDataUsage against the fake ndsctl on PATH.

// failOpenSession installs a bytes session for renewalMAC with a 1-byte
// allotment: any measurable usage reaches the allotment.
func failOpenSession(t *testing.T, m *Merchant) {
	t.Helper()

	m.sessionMu.Lock()
	m.customerSessions[renewalMAC] = &CustomerSession{
		MacAddress: renewalMAC,
		StartTime:  time.Now().Unix(),
		Metric:     "bytes",
		Allotment:  1,
	}
	m.sessionMu.Unlock()
}

func sessionPresent(m *Merchant) bool {
	m.sessionMu.RLock()
	defer m.sessionMu.RUnlock()
	_, exists := m.customerSessions[renewalMAC]
	return exists
}

// TestUsageMonitorKeepsTheSessionWhenTheGateCloseFails is C1-2(b): before the
// fix, checkDataUsage called expireSessionLocked on the error branch too, so a
// failed close destroyed the only record that the client should be closed —
// /session-state answered "none" for a client that was still online through an
// open gate, and nothing retried.
func TestUsageMonitorKeepsTheSessionWhenTheGateCloseFails(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")

	failOpenSession(t, m)

	if err := valve.SetDataBaseline(renewalMAC); err != nil {
		t.Fatalf("SetDataBaseline: %v", err)
	}
	// Drive the reported usage well past the 1-byte allotment.
	ndsctl.setClientKB(t, 4096, 512)
	// The gate cannot be closed right now.
	ndsctl.failDeauth(t, true)

	deauthsBefore := ndsctl.count(t, "DEAUTH ")
	m.checkDataUsage()

	if !sessionPresent(m) {
		t.Fatal("the usage monitor retired the session even though the gate close FAILED: the client keeps an open gate with no record left, so nothing meters or closes it")
	}
	if got := ndsctl.count(t, "DEAUTH ") - deauthsBefore; got == 0 {
		t.Fatal("the usage monitor did not attempt to close the gate at all")
	}

	// ndsctl recovers: the next sweep must close the gate and only retire the
	// session once that close is confirmed.
	ndsctl.failDeauth(t, false)
	m.checkDataUsage()

	if sessionPresent(m) {
		t.Fatal("the session was never retired after the gate close succeeded")
	}
	if got := ndsctl.count(t, "DEAUTH ") - deauthsBefore; got < 2 {
		t.Fatalf("ndsctl deauth attempts = %d, want >= 2: the failed close was not retried", got)
	}
	state, err := m.GetSessionState(renewalMAC)
	if err != nil {
		t.Fatalf("GetSessionState: %v", err)
	}
	if state != SessionStateExpired {
		t.Fatalf("session state after the confirmed close = %q, want %q", state, SessionStateExpired)
	}
}

// TestUsageMonitorMetersASessionWhoseBaselineWasNeverSet is C1-2(c): a bytes
// session with no metering baseline was skipped by every sweep
// (`if !valve.HasDataBaseline(mac) { continue }`), so its allotment was purely
// decorative — the gate was never closed and the customer's usage was never
// counted. The monitor must establish the baseline instead of skipping.
func TestUsageMonitorMetersASessionWhoseBaselineWasNeverSet(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")

	failOpenSession(t, m)

	if valve.HasDataBaseline(renewalMAC) {
		t.Fatal("precondition: this test needs a session with no metering baseline")
	}

	// Sweep 1: the monitor has to establish the baseline it needs.
	m.checkDataUsage()
	if !valve.HasDataBaseline(renewalMAC) {
		t.Fatal("the usage monitor left a bytes session without a metering baseline: the session can never be metered or closed")
	}

	// The customer uses data, and the next sweep must meter and close it.
	ndsctl.setClientKB(t, 4096, 512)
	m.checkDataUsage()

	if sessionPresent(m) {
		t.Fatal("a bytes session that reached its allotment was left tracked: the gate stays open and unmetered")
	}
	if got := ndsctl.count(t, "DEAUTH "); got == 0 {
		t.Fatal("the usage monitor never closed the gate of the metered session")
	}
}

// TestUsageMonitorClosesAGateItCannotMeter covers the other half of the same
// skip: when ndsctl cannot report the client's counters, the monitor used to
// read 0 bytes for ever, which can never reach an allotment — the session and
// its open gate survived indefinitely. A session the module cannot meter must be
// closed (and the close is itself retried, never assumed).
func TestUsageMonitorClosesAGateItCannotMeter(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")

	failOpenSession(t, m)

	if err := valve.SetDataBaseline(renewalMAC); err != nil {
		t.Fatalf("SetDataBaseline: %v", err)
	}

	// NDS stops answering for this client (`ndsctl json` -> "{}"), so its usage
	// cannot be read on any sweep.
	ndsctl.setRegistered(t, false)

	// Enough sweeps to pass the monitor's grace window.
	for i := 0; i < 45; i++ {
		m.checkDataUsage()
	}

	if sessionPresent(m) {
		t.Fatal("the usage monitor left a session it can never meter open for ever: the gate stays open and no usage is counted")
	}
	if got := ndsctl.count(t, "DEAUTH "); got == 0 {
		t.Fatal("the usage monitor never attempted to close a gate it cannot meter")
	}
}
