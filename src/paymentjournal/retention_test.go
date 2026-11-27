package paymentjournal

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// openJournal opens a store in a nested temp dir. The parent must be syncable, so a bare
// t.TempDir() is not enough (see the note in the other tests here).
func openJournal(t *testing.T) *Store {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "journal")
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// fill writes n records and returns the ids, oldest first.
func fill(t *testing.T, s *Store, n int, prefix string) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		op, err := s.Prepare(Operation{
			ID:         fmt.Sprintf("%s-%05d", prefix, i),
			WalletID:   "w",
			Direction:  Incoming,
			AmountMSat: 1000,
			Recipient:  "r",
			Request:    []byte("x"),
		}, Budget{})
		if err != nil {
			t.Fatalf("prepare %s-%d: %v", prefix, i, err)
		}
		ids = append(ids, op.ID)
	}
	return ids
}

// fillSettleable writes n INCOMING records that can actually reach Paid, by walking the path the
// store requires: Prepare has no hash, so the invoice is bound afterwards with BindInvoice
// (Submitted -> Awaiting), and only then can Awaiting -> Paid happen. `fill` cannot reach Paid
// at all, so a test using it would be asserting about a state the store can never enter.
func fillSettleable(t *testing.T, s *Store, n int, prefix string) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		op, err := s.Prepare(Operation{
			ID:         fmt.Sprintf("%s-%05d", prefix, i),
			WalletID:   "w",
			Direction:  Incoming,
			AmountMSat: 1000,
			Recipient:  "r",
			Request:    []byte("x"),
		}, Budget{})
		if err != nil {
			t.Fatalf("prepare %s-%d: %v", prefix, i, err)
		}
		if _, err := s.Advance(op.ID, op.Version, Submitted, 0); err != nil {
			t.Fatalf("submit %s: %v", op.ID, err)
		}
		if _, err := s.BindInvoice(op.ID, 2, fmt.Sprintf("%064x", i+1),
			[]byte(fmt.Sprintf("lnbc10u1p%040x", i+1))); err != nil {
			t.Fatalf("bind invoice %s: %v", op.ID, err)
		}
		ids = append(ids, op.ID)
	}
	return ids
}

