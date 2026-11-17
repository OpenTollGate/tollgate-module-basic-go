package merchant

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
)

// The paid-through watermark, and the top-up that extends it
// (docs/architecture/incremental-topup-watermark-decision.md).
//
// A session has ONE quantity that says when its paid entitlement ends: the
// paid-through watermark, the value of the session's own meter at which the
// customer has used what they paid for. Both meters measure from the session's
// anchor (StartTime): `milliseconds` as elapsed time, `bytes` as the carried
// total consumed since the session's first baseline.
//
// That makes the top-up rule a statement about the ANCHOR, not about the
// amount:
//
//   - a top-up of a LIVE session may only GROW the watermark (Allotment) and
//     must leave StartTime where it is. Rewriting the anchor re-bases the meter
//     to zero, which hands back every second (or every byte) the customer has
//     already spent — the second purchase then buys its allotment on top of the
//     refunded one instead of adding it.
//   - a purchase after the watermark was reached is NOT a top-up: the spent
//     record is retired and the purchase starts a fresh clock, which is what
//     TestRepurchaseAfterExpiryDoesNotResurrectStaleAllotment already pins.
//
// The harness is the renewal suite's: a stub wallet whose Receive always
// succeeds, and a fake ndsctl on PATH so the valve seam's only observable
// effects (auth/deauth, counters) are assertable.

// backdateSession moves a session's anchor into the past, modelling elapsed paid
// time without waiting for it. The caller is responsible for keeping the age
// inside the allotment when it means the session to stay live.
func backdateSession(t *testing.T, m *Merchant, macAddress string, age time.Duration) int64 {
	t.Helper()

	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()

	session, exists := m.customerSessions[macAddress]
	if !exists {
		t.Fatalf("no session for %s to backdate", macAddress)
	}
	session.StartTime = time.Now().Add(-age).Unix()
	return session.StartTime
}

// usageParts splits a /usage answer into its used and allotment halves.
func usageParts(t *testing.T, m *Merchant, macAddress string) (uint64, uint64) {
	t.Helper()

	usage, err := m.GetUsage(macAddress)
	if err != nil {
		t.Fatalf("GetUsage(%s): %v", macAddress, err)
	}
	parts := strings.Split(usage, "/")
	if len(parts) != 2 {
		t.Fatalf("GetUsage(%s) = %q, want used/allotment", macAddress, usage)
	}
	used, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		t.Fatalf("GetUsage(%s) used half %q is not a number: %v", macAddress, parts[0], err)
	}
	allotment, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		t.Fatalf("GetUsage(%s) allotment half %q is not a number: %v", macAddress, parts[1], err)
	}
	return used, allotment
}

