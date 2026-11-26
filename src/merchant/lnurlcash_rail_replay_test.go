package merchant

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/lnurlcash"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/paymentjournal"
)

// A CRASH BETWEEN Prepare AND BindInvoice MUST NOT FREE THE NOTE.
//
// The rail's replay guard has two halves: an in-memory set, and a durable
// journal lookup. Only the journal survives a restart, so the journal lookup is
// the whole safety property — the in-memory set is just a concurrency
// optimisation.
//
// BindInvoice is what writes the note's fingerprint into Operation.PaymentHash.
// Before that call the record exists but carries no PaymentHash, so a lookup
// that matches on PaymentHash alone cannot see it. If the process dies after
// Prepare and before BindInvoice — a power cut on a router is exactly this —
// the note is recorded as `prepared`, is invisible to a PaymentHash-only
// lookup, and can be presented again. The second presentation reaches the
// issuer, and the issuer burns the note the first attempt may already have
// burned. That is the double-melt this rail exists to prevent.
//
// priorAttempts therefore has a second clause that matches the derived id.
// These tests pin that clause, because it is the only thing standing between a
// power cut and a re-presented note, and because a refactor that "simplifies"
// the lookup down to PaymentHash would look entirely reasonable.

const crashNoteSpend = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

// crashTestNote parses the real LUD-25 bearer vector, so the tests exercise the
// same Note the rail would see in production rather than a constructed struct.
func crashTestNote(t *testing.T) lnurlcash.Note {
	t.Helper()
	n, err := lnurlcash.Parse(crashNoteSpend, "mint.example")
	if err != nil {
		t.Fatalf("parsing the bearer-note test vector: %v", err)
	}
	return n
}

