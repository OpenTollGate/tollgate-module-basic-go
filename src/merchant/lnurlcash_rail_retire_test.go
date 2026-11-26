package merchant

import (
	"fmt"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/lnurlcash"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/paymentjournal"
)

// WHAT Settle IS AND IS NOT.
//
// It IS: the record stops claiming to be `awaiting` after the money arrived, so the operator
// report and `tollgate lnurlcash` stop flagging sales that are perfectly fine. Before it,
// every sale a gate ever made was reported as "the melt was attempted and the outcome is
// unknown", which is a lie that would send someone chasing nothing.
//
// It IS NOT: a fix for the journal's hard cap. The store refuses Prepare when
// len(s.data.Operations) >= maxRecords, counting EVERY record whatever its state, so moving a
// record to `paid` renames it and frees nothing. TestSettledSalesDoNotConsumeJournalCapacity
// measures that: the rail still refuses the 1025th sale.
//
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

// THE JOURNAL CAP IS A REAL OPERATING LIMIT, AND THIS PINS IT.
//
// A busy gate stops accepting LNURLcash after maxRecords sales and stays stopped. That is
// recorded in the ADR with the measurement and in the PR body as operator guidance; this test
// is what stops the two of them drifting apart.
//
// It is a CHARACTERISATION test: it is green while the limitation exists and goes red the
// moment it is fixed, which is exactly when someone must update the ADR, the PR body and the
// operator guidance. A test that asserted the fix we do not have would just be red forever and
// would train everyone to ignore it.
//
// TestSettledSalesDoNotConsumeJournalCapacity is the name of the property we WANT; the
// assertion below is the property we HAVE.
func TestSettledSalesDoNotConsumeJournalCapacity(t *testing.T) {
	if testing.Short() {
		t.Skip("fills the journal to its cap; slow by construction")
	}
	store := newCrashTestStore(t)

	calls := 0
	rail := NewLNURLCashRail(
		store,
		0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			invoice := fmt.Sprintf("lnbc1invoice-%d", calls)
			calls++
			return fmt.Sprintf("quote-%d", calls), invoice, nil
		},
		func(n lnurlcash.Note, cb IssuerCallback, invoice string) error { return nil },
	)

	// Pay and settle until the rail refuses, or until we are well past the cap.
	const limit = 1100
	var refusedAt = -1
	for i := 0; i < limit; i++ {
		note, perr := lnurlcash.Parse(fmt.Sprintf("%064x", i+1), "mint.example")
		if perr != nil {
			t.Fatalf("parse %d: %v", i, perr)
		}
		if _, rerr := rail.Redeem(note, 1000, "00:00:00:00:00:01", "https://mint.example"); rerr != nil {
			refusedAt = i
			break
		}
		rail.Settle(fmt.Sprintf("lnbc1invoice-%d", i))
	}

	if refusedAt < 0 {
		t.Fatalf("the rail accepted %d settled sales without refusing one — the journal cap this ADR documents no longer bites. That is a FIX, not a failure: update the ADR's \"Known limitation\" section, the PR body's operator guidance, and this test.", limit)
	}
	if refusedAt != 1024 {
		t.Fatalf("the rail refused sale %d, not 1024 — the documented limit moved. Update the ADR and the PR body to the measured number, and check whether retention changed.", refusedAt)
	}
	t.Logf("confirmed: the rail refuses sale %d, as the ADR states; one settled sale still consumes one journal slot", refusedAt)
}
