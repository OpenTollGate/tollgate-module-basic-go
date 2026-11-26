package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/paymentjournal"
)

// openTestJournalAt creates a journal where the CLI expects it (a sibling of the config
// file) and returns a CLOSER.
//
// The store holds an exclusive flock — the single-writer rule from #504 — so the CLI
// read below cannot open the same directory while this handle is live. Closing explicitly
// is the honest way to model the production case (the daemon is not running when an
// operator inspects the journal) and it is also why the returned closer exists rather
// than a t.Cleanup: the test must close before it reads.
func openTestJournalAt(t *testing.T, configDir string) (*paymentjournal.Store, func()) {
	t.Helper()
	store, err := paymentjournal.Open(filepath.Join(configDir, "lnurlcash-journal"))
	if err != nil {
		t.Fatalf("opening the journal: %v", err)
	}
	closed := false
	closeOnce := func() {
		if !closed {
			closed = true
			_ = store.Close()
		}
	}
	t.Cleanup(closeOnce)
	return store, closeOnce
}

// AN AWAITING RECORD IS THE WHOLE REASON THIS COMMAND EXISTS.
//
// The rail keeps an ambiguous redemption reserved on purpose. Nothing in the shipped
// binary could retire that record, so an operator had no way to see it, let alone act.
func TestJournalCommandSurfacesAwaitingRedemptions(t *testing.T) {
	dir := t.TempDir()
	store, closeJournal := openTestJournalAt(t, dir)

	op, err := store.Prepare(paymentjournal.Operation{
		ID:         "lnurlcash-abc123",
		WalletID:   "mint-key",
		Direction:  paymentjournal.Incoming,
		AmountMSat: 21000,
		Recipient:  "issuer.example",
		Request:    []byte("cp1"),
	}, paymentjournal.Budget{})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	submitted, err := store.Advance(op.ID, op.Version, paymentjournal.Submitted, 0)
	if err != nil {
		t.Fatalf("advance to submitted: %v", err)
	}
	// Awaiting is produced by BindInvoice (the note is bound to our invoice and the melt
	// is expected), not by Advance — Advance cannot reach it, which is what the first
	// attempt at this test discovered.
	// A payment hash is 32 bytes of hex, and the version must be the record's CURRENT
	// one (CAS): BindInvoice refuses a stale version, which is what the first two
	// attempts at this test ran into.
	if _, err := store.BindInvoice(op.ID, submitted.Version, strings.Repeat("ab", 32), []byte("lnbc1invoice")); err != nil {
		t.Fatalf("bind invoice (awaiting): %v", err)
	}
	closeJournal()

	entries, err := lnurlCashJournalEntries(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("reading the journal: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one redemption, got %d", len(entries))
	}
	e := entries[0]
	if !e.NeedsOperator {
		t.Fatal("an awaiting redemption must be flagged as needing an operator: it cannot resolve itself and the note is reserved")
	}
	if e.State != "awaiting" {
		t.Fatalf("state = %q, want awaiting", e.State)
	}
	if e.Issuer != "issuer.example" || e.AmountMSat != 21000 {
		t.Fatalf("entry lost detail: %+v", e)
	}
	if e.NextStep == "" {
		t.Fatal("an operator-facing entry must say what to do next, not just name a state")
	}

	// The human form must lead with the marker so a support session sees it immediately.
	out := formatLNURLCashJournal(entries)
	if !strings.Contains(out, "! lnurlcash-abc123") {
		t.Fatalf("the human form did not mark the entry as needing attention:\n%s", out)
	}
	if !strings.Contains(out, "1 needing an operator") {
		t.Fatalf("the summary did not count the entry needing an operator:\n%s", out)
	}
}

// The journal is shared with the Lightning rail. This command must not describe another
// rail's records as LNURLcash ones — an operator reading "awaiting" against the wrong
// subsystem would chase the wrong thing.
func TestJournalCommandIgnoresOtherRailsRecords(t *testing.T) {
	dir := t.TempDir()
	store, closeJournal := openTestJournalAt(t, dir)

	if _, err := store.Prepare(paymentjournal.Operation{
		ID:         "lightning-quote-xyz",
		WalletID:   "mint-key",
		Direction:  paymentjournal.Incoming,
		AmountMSat: 1000,
		Recipient:  "someone",
		Request:    []byte("x"),
	}, paymentjournal.Budget{}); err != nil {
		t.Fatalf("prepare: %v", err)
	}

	closeJournal()
	entries, err := lnurlCashJournalEntries(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("reading the journal: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("a Lightning rail record was reported as an LNURLcash redemption: %+v", entries)
	}
	if out := formatLNURLCashJournal(entries); !strings.Contains(out, "no LNURLcash redemptions") {
		t.Fatalf("empty journal should say so plainly, got: %s", out)
	}
}

// A failed-before-the-issuer record frees the note, so it must NOT be flagged as needing
// an operator: the customer can simply try again.
func TestJournalCommandDoesNotFlagAPreIssuerFailure(t *testing.T) {
	dir := t.TempDir()
	store, closeJournal := openTestJournalAt(t, dir)

	op, err := store.Prepare(paymentjournal.Operation{
		ID:         "lnurlcash-failed1",
		WalletID:   "mint-key",
		Direction:  paymentjournal.Incoming,
		AmountMSat: 1000,
		Recipient:  "issuer.example",
		Request:    []byte("cp1"),
	}, paymentjournal.Budget{})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if _, err := store.Advance(op.ID, op.Version, paymentjournal.Failed, 0); err != nil {
		t.Fatalf("advance to failed: %v", err)
	}
	closeJournal()

	entries, err := lnurlCashJournalEntries(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("reading the journal: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one redemption, got %d", len(entries))
	}
	if entries[0].NeedsOperator {
		t.Fatal("a pre-issuer failure frees the note; flagging it as needing an operator would send someone chasing a non-issue")
	}
}
