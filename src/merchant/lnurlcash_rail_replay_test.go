package merchant

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
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

// TWO SIMULTANEOUS REQUESTS FOR ONE NOTE MUST NOT BOTH REACH THE MELT.
//
// The in-memory reservation is the only thing standing between a double-clicked
// "pay" button and two melts of the same bearer note. The issuer's own replay
// protection would catch the second one — on someone else's server, after the
// first may already have been paid, and only if that issuer implements it. The
// gate must not depend on that.
//
// The check and the insert share one mutex, but "the code looks atomic" is not
// evidence. This drives N goroutines at one note and counts melts.
func TestConcurrentRedemptionsOfOneNoteMeltOnlyOnce(t *testing.T) {
	store := newCrashTestStore(t)

	var mu sync.Mutex
	melts := 0
	release := make(chan struct{})

	rail := NewLNURLCashRail(
		store,
		0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			return "quote-conc", "lnbc10u1pexample", nil
		},
		func(note lnurlcash.Note, callback IssuerCallback, invoice string) error {
			mu.Lock()
			melts++
			mu.Unlock()
			return nil
		},
	)

	const racers = 8
	note := crashTestNote(t)
	var wg sync.WaitGroup
	results := make([]error, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // release them together, so they contend for the same reservation
			_, results[i] = rail.Redeem(note, 1000, "00:00:00:00:00:01", "https://mint.example")
		}(i)
	}
	close(start)
	wg.Wait()
	close(release)

	mu.Lock()
	got := melts
	mu.Unlock()

	if got != 1 {
		t.Fatalf("%d concurrent redemptions of ONE note reached the melt %d time(s); exactly one must win", racers, got)
	}

	// Exactly one caller may be told "here is your quote". The rest must be refused as
	// already-redeemed, not handed a second quote for the same note.
	winners, refused := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrLNURLCashNoteAlreadyRedeemed):
			refused++
		default:
			t.Fatalf("unexpected error from a concurrent redemption: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("expected exactly one caller to receive a quote, got %d", winners)
	}
	if refused != racers-1 {
		t.Fatalf("expected %d callers refused as already-redeemed, got %d", racers-1, refused)
	}
}

// ONE NOTE MUST NOT BE ABLE TO EXHAUST THE JOURNAL.
//
// Every attempt on a note that fails pre-issuer costs one permanent journal record, and
// nothing ever reclaims it. At the store's cap, Prepare refuses for EVERY caller — so an
// unbounded retry loop on a single note is a denial of service against every other
// customer, delivered through a path that looks like an ordinary failed payment.
//
// This drives one note past the budget and asserts the refusal is the bounded one, not
// an ever-growing trail of records.
func TestRetriesAreBoundedPerNote(t *testing.T) {
	store := newCrashTestStore(t)

	// A mint that always refuses to issue us an invoice: the pre-issuer failure path
	// that frees the note for retry. This is the shape of attack.
	rail := NewLNURLCashRail(
		store,
		0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			return "", "", errors.New("mint configuration not found")
		},
		func(note lnurlcash.Note, callback IssuerCallback, invoice string) error {
			t.Fatal("the melt must never be reached when the invoice step fails")
			return nil
		},
	)

	note := crashTestNote(t)
	attempts := 0
	var lastErr error
	for i := 0; i < 40; i++ {
		_, err := rail.Redeem(note, 1000, "00:00:00:00:00:01", "https://mint.example")
		if err == nil {
			t.Fatal("a note whose invoice cannot be issued must not succeed")
		}
		if errors.Is(err, ErrLNURLCashRetriesExhausted) {
			lastErr = err
			break
		}
		attempts++
	}
	if lastErr == nil {
		t.Fatalf("40 attempts on one note never hit the retry bound; it consumed %d journal records", attempts)
	}
	if attempts > maxRedeemAttemptsPerNote {
		t.Fatalf("the note consumed %d journal records before the bound bit; the bound is %d", attempts, maxRedeemAttemptsPerNote)
	}

	// The bound must be on RECORDS, not just on the error: a bound that still writes a
	// record per attempt would be decoration.
	ops, err := store.List()
	if err != nil {
		t.Fatalf("listing the journal: %v", err)
	}
	incoming := 0
	for _, op := range ops {
		if op.Direction == paymentjournal.Incoming {
			incoming++
		}
	}
	if incoming > maxRedeemAttemptsPerNote {
		t.Fatalf("one note left %d permanent journal records (bound %d) — at the store's cap this refuses everyone else", incoming, maxRedeemAttemptsPerNote)
	}

	// And an unrelated note must still get through: the DoS claim is about OTHER
	// customers, so assert on another customer.
	// A DIFFERENT note, from a different preimage, so it is a different fingerprint.
	const otherSpend = "101112131415161718191a1b1c1d1e1f202122232425262728292a2b2c2d2e2f"
	other, perr := lnurlcash.Parse(otherSpend, "mint.example")
	if perr != nil {
		t.Fatalf("parsing the second note: %v", perr)
	}
	okRail := NewLNURLCashRail(
		store,
		0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			return "quote-other", "lnbc10u1pexample", nil
		},
		func(note lnurlcash.Note, callback IssuerCallback, invoice string) error { return nil },
	)
	if _, err := okRail.Redeem(other, 1000, "00:00:00:00:00:02", "https://mint.example"); err != nil {
		t.Fatalf("a different customer's note was blocked by one repeated note: %v", err)
	}
}

