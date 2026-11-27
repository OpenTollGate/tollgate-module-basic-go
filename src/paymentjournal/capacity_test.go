package paymentjournal

import (
	"fmt"
	"path/filepath"
	"testing"
)

// THE RECORD CAP IS A HARD STOP AND IT COUNTS EVERY OPERATION, NOT LIVE ONES.
//
// Nothing in this package tested maxRecords. It is documented as "a bounded single-writer
// journal", and the bound is what makes its bytes-at-rest story and its fsync cost finite —
// but a caller that journals one record per unit of business can exhaust it permanently,
// because there is no prune, expiry or delete anywhere in the store.
//
// This pins the behaviour so a caller cannot be surprised by it: at the cap, Prepare refuses
// for EVERYONE, and a terminal record occupies a slot exactly as much as an in-flight one.
func TestCapRefusesAllWritersAndIgnoresState(t *testing.T) {
	if testing.Short() {
		t.Skip("fills the journal to its cap by construction")
	}
	s, err := Open(filepath.Join(t.TempDir(), "journal"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Fill to exactly the cap, marking half of them terminally paid along the way, so the
	// test can prove that STATE does not matter — only count.
	for i := 0; i < maxRecords; i++ {
		op, perr := s.Prepare(Operation{
			ID:         fmt.Sprintf("cap-%05d", i),
			WalletID:   "w",
			Direction:  Incoming,
			AmountMSat: 1000,
			Recipient:  "r",
			Request:    []byte("x"),
		}, Budget{})
		if perr != nil {
			t.Fatalf("prepare %d refused before the cap: %v", i, perr)
		}
		if i%2 == 0 {
			if _, aerr := s.Advance(op.ID, op.Version, Failed, 0); aerr != nil {
				t.Fatalf("retire %d: %v", i, aerr)
			}
		}
	}

	// Every other record is now terminal, and it changes nothing.
	if _, err := s.Prepare(Operation{
		ID: "one-more", WalletID: "w", Direction: Incoming,
		AmountMSat: 1000, Recipient: "r", Request: []byte("x"),
	}, Budget{}); err == nil {
		t.Fatal("the journal accepted another record at its cap — the bound does not bind")
	}

	// And an EXISTING operation can still be advanced after the cap. That matters because it is
	// the ONLY thing that still works: reconciliation is possible, reclaiming a slot is not.
	// There is no delete path in this store, so advancing records to a terminal state tidies
	// the operator's view and frees nothing.
	ops, lerr := s.List()
	if lerr != nil {
		t.Fatalf("list: %v", lerr)
	}
	var open Operation
	for _, op := range ops {
		if op.State == Prepared {
			open = op
			break
		}
	}
	if _, aerr := s.Advance(open.ID, open.Version, Failed, 0); aerr != nil {
		t.Fatalf("an existing record must still be advanceable at the cap, or an operator has no way to reclaim anything: %v", aerr)
	}
	if _, err := s.Prepare(Operation{
		ID: "still-full", WalletID: "w", Direction: Incoming,
		AmountMSat: 1000, Recipient: "r", Request: []byte("x"),
	}, Budget{}); err == nil {
		t.Fatal("advancing an existing record freed a slot; it should not, and if this ever changes the rail's assumptions change with it")
	}
}
