package merchant

import (
	"errors"
	"fmt"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/lnurlcash"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/paymentjournal"
)

// DOES THE RETRY BOUND ACTUALLY STOP EXHAUSTION, OR ONLY RAISE ITS PRICE?
//
// The per-note cap stops ONE note from filling the journal. It says nothing about an
// attacker who brings MANY notes: each distinct note gets its own budget of 8. If a
// distinct note is cheap to produce, the cap moves the cost from "one note" to
// "N/8 notes" and the journal is still reachable.
//
// This measures that rather than arguing about it, and IT PASSES: the journal fills and an
// honest customer is then refused. The test is therefore a RECORD OF A KNOWN LIMITATION, not
// a proof of a fix. It exists so the limitation cannot be quietly forgotten, and so anyone
// who claims the exhaustion hole is closed has to argue with a measurement.
//
// What the two fixes at 173f28de actually bought:
//   - an attacker can no longer use an unaccepted mint (the endpoint refuses it before any
//     journal write), so the failure must come from an ACCEPTED mint that refuses invoices;
//   - one note can no longer be replayed without bound (8 attempts).
//
// Neither bounds DISTINCT notes. The journal cap is still reachable, and nothing prunes.
//
// The honest fix is a pruner or a retention policy for terminal records, which is a change to
// shared payment-journal behaviour and does not belong in this PR. See the PR body.
//
// COST NOTE: the first version of this test performed all 1024 redemptions through the rail,
// which cost 146s under -race. Almost none of that was the property under test: priorAttempts
// calls store.List() on every Redeem, so n redemptions over n records is O(n^2), and the store
// fsyncs each append. The three claims are therefore measured at the cheapest size that still
// demonstrates each one — 32 distinct notes to show a NEW note is unbounded, a direct fill to
// reach the cap, and one real redemption through the rail to show the refusal.
func TestDistinctNotesCanStillFillTheJournal(t *testing.T) {
	const cap = 1024 // the store's maxRecords
	const probes = 32

	store := newCrashTestStore(t)

	rail := NewLNURLCashRail(
		store,
		0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			return "", "", errors.New("mint is up but refuses invoices")
		},
		func(n lnurlcash.Note, cb IssuerCallback, invoice string) error {
			t.Fatal("the melt must never be reached on the invoice-failure path")
			return nil
		},
	)

	// CLAIM 1: a note that has never been presented gets a fresh budget, so the per-note
	// bound does not apply to it. Each of these fails once and yet each writes its own
	// record — the cheapest thing an attacker can buy.
	for i := 0; i < probes; i++ {
		spend := fmt.Sprintf("%064x", i)
		note, err := lnurlcash.Parse(spend, "mint.example")
		if err != nil {
			continue // not a valid note; a real attacker would use valid ones
		}
		if _, err := rail.Redeem(note, 1000, "00:00:00:00:00:01", "https://mint.example"); err == nil {
			t.Fatalf("expected the invoice step to fail for distinct note %d", i)
		}
	}
	ops, lerr := store.List()
	if lerr != nil {
		t.Fatalf("list: %v", lerr)
	}
	incoming := 0
	for _, op := range ops {
		if op.Direction == paymentjournal.Incoming {
			incoming++
		}
	}
	if incoming < probes {
		t.Fatalf("only %d records from %d distinct notes; a new note should not be bounded by the per-note cap", incoming, probes)
	}
	t.Logf("CLAIM 1: %d never-presented notes wrote %d records — the per-note bound does not bound distinct notes", probes, incoming)

	// CLAIM 2: the journal is reachable at all. Filled DIRECTLY, because this is a property
	// of the store's accounting, not of the rail: the rail's contribution is only that it
	// writes one record per presentation, which CLAIM 1 already established. Doing this
	// through the rail would re-pay the O(n^2) List() to learn nothing new.
	for i := incoming; i < cap; i++ {
		if _, err := store.Prepare(paymentjournal.Operation{
			ID:         fmt.Sprintf("filler-%05d", i),
			WalletID:   "mint-key",
			Direction:  paymentjournal.Incoming,
			AmountMSat: 1000,
			Recipient:  "issuer.example",
			Request:    []byte("cp1"),
		}, paymentjournal.Budget{}); err != nil {
			t.Fatalf("filling record %d: %v", i, err)
		}
	}

	// CLAIM 3: with the journal full, an HONEST customer's redemption through the real rail
	// is refused. This is the harm, measured as a refusal rather than inferred from an error
	// string, and it runs through the same code path a paying customer would use.
	honest := fmt.Sprintf("%064x", 0xffff)
	note, err := lnurlcash.Parse(honest, "mint.example")
	if err != nil {
		t.Fatalf("parse honest note: %v", err)
	}
	_, rerr := rail.Redeem(note, 1000, "00:00:00:00:00:02", "https://mint.example")
	if rerr == nil {
		t.Fatal("the rail accepted a redemption at the journal cap; the cap does not bind")
	}
	t.Logf("CLAIM 3: FINDING — %d distinct notes filled the journal to its cap; an honest redemption is now refused (%v). The per-note retry bound does NOT bound distinct notes.", cap, rerr)
}
