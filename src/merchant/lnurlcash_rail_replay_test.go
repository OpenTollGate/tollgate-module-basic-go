package merchant

import (
	"errors"
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

// THE MELT FAILURE IS THE POINT OF NO RETURN, AND THE NOTE MUST STAY RESERVED.
//
// When the melt fails, the gate does not know whether the issuer burned the note.
// Both answers must lead to the same local action: keep the note reserved. Releasing
// it would let a second presentation melt a note that may already be spent — a
// double-melt, and the issuer's own replay protection is the only thing that would
// catch it, on someone else's server.
//
// This is the property the whole rail exists to hold, and until now it was asserted
// only in a comment.
func TestMeltFailureKeepsTheNoteReserved(t *testing.T) {
	store := newCrashTestStore(t)

	meltErr := errors.New("lnurlcash: issuer unreachable — outcome unknown: context deadline exceeded")
	rail := NewLNURLCashRail(
		store,
		0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			return "quote-meltfail", "lnbc10u1pexample", nil
		},
		func(note lnurlcash.Note, callback IssuerCallback, invoice string) error {
			return meltErr
		},
	)

	note := crashTestNote(t)
	quoteID, err := rail.Redeem(note, 1000, "00:00:00:00:00:01", "https://mint.example")
	if err == nil {
		t.Fatal("Redeem must report the melt failure, not swallow it")
	}
	if quoteID != "" {
		t.Fatalf("a failed melt must not return a quote id; got %q", quoteID)
	}

	// The durable record must survive as a reservation, not as a failure that frees
	// the note. Assert the STATE, because "the note is refused" is only correct for
	// the right reason — a bug that refused it via a different path would pass a
	// weaker test.
	ops, lerr := store.List()
	if lerr != nil {
		t.Fatalf("listing the journal: %v", lerr)
	}
	var found bool
	for _, op := range ops {
		if op.Direction != paymentjournal.Incoming {
			continue
		}
		found = true
		if op.State == paymentjournal.Failed {
			t.Fatalf("the melt failed but the record was marked %q — that FREES the note for a retry and risks a double-melt", op.State)
		}
		if op.State != paymentjournal.Awaiting {
			t.Fatalf("expected the interrupted redemption to stay %q, got %q", paymentjournal.Awaiting, op.State)
		}
	}
	if !found {
		t.Fatal("the melt failure left no journal record at all — nothing would stop a replay")
	}

	// And a restart must refuse the note. This is the assertion that matters to a
	// customer and to the operator's float.
	restarted := NewLNURLCashRail(
		store,
		0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			t.Fatal("a re-presented note must be refused BEFORE a second invoice is requested")
			return "", "", nil
		},
		func(note lnurlcash.Note, callback IssuerCallback, invoice string) error {
			t.Fatal("a re-presented note must never reach a second melt")
			return nil
		},
	)
	if _, rerr := restarted.Redeem(note, 1000, "00:00:00:00:00:01", "https://mint.example"); rerr != ErrLNURLCashNoteAlreadyRedeemed {
		t.Fatalf("after an ambiguous melt the note must stay reserved across a restart; got %v", rerr)
	}
}

// A SHORT ID FROM AN UNRELATED JOURNAL WRITER MUST NOT PANIC THE REDEMPTION PATH.
//
// priorAttempts compares every Incoming record's id against the note's derived id.
// It used to slice op.ID[:42] behind a `len(op.ID) > 32` guard — true for a 35-char
// id, false for the slice. Any other subsystem that journalled an Incoming operation
// with an id of 33..41 characters made every LNURLcash redemption panic with
// "slice bounds out of range [:42] with length 35".
//
// A panic here is not a crash on a quiet path: it is a customer with a note in hand
// and a gate that falls over, and net/http recovers it into a closed connection.
func TestShortForeignJournalIDDoesNotPanic(t *testing.T) {
	store := newCrashTestStore(t)

	// 35 characters: long enough to pass the old len>32 guard, too short to slice.
	const foreignID = "lightning-short-id-0123456789012345"
	if len(foreignID) <= 32 || len(foreignID) >= 42 {
		t.Fatalf("this test only bites in the 33..41 window; id is %d chars", len(foreignID))
	}
	if _, err := store.Prepare(paymentjournal.Operation{
		ID:         foreignID,
		WalletID:   "wallet-key",
		Direction:  paymentjournal.Incoming,
		AmountMSat: 1000,
		Recipient:  "someone-else",
		Request:    []byte("x"),
	}, paymentjournal.Budget{}); err != nil {
		t.Fatalf("seeding the foreign record: %v", err)
	}

	melted := false
	rail := NewLNURLCashRail(
		store,
		0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			return "quote-panic", "lnbc10u1pexample", nil
		},
		func(note lnurlcash.Note, callback IssuerCallback, invoice string) error {
			melted = true
			return nil
		},
	)

	// Must not panic. An unrelated record must not match this note either, so the
	// redemption should proceed normally rather than be refused as a replay.
	if _, err := rail.Redeem(crashTestNote(t), 1000, "00:00:00:00:00:01", "https://mint.example"); err != nil {
		t.Fatalf("an unrelated short journal id must not affect this note: %v", err)
	}
	if !melted {
		t.Fatal("expected the redemption to reach the melt once the unrelated record was ignored")
	}
}
