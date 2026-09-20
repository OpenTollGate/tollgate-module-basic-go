package upstream_session_manager

import (
	"sync"
	"testing"
	"time"
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
