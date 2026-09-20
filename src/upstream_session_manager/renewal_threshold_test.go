package upstream_session_manager

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// renewalRecorder captures renewal callback invocations with their arguments.
type renewalRecorder struct {
	mu    sync.Mutex
	calls []struct {
		gateway string
		usage   uint64
	}
	ch chan struct{}
}

func newRenewalRecorder() *renewalRecorder {
	return &renewalRecorder{ch: make(chan struct{}, 16)}
}

func (r *renewalRecorder) callback(gateway string, usage uint64) error {
	r.mu.Lock()
	r.calls = append(r.calls, struct {
		gateway string
		usage   uint64
	}{gateway, usage})
	r.mu.Unlock()
	r.ch <- struct{}{}
	return nil
}

// waitForRenewal blocks until a renewal fires or the timeout elapses.
// Returns true if a renewal fired.
func (r *renewalRecorder) waitForRenewal(timeout time.Duration) bool {
	select {
	case <-r.ch:
		return true
	case <-time.After(timeout):
		return false
	}
}

// TestRenewalNotTriggeredWhenOffsetExceedsAllotment pins #430: with the
// default bytes config (preferred increment == renewal offset ==
// 131,100,000) against an upstream whose step_size quantizes the purchased
// allotment to 5 x 22,020,096 = 110,100,480 bytes, the renewal check must
// NOT fire at near-zero usage — remaining (110,091,264) is below the
// configured offset (131,100,000), but the session is essentially unused.
// The lab log showed exactly this firing an immediate +5 sats renewal.
func TestRenewalNotTriggeredWhenOffsetExceedsAllotment(t *testing.T) {
	const (
		defaultBytesRenewalOffset = 131_100_000
		purchasedAllotment        = 5 * 22_020_096 // 110,100,480
		labObservedUsage          = 9_216
	)

	rec := newRenewalRecorder()
	tracker := NewUpstreamUsageTracker(
		"192.168.1.1",
		defaultBytesRenewalOffset,
		rec.callback,
	)

	tracker.checkRenewal(labObservedUsage, purchasedAllotment)

	if rec.waitForRenewal(500 * time.Millisecond) {
		t.Fatalf(
			"renewal fired immediately: allotment=%d usage=%d remaining=%d with renewalOffset=%d — "+
				"every default-config bytes session double-purchases on startup (#430)",
			purchasedAllotment, labObservedUsage, purchasedAllotment-labObservedUsage, defaultBytesRenewalOffset,
		)
	}
}

// TestRenewalTriggeredNearLimit is the control: a session genuinely close
// to its allotment must still renew with the same tracker configuration.
func TestRenewalTriggeredNearLimit(t *testing.T) {
	const (
		defaultBytesRenewalOffset = 131_100_000
		purchasedAllotment        = 5 * 22_020_096 // 110,100,480
		nearLimitUsage            = 105_000_000    // remaining = 5,100,480
	)

	rec := newRenewalRecorder()
	tracker := NewUpstreamUsageTracker(
		"192.168.1.1",
		defaultBytesRenewalOffset,
		rec.callback,
	)

	tracker.checkRenewal(nearLimitUsage, purchasedAllotment)

	if !rec.waitForRenewal(2 * time.Second) {
		t.Fatalf(
			"renewal did not fire near limit: remaining=%d with renewalOffset=%d",
			purchasedAllotment-nearLimitUsage, defaultBytesRenewalOffset,
		)
	}
}

