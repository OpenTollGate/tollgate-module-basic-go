package merchant

import (
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
)

// This file pins the stale-binding reconciliation contract (pre16 identity
// hardening): a session whose client is NO LONGER on the network must not keep
// its address authorised.
//
// What happens today to a client that rotates its Wi-Fi address mid-session (or
// simply goes away for good): the entitlement is keyed to the address it bought
// on, so the module keeps holding a gate for an address no device holds — and on
// the default `bytes` metric the per-MAC meter has nothing left to measure, so
// whichever enforcement reads the counters can never end the session. The gate
// stays authorised until the process dies, and any later holder of that address
// (a MAC fallback to the hardware address, a spoof, a collision) inherits
// metered internet for free. On top of that, a gate the module holds for an
// address it has no session record for is examined by nothing at all today.
//
// The tests drive the real usage-monitor entry point (`checkDataUsage`) against
// the fake ndsctl on PATH, and stub the read-only NDS identity probe
// (`ndsClientCheck`) whenever they need to model "NoDogSplash no longer lists
// this client" independently of what the byte counters answer. That separation
// is the point: the reconciliation must be decided by the device's presence, not
// by whether its counters happen to be readable.
//
// The addresses below belong to these tests only. The valve's gate state is
// package-global (shared by the whole test binary), so a test that borrowed
// another file's MAC would see that file's gate timers, and a deauth counter
// taken over the whole ndsctl log would count another test's closes — every
// assertion here is therefore per-MAC.

const (
	// staleBindingSweeps is how many 2s-monitor sweeps a test drives before it
	// looks for an effect. Long enough for the reconciliation to run more than
	// once at its production cadence, short enough to keep the suite fast.
	staleBindingSweeps = 45

	// janitorBytesMAC carries the bytes sessions in this file.
	janitorBytesMAC = "aa:bb:cc:dd:ee:0f"

	// janitorOrphanMAC is a second address so an assertion can attribute a
	// deauth to the right binding.
	janitorOrphanMAC = "aa:bb:cc:dd:ee:01"
)

// stubClientProbe replaces the reconciliation's NDS identity probe for ONE
// merchant. The override is per merchant rather than the package-level
// `ndsClientCheck` seam on purpose: the usage monitor runs on its own goroutine,
// and several tests in this package leave a real monitor running, so writing a
// package-global here would race with a sweep this test does not control.
func stubClientProbe(t *testing.T, m *Merchant, probe func(string) (valve.ClientState, error)) {
	t.Helper()

	m.setStaleBindingProbe(probe)
	t.Cleanup(func() { m.setStaleBindingProbe(nil) })
}

// clientIsGone models NoDogSplash answering that it does not list the client at
// all — the answer a rotated address leaves behind.
func clientIsGone(string) (valve.ClientState, error) {
	return valve.ClientState{}, nil
}

// clientIsLive models a device that is associated and authenticated, including
// one that is simply idle.
func clientIsLive(string) (valve.ClientState, error) {
	return valve.ClientState{Registered: true, Authenticated: true}, nil
}

// installBytesSession gives macAddress a paid bytes session with an allotment no
// test traffic will reach, so the counter-based enforcement cannot end it and
// the reconciliation is the only thing under test.
func installBytesSession(t *testing.T, m *Merchant, macAddress string, allotment uint64) {
	t.Helper()

	m.sessionMu.Lock()
	m.customerSessions[macAddress] = &CustomerSession{
		MacAddress: macAddress,
		StartTime:  time.Now().Unix(),
		Metric:     "bytes",
		Allotment:  allotment,
	}
	m.sessionMu.Unlock()
}

// hasSession reports whether this merchant still tracks a session for an address.
func hasSession(m *Merchant, macAddress string) bool {
	m.sessionMu.RLock()
	defer m.sessionMu.RUnlock()

	_, exists := m.customerSessions[macAddress]
	return exists
}

// deauthsFor counts the deauths of ONE address in the fake ndsctl's log, so an
// assertion cannot be satisfied (or broken) by another test's closes.
func deauthsFor(t *testing.T, ndsctl *renewalNdsctl, macAddress string) int {
	t.Helper()

	return ndsctl.count(t, "DEAUTH "+macAddress)
}

// closeGateCleanup returns the addressed gate to a closed state when the test
// ends, so a gate this file opens cannot become the next test's stale binding.
func closeGateCleanup(t *testing.T, macAddress string) {
	t.Helper()

	t.Cleanup(func() {
		if err := valve.CloseGate(macAddress); err != nil {
			t.Logf("cleanup: could not close the gate of %s: %v", macAddress, err)
		}
	})
}

func sweep(t *testing.T, m *Merchant, times int) {
	t.Helper()

	for i := 0; i < times; i++ {
		m.checkDataUsage()
	}
}