func newCrashTestStore(t *testing.T) *paymentjournal.Store {
	t.Helper()
	// A nested directory, matching how the store's own tests open it: the store
	// syncs the PARENT of the journal directory on open, and a bare t.TempDir()
	// is already a fresh entry whose parent is shared and refuses the sync.
	store, err := paymentjournal.Open(filepath.Join(t.TempDir(), "journal"))
	if err != nil {
		t.Fatalf("opening the journal: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func crashTestRail(store *paymentjournal.Store) *LNURLCashRail {
	return NewLNURLCashRail(
		store,
		0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			return "quote-1", "lnbc10u1pexample", nil
		},
		func(note lnurlcash.Note, callback IssuerCallback, invoice string) error {
			return nil
		},
	)
}

// The crash window: prepared, never bound, process gone.
func TestPreparedButUnboundOperationStillBlocksReplay(t *testing.T) {
	store := newCrashTestStore(t)

	fingerprint := noteFingerprint(crashNoteSpend)
	opID := "lnurlcash-" + fingerprint[:32]

	// Simulate the exact state a power cut leaves: the intent is durable, the
	// invoice was never bound because the process died first.
	if _, err := store.Prepare(paymentjournal.Operation{
		ID:         opID,
		WalletID:   walletKeyForMint("https://mint.example"),
		Direction:  paymentjournal.Incoming,
		AmountMSat: 1000,
		Recipient:  "mint.example",
		Request:    []byte("cp1example"),
	}, paymentjournal.Budget{}); err != nil {
		t.Fatalf("preparing the interrupted operation: %v", err)
	}

	// A fresh rail object, as after a restart: the in-memory set is empty, so
	// only the journal can refuse.
	restarted := crashTestRail(store)
	prior, err := restarted.priorAttempts(fingerprint)
	if err != nil {
		t.Fatalf("priorAttempts: %v", err)
	}
	if len(prior) != 1 {
		t.Fatalf("an unbound operation must still be recognised as a prior attempt; got %d records", len(prior))
	}
	if prior[0].ID != opID {
		t.Fatalf("recognised the wrong record: %q", prior[0].ID)
	}
	if prior[0].State != paymentjournal.Prepared {
		t.Fatalf("expected the interrupted record to be %q, got %q", paymentjournal.Prepared, prior[0].State)
	}

	// And the guard must refuse it, not merely see it.
	if _, err := restarted.Redeem(crashTestNote(t), 1000, "00:00:00:00:00:01", "https://mint.example"); err != ErrLNURLCashNoteAlreadyRedeemed {
		t.Fatalf("a note whose first presentation was interrupted must be refused; got %v", err)
	}
}

// The retry suffix must not defeat the id match. A note that failed once is freed
// for a retry under a suffixed id; if the retry is itself interrupted, the second
// record must still be found.
func TestSuffixedRetryOperationStillBlocksReplay(t *testing.T) {
	store := newCrashTestStore(t)
	rail := crashTestRail(store)

	fingerprint := noteFingerprint(crashNoteSpend)
	base := "lnurlcash-" + fingerprint[:32]

	first, err := store.Prepare(paymentjournal.Operation{
		ID:         base,
		WalletID:   walletKeyForMint("https://mint.example"),
		Direction:  paymentjournal.Incoming,
		AmountMSat: 1000,
		Recipient:  "mint.example",
		Request:    []byte("cp1example"),
	}, paymentjournal.Budget{})
	if err != nil {
		t.Fatalf("preparing the first attempt: %v", err)
	}
	if _, err := store.Advance(first.ID, first.Version, paymentjournal.Failed, 0); err != nil {
		t.Fatalf("failing the first attempt: %v", err)
	}

	// The retry, interrupted before BindInvoice.
	retryID := base + "-r2"
	if _, err := store.Prepare(paymentjournal.Operation{
		ID:         retryID,
		WalletID:   walletKeyForMint("https://mint.example"),
		Direction:  paymentjournal.Incoming,
		AmountMSat: 1000,
		Recipient:  "mint.example",
		Request:    []byte("cp1example"),
	}, paymentjournal.Budget{}); err != nil {
		t.Fatalf("preparing the retry: %v", err)
	}

	prior, err := rail.priorAttempts(fingerprint)
	if err != nil {
		t.Fatalf("priorAttempts: %v", err)
	}
	if len(prior) != 2 {
		t.Fatalf("both the failed attempt and the interrupted retry must be found; got %d", len(prior))
	}

	ids := []string{prior[0].ID, prior[1].ID}
	if !strings.Contains(ids[0]+ids[1], retryID) {
		t.Fatalf("the suffixed retry record was not recognised: %v", ids)
	}

	// The retry is not Failed, so the note stays reserved.
	if _, err := rail.Redeem(crashTestNote(t), 1000, "00:00:00:00:00:01", "https://mint.example"); err != ErrLNURLCashNoteAlreadyRedeemed {
		t.Fatalf("an interrupted retry must keep the note reserved; got %v", err)
	}
}

// An unrelated operation whose id merely starts with the same prefix must not be
// mistaken for this note. A match that is too loose would refuse honest notes.
func TestUnrelatedOperationIsNotMistakenForThisNote(t *testing.T) {
	store := newCrashTestStore(t)
	rail := crashTestRail(store)

	fingerprint := noteFingerprint(crashNoteSpend)

	// Same 32-char prefix but a different suffix: a distinct note whose
	// fingerprint happens to share a prefix would be a sha256 collision, so this
	// is really testing that the prefix comparison does not overrun.
	other := "lnurlcash-" + strings.Repeat("f", 32)
	if other == "lnurlcash-"+fingerprint[:32] {
		t.Skip("prefix collision with the test fingerprint; nothing to prove")
	}
	if _, err := store.Prepare(paymentjournal.Operation{
		ID:         other,
		WalletID:   walletKeyForMint("https://mint.example"),
		Direction:  paymentjournal.Incoming,
		AmountMSat: 1000,
		Recipient:  "mint.example",
		Request:    []byte("cp1other"),
	}, paymentjournal.Budget{}); err != nil {
		t.Fatalf("preparing the unrelated operation: %v", err)
	}

	prior, err := rail.priorAttempts(fingerprint)
	if err != nil {
		t.Fatalf("priorAttempts: %v", err)
	}
	if len(prior) != 0 {
		t.Fatalf("an unrelated record must not block this note; got %d", len(prior))
	}
}

// THE HAPPY PATH, WHICH IS THE ONLY TEST THAT CATCHES A BROKEN Prepare.
//
// The replay tests above exercise the guard, and the guard runs BEFORE Prepare —
// so they pass even when Prepare itself is refused for every input. That is how a
// rail can look tested, build clean, and refuse every real payment. This test
// walks one fresh note all the way to the melt and asserts it returned a quote.
func TestRedeemReachesTheMeltForAFreshNote(t *testing.T) {
	store := newCrashTestStore(t)

	var gotInvoice string
	var melted bool
	rail := NewLNURLCashRail(
		store,
		0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			if sats != 1 {
				t.Fatalf("expected the satoshi value derived from the confirmed amount, got %d", sats)
			}
			return "quote-happy", "lnbc10u1pexample", nil
		},
		func(note lnurlcash.Note, callback IssuerCallback, invoice string) error {
			melted = true
			gotInvoice = invoice
			return nil
		},
	)

	quoteID, err := rail.Redeem(crashTestNote(t), 1000, "00:00:00:00:00:01", "https://mint.example")
	if err != nil {
		t.Fatalf("a fresh note must be redeemable, but the rail refused it: %v", err)
	}
	if quoteID != "quote-happy" {
		t.Fatalf("expected the invoice's quote id back, got %q", quoteID)
	}
	if !melted {
		t.Fatal("the rail returned a quote id without ever asking the issuer to melt")
	}
	if gotInvoice != "lnbc10u1pexample" {
		t.Fatalf("the melt was handed the wrong invoice: %q", gotInvoice)
	}

	// And the durable record must exist, bound to our invoice.
	ops, err := store.List()
	if err != nil {
		t.Fatalf("listing the journal: %v", err)
	}
	var found bool
	for _, op := range ops {
		if op.Direction == paymentjournal.Incoming && op.State == paymentjournal.Awaiting {
			if string(op.Invoice) != "lnbc10u1pexample" {
				t.Fatalf("the record is bound to the wrong invoice: %q", op.Invoice)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("a completed redemption left no awaiting record bound to its invoice")
	}
}