// THE WHOLE POINT: pruning finished records gives the journal its lifetime back.
//
// Before this, a caller that journalled one record per unit of business stopped being able to
// record anything after maxRecords units — permanently, with no adversary involved. This test
// does the smallest thing that proves the fix: fill to the cap, prove the refusal, retire the
// records, prune, and prove a NEW record is accepted. That last step is the one that failed
// before.
func TestPruneFreesCapacityForNewWork(t *testing.T) {
	s := openJournal(t)
	ids := fill(t, s, maxRecords, "sale")

	// At the cap: refused, as it always was.
	if _, err := s.Prepare(Operation{
		ID: "blocked", WalletID: "w", Direction: Incoming,
		AmountMSat: 1000, Recipient: "r", Request: []byte("x"),
	}, Budget{}); err == nil {
		t.Fatal("journal accepted a record at its cap — the bound does not bind, so this test proves nothing")
	}

	// Retire every record to a terminal state, as a real caller eventually does.
	for _, id := range ids {
		op, err := s.Get(id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if _, err := s.Advance(id, op.Version, Failed, 0); err != nil {
			t.Fatalf("retire %s: %v", id, err)
		}
	}

	// Nothing frees until the retention policy runs: retiring alone was never enough, which is
	// the "settlement frees nothing" finding this store earned.
	if _, err := s.Prepare(Operation{
		ID: "still-blocked", WalletID: "w", Direction: Incoming,
		AmountMSat: 1000, Recipient: "r", Request: []byte("x"),
	}, Budget{}); err == nil {
		t.Fatal("a terminal state freed a slot by itself; if that is now true, this test is stale and the ADR is wrong")
	}

	dropped, err := s.Prune(Retention{KeepFor: time.Hour}, time.Now().Add(2*time.Hour))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if dropped != maxRecords {
		t.Fatalf("pruned %d records, want %d", dropped, maxRecords)
	}

	// The point of the whole exercise.
	if _, err := s.Prepare(Operation{
		ID: "after-prune", WalletID: "w", Direction: Incoming,
		AmountMSat: 1000, Recipient: "r", Request: []byte("x"),
	}, Budget{}); err != nil {
		t.Fatalf("journal still refuses new work after pruning terminal records: %v", err)
	}
}

// AN IN-FLIGHT RECORD MUST NEVER BE RECLAIMED. Age is not progress.
//
// These states are what reconciliation and duplicate-suppression read. Dropping one re-opens a
// money move for a second attempt — the exact failure this store exists to prevent. The policy
// must refuse to touch them no matter how old they are.
func TestPruneNeverDropsAnInFlightRecord(t *testing.T) {
	s := openJournal(t)
	ids := fill(t, s, 6, "op")

	// Move each into a distinct non-terminal state that a real caller can reach.
	type want struct {
		id string
		st State
	}
	// op-00000 stays Prepared (never submitted).
	if _, err := s.Advance(ids[1], 1, Submitted, 0); err != nil { // op-00001 -> Submitted
		t.Fatalf("submit: %v", err)
	}
	if _, err := s.Advance(ids[2], 1, Submitted, 0); err != nil { // op-00002 -> Submitted -> Unknown
		t.Fatalf("submit: %v", err)
	}
	if _, err := s.Advance(ids[2], 2, Unknown, 0); err != nil {
		t.Fatalf("mark unknown: %v", err)
	}

	inFlight := []want{
		{ids[0], Prepared},
		{ids[1], Submitted},
		{ids[2], Unknown},
	}
	// The rest go terminal and SHOULD be pruned, so the test also shows the policy is not
	// simply refusing to do anything.
	for _, id := range ids[3:] {
		op, err := s.Get(id)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if _, err := s.Advance(id, op.Version, Failed, 0); err != nil {
			t.Fatalf("retire: %v", err)
		}
	}

	// Prune with a cutoff far in the future: everything is "old enough".
	if _, err := s.Prune(Retention{KeepFor: time.Nanosecond}, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("prune: %v", err)
	}

	for _, w := range inFlight {
		op, err := s.Get(w.id)
		if err != nil {
			t.Fatalf("in-flight record %s (%s) was DROPPED by prune: %v", w.id, w.st, err)
		}
		if op.State != w.st {
			t.Fatalf("in-flight record %s is now %q, want %q", w.id, op.State, w.st)
		}
	}
	// And the terminal ones really are gone.
	for _, id := range ids[3:] {
		if _, err := s.Get(id); err == nil {
			t.Fatalf("terminal record %s survived a prune that should have reclaimed it", id)
		}
	}
}

// KeepSettled keeps the only local evidence that a payment happened.
//
// A caller whose settled record is the sole proof of a completed sale cannot afford to forget
// it; the safe default in that case is to reclaim only definitively-failed work.
func TestPruneKeepSettledSparesPaidAndCompleted(t *testing.T) {
	s := openJournal(t)
	ids := fillSettleable(t, s, 4, "settled")

	// paid, completed, failed, failed — following the transitions the store actually allows.
	// fillSettleable leaves each record in Awaiting with a bound invoice.
	steps := []struct {
		id  string
		seq []State
	}{
		{ids[0], []State{Paid}},
		{ids[1], []State{Paid, GrantPending, Completed}},
		{ids[2], []State{Failed}},
		{ids[3], []State{Failed}},
	}
	for _, st := range steps {
		ver := uint64(3) // Prepare=1, Submitted=2, BindInvoice=3
		for _, next := range st.seq {
			if _, err := s.Advance(st.id, ver, next, 0); err != nil {
				t.Fatalf("advance %s -> %s: %v", st.id, next, err)
			}
			ver++
		}
	}

	if _, err := s.Prune(Retention{KeepFor: time.Nanosecond, KeepSettled: true}, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("prune: %v", err)
	}

	for _, id := range ids[:2] {
		if _, err := s.Get(id); err != nil {
			t.Fatalf("KeepSettled dropped settled record %s, which is the only local proof it happened: %v", id, err)
		}
	}
	for _, id := range ids[2:] {
		if _, err := s.Get(id); err == nil {
			t.Fatalf("KeepSettled kept definitively-failed record %s; that is the one thing it may reclaim", id)
		}
	}
}

// A record younger than the retention window is untouched, so a caller can still read back
// what it just did.
func TestPruneLeavesRecentTerminalRecordsAlone(t *testing.T) {
	s := openJournal(t)
	ids := fill(t, s, 2, "recent")
	op, _ := s.Get(ids[0])
	if _, err := s.Advance(ids[0], op.Version, Failed, 0); err != nil {
		t.Fatalf("retire: %v", err)
	}

	dropped, err := s.Prune(Retention{KeepFor: time.Hour}, time.Now())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if dropped != 0 {
		t.Fatalf("pruned %d recent records, want 0", dropped)
	}
	if _, err := s.Get(ids[0]); err != nil {
		t.Fatalf("recent terminal record was dropped: %v", err)
	}
}

// Zero KeepFor is the current behaviour: prune nothing. Silence must not reclaim anything.
func TestPruneDisabledByDefault(t *testing.T) {
	s := openJournal(t)
	ids := fill(t, s, 2, "keep")
	op, _ := s.Get(ids[0])
	if _, err := s.Advance(ids[0], op.Version, Failed, 0); err != nil {
		t.Fatalf("retire: %v", err)
	}
	dropped, err := s.Prune(Retention{}, time.Now().Add(1000*time.Hour))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if dropped != 0 {
		t.Fatalf("a zero KeepFor pruned %d records; silence must reclaim nothing", dropped)
	}
}

// The pruned journal must still be a valid file: reopening it must work, and the surviving
// records must still be readable. A "prune" that corrupts the store would be worse than the cap.
func TestPrunedJournalReopensAndKeepsSurvivors(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "journal")
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ids := fill(t, s, 3, "mix")
	// Make the middle one in flight and the other two terminal.
	op1, _ := s.Get(ids[1])
	if _, err := s.Advance(ids[1], op1.Version, Submitted, 0); err != nil {
		t.Fatalf("submit: %v", err)
	}
	for _, id := range []string{ids[0], ids[2]} {
		op, _ := s.Get(id)
		if _, err := s.Advance(id, op.Version, Failed, 0); err != nil {
			t.Fatalf("retire: %v", err)
		}
	}
	if _, err := s.Prune(Retention{KeepFor: time.Nanosecond}, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	re, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen after prune: %v (a prune that makes the journal unreadable is worse than the cap)", err)
	}
	defer func() { _ = re.Close() }()

	if _, err := re.Get(ids[1]); err != nil {
		t.Fatalf("surviving in-flight record is gone after reopen: %v", err)
	}
	for _, id := range []string{ids[0], ids[2]} {
		if _, err := re.Get(id); err == nil {
			t.Fatalf("pruned record %s came back after reopen", id)
		}
	}
}

// Capacity distinguishes a journal full of finished work (prunable, benign) from one whose
// slots are all held by operations that never resolved (a real wedge pruning cannot fix).
func TestCapacityAndOldestInFlightDistinguishTheTwoFullJournals(t *testing.T) {
	s := openJournal(t)
	fill(t, s, 3, "cap")

	used, limit, err := s.Capacity()
	if err != nil {
		t.Fatalf("capacity: %v", err)
	}
	if used != 3 || limit != maxRecords {
		t.Fatalf("capacity = %d/%d, want 3/%d", used, limit, maxRecords)
	}

	// All Prepared => all in flight => a wedge.
	oldest, ok, err := s.OldestInFlight()
	if err != nil {
		t.Fatalf("oldest in flight: %v", err)
	}
	if !ok {
		t.Fatal("a journal of Prepared records reports nothing in flight")
	}
	if oldest.ID == "" {
		t.Fatal("oldest in-flight record has no id")
	}
}
