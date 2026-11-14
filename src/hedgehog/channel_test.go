package hedgehog

import (
	"testing"
	"time"
)

// newTestChannel builds a channel with deterministic keys for the state tests.
func newTestChannel(t *testing.T) *Channel {
	t.Helper()
	payerX, err := XOnlyOf(seq32(0x11))
	if err != nil {
		t.Fatal(err)
	}
	payeeX, err := XOnlyOf(seq32(0x22))
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewChannel(payerX, payeeX, 2016,
		b20(0xa1), b20(0xb2))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func seq32(b byte) []byte {
	s := make([]byte, 32)
	for i := range s {
		s[i] = b
	}
	return s
}

func b20(b byte) []byte {
	s := make([]byte, 20)
	for i := range s {
		s[i] = b
	}
	return s
}

// TestPayerProposesWithNoCounterparty is the test that encodes the whole reason
// this protocol was chosen: the PAYER produces a spendable-by-the-payee update
// while the payee is completely absent. No counterparty object is touched, and
// no network is involved.
//
// The PR #944 research originally claimed this direction was impossible and
// retracted the claim after executing it. This test is the regression guard.
func TestPayerProposesWithNoCounterparty(t *testing.T) {
	c := newTestChannel(t)
	payer, err := NewChannelState(c, RolePayer, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("before: %s", payer.Describe())

	// The payee is NOT constructed, NOT contacted, NOT reachable. That is the
	// point: nothing below references it.
	u, err := payer.Propose(250_000, "deadbeef", []byte("payer-signature-bytes"))
	if err != nil {
		t.Fatalf("payer must be able to propose alone: %v", err)
	}
	if u.Seq != 1 {
		t.Errorf("first proposal must be seq 1, got %d", u.Seq)
	}
	if u.PayerBalance != 750_000 || u.PayeeBalance != 250_000 {
		t.Errorf("balances wrong: payer=%d payee=%d", u.PayerBalance, u.PayeeBalance)
	}
	if payer.State != StateProposed {
		t.Errorf("state should be proposed, got %s", payer.State)
	}

	// CRITICAL: the enforceable state must NOT have moved. Until the payee
	// accepts, a pending update is not settlement -- treating it as such would
	// enforce a state the counterparty never agreed to.
	if payer.Current.Seq != 0 {
		t.Errorf("enforceable state moved before acceptance: seq=%d", payer.Current.Seq)
	}
	if payer.Current.PayerBalance != 1_000_000 {
		t.Errorf("enforceable balance moved before acceptance: %d", payer.Current.PayerBalance)
	}
	t.Logf("after : %s", payer.Describe())
	t.Log("payer produced a cheque with NO counterparty involvement")
}

// TestPayeeAcceptsLaterAlone is the other half: the payee completes the update
// using only the cheque bytes, with the payer gone.
func TestPayeeAcceptsLaterAlone(t *testing.T) {
	c := newTestChannel(t)

	payer, _ := NewChannelState(c, RolePayer, 1_000_000)
	cheque, err := payer.Propose(250_000, "txid-cafe", []byte("payer-sig"))
	if err != nil {
		t.Fatal(err)
	}

	// The payer is now offline forever. Everything below uses only the cheque.
	payee, err := NewChannelState(c, RolePayee, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := payee.Accept(cheque, []byte("payee-sig")); err != nil {
		t.Fatalf("payee must accept alone: %v", err)
	}
	if payee.State != StateAccepted {
		t.Errorf("state should be accepted, got %s", payee.State)
	}
	if payee.Current.PayeeBalance != 250_000 {
		t.Errorf("payee balance should be 250000, got %d", payee.Current.PayeeBalance)
	}
	if len(payee.Current.PayeeSignature) == 0 {
		t.Error("acceptance must record our signature")
	}
	t.Logf("payee completed alone: %s", payee.Describe())
}

// TestPayeeRejectsStaleUpdate guards the ordering invariant: an older update must
// never supersede a newer one, or a party could replay a favourable past state.
func TestPayeeRejectsStaleUpdate(t *testing.T) {
	c := newTestChannel(t)
	payer, _ := NewChannelState(c, RolePayer, 1_000_000)

	first, _ := payer.Propose(100_000, "t1", []byte("s1"))
	second, _ := payer.Propose(200_000, "t2", []byte("s2"))

	payee, _ := NewChannelState(c, RolePayee, 1_000_000)
	if err := payee.Accept(second, []byte("ps2")); err != nil {
		t.Fatal(err)
	}
	// Replaying the FIRST update must fail.
	if err := payee.Accept(first, []byte("ps1")); err == nil {
		t.Fatal("payee accepted a stale update -- replay protection is broken")
	} else {
		t.Logf("stale update correctly rejected: %v", err)
	}
}

// TestRoleEnforcement stops a participant performing the other's operation.
func TestRoleEnforcement(t *testing.T) {
	c := newTestChannel(t)
	payee, _ := NewChannelState(c, RolePayee, 1_000_000)
	if _, err := payee.Propose(1000, "t", []byte("s")); err == nil {
		t.Error("payee must not be able to propose")
	}
	payer, _ := NewChannelState(c, RolePayer, 1_000_000)
	if err := payer.Accept(&Update{Seq: 1}, []byte("s")); err == nil {
		t.Error("payer must not be able to accept")
	}
}

// TestProposeRejectsOverspend covers the balance limit.
func TestProposeRejectsOverspend(t *testing.T) {
	c := newTestChannel(t)
	payer, _ := NewChannelState(c, RolePayer, 1_000)
	if _, err := payer.Propose(1_001, "t", []byte("s")); err == nil {
		t.Error("proposing more than the balance must fail")
	}
	if _, err := payer.Propose(0, "t", []byte("s")); err == nil {
		t.Error("zero-amount proposal must fail")
	}
}

// TestProposeRequiresSignature is the safety property that makes a cheque safe to
// transmit as plaintext: an UNSIGNED proposal must not exist, and the payee must
// refuse one. A proposal without a signature is not a payment.
func TestProposeRequiresSignature(t *testing.T) {
	c := newTestChannel(t)
	payer, _ := NewChannelState(c, RolePayer, 1_000)
	if _, err := payer.Propose(500, "t", nil); err == nil {
		t.Error("a proposal with no signature must be rejected")
	}
	payee, _ := NewChannelState(c, RolePayee, 1_000)
	unsigned := &Update{Seq: 1, PayerBalance: 500, PayeeBalance: 500}
	if err := payee.Accept(unsigned, []byte("ps")); err == nil {
		t.Error("payee must refuse to accept an unsigned update")
	}
}

// TestWatchWindowIsTheTimeObligation pins the D5 finding in code: the CSV delay
// IS the period during which somebody must watch the chain.
func TestWatchWindowIsTheTimeObligation(t *testing.T) {
	c := newTestChannel(t)
	st, _ := NewChannelState(c, RolePayer, 1_000_000)
	if st.CSVBlocks() != 2016 {
		t.Fatalf("csv blocks: got %d", st.CSVBlocks())
	}
	want := 2016 * 10 * time.Minute
	if st.WatchWindow() != want {
		t.Errorf("watch window: got %s, want %s", st.WatchWindow(), want)
	}
	t.Logf("time obligation: %s during which SOME party must observe the chain. "+
		"For a TollGate purchase that duty lands on the router, which is online.",
		st.WatchWindow())
}

// TestCloseRequiresAcceptedState prevents settling a channel on an unaccepted
// state, which would let one party exit on terms the other never agreed to.
func TestCloseRequiresAcceptedState(t *testing.T) {
	c := newTestChannel(t)
	payer, _ := NewChannelState(c, RolePayer, 1_000_000)
	if err := payer.Close(); err == nil {
		t.Error("closing an open (never-updated) channel cooperatively must fail")
	}
	if _, err := payer.Propose(100, "t", []byte("s")); err != nil {
		t.Fatal(err)
	}
	if err := payer.Close(); err == nil {
		t.Error("closing a merely PROPOSED channel must fail -- use force-close")
	}
}
