package paymentjournal

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestPaidFeePendingRequiresSettlementAndUnlocksOnResolution(t *testing.T) {
	s, dir := openTest(t)
	budget := Budget{LimitMSat: 2200}
	op, err := s.Prepare(intent("paid-fee"), budget)
	if err != nil {
		t.Fatal(err)
	}
	op, err = s.SubmitOutgoing(op.ID, op.Version, budget)
	if err != nil {
		t.Fatal(err)
	}
	op, err = s.RecordOutgoingPaid(op.ID, op.Version)
	if err != nil {
		t.Fatal("record settlement:", err)
	}
	if op.State != PaidFeePending || op.ActualFeeMSat != 0 {
		t.Fatalf("settled payment state = %s fee=%d, want %s fee=0", op.State, op.ActualFeeMSat, PaidFeePending)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	got, err := s.Get(op.ID)
	if err != nil || got.State != PaidFeePending || got.ActualFeeMSat != 0 {
		t.Fatalf("PaidFeePending did not survive reopen: %+v %v", got, err)
	}

	next := intent("blocked")
	if _, err = s.Prepare(next, budget); !errors.Is(err, ErrUnresolvedFee) {
		t.Fatalf("new outgoing prepare = %v, want ErrUnresolvedFee", err)
	}
	incoming := Operation{ID: "incoming-ok", WalletID: "wallet-a", Direction: Incoming, AmountMSat: 1000, Recipient: "client-a"}
	if _, err = s.Prepare(incoming, Budget{}); err != nil {
		t.Fatalf("incoming work must remain available: %v", err)
	}
	other := intent("other-wallet")
	other.WalletID = "wallet-b"
	if _, err = s.Prepare(other, budget); err != nil {
		t.Fatalf("other wallet domain should remain available: %v", err)
	}

	if _, err = s.ResolveOutgoingFee(op.ID, op.Version+1, 0); err == nil {
		t.Fatal("stale resolution version accepted")
	}
	if _, err = s.ResolveOutgoingFee(op.ID, op.Version, math.MaxInt64+1); err == nil {
		t.Fatal("unrepresentable fee accepted")
	}
	if got, err = s.Get(op.ID); err != nil || got.State != PaidFeePending {
		t.Fatalf("refused resolution changed the paid marker: %+v %v", got, err)
	}

	got, err = s.ResolveOutgoingFee(op.ID, op.Version, 0)
	if err != nil {
		t.Fatal("explicit zero fee:", err)
	}
	if got.State != Paid || got.ActualFeeMSat != 0 {
		t.Fatalf("resolved state = %s fee=%d, want paid/0", got.State, got.ActualFeeMSat)
	}
	if _, err = s.Prepare(next, budget); err != nil {
		t.Fatalf("resolved zero-fee hold did not release: %v", err)
	}
}

func TestPaidFeePendingHoldsPreparedSubmissionAcrossPeriods(t *testing.T) {
	s, _ := openTest(t)
	budget := Budget{LimitMSat: 2200}
	a, err := s.Prepare(intent("paid-a"), budget)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Prepare(intent("prepared-b"), budget)
	if err != nil {
		t.Fatal(err)
	}
	a, err = s.SubmitOutgoing(a.ID, a.Version, budget)
	if err != nil {
		t.Fatal(err)
	}
	a, err = s.RecordOutgoingPaid(a.ID, a.Version)
	if err != nil {
		t.Fatal(err)
	}

	future := Budget{LimitMSat: 1100, Since: time.Now().Add(time.Hour)}
	if _, err = s.SubmitOutgoing(b.ID, b.Version, future); !errors.Is(err, ErrUnresolvedFee) {
		t.Fatalf("prepared submission under unresolved fee = %v, want ErrUnresolvedFee", err)
	}
	if _, err = s.ResolveOutgoingFee(a.ID, a.Version, 50); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SubmitOutgoing(b.ID, b.Version, future); err != nil {
		t.Fatalf("resolved history should be outside the explicit period: %v", err)
	}
}

func TestPaidFeePendingOverReserveRecordsFullCost(t *testing.T) {
	s, dir := openTest(t)
	budget := Budget{LimitMSat: 1101}
	op, err := s.Prepare(intent("over-reserve"), budget)
	if err != nil {
		t.Fatal(err)
	}
	op, err = s.SubmitOutgoing(op.ID, op.Version, budget)
	if err != nil {
		t.Fatal(err)
	}
	op, err = s.RecordOutgoingPaid(op.ID, op.Version)
	if err != nil {
		t.Fatal(err)
	}
	op, err = s.ResolveOutgoingFee(op.ID, op.Version, 1101)
	if err != nil {
		t.Fatal("verified over-reserve fee was refused:", err)
	}
	if op.ActualFeeMSat != 1101 {
		t.Fatalf("actual fee = %d, want 1101", op.ActualFeeMSat)
	}
	next := intent("after-over-reserve")
	next.AmountMSat = 1
	next.FeeReserveMSat = 0
	if _, err = s.Prepare(next, budget); err == nil {
		t.Fatal("over-reserve cost manufactured budget headroom")
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
	if err != nil || got.State != Paid || got.ActualFeeMSat != 1101 {
		t.Fatalf("full cost did not survive reopen: %+v %v", got, err)
	}
	if _, err = again.Prepare(next, budget); err == nil {
		t.Fatal("reopened over-reserve cost manufactured headroom")
	}
}

func TestAdvanceCannotReachOutgoingPaidStates(t *testing.T) {
	s, _ := openTest(t)
	budget := Budget{LimitMSat: 2200}
	op, err := s.Prepare(intent("no-bypass"), budget)
	if err != nil {
		t.Fatal(err)
	}
	op, err = s.SubmitOutgoing(op.ID, op.Version, budget)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Advance(op.ID, op.Version, Paid, 0); err == nil {
		t.Fatal("Advance reached outgoing Paid")
	}
	if _, err = s.Advance(op.ID, op.Version, PaidFeePending, 0); err == nil {
		t.Fatal("Advance reached PaidFeePending")
	}
	op, err = s.RecordOutgoingPaid(op.ID, op.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Advance(op.ID, op.Version, Paid, 0); err == nil {
		t.Fatal("Advance downgraded PaidFeePending to Paid")
	}
	if _, err = s.Advance(op.ID, op.Version, Failed, 0); err == nil {
		t.Fatal("Advance erased a paid marker")
	}
	op, err = s.ResolveOutgoingFee(op.ID, op.Version, 50)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Advance(op.ID, op.Version, PaidFeePending, 0); err == nil {
		t.Fatal("Advance rewound Paid to PaidFeePending")
	}
}

func TestRecordOutgoingPaidRefusesWrongDirectionOrState(t *testing.T) {
	s, _ := openTest(t)
	budget := Budget{LimitMSat: 2200}
	prepared, err := s.Prepare(intent("prepared"), budget)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordOutgoingPaid(prepared.ID, prepared.Version); err == nil {
		t.Fatal("RecordOutgoingPaid accepted a prepared operation")
	}
	incoming := Operation{ID: "incoming", WalletID: "wallet-a", Direction: Incoming, AmountMSat: 1000, Recipient: "client-a"}
	in, err := s.Prepare(incoming, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	in, err = s.Advance(in.ID, in.Version, Submitted, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordOutgoingPaid(in.ID, in.Version); err == nil {
		t.Fatal("RecordOutgoingPaid accepted an incoming operation")
	}
	if _, err = s.RecordOutgoingPaid(prepared.ID, prepared.Version+1); err == nil {
		t.Fatal("RecordOutgoingPaid accepted a stale version")
	}
}
