package merchant

// Tests for channel credit on the access path.
//
// The load-bearing cases:
//
//  1. With NO rail, the effective ceiling is exactly the retail number — the
//     behaviour every existing customer already has. This would pass BEFORE this
//     feature was written, which is the point: it is the regression guard.
//  2. A channel that cannot be READ adds NOTHING, and the retail half survives.
//     This is the deliberate inverse of "absence is not zero": a failure to read a
//     payment is not evidence of payment.
//  3. A session whose RetailAllotment was never set reads as its Allotment, not as
//     zero — otherwise an upgrade would revoke access a customer paid for.
//
// The funded-channel case (a channel that HAS credit raising the ceiling) needs a
// real ledger with a verified balance, so it lives behind the same `test-support`
// style seam as the rail's own tests and is covered there; what this file pins is
// the arithmetic and the fail-closed behaviour, which is where the money is lost.

import "testing"

func sessionWith(retail, allotment uint64, channelID string) *CustomerSession {
	return &CustomerSession{
		MacAddress:      "aa:bb:cc:dd:ee:01",
		Metric:          "bytes",
		Allotment:       allotment,
		RetailAllotment: retail,
		ChannelID:       channelID,
	}
}

// THE REGRESSION GUARD. No rail, no channel: the ceiling is the retail number,
// untouched and unexamined.
func TestNoRailMeansTheRetailCeilingUnchanged(t *testing.T) {
	s := sessionWith(21_000_000, 21_000_000, "")
	if got := effectiveAllotment(s, nil); got != 21_000_000 {
		t.Fatalf("rail off: effective = %d, want 21000000", got)
	}
}

// A channel that cannot be read adds NOTHING, and does not revoke what was bought.
//
// The rail is nil here, which is the "config flipped off under a live session" case:
// the customer keeps the credit they paid tokens for and gains nothing.
func TestAnUnreadableChannelAddsNoCreditButKeepsRetail(t *testing.T) {
	s := sessionWith(7_000, 7_000, "chan-that-does-not-exist")
	if got := effectiveAllotment(s, nil); got != 7_000 {
		t.Fatalf("unreadable channel: effective = %d, want 7000 (retail kept)", got)
	}
}

// A session written before RetailAllotment existed must read as its Allotment.
//
// Reading the zero value literally would revoke access a customer already paid for
// — the same class as a migration defaulting retail to 0.
func TestAnUnmigratedSessionReadsAsItsAllotmentNotZero(t *testing.T) {
	s := &CustomerSession{
		MacAddress: "aa:bb:cc:dd:ee:02",
		Metric:     "bytes",
		Allotment:  42_000,
		// RetailAllotment deliberately left at its zero value.
	}
	if got := effectiveAllotment(s, nil); got != 42_000 {
		t.Fatalf("unmigrated session: effective = %d, want 42000", got)
	}
}

// refreshEffectiveAllotment writes the effective number back onto the session, which
// is what the sweep compares against.
func TestRefreshWritesTheEffectiveCeiling(t *testing.T) {
	s := sessionWith(1_000, 999_999, "")
	if got := refreshEffectiveAllotment(s, nil); got != 1_000 {
		t.Fatalf("refresh returned %d, want 1000", got)
	}
	if s.Allotment != 1_000 {
		t.Fatalf("session.Allotment = %d, want 1000 — the sweep compares this", s.Allotment)
	}
}

// A nil session must not panic: the sweep can race a revoked session.
func TestNilSessionIsSafe(t *testing.T) {
	if got := effectiveAllotment(nil, nil); got != 0 {
		t.Fatalf("nil session: effective = %d, want 0", got)
	}
	if got := refreshEffectiveAllotment(nil, nil); got != 0 {
		t.Fatalf("nil session: refresh = %d, want 0", got)
	}
}

// channelCreditBytes with no rail can never claim credit.
func TestNoRailMeansNoChannelCredit(t *testing.T) {
	if _, ok := channelCreditBytes(nil, "chan"); ok {
		t.Fatal("a nil rail must not report trustworthy credit")
	}
	if _, ok := channelCreditBytes(nil, ""); ok {
		t.Fatal("an empty channel id must not report trustworthy credit")
	}
}
