package valve

import (
	"reflect"
	"testing"
)

// TrackedGates is the read path into the module's own gate bookkeeping. It
// exists because the merchant's stale-binding reconciliation has to find the
// gates nothing else looks at, so its contract is small but load-bearing: it
// reports exactly the gates the module still believes it holds (including the
// ones whose close is NOT confirmed — a failed deauth is not a close, C1-2), it
// is sorted so a diff of two reports is meaningful, and it hands out a copy
// rather than the module's own state.
func TestTrackedGatesReportsHeldGatesSortedAndAsACopy(t *testing.T) {
	ndsctl := setUpGateTest(t, 1<<30) // every deauth fails: a failed close must stay visible
	const (
		lowerMAC  = "aa:bb:cc:dd:ee:41"
		higherMAC = "aa:bb:cc:dd:ee:42"
	)

	if err := OpenGate(higherMAC); err != nil {
		t.Fatalf("OpenGate(%s): %v", higherMAC, err)
	}
	if err := OpenGate(lowerMAC); err != nil {
		t.Fatalf("OpenGate(%s): %v", lowerMAC, err)
	}

	got := TrackedGates()
	want := []string{lowerMAC, higherMAC}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TrackedGates() = %v, want %v (the gates the module holds, sorted)", got, want)
	}

	// The caller gets its own slice: a report must never be a handle on the
	// module's state that a caller can edit.
	got[0] = "00:00:00:00:00:00"
	if !gateTracked(lowerMAC) {
		t.Fatal("TrackedGates handed out the module's own bookkeeping: writing to the returned slice changed which gates are tracked")
	}

	// The case the reconciliation exists for: a close that failed leaves the
	// gate AUTHORISED, so it must still be reported — this is the gate that
	// otherwise stays open with nothing owning it.
	if err := CloseGate(lowerMAC); err == nil {
		t.Fatal("CloseGate returned nil although every deauth fails")
	}
	if !trackedGateIn(TrackedGates(), lowerMAC) {
		t.Fatal("a gate whose close was NOT confirmed is no longer reported: it is still authorised and nothing owns it any more")
	}

	// ndsctl recovers: a CONFIRMED close drops the gate from the report, so a
	// later pass of the reconciliation does not keep probing an address that is
	// no longer held.
	ndsctl.allowDeauth()
	if err := CloseGate(lowerMAC); err != nil {
		t.Fatalf("CloseGate after ndsctl recovered: %v", err)
	}
	if trackedGateIn(TrackedGates(), lowerMAC) {
		t.Fatal("a confirmed-closed gate is still reported as held")
	}
}

// trackedGateIn reports whether macAddress appears in a TrackedGates report.
func trackedGateIn(report []string, macAddress string) bool {
	for _, entry := range report {
		if entry == macAddress {
			return true
		}
	}
	return false
}
