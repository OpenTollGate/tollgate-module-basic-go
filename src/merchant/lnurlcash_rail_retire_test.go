package merchant

import (
	"fmt"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/lnurlcash"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/paymentjournal"
)

// A SALE MUST RETIRE ITS JOURNAL RECORD, OR THE RAIL HAS A HARD LIFETIME.
//
// Redeem binds the note to our invoice, which sets the record to `awaiting` (BindInvoice),
// and then the melt succeeds. The quote monitor is the only thing that learns the money
// arrived, and before Settle it never told the rail: the grant path and the journal were
// never joined. Every HONEST redemption therefore leaked one permanent slot, and at the
// store's 1024-record cap Prepare refuses for everyone — no attacker, no malformed note, no
// ambiguity, just 1024 people paying.
func TestSettlingASaleRetiresItsRecord(t *testing.T) {
	store := newCrashTestStore(t)
	const invoice = "lnbc10u1pexample"

	rail := NewLNURLCashRail(
		store,
		0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			return "quote-retire", invoice, nil
		},
		func(n lnurlcash.Note, cb IssuerCallback, inv string) error { return nil },
	)

	note, err := lnurlcash.Parse("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", "mint.example")
	if err != nil {
		t.Fatalf("parse note: %v", err)
	}
	if _, err := rail.Redeem(note, 21000, "00:00:00:00:00:01", "https://mint.example"); err != nil {
		t.Fatalf("redeem: %v", err)
	}

	stateOf := func() paymentjournal.State {
		t.Helper()
		ops, lerr := store.List()
		if lerr != nil {
			t.Fatalf("list: %v", lerr)
		}
		for _, op := range ops {
			if op.Direction == paymentjournal.Incoming {
				return op.State
			}
		}
		t.Fatal("no record found")
		return ""
	}

	// Immediately after the melt the record MUST still be awaiting: the money has not
	// arrived yet, and releasing the note before settlement would let it be re-presented.
	if got := stateOf(); got != paymentjournal.Awaiting {
		t.Fatalf("after the melt the record is %q, want awaiting — the note must stay reserved until the quote settles", got)
	}

	// The monitor has now seen the quote paid and granted access. That is the signal.
	rail.Settle(invoice)

	if got := stateOf(); got != paymentjournal.Paid {
		t.Fatalf("after settlement the record is %q, want paid — the slot is leaked and the rail will die at 1024 sales", got)
	}

	// A second settle (the monitor can fire twice across a restart) must be harmless: it
	// must not resurrect the record or fail.
	rail.Settle(invoice)
	if got := stateOf(); got != paymentjournal.Paid {
		t.Fatalf("a repeated settlement changed the record to %q", got)
	}

	// An invoice this rail never journalled must touch nothing.
	rail.Settle("lnbc1someotherinvoice")
	if got := stateOf(); got != paymentjournal.Paid {
		t.Fatalf("settling an unknown invoice disturbed our record: %q", got)
	}

	// The note must still be refused: settling means the operator was paid, NOT that the
	// note may be presented again.
	if _, err := rail.Redeem(note, 21000, "00:00:00:00:00:01", "https://mint.example"); err != ErrLNURLCashNoteAlreadyRedeemed {
		t.Fatalf("a note whose sale settled was accepted again (%v) — retiring a record must not release the note", err)
	}
}

// THE LIFETIME PROPERTY, not just one record: 12 sales, 12 settlements, 0 leaked slots.
func TestRepeatedSalesDoNotAccumulateJournalRecords(t *testing.T) {
	store := newCrashTestStore(t)
	const payments = 12

	// Each sale gets a DISTINCT invoice, as real quotes do. Reusing one string made the
	// first version of this test vacuous: every record matched the same invoice, so settling
	// retired one record and left eleven, and the failure looked like a code bug rather than
	// a fixture that could not distinguish the sales it was pretending to model.
	// A counter, not a value derived from the amount: every sale here is for the same amount,
	// so `sats` cannot distinguish them.
	invoiceFor := func(i int) string { return fmt.Sprintf("lnbc1invoice-%d", i) }
	calls := 0
	rail := NewLNURLCashRail(
		store,
		0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			inv := invoiceFor(calls)
			calls++
			return fmt.Sprintf("quote-%d", calls), inv, nil
		},
		func(n lnurlcash.Note, cb IssuerCallback, inv string) error { return nil },
	)

	for i := 0; i < payments; i++ {
		note, perr := lnurlcash.Parse(fmt.Sprintf("%064x", i+1), "mint.example")
		if perr != nil {
			t.Fatalf("parse %d: %v", i, perr)
		}
		if _, rerr := rail.Redeem(note, 1000, "00:00:00:00:00:01", "https://mint.example"); rerr != nil {
			t.Fatalf("redeem %d: %v", i, rerr)
		}
		// The monitor settles each sale with the invoice the rail journalled.
		rail.Settle(invoiceFor(i))
	}

	ops, err := store.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	awaiting := 0
	for _, op := range ops {
		if op.Direction == paymentjournal.Incoming && op.State == paymentjournal.Awaiting {
			awaiting++
		}
	}
	if awaiting != 0 {
		t.Fatalf("%d settled sales left %d records still awaiting; each one is a slot the rail can never reclaim", payments, awaiting)
	}
}