// TestLiveTopUpExtendsTheWatermarkWithoutRestartingTheClock is invariant 1 on
// the metric where the defect was observable: a `milliseconds` session's meter
// is elapsed time since StartTime, so an AddAllotment that rewrites StartTime to
// now refunds everything already spent. Two 600 s purchases on a session with
// 120 s already spent must leave 1200 s of paid time running from the FIRST
// purchase — 1080 s left, not a fresh 1200 s.
func TestLiveTopUpExtendsTheWatermarkWithoutRestartingTheClock(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	ndsctl.setRegistered(t, true)
	m, wallet := newRenewalMerchant(t, "milliseconds")

	firstEvent, err := m.PurchaseSession("cashuBfirst", renewalMAC)
	if err != nil {
		t.Fatalf("first purchase: %v", err)
	}
	if firstEvent.Kind != 1022 {
		t.Fatalf("first purchase returned kind %d (code=%q), want 1022", firstEvent.Kind, noticeErrorCode(t, firstEvent))
	}

	// The customer keeps browsing: 120 s of the first purchase is spent.
	const spent = 120 * time.Second
	anchor := backdateSession(t, m, renewalMAC, spent)

	usedBefore, allotmentBefore := usageParts(t, m, renewalMAC)
	if allotmentBefore != renewalFreshAllotment {
		t.Fatalf("allotment after the first purchase = %d, want %d", allotmentBefore, renewalFreshAllotment)
	}
	if usedBefore < uint64(spent.Milliseconds()) {
		t.Fatalf("usage after %s = %d ms, want at least %d", spent, usedBefore, spent.Milliseconds())
	}

	// The top-up: the customer buys again while still live.
	topUpEvent, err := m.PurchaseSession("cashuBtopup", renewalMAC)
	if err != nil {
		t.Fatalf("top-up purchase: %v", err)
	}
	if topUpEvent.Kind != 1022 {
		t.Fatalf("top-up returned kind %d (code=%q), want 1022 (a top-up of a live session is served, not refused)",
			topUpEvent.Kind, noticeErrorCode(t, topUpEvent))
	}
	if got := wallet.received(); got != 2 {
		t.Fatalf("wallet Receive called %d times, want 2 (the top-up must reach the money path)", got)
	}

	session, err := m.GetSession(renewalMAC)
	if err != nil {
		t.Fatalf("GetSession after the top-up: %v", err)
	}
	if session.Allotment != 2*renewalFreshAllotment {
		t.Fatalf("allotment after the top-up = %d, want %d (the watermark only grows)",
			session.Allotment, 2*renewalFreshAllotment)
	}
	if session.StartTime != anchor {
		t.Fatalf("StartTime after the top-up = %d, want %d unchanged: rewriting the anchor re-bases the meter to zero and hands back the %s already spent",
			session.StartTime, anchor, spent)
	}

	usedAfter, allotmentAfter := usageParts(t, m, renewalMAC)
	if allotmentAfter != 2*renewalFreshAllotment {
		t.Fatalf("GetUsage allotment after the top-up = %d, want %d", allotmentAfter, 2*renewalFreshAllotment)
	}
	if usedAfter+uint64(2*time.Second.Milliseconds()) < usedBefore {
		t.Fatalf("usage went backwards across the top-up (%d ms -> %d ms): the top-up refunded paid time", usedBefore, usedAfter)
	}
	if usedAfter < uint64(spent.Milliseconds()) {
		t.Fatalf("usage after the top-up = %d ms, want at least the %d ms already spent", usedAfter, spent.Milliseconds())
	}

	// The horizon must have moved by exactly one allotment, not by the allotment
	// plus the already-spent time.
	remainingBefore := allotmentBefore - usedBefore
	remainingAfter := allotmentAfter - usedAfter
	gained := remainingAfter - remainingBefore
	wantGained := uint64(renewalFreshAllotment)
	if gained+uint64(2*time.Second.Milliseconds()) < wantGained ||
		gained > wantGained+uint64(2*time.Second.Milliseconds()) {
		t.Fatalf("the top-up bought %d ms of paid time, want %d (one allotment — not the allotment plus the %s already spent)",
			gained, wantGained, spent)
	}

	// And the paid-through instant is still anchored to the first purchase.
	wantPaidThrough := anchor + int64(2*renewalFreshAllotment/1000)
	if got := sessionPaidThroughUnix(session); got != wantPaidThrough {
		t.Fatalf("paid-through instant after the top-up = %d, want %d (anchor + the total paid allotment)", got, wantPaidThrough)
	}

	if state, err := m.GetSessionState(renewalMAC); err != nil || state != SessionStateActive {
		t.Fatalf("GetSessionState after the top-up = %q, %v; want %q, nil", state, err, SessionStateActive)
	}
}

// TestLiveTopUpDoesNotResetTheByteMeter is invariant 2: the byte meter is the
// session's whole ledger (Consumed plus the current attachment's usage since its
// baseline), and a top-up re-opens the gate, which records a fresh baseline. The
// ledger must not go backwards across that: the customer must not be handed the
// bytes they have already used.
func TestLiveTopUpDoesNotResetTheByteMeter(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	ndsctl.setRegistered(t, true)
	m, _ := newRenewalMerchant(t, "bytes")

	// bytes: one purchase buys 10 sats × 60000 = 600000 bytes of allotment.
	if _, err := m.PurchaseSession("cashuBfirst", renewalMAC); err != nil {
		t.Fatalf("first purchase: %v", err)
	}

	// The customer downloads 100 kB more than the baseline the gate recorded
	// when it opened. The sweep records it as this attachment's high-water mark,
	// which is what a re-baselined meter falls back to.
	const downloaded = 1124 // kB; the fake's gate-open baseline is 1024 kB
	ndsctl.setClientKB(t, downloaded, 512)
	m.checkDataUsage()

	usedBefore, allotmentBefore := usageParts(t, m, renewalMAC)
	if usedBefore < 100*1024 {
		t.Fatalf("usage after 100 kB past the baseline = %d bytes, want at least %d", usedBefore, 100*1024)
	}
	if allotmentBefore != renewalFreshAllotment {
		t.Fatalf("allotment after the first purchase = %d, want %d", allotmentBefore, renewalFreshAllotment)
	}

	// Top up while live. This re-opens the gate and therefore records a new
	// metering baseline (valve.OpenGate).
	if _, err := m.PurchaseSession("cashuBtopup", renewalMAC); err != nil {
		t.Fatalf("top-up purchase: %v", err)
	}

	usedAfter, allotmentAfter := usageParts(t, m, renewalMAC)
	if allotmentAfter != 2*renewalFreshAllotment {
		t.Fatalf("allotment after the top-up = %d, want %d", allotmentAfter, 2*renewalFreshAllotment)
	}
	if usedAfter < usedBefore {
		t.Fatalf("the byte meter went backwards across the top-up (%d -> %d bytes): the fresh baseline refunded data the customer already used",
			usedBefore, usedAfter)
	}

	// The follow-up sweep must gauge the extended watermark, not close a gate the
	// customer has just paid to keep open.
	ndsctl.setClientKB(t, downloaded+100, 512)
	deauthsBefore := ndsctl.count(t, "DEAUTH ")
	m.checkDataUsage()
	if got := ndsctl.count(t, "DEAUTH ") - deauthsBefore; got != 0 {
		t.Fatalf("the sweep deauthorised a topped-up session %d time(s): %d bytes used of %d paid is inside the watermark",
			got, usedAfter, allotmentAfter)
	}
	if state, err := m.GetSessionState(renewalMAC); err != nil || state != SessionStateActive {
		t.Fatalf("GetSessionState after the top-up and a sweep = %q, %v; want %q, nil", state, err, SessionStateActive)
	}
}

