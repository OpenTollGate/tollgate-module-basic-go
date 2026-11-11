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
	a, err = s.Advance(a.ID, a.Version, Submitted, 0)
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
