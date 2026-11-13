package paymentjournal

import (
	"errors"
	"testing"
	"time"
)

func TestSubmitOutgoingPersistsUnknownOnRestart(t *testing.T) {
	s, dir := openTest(t)
	op, err := s.Prepare(intent("send"), Budget{LimitMSat: 1100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SubmitOutgoing(op.ID, op.Version, Budget{LimitMSat: 1100}); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	got, err := again.Get(op.ID)
	if err != nil || got.State != Unknown {
		t.Fatal("submitted send was not durably recovered as unknown", got, err)
	}
	if _, err = again.SubmitOutgoing(got.ID, got.Version, Budget{LimitMSat: 1100}); err == nil {
		t.Fatal("unknown send was resubmitted")
	}
	if _, err = again.Prepare(intent("next"), Budget{LimitMSat: 1100}); err == nil {
		t.Fatal("recovered submission released its reservation")
	}
}

func TestSubmitOutgoingRefusesFailedPersistence(t *testing.T) {
	s, dir := openTest(t)
	op, err := s.Prepare(intent("send"), Budget{LimitMSat: 1100})
	if err != nil {
		t.Fatal(err)
	}
	s.rename = func(string, string) error { return errors.New("synthetic rename failure") }
	if _, err = s.SubmitOutgoing(op.ID, op.Version, Budget{LimitMSat: 1100}); err == nil {
		t.Fatal("submission succeeded without durable state")
	}
	if _, err = s.Get(op.ID); err == nil {
		t.Fatal("failed writer remained usable")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	got, err := again.Get(op.ID)
	if err != nil || got.State != Prepared || got.Version != op.Version {
		t.Fatal("pre-rename failure changed the durable intent", got, err)
	}
}

func TestSubmitOutgoingRechecksCurrentBudgetWithoutDoubleCounting(t *testing.T) {
	s, _ := openTest(t)
	a, err := s.Prepare(intent("first"), Budget{LimitMSat: 2200})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Prepare(intent("second"), Budget{LimitMSat: 2200}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SubmitOutgoing(a.ID, a.Version, Budget{LimitMSat: 1100}); err == nil {
		t.Fatal("reduced limit ignored outstanding reservation")
	}
	got, err := s.Get(a.ID)
	if err != nil || got.State != Prepared || got.Version != a.Version {
		t.Fatal(got, err)
	}
	got, err = s.SubmitOutgoing(a.ID, a.Version, Budget{LimitMSat: 2200})
	if err != nil || got.State != Submitted {
		t.Fatal("target counted twice or submission failed", got, err)
	}
	if _, err = s.SubmitOutgoing(got.ID, got.Version, Budget{LimitMSat: 2200}); err == nil {
		t.Fatal("submitted operation resubmitted")
	}
}

func TestSubmitOutgoingKeepsUnknownReservationsAcrossPeriods(t *testing.T) {
	s, _ := openTest(t)
	a, err := s.Prepare(intent("unknown"), Budget{LimitMSat: 2200})
	if err != nil {
		t.Fatal(err)
	}
	a, err = s.SubmitOutgoing(a.ID, a.Version, Budget{LimitMSat: 2200})
	if err != nil {
		t.Fatal(err)
	}
	a, err = s.Advance(a.ID, a.Version, Unknown, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Prepare(intent("pending"), Budget{LimitMSat: 2200})
	if err != nil {
		t.Fatal(err)
	}
	budget := Budget{LimitMSat: 1100, Since: time.Now().Add(time.Hour)}
	if _, err = s.SubmitOutgoing(b.ID, b.Version, budget); err == nil {
		t.Fatal("unknown reserve released at period boundary")
	}
	if _, err = s.Advance(a.ID, a.Version, Paid, 50); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SubmitOutgoing(b.ID, b.Version, budget); err != nil {
		t.Fatal("resolved old history should be outside explicit period", err)
	}
}

// Outgoing monetary submission has exactly one public route: SubmitOutgoing
// with the current period budget. Advance must refuse it.
func TestAdvanceRefusesOutgoingButSubmitsIncoming(t *testing.T) {
	s, _ := openTest(t)
	op, err := s.Prepare(intent("advance-refused"), Budget{LimitMSat: 1100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Advance(op.ID, op.Version, Submitted, 0); err == nil {
		t.Fatal("Advance submitted an outgoing payment")
	}
	got, err := s.Get(op.ID)
	if err != nil || got.State != Prepared || got.Version != op.Version {
		t.Fatal("refused Advance still mutated the prepared operation", got, err)
	}
	incoming := Operation{ID: "incoming-submit", WalletID: "wallet-a", Direction: Incoming, AmountMSat: 1000, Recipient: "client-a"}
	in, err := s.Prepare(incoming, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Advance(in.ID, in.Version, Submitted, 0); err != nil {
		t.Fatal("Advance refused an incoming submission")
	}
}

// A budget tightened below the reservation after Prepare must be enforced at
// submission; Prepare alone never authorizes a send under an old budget.
func TestSubmitOutgoingRefusesBudgetTightenedAfterPrepare(t *testing.T) {
	s, _ := openTest(t)
	op, err := s.Prepare(intent("tightened"), Budget{LimitMSat: 1100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SubmitOutgoing(op.ID, op.Version, Budget{LimitMSat: 1099}); err == nil {
		t.Fatal("submission admitted under a budget tightened below the reservation")
	}
	got, err := s.Get(op.ID)
	if err != nil || got.State != Prepared || got.Version != op.Version {
		t.Fatal("refused submission still mutated the operation", got, err)
	}
	if _, err = s.SubmitOutgoing(op.ID, op.Version, Budget{LimitMSat: 1100}); err != nil {
		t.Fatal("submission refused under the meaningful current budget", err)
	}
}

// A verified settlement fee above the pre-send reservation is recorded
// truthfully, survives reopen, and exhausts the budget for later payments:
// the reservation is a pre-send headroom check, never a retroactive
// permission to suppress a verified expense or create false headroom.
func TestVerifiedActualFeeOverReserveIsDurableAndExhaustsBudget(t *testing.T) {
	s, dir := openTest(t)
	budget := Budget{LimitMSat: 1101}
	op, err := s.Prepare(intent("fee-over-reserve"), budget)
	if err != nil {
		t.Fatal(err)
	}
	op, err = s.SubmitOutgoing(op.ID, op.Version, budget)
	if err != nil {
		t.Fatal(err)
	}
	op, err = s.Advance(op.ID, op.Version, Paid, 101)
	if err != nil {
		t.Fatal("verified actual fee above reservation was refused", err)
	}
	if op.ActualFeeMSat != 101 {
		t.Fatal("verified actual fee was not recorded truthfully")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	got, err := again.Get(op.ID)
	if err != nil || got.State != Paid || got.ActualFeeMSat != 101 {
		t.Fatal("verified actual fee did not survive reopen", got, err)
	}
	next := intent("next-principal")
	next.AmountMSat = 1
	next.FeeReserveMSat = 0
	if _, err = again.Prepare(next, budget); err == nil {
		t.Fatal("reserve-capped accounting created false headroom for the next payment")
	}
}

func TestSubmitOutgoingRefusesIncomingStaleAndClosed(t *testing.T) {
	s, _ := openTest(t)
	a, err := s.Prepare(intent("send"), Budget{LimitMSat: 1100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SubmitOutgoing(a.ID, a.Version+1, Budget{LimitMSat: 1100}); err == nil {
		t.Fatal("stale CAS accepted")
	}
	b := intent("receive")
	b.Direction = Incoming
	b.FeeReserveMSat = 0
	b, err = s.Prepare(b, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SubmitOutgoing(b.ID, b.Version, Budget{LimitMSat: 1100}); err == nil {
		t.Fatal("incoming submitted by spending method")
	}
	s.Close()
	if _, err = s.SubmitOutgoing(a.ID, a.Version, Budget{LimitMSat: 1100}); err == nil {
		t.Fatal("closed store submitted send")
	}
}
