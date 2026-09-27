package merchant

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
)

// This file pins the machinery of the stale-binding reconciliation: when it
// runs, what it refuses to do, and what the address it reconciles can no longer
// hand to whoever holds it next.
//
// The companion file (stale_binding_janitor_test.go) drives the production
// cadence; here every test shrinks the cadence and the grace window so a
// scenario is a handful of sweeps instead of a minute of them.
//
// Every address and every counter here is per-MAC on purpose: the valve's gate
// state and the fake ndsctl's log are shared by the whole test binary.

const janitorMillisMAC = "aa:bb:cc:dd:ee:02"

// shortReconcileCadence runs the reconciliation on every sweep with a two-pass
// grace window.
func shortReconcileCadence(t *testing.T, m *Merchant) {
	t.Helper()

	m.tuneStaleBindingReconciliation(1, 2)
}

// probeSpy counts how often the reconciliation asked about ONE address.
type probeSpy struct {
	macAddress string
	calls      int32
}

func (s *probeSpy) stub(t *testing.T, m *Merchant, probe func(string) (valve.ClientState, error)) {
	t.Helper()

	stubClientProbe(t, m, func(macAddress string) (valve.ClientState, error) {
		if macAddress == s.macAddress {
			atomic.AddInt32(&s.calls, 1)
		}
		return probe(macAddress)
	})
}

func (s *probeSpy) count() int { return int(atomic.LoadInt32(&s.calls)) }

// TestStaleBindingReconcileProbesOnItsOwnCadenceNotOnEverySweep keeps the
// reconciliation from turning the 2 s meter into a probe of every session on
// every sweep: the cadence is what bounds the extra ndsctl work.
func TestStaleBindingReconcileProbesOnItsOwnCadenceNotOnEverySweep(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")
	closeGateCleanup(t, janitorBytesMAC)

	installBytesSession(t, m, janitorBytesMAC, 1<<40)
	ndsctl.setClientKB(t, 1024, 512)

	m.tuneStaleBindingReconciliation(5, 100) // never act; this test is about the cadence

	spy := &probeSpy{macAddress: janitorBytesMAC}
	spy.stub(t, m, clientIsLive)

	sweep(t, m, 20)

	if got, want := spy.count(), 4; got != want {
		t.Fatalf("NDS identity probes of %s over 20 sweeps at a 5-sweep cadence = %d, want %d (the reconciliation must not probe on every sweep)",
			janitorBytesMAC, got, want)
	}
}

// TestStaleBindingReconcileLeavesALiveClientAlone is the negative control the
// design asks for: a client that is still listed must keep its session and its
// gate, however idle it is — otherwise the reconciliation becomes the thing that
// takes internet away from a paying customer.
func TestStaleBindingReconcileLeavesALiveClientAlone(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")
	shortReconcileCadence(t, m)
	closeGateCleanup(t, janitorBytesMAC)

	installBytesSession(t, m, janitorBytesMAC, 1<<40)
	if err := valve.OpenGate(janitorBytesMAC); err != nil {
		t.Fatalf("OpenGate: %v", err)
	}
	ndsctl.setClientKB(t, 1024, 512)

	stubClientProbe(t, m, clientIsLive)

	sweep(t, m, 10)

	if got := deauthsFor(t, ndsctl, janitorBytesMAC); got != 0 {
		t.Fatalf("a live client was deauthorised %d time(s): the reconciliation closed the gate of a customer that is still on the network", got)
	}
	if !hasSession(m, janitorBytesMAC) {
		t.Fatal("a live client lost its session")
	}
	if !trackedGate(janitorBytesMAC) {
		t.Fatal("a live client's gate stopped being tracked")
	}
}

