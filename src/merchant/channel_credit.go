package merchant

// Channel credit on the access path: a Spilman channel must be able to pay for a
// client's bytes.
//
// # Why this file exists
//
// The Go rail could verify a payment and serve /spilman/*, and the bytes a client
// consumed were still judged purely against `session.Allotment` — the token-purchased
// number. So a channel bought a client exactly nothing, which is the same gap the
// Rust side had before its access-path wiring. This is the Go equivalent.
//
// # The shape
//
//	effective = RetailAllotment + channelVerifiedBytes(ChannelID)
//
// `Allotment` stays the EFFECTIVE ceiling. A channel RAISES A FLOOR and never lowers
// it, and with no channel bound the two numbers are equal — which is what makes the
// rail-off path behave exactly as it did before channels existed rather than merely
// similarly.
//
// # The rule that matters most
//
// A channel that cannot be READ is not a channel that is PAID. Every failure mode —
// unknown channel, unreadable ledger, unconvertible unit — resolves to "no credit
// added", never to "assume the retail allotment is still fine". A failure to read a
// payment is not evidence of payment. The retail half survives every failure, because
// that money was really spent and revoking it would be theft.
//
// # Ordering
//
// Credit is read from the ledger, which only ever stores a balance that
// `ChannelLedger.RecordPayment` accepted AFTER upstream's validator verified the
// sender's signature. An unverified balance is never recorded, so it can never be
// read here. This function does not need to re-check a signature: it cannot see
// anything unsigned.

import (
	"context"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/spilman"
)

// channelCreditBytes returns the bytes a channel's verified balance buys, and
// whether that answer is trustworthy.
//
// `ok=false` means "we cannot say" — an unknown channel, an unreadable ledger, or a
// balance whose unit will not convert. It is deliberately distinct from a trustworthy
// zero: both deny the channel half, but keeping them apart means a future caller
// cannot mistake an error for a confirmed zero.
func channelCreditBytes(rail *spilman.SellerRail, channelID string) (uint64, bool) {
	if rail == nil || channelID == "" {
		return 0, false
	}
	status, err := rail.Status(context.Background(), channelID)
	if err != nil {
		// Unknown channel and unreadable ledger both land here. Neither is credit.
		return 0, false
	}
	if status.PaymentCount == 0 {
		// Registered but never paid: no balance row. That is a trustworthy zero, but
		// it buys nothing, so the caller adds nothing either way. Reported as ok so
		// a caller can distinguish "paid nothing yet" from "could not read".
		return 0, true
	}
	millisats, err := spilman.RawUnitsToMillisats(status.Unit, status.Balance)
	if err != nil {
		return 0, false
	}
	// The same rate the rail bills at, so the access path and the authorisation check
	// cannot disagree about how many bytes a millisat buys. Saturating, because a
	// large balance must not wrap into a small ceiling.
	rate := uint64(spilman.TollgateParityInBytesPerMillisat)
	if millisats > 0 && rate > ^uint64(0)/millisats {
		return ^uint64(0), true
	}
	return millisats * rate, true
}

// effectiveAllotment returns the ceiling a session should be judged against.
//
// Pure with respect to the session: it reads the channel and returns a number. That
// keeps the interesting cases (rail off, unknown channel, exhausted channel) testable
// without a running merchant, a sweep, or a gate.
func effectiveAllotment(session *CustomerSession, rail *spilman.SellerRail) uint64 {
	if session == nil {
		return 0
	}
	retail := session.RetailAllotment
	if retail == 0 {
		// A session created before this field existed, or by a path that set only
		// Allotment. Reading 0 here would revoke access the customer paid for, so the
		// effective number IS the retail number in that case.
		retail = session.Allotment
	}
	if session.ChannelID == "" {
		// The retail path: unexamined, unchanged.
		return retail
	}
	credit, _ := channelCreditBytes(rail, session.ChannelID)
	return retail + credit
}

// SetChannelRail installs the Spilman payee rail on this merchant.
//
// Injected rather than looked up: the merchant package must not reach into package
// main, and a nil rail is a meaningful state (the rail is off) rather than an
// oversight. Called once at construction; the rail is itself goroutine-safe.
func (m *Merchant) SetChannelRail(rail *spilman.SellerRail) {
	m.channelRail = rail
}

// BindChannel binds a live session to a Spilman channel and returns the effective
// ceiling.
//
// # Why this exists
//
// It is the ONLY production writer of `CustomerSession.ChannelID`. Without it the
// field is always empty, `effectiveAllotment` always returns at its first branch, and
// a channel buys a client nothing — code that is correct and unreachable, which a
// cold review found on the Rust side and which is the same defect here.
//
// # What it deliberately does not do
//
// It grants nothing. `Allotment` is left alone; the 2-second sweep recomputes it from
// the channel's VERIFIED balance. A binding to a channel with no credit is therefore
// worth zero bytes, which is right: the binding is a pointer, not a payment.
//
// `ok=false` means there is no live session for that MAC. A binding must never be how
// access is created, or anyone able to reach the route could conjure a session.
func (m *Merchant) BindChannel(macAddress, channelID string) (uint64, bool) {
	macAddress = NormalizeMACAddress(macAddress)
	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()
	session, exists := m.customerSessions[macAddress]
	if !exists {
		return 0, false
	}
	session.ChannelID = channelID
	// The effective ceiling does NOT change here: until the sweep has read the
	// channel, the session must not be granted more than it has actually bought.
	return session.Allotment, true
}

// spilmanRail returns the installed rail, or nil when the rail is off.
func (m *Merchant) spilmanRail() *spilman.SellerRail {
	return m.channelRail
}

// refreshEffectiveAllotment recomputes `Allotment` from the channel and returns it.
//
// Called from the sweep, which already runs every 2 seconds and already holds the
// session. Recomputing per tick is the point: a payment that landed since the last
// tick extends access WITHOUT a new grant, and an exhausted channel stops granting.
// Both fall out of asking the ledger each time rather than remembering a number from
// grant time.
func refreshEffectiveAllotment(session *CustomerSession, rail *spilman.SellerRail) uint64 {
	if session == nil {
		return 0
	}
	session.Allotment = effectiveAllotment(session, rail)
	return session.Allotment
}
