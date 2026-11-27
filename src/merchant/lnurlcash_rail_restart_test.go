package merchant

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/lnurlcash"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/paymentjournal"
)

// A REAL RESTART: CLOSE THE STORE, REOPEN IT FROM DISK, AND TRY AGAIN.
//
// Every other "restart" test in this package builds a fresh rail over the SAME *Store. The
// state it reads is still the in-memory map that has been there all along — the journal file
// is never re-read, so the durable path is never exercised. The second review caught that
// shape in the fingerprint test; it applies here too, and here it matters more, because the
// crash-consistency claim in the ADR is entirely about what survives a reboot.
//
// This closes the store (as a killed process would), reopens it from the directory, and
// asserts the note is still refused. If the journal were not actually fsynced and renamed,
// the record would be gone and the note would be accepted twice.
func TestReplayRefusalSurvivesAStoreReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "journal")

	// --- process 1: an ambiguous melt, then the process dies -------------------
	store, err := paymentjournal.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	rail := NewLNURLCashRail(
		store, 0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			return "quote-restart", "lnbc10u1pexample", nil
		},
		func(n lnurlcash.Note, cb IssuerCallback, invoice string) error {
			// The issuer may or may not have burned the note. This is the point of no return.
			return errors.New("issuer unreachable — outcome unknown")
		},
	)
	note, perr := lnurlcash.Parse("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", "mint.example")
	if perr != nil {
		t.Fatalf("parse: %v", perr)
	}
	if _, rerr := rail.Redeem(note, 21000, "00:00:00:00:00:01", "https://mint.example"); rerr == nil {
		t.Fatal("expected the melt to fail")
	}
	// Kill it. Close releases the flock, as process death does.
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// --- process 2: fresh store from disk, fresh rail, empty memory -----------
	store2, err := paymentjournal.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = store2.Close() }()

	ops, lerr := store2.List()
	if lerr != nil {
		t.Fatalf("list after reopen: %v", lerr)
	}
	if len(ops) != 1 {
		t.Fatalf("after reopening from disk the journal has %d records, want 1 — the durable intent did not survive, so a crash frees the note for a second melt", len(ops))
	}
	if ops[0].State != paymentjournal.Awaiting {
		t.Fatalf("the record came back as %q, want awaiting — an ambiguous melt must stay reserved across a restart", ops[0].State)
	}

	melted := false
	rail2 := NewLNURLCashRail(
		store2, 0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			t.Fatal("a note with an unresolved melt must be refused before a second invoice")
			return "", "", nil
		},
		func(n lnurlcash.Note, cb IssuerCallback, invoice string) error {
			melted = true
			return nil
		},
	)
	if _, rerr := rail2.Redeem(note, 21000, "00:00:00:00:00:01", "https://mint.example"); rerr != ErrLNURLCashNoteAlreadyRedeemed {
		t.Fatalf("after a REAL restart the note was not refused as a replay (got %v) — the issuer may already have burned it", rerr)
	}
	if melted {
		t.Fatal("the note reached a second melt after a restart")
	}
}

// THE SAME, FOR A SALE WITH A KNOWN OUTCOME: a settled record must not be re-presentable
// either, and it must come back settled rather than as a fresh reservation.
func TestSettledRecordSurvivesAStoreReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "journal")
	store, err := paymentjournal.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	rail := NewLNURLCashRail(
		store, 0,
		func(macAddress, mintURL string, sats uint64) (string, string, error) {
			return "quote-settled", "lnbc10u1pexample", nil
		},
		func(n lnurlcash.Note, cb IssuerCallback, invoice string) error { return nil },
	)
	note, _ := lnurlcash.Parse("202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f", "mint.example")
	if _, rerr := rail.Redeem(note, 21000, "00:00:00:00:00:02", "https://mint.example"); rerr != nil {
		t.Fatalf("redeem: %v", rerr)
	}
	rail.Settle("lnbc10u1pexample")
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	store2, err := paymentjournal.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = store2.Close() }()
	ops, _ := store2.List()
	if len(ops) != 1 || ops[0].State != paymentjournal.Paid {
		t.Fatalf("after reopening, state = %v (want one record in paid); an operator reading the journal after a reboot must see a settled sale, not an unresolved one", ops)
	}
}