// TestStaleBindingIsReconciledWhenTheClientIsGone is the rotation case, and the
// one a per-MAC meter can never fix: the address is still authorised, the
// counters are still readable (they are simply frozen at the last values the
// departed client left), and the allotment is nowhere near reached. Nothing that
// measures traffic can end this session — only the client's absence can.
//
// On a real router both answers come from the same command (`ndsctl json <mac>`);
// the probe is stubbed here so the contract this pins is unambiguous — the
// reconciliation's decision comes from whether the DEVICE is still there, never
// from whether its counters happen to be readable. (If NoDogSplash keeps listing
// a departed address, the module cannot tell it from an idle customer and
// deliberately leaves it alone; that residue is the session-ticket work, and it
// is stated as the limit in docs/operator-guide.md and in the PR body.)
func TestStaleBindingIsReconciledWhenTheClientIsGone(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")
	closeGateCleanup(t, janitorBytesMAC)

	installBytesSession(t, m, janitorBytesMAC, 1<<40)
	if err := valve.SetDataBaseline(janitorBytesMAC); err != nil {
		t.Fatalf("SetDataBaseline: %v", err)
	}
	// The counters of the address the client left: readable, frozen, far below
	// the allotment.
	ndsctl.setClientKB(t, 1024, 512)

	stubClientProbe(t, m, clientIsGone)

	deauthsBefore := deauthsFor(t, ndsctl, janitorBytesMAC)
	sweep(t, m, staleBindingSweeps)

	if got := deauthsFor(t, ndsctl, janitorBytesMAC) - deauthsBefore; got == 0 {
		t.Fatal("the address of a client that is no longer on the network is still authorised: the gate stays open for ever on the default bytes metric, and any later holder of that address inherits it")
	}

	if hasSession(m, janitorBytesMAC) {
		t.Fatal("the session of a client that is gone was left tracked: /session-state keeps answering active for an address no device holds")
	}

	state, err := m.GetSessionState(janitorBytesMAC)
	if err != nil {
		t.Fatalf("GetSessionState: %v", err)
	}
	if state != SessionStateExpired {
		t.Fatalf("session state after the binding was reconciled = %q, want %q", state, SessionStateExpired)
	}

	if valve.HasDataBaseline(janitorBytesMAC) {
		t.Fatal("the metering baseline of the abandoned address survived the reconciliation: the NEXT holder of that address would inherit the departed client's accounting instead of starting clean")
	}
}

// TestStaleBindingIsReconciledEvenWithoutASessionRecord covers the other half of
// the same leak: a gate the module still holds for an address it has no session
// record for. No monitor looks at those at all today, so the address stays
// authorised until the process ends.
func TestStaleBindingIsReconciledEvenWithoutASessionRecord(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")
	closeGateCleanup(t, janitorOrphanMAC)

	if err := valve.CloseGate(janitorOrphanMAC); err != nil {
		t.Fatalf("CloseGate (precondition): %v", err)
	}
	if err := valve.OpenGate(janitorOrphanMAC); err != nil {
		t.Fatalf("OpenGate (precondition): %v", err)
	}

	stubClientProbe(t, m, clientIsGone)

	deauthsBefore := deauthsFor(t, ndsctl, janitorOrphanMAC)
	sweep(t, m, staleBindingSweeps)

	if got := deauthsFor(t, ndsctl, janitorOrphanMAC) - deauthsBefore; got == 0 {
		t.Fatal("a gate held for an address with no session record was never closed: nothing tracks it, so it stays authorised for the life of the process")
	}
}

// TestStaleBindingSurvivesAnUnconfirmedClose is the C1-2 rule applied to the
// reconciliation: the record that says "this address must be closed" is deleted
// only once ndsctl confirms the close. A failed deauth leaves the client
// authorised, so retiring the record on that branch would be the free gate all
// over again.
func TestStaleBindingSurvivesAnUnconfirmedClose(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")
	closeGateCleanup(t, janitorBytesMAC)

	installBytesSession(t, m, janitorBytesMAC, 1<<40)
	if err := valve.SetDataBaseline(janitorBytesMAC); err != nil {
		t.Fatalf("SetDataBaseline: %v", err)
	}
	ndsctl.setClientKB(t, 1024, 512)
	stubClientProbe(t, m, clientIsGone)

	deauthsBefore := deauthsFor(t, ndsctl, janitorBytesMAC)
	ndsctl.failDeauth(t, true)
	sweep(t, m, staleBindingSweeps)

	if got := deauthsFor(t, ndsctl, janitorBytesMAC) - deauthsBefore; got == 0 {
		t.Fatal("the reconciliation never attempted to close the abandoned address")
	}
	if !hasSession(m, janitorBytesMAC) {
		t.Fatal("the session was retired although the gate close was NOT confirmed: the address is still authorised and the record that says it must be closed is gone")
	}

	// ndsctl recovers: the next reconciliation must close the gate and retire
	// the record only now.
	ndsctl.failDeauth(t, false)
	sweep(t, m, staleBindingSweeps)

	if hasSession(m, janitorBytesMAC) {
		t.Fatal("the session of a departed client was never retired after the gate close finally succeeded")
	}
}
