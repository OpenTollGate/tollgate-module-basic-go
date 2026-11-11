package paymentjournal

import (
	"strings"
	"testing"
	"time"
)

func futureOperation(t *testing.T, s *Store, id string) Operation {
	t.Helper()
	d := s.copyData()
	o := d.Operations[id]
	o.CreatedAt = time.Now().UTC().Add(24 * time.Hour)
	o.UpdatedAt = o.CreatedAt.Add(time.Hour)
	d.Operations[id] = o
	if err := s.persist(d, false); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestBackwardClockAdvanceKeepsJournalReopenable(t *testing.T) {
	s, dir := openTest(t)
	if _, err := s.Prepare(intent("out"), Budget{LimitMSat: 1100}); err != nil {
		t.Fatal(err)
	}
	before := futureOperation(t, s, "out")
	after, err := s.Advance(before.ID, before.Version, Submitted, 0)
	if err != nil || after.UpdatedAt.Before(before.UpdatedAt) {
		t.Fatal("clock rollback corrupted outgoing timestamp")
	}
	_ = s.Close()
	again, err := Open(dir)
	if err != nil {
		t.Fatal("outgoing journal failed to reopen")
	}
	defer again.Close()
}

func TestPreNTPSubmittedRecoveryPreservesJournalAndUnknownBudget(t *testing.T) {
	s, dir := openTest(t)
	o, err := s.Prepare(intent("out"), Budget{LimitMSat: 1100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(o.ID, o.Version, Submitted, 0); err != nil {
		t.Fatal(err)
	}
	before := futureOperation(t, s, "out")
	_ = s.Close()
	again, err := Open(dir)
	if err != nil {
		t.Fatal("first recovery failed")
	}
	defer again.Close()
	after, err := again.Get("out")
	if err != nil || after.State != Unknown || after.UpdatedAt.Before(before.UpdatedAt) {
		t.Fatal("pre-NTP recovery corrupted unknown operation")
	}
	if _, err := again.Prepare(intent("second"), Budget{LimitMSat: 1100}); err == nil {
		t.Fatal("recovery released unknown budget")
	}
	_ = again.Close()
	last, err := Open(dir)
	if err != nil {
		t.Fatal("recovery wrote an unreopenable journal")
	}
	_ = last.Close()
}

func TestBackwardClockIncomingBindingKeepsJournalReopenable(t *testing.T) {
	s, dir := openTest(t)
	o, err := s.Prepare(Operation{ID: "incoming", WalletID: "wallet", Direction: Incoming, AmountMSat: 1000, Recipient: "customer"}, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(o.ID, o.Version, Submitted, 0); err != nil {
		t.Fatal(err)
	}
	before := futureOperation(t, s, o.ID)
	after, err := s.BindInvoice(before.ID, before.Version, strings.Repeat("a", 64), []byte("synthetic-verified-invoice"))
	if err != nil || after.State != Awaiting || after.UpdatedAt.Before(before.UpdatedAt) {
		t.Fatal("clock rollback corrupted invoice binding")
	}
	_ = s.Close()
	again, err := Open(dir)
	if err != nil {
		t.Fatal("incoming journal failed to reopen")
	}
	_ = again.Close()
}