// TestStaleBindingReconcileIgnoresAProbeItCannotRead: an unreadable probe is not
// evidence that a customer left, and it must not count towards the grace window
// either — otherwise a flaky ndsctl would eventually close a paying customer's
// gate without a single successful "gone" observation.
func TestStaleBindingReconcileIgnoresAProbeItCannotRead(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")
	shortReconcileCadence(t, m)
	closeGateCleanup(t, janitorBytesMAC)

	installBytesSession(t, m, janitorBytesMAC, 1<<40)
	if err := valve.OpenGate(janitorBytesMAC); err != nil {
		t.Fatalf("OpenGate: %v", err)
	}
	ndsctl.setClientKB(t, 1024, 512)

	// Ten passes that cannot read the probe: no absence is observed.
	stubClientProbe(t, m, func(string) (valve.ClientState, error) {
		return valve.ClientState{}, errors.New("ndsctl: connection refused")
	})
	sweep(t, m, 10)

	if !hasSession(m, janitorBytesMAC) {
		t.Fatal("the session was dropped by a probe the module could not read")
	}
	if got := deauthsFor(t, ndsctl, janitorBytesMAC); got != 0 {
		t.Fatalf("ndsctl deauths of %s = %d, want 0: an unreadable probe caused a close", janitorBytesMAC, got)
	}

	// The client is now genuinely gone: the grace window starts here, so it must
	// take a full window from now — not close on the next pass.
	stubClientProbe(t, m, clientIsGone)
	sweep(t, m, 1)
	if !hasSession(m, janitorBytesMAC) {
		t.Fatal("the grace window counted the unreadable passes: the binding was closed after a single genuine absence observation")
	}
	sweep(t, m, 1)
	if hasSession(m, janitorBytesMAC) {
		t.Fatal("the session of a client that is gone was never retired after a full grace window of absences")
	}
}

// TestStaleBindingReconcileDoesNotCutAMillisecondsSessionShort: a paid duration
// has its own timer, so its gate is bounded — and a customer who closed a laptop
// lid must not lose the time they paid for. Milliseconds sessions are excluded
// from the reconciliation on purpose.
func TestStaleBindingReconcileDoesNotCutAMillisecondsSessionShort(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "milliseconds")
	shortReconcileCadence(t, m)
	closeGateCleanup(t, janitorMillisMAC)

	m.sessionMu.Lock()
	m.customerSessions[janitorMillisMAC] = &CustomerSession{
		MacAddress: janitorMillisMAC,
		StartTime:  time.Now().Unix(),
		Metric:     "milliseconds",
		Allotment:  renewalFreshAllotment,
	}
	m.sessionMu.Unlock()

	if err := valve.OpenGateUntil(janitorMillisMAC, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatalf("OpenGateUntil: %v", err)
	}

	stubClientProbe(t, m, clientIsGone)

	sweep(t, m, 10)

	if got := deauthsFor(t, ndsctl, janitorMillisMAC); got != 0 {
		t.Fatalf("ndsctl deauths of %s = %d, want 0: a paid duration was cut short because its device went quiet", janitorMillisMAC, got)
	}
	if !hasSession(m, janitorMillisMAC) {
		t.Fatal("a milliseconds session was retired by the reconciliation")
	}
}

// TestStaleBindingReconcileResetsTheGraceWindowWhenTheClientComesBack: the window
// counts CONSECUTIVE absences, so a device that roams out and back (or one whose
// NDS record blinks) never accumulates its way to a close.
func TestStaleBindingReconcileResetsTheGraceWindowWhenTheClientComesBack(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")
	shortReconcileCadence(t, m)
	closeGateCleanup(t, janitorBytesMAC)

	installBytesSession(t, m, janitorBytesMAC, 1<<40)
	if err := valve.OpenGate(janitorBytesMAC); err != nil {
		t.Fatalf("OpenGate: %v", err)
	}
	ndsctl.setClientKB(t, 1024, 512)

	// A scripted probe for the address under test, and a "live" answer for
	// anything else. The probe seam is package-global, so an answer must never
	// depend on state another goroutine — a monitor goroutine a previous test
	// leaked — could touch: the script is keyed to one MAC and its cursor is
	// atomic.
	sweepProbes := []func(string) (valve.ClientState, error){
		clientIsGone, // sweep 1: absence 1
		clientIsLive, // sweep 2: the client is back → the window resets
		clientIsGone, // sweep 3: absence 1 again
	}
	var pass int32
	stubClientProbe(t, m, func(macAddress string) (valve.ClientState, error) {
		if macAddress != janitorBytesMAC {
			return clientIsLive(macAddress)
		}
		next := int(atomic.AddInt32(&pass, 1)) - 1
		if next >= len(sweepProbes) {
			return clientIsLive(macAddress)
		}
		return sweepProbes[next](macAddress)
	})

	sweep(t, m, 3)

	if got := deauthsFor(t, ndsctl, janitorBytesMAC); got != 0 {
		t.Fatalf("ndsctl deauths of %s = %d, want 0: the grace window did not reset when the client came back", janitorBytesMAC, got)
	}
	if !hasSession(m, janitorBytesMAC) {
		t.Fatal("the session was retired even though the client came back inside the grace window")
	}
}

