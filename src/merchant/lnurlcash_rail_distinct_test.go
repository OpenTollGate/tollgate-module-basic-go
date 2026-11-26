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
// This measures that directly rather than arguing about it, and IT PASSES: the journal
// fills. The test is therefore a RECORD OF A KNOWN LIMITATION, not a proof of a fix. It
// exists so the limitation cannot be quietly forgotten, and so anyone who claims the
// exhaustion hole is closed has to argue with a measurement.
//
// What the two fixes at 173f28de actually bought:
//   - an attacker can no longer use an unaccepted mint (the endpoint refuses it before any
//     journal write), so the failure must come from an ACCEPTED mint that refuses invoices;
//   - one note can no longer be replayed without bound (8 attempts).
//
// Neither bounds DISTINCT notes. The journal cap is still reachable, and nothing prunes.
//
// The honest fix is a pruner or a retention policy for Failed records, which is a change to
// shared payment-journal behaviour and does not belong in this PR. See the PR body.
func TestDistinctNotesCanStillFillTheJournal(t *testing.T) {
	if testing.Short() {
		t.Skip("fills a 1024-record journal; slow by construction")
	}
	const cap = 1024 // the store's maxRecords
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

	// Each iteration: a NOTE THAT HAS NEVER BEEN PRESENTED, failing once. This is the
	// cheapest possible record an attacker can buy, and the per-note cap does not apply
	// because every note is new.
	records := 0
	for i := 0; i < cap+8; i++ {
		spend := fmt.Sprintf("%064x", i)
		note, err := lnurlcash.Parse(spend, "mint.example")
		if err != nil {
			continue // not a valid note; a real attacker would use valid ones
		}
		if _, err := rail.Redeem(note, 1000, "00:00:00:00:00:01", "https://mint.example"); err == nil {
			t.Fatalf("expected the invoice step to fail")
		}
		ops, lerr := store.List()
		if lerr != nil {
			t.Fatalf("list: %v", lerr)
		}
		n := 0
		for _, op := range ops {
			if op.Direction == paymentjournal.Incoming {
				n++
			}
		}
		records = n

	}
	if records < cap {
		t.Fatalf("only %d records accumulated from %d distinct notes", records, cap+8)
	}

	// The journal is now AT its cap. Ask it to accept one more record, the way an honest
	// customer's redemption would — this is the harm, measured directly rather than
	// inferred from an error string.
	_, perr := store.Prepare(paymentjournal.Operation{
		ID:         "honest-customer-attempt",
		WalletID:   "mint-key",
		Direction:  paymentjournal.Incoming,
		AmountMSat: 21000,
		Recipient:  "issuer.example",
		Request:    []byte("cp1"),
	}, paymentjournal.Budget{})
	if perr == nil {
		t.Fatal("the journal accepted another record at its cap; the cap does not bind")
	}
	t.Logf("FINDING: %d distinct notes filled the journal to %d records; an honest redemption is now refused (%v). The per-note retry bound does NOT bound distinct notes.", cap, records, perr)
}