// THE MELT MUST GO WHERE THE ISSUER SAID, NOT WHERE THE NOTE'S URL HAPPENED TO POINT.
//
// LUD-03/LUD-25 allow the withdraw response to name a callback endpoint distinct from the
// informational URL. The client validated that callback (host, scheme, same domain) and
// then dropped it, so the melt used the note's path unconditionally. For a conforming
// issuer that publishes a different path, every redemption 404s: the note is reserved but
// never redeemed, so the customer's money is stuck in limbo with no recovery path.
func TestMeltUsesTheIssuersCallbackPath(t *testing.T) {
	store := newCrashTestStore(t)

	var gotPath string
	rail := NewLNURLCashRail(
		store,
		0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			return "quote-cb", "lnbc10u1pexample", nil
		},
		func(note lnurlcash.Note, callback IssuerCallback, invoice string) error {
			gotPath = callback.Path
			return nil
		},
	)

	note := crashTestNote(t)
	const issuerCallback = "/lnurl/cash/redeem"
	if note.WithdrawURL.Path == issuerCallback {
		t.Fatalf("test bug: the note's path already equals the issuer's, so this proves nothing")
	}
	if _, err := rail.RedeemAt(note, 1000, "00:00:00:00:00:01", "https://mint.example", issuerCallback); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if gotPath != issuerCallback {
		t.Fatalf("the melt went to %q; the issuer named %q — a conforming issuer would 404 and the note would be stuck", gotPath, issuerCallback)
	}

	// And with no issuer answer, the note's own path is still the default: that is the
	// bare-k1 case, and changing it would be a regression.
	var defaultPath string
	rail2 := NewLNURLCashRail(
		store,
		0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			return "quote-cb2", "lnbc10u1pexample", nil
		},
		func(note lnurlcash.Note, callback IssuerCallback, invoice string) error {
			defaultPath = callback.Path
			return nil
		},
	)
	other, perr := lnurlcash.Parse("202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f", "mint.example")
	if perr != nil {
		t.Fatalf("parsing the second note: %v", perr)
	}
	if _, err := rail2.Redeem(other, 1000, "00:00:00:00:00:02", "https://mint.example"); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if defaultPath != other.WithdrawURL.Path {
		t.Fatalf("with no issuer answer the melt should use the note's path %q, got %q", other.WithdrawURL.Path, defaultPath)
	}
}