// TestRenewalBoundaryAtHalfAllotment pins the exact clamp factor (review
// finding F1 on the #430 fix): with the offset clamped to half the
// allotment, renewal must fire at usage == allotment/2 and must NOT fire
// one byte earlier. These two cases bracket the policy boundary so that
// any change to the factor (e.g. /3 or *2/3) flips at least one of them.
func TestRenewalBoundaryAtHalfAllotment(t *testing.T) {
	const (
		defaultBytesRenewalOffset = 131_100_000 // clamp binds: exceeds allotment/2
		purchasedAllotment        = 5 * 22_020_096
	)

	t.Run("must_not_renew_below_half_usage", func(t *testing.T) {
		rec := newRenewalRecorder()
		tracker := NewUpstreamUsageTracker("192.168.1.1", defaultBytesRenewalOffset, rec.callback)

		var usage uint64 = purchasedAllotment/2 - 1 // remaining = allotment/2 + 1
		tracker.checkRenewal(usage, purchasedAllotment)

		if rec.waitForRenewal(500 * time.Millisecond) {
			t.Fatalf(
				"renewal fired below the half-allotment boundary: usage=%d remaining=%d — the clamp factor has drifted past 1/2",
				usage, purchasedAllotment-usage,
			)
		}
	})

	t.Run("must_renew_at_half_usage", func(t *testing.T) {
		rec := newRenewalRecorder()
		tracker := NewUpstreamUsageTracker("192.168.1.1", defaultBytesRenewalOffset, rec.callback)

		var usage uint64 = purchasedAllotment / 2 // remaining = allotment/2, at the boundary
		tracker.checkRenewal(usage, purchasedAllotment)

		if !rec.waitForRenewal(2 * time.Second) {
			t.Fatalf(
				"renewal did not fire at the half-allotment boundary: usage=%d remaining=%d — the clamp factor has drifted below 1/2",
				usage, purchasedAllotment-usage,
			)
		}
	})
}

// clampLogCollector records log entries whose message mentions the clamp.
type clampLogCollector struct {
	mu      sync.Mutex
	matches []string
}

func (c *clampLogCollector) Levels() []logrus.Level { return logrus.AllLevels }

func (c *clampLogCollector) Fire(e *logrus.Entry) error {
	if strings.Contains(e.Message, "clamped") {
		c.mu.Lock()
		c.matches = append(c.matches, e.Message)
		c.mu.Unlock()
	}
	return nil
}

func (c *clampLogCollector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.matches)
}

// TestClampLogEmittedOnChangeNotPerPoll pins the observability contract of
// the clamp (review finding F2 on the #430 fix): the warning that the
// configured offset was overridden must fire when the clamp (re)binds or
// its effective value changes — never once per poll, which would spam the
// router log once per second for the whole lifetime of every default-config
// bytes session.
func TestClampLogEmittedOnChangeNotPerPoll(t *testing.T) {
	const (
		defaultBytesRenewalOffset = 131_100_000
		purchasedAllotment        = 5 * 22_020_096 // 110,100,480
		belowHalfUsage            = 1_000_000      // clamp binds, renewal must not fire
	)

	collector := &clampLogCollector{}
	prev := logrus.StandardLogger().ReplaceHooks(logrus.LevelHooks{})
	logrus.StandardLogger().AddHook(collector)
	defer logrus.StandardLogger().ReplaceHooks(prev)

	rec := newRenewalRecorder()
	tracker := NewUpstreamUsageTracker(
		"192.168.1.1",
		defaultBytesRenewalOffset,
		rec.callback,
	)

	// Five consecutive polls with the clamp binding and the same effective
	// offset: exactly one announcement.
	for i := 0; i < 5; i++ {
		tracker.checkRenewal(belowHalfUsage+uint64(i), purchasedAllotment)
	}
	if n := collector.count(); n != 1 {
		t.Fatalf("clamp log emitted %d times across 5 polls with unchanged effective offset, want exactly 1", n)
	}

	// Allotment doubles (renewal completed upstream): the effective offset
	// changes, so the clamp must be announced again — once, not per poll.
	doubledAllotment := uint64(purchasedAllotment * 2)
	for i := 0; i < 3; i++ {
		tracker.checkRenewal(belowHalfUsage+uint64(i), doubledAllotment)
	}
	if n := collector.count(); n != 2 {
		t.Fatalf("clamp log emitted %d times total after allotment change, want exactly 2 (one per distinct effective offset)", n)
	}

	// Clamp stops binding (offset fits within half the allotment): no new
	// announcements, and a later re-bind is announced again.
	hugeAllotment := uint64(1_000_000_000) // half = 500,000,000 > offset
	for i := 0; i < 3; i++ {
		tracker.checkRenewal(belowHalfUsage+uint64(i), hugeAllotment)
	}
	if n := collector.count(); n != 2 {
		t.Fatalf("clamp log emitted while clamp not binding, want still 2")
	}
	for i := 0; i < 2; i++ {
		tracker.checkRenewal(belowHalfUsage+uint64(i), purchasedAllotment)
	}
	if n := collector.count(); n != 3 {
		t.Fatalf("clamp re-bind after unbind was not announced, want 3 total")
	}
}