// TestStaleBindingReconcileRetiresTheRecordSoTheNextHolderStartsWithNothing is
// the "rebind" half of the contract: once the abandoned binding is reconciled,
// the record, its meter and its gate are gone — so a device that later holds
// that address (a hardware-MAC fallback, a spoof, a collision, or the same
// customer who happens to be assigned it again) starts from nothing and has to
// buy its own access, and the remainder the departed customer paid for is not
// silently transferred to it.
func TestStaleBindingReconcileRetiresTheRecordSoTheNextHolderStartsWithNothing(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")
	shortReconcileCadence(t, m)
	closeGateCleanup(t, janitorBytesMAC)

	const departedAllotment = 1 << 40
	installBytesSession(t, m, janitorBytesMAC, departedAllotment)
	if err := valve.SetDataBaseline(janitorBytesMAC); err != nil {
		t.Fatalf("SetDataBaseline: %v", err)
	}
	ndsctl.setClientKB(t, 1024, 512)

	stubClientProbe(t, m, clientIsGone)
	sweep(t, m, 2)

	if hasSession(m, janitorBytesMAC) {
		t.Fatal("precondition: the abandoned binding was not reconciled")
	}
	if valve.HasDataBaseline(janitorBytesMAC) {
		t.Fatal("the abandoned address kept its metering baseline: the next holder of it would inherit the departed client's accounting")
	}
	if trackedGate(janitorBytesMAC) {
		t.Fatal("the abandoned address is still authorised: the next holder of it inherits an open gate")
	}

	// The next holder of the address buys their own access.
	stubClientProbe(t, m, clientIsLive)
	if _, err := m.PurchaseSession("cashuBnext", janitorBytesMAC); err != nil {
		t.Fatalf("the next holder of the address could not buy access: %v", err)
	}

	session, err := m.GetSession(janitorBytesMAC)
	if err != nil {
		t.Fatalf("GetSession after the next holder bought access: %v", err)
	}
	if session.Allotment == departedAllotment {
		t.Fatal("the departed client's allotment was resurrected for the next holder of the address")
	}
	if session.Allotment == 0 {
		t.Fatal("the next holder of the address bought a session with no allotment")
	}
	if session.StartTime < time.Now().Add(-time.Minute).Unix() {
		t.Fatalf("the next holder's session reuses a start time from the past (%d): the departed client's clock was inherited", session.StartTime)
	}
}

// trackedGate reports whether the module still holds a gate for macAddress.
func trackedGate(macAddress string) bool {
	for _, tracked := range valve.TrackedGates() {
		if tracked == macAddress {
			return true
		}
	}
	return false
}

// TestTrackedGatesReportsWhatTheModuleHolds is the read-only accessor the
// reconciliation is built on: it must list the gates the module tracks, sorted,
// and it must not invent one.
func TestTrackedGatesReportsWhatTheModuleHolds(t *testing.T) {
	installRenewalNdsctl(t)
	m, _ := newRenewalMerchant(t, "bytes")
	closeGateCleanup(t, janitorOrphanMAC)
	closeGateCleanup(t, janitorBytesMAC)

	if err := valve.CloseGate(janitorBytesMAC); err != nil {
		t.Fatalf("CloseGate (precondition): %v", err)
	}
	if err := valve.CloseGate(janitorOrphanMAC); err != nil {
		t.Fatalf("CloseGate (precondition): %v", err)
	}

	for _, tracked := range valve.TrackedGates() {
		if tracked == janitorBytesMAC || tracked == janitorOrphanMAC {
			t.Fatalf("TrackedGates lists %s before its gate was opened: %v", tracked, valve.TrackedGates())
		}
	}

	if err := valve.OpenGate(janitorOrphanMAC); err != nil {
		t.Fatalf("OpenGate: %v", err)
	}
	if err := valve.OpenGate(janitorBytesMAC); err != nil {
		t.Fatalf("OpenGate: %v", err)
	}

	tracked := valve.TrackedGates()
	if !contains(tracked, janitorOrphanMAC) || !contains(tracked, janitorBytesMAC) {
		t.Fatalf("TrackedGates = %v, want it to contain %s and %s", tracked, janitorOrphanMAC, janitorBytesMAC)
	}
	for i := 1; i < len(tracked); i++ {
		if tracked[i-1] > tracked[i] {
			t.Fatalf("TrackedGates is not sorted: %v", tracked)
		}
	}

	if !m.staleBindingCandidatesContain(janitorBytesMAC) {
		t.Fatal("a tracked gate was not a candidate for reconciliation")
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (m *Merchant) staleBindingCandidatesContain(macAddress string) bool {
	return contains(m.staleBindingCandidates(), macAddress)
}
