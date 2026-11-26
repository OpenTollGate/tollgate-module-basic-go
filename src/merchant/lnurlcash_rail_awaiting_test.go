package merchant

import (
	"fmt"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/lnurlcash"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/paymentjournal"
)

// DOES THE `awaiting` STATE ACCUMULATE WITHOUT BOUND?
//
// Review r2 claimed the awaiting happy path is unbounded "and needs no attacker at all". That
// was true when it was written: nothing advanced a settled record. Settle now does. So the
// question is whether a sale that COMPLETES leaves an `awaiting` record behind, or a `paid` one.
//
// It matters because `awaiting` is the state that makes the CLI flag a record for a human and
// that blocks the note from being retried; a gate that accumulates them is a gate whose operator
// report fills with false alarms. Capacity is a separate, unfixed question — see
// TestSettledSalesDoNotConsumeJournalCapacity.
func TestASettledSaleLeavesNoAwaitingRecord(t *testing.T) {
	store := newCrashTestStore(t)
	n := 0
	rail := NewLNURLCashRail(
		store, 0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			inv := fmt.Sprintf("lnbc1inv%d", n)
			n++
			return fmt.Sprintf("q%d", n), inv, nil
		},
		func(note lnurlcash.Note, cb IssuerCallback, invoice string) error { return nil },
	)

	const sales = 25
	for i := 0; i < sales; i++ {
		note, _ := lnurlcash.Parse(fmt.Sprintf("%064x", i+1), "mint.example")
		if _, err := rail.Redeem(note, 1000, "00:00:00:00:00:01", "https://mint.example"); err != nil {
			t.Fatalf("sale %d: %v", i, err)
		}
		rail.Settle(fmt.Sprintf("lnbc1inv%d", i))
	}

	ops, err := store.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var awaiting, paid int
	for _, op := range ops {
		switch op.State {
		case paymentjournal.Awaiting:
			awaiting++
		case paymentjournal.Paid:
			paid++
		}
	}
	if awaiting != 0 {
		t.Fatalf("%d of %d completed sales are still `awaiting`; the operator report would flag them as \"outcome unknown\" forever, which is the exact false alarm Settle exists to remove", awaiting, sales)
	}
	if paid != sales {
		t.Fatalf("%d paid records for %d sales", paid, sales)
	}
}