// TestWatermarkIsTheOneEnforcementRule pins the rule every enforcement site
// shares: the meter is inside the watermark strictly below it, and reached AT it.
// The two places that enforce (the 2-second byte sweep and the milliseconds
// expiry lookup) used to phrase this separately; a boundary that disagrees
// between them is a session that keeps an open gate one sweep too long, or one
// that is closed while it still has paid time.
func TestWatermarkIsTheOneEnforcementRule(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name     string
		session  *CustomerSession
		usageMS  uint64
		wantLive bool
	}{
		{
			name:     "below the watermark is live",
			session:  &CustomerSession{Metric: "milliseconds", Allotment: 600000, StartTime: now.Unix()},
			usageMS:  599999,
			wantLive: true,
		},
		{
			name:     "at the watermark is reached",
			session:  &CustomerSession{Metric: "milliseconds", Allotment: 600000, StartTime: now.Unix()},
			usageMS:  600000,
			wantLive: false,
		},
		{
			name:     "past the watermark is reached",
			session:  &CustomerSession{Metric: "milliseconds", Allotment: 600000, StartTime: now.Unix()},
			usageMS:  600001,
			wantLive: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionWithinWatermark(tc.usageMS, tc.session); got != tc.wantLive {
				t.Fatalf("sessionWithinWatermark(%d) = %v, want %v", tc.usageMS, got, tc.wantLive)
			}

			// The lookup path must agree with the rule, exactly.
			elapsed := time.Duration(tc.usageMS) * time.Millisecond
			expired := sessionHasExpired(tc.session, time.Unix(tc.session.StartTime, 0).Add(elapsed))
			if expired == tc.wantLive {
				t.Fatalf("sessionHasExpired at %d ms = %v, disagrees with the watermark rule (live=%v)",
					tc.usageMS, expired, tc.wantLive)
			}

			// And the timer arms at the same instant the rule calls reached.
			wantPaidThrough := tc.session.StartTime + int64(tc.session.Allotment/1000)
			if got := sessionPaidThroughUnix(tc.session); got != wantPaidThrough {
				t.Fatalf("sessionPaidThroughUnix = %d, want %d", got, wantPaidThrough)
			}
			// The timer path must arm at that instant, not at "anchor + something".
			armAt := time.Unix(sessionPaidThroughUnix(tc.session), 0)
			if !armAt.After(time.Unix(tc.session.StartTime, 0)) {
				t.Fatalf("the paid-through instant %s is not after the anchor %d", armAt, tc.session.StartTime)
			}
		})
	}

	// A bytes session carries the same rule, with the carried total inside the
	// watermark: the sweep compares the whole ledger against it.
	bytesSession := &CustomerSession{Metric: "bytes", Allotment: 1000, Consumed: 400}
	if !sessionWithinWatermark(900, bytesSession) {
		t.Fatal("400 carried + 500 live = 900 of 1000 bytes must be inside the watermark")
	}
	if sessionWithinWatermark(1000, bytesSession) {
		t.Fatal("1000 of 1000 bytes must be at the watermark, i.e. enforced")
	}
}

// TestTopUpDoesNotSurviveAsASecondGateForTheSameSession: a top-up is not a
// re-authorisation of a different client. The gate for the session's address is
// opened once and then extended by the timer horizon; a top-up must not leave a
// second tracked gate behind for the same MAC (valve.TrackedGates is the
// module's own view of what it still enforces).
func TestTopUpLeavesExactlyOneTrackedGate(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	ndsctl.setRegistered(t, true)
	m, _ := newRenewalMerchant(t, "milliseconds")

	if _, err := m.PurchaseSession("cashuBfirst", renewalMAC); err != nil {
		t.Fatalf("first purchase: %v", err)
	}
	if _, err := m.PurchaseSession("cashuBtopup", renewalMAC); err != nil {
		t.Fatalf("top-up purchase: %v", err)
	}

	tracked := 0
	for _, mac := range valve.TrackedGates() {
		if mac == renewalMAC {
			tracked++
		}
	}
	if tracked != 1 {
		t.Fatalf("valve.TrackedGates holds %d gate(s) for %s after one purchase and one top-up, want 1",
			tracked, renewalMAC)
	}
}
