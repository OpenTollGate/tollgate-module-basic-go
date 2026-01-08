package chandler

import (
	"time"

	"github.com/sirupsen/logrus"
)

// NewTimeUsageTracker creates a new time-based usage tracker
func NewTimeUsageTracker() *TimeUsageTracker {
	return &TimeUsageTracker{
		done: make(chan bool),
	}
}

// Start begins monitoring time usage for the session
func (t *TimeUsageTracker) Start(session *ChandlerSession, chandler ChandlerInterface) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.upstreamPubkey = session.UpstreamTollgate.Advertisement.PubKey
	t.chandler = chandler
	t.startTime = time.Now()
	t.pausedTime = 0
	t.totalAllotment = session.TotalAllotment
	t.currentIncrement = session.TotalAllotment

	// Set up timer for renewal offset
	t.setupRenewalTimer(0) // Start with 0 usage at initialization

	logrus.WithFields(logrus.Fields{
		"upstream_pubkey": t.upstreamPubkey,
		"total_allotment": t.totalAllotment,
		"renewal_offset":  t.renewalOffset,
	}).Info("Time usage tracker started with renewal timer")

	return nil
}

// Stop stops the usage tracker
func (t *TimeUsageTracker) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Stop active timer
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}

	select {
	case t.done <- true:
	default:
	}

	logrus.WithFields(logrus.Fields{
		"upstream_pubkey": t.upstreamPubkey,
		"total_usage":     t.GetCurrentUsage(),
	}).Info("Time usage tracker stopped")

	return nil
}

// GetCurrentUsage returns current usage in milliseconds
func (t *TimeUsageTracker) GetCurrentUsage() uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.startTime.IsZero() {
		return 0
	}

	elapsed := time.Since(t.startTime) - t.pausedTime
	return uint64(elapsed.Milliseconds())
}

// UpdateUsage manually updates usage (not applicable for time tracking)
func (t *TimeUsageTracker) UpdateUsage(amount uint64) error {
	// Time tracking is automatic, manual updates don't apply
	return nil
}

// SetRenewalOffset sets the offset for renewal callbacks
func (t *TimeUsageTracker) SetRenewalOffset(offset uint64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.renewalOffset = offset

	// If we're already started, reset timer
	if t.upstreamPubkey != "" {
		currentUsage := t.GetCurrentUsage()
		t.setupRenewalTimer(currentUsage)
	}

	return nil
}

// SessionChanged is called when the session is updated
func (t *TimeUsageTracker) SessionChanged(session *ChandlerSession) error {
	// Get current usage before calling continuing to avoid deadlock
	currentUsage := t.GetCurrentUsage()
	t.mu.Lock()
	defer t.mu.Unlock()

	// Calculate the increment from the previous total allotment to the new total allotment
	previousTotalAllotment := t.totalAllotment
	t.totalAllotment = session.TotalAllotment
	t.currentIncrement = t.totalAllotment - previousTotalAllotment

	logrus.WithFields(logrus.Fields{
		"upstream_pubkey":          t.upstreamPubkey,
		"previous_total_allotment": previousTotalAllotment,
		"new_total_allotment":      t.totalAllotment,
		"current_increment":        t.currentIncrement,
	}).Info("Session changed, updating usage tracker")

	t.setupRenewalTimer(currentUsage)
	return nil
}

// setupRenewalTimer creates a timer for the renewal offset
func (t *TimeUsageTracker) setupRenewalTimer(currentUsage uint64) {
	logrus.WithFields(logrus.Fields{
		"upstream_pubkey":   t.upstreamPubkey,
		"total_allotment":   t.totalAllotment,
		"current_increment": t.currentIncrement,
		"renewal_offset":    t.renewalOffset,
	}).Info("Setting up renewal timer")

	// Stop existing timer
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}

	if t.upstreamPubkey == "" || t.totalAllotment == 0 {
		logrus.WithFields(logrus.Fields{
			"upstream_pubkey": t.upstreamPubkey,
			"total_allotment": t.totalAllotment,
		}).Warn("Cannot setup renewal timer: upstreamPubkey is empty or total allotment is 0")
		return
	}

	// Calculate renewal point: allotment - offset
	var renewalPoint uint64
	if t.totalAllotment > t.renewalOffset {
		renewalPoint = t.totalAllotment - t.renewalOffset
	} else {
		// If offset is larger than allotment, renew immediately
		renewalPoint = 0
	}

	logrus.WithFields(logrus.Fields{
		"upstream_pubkey":   t.upstreamPubkey,
		"renewal_point":     renewalPoint,
		"current_usage":     currentUsage,
		"current_increment": t.currentIncrement,
		"total_allotment":   t.totalAllotment,
		"renewal_offset":    t.renewalOffset,
	}).Info("Renewal timer calculation details")

	// Calculate remaining time until renewal point
	var duration time.Duration
	if renewalPoint > currentUsage {
		duration = time.Duration(renewalPoint-currentUsage) * time.Millisecond
		logrus.WithFields(logrus.Fields{
			"upstream_pubkey": t.upstreamPubkey,
			"duration_ms":     duration.Milliseconds(),
		}).Debug("Setting renewal timer for duration")
	} else {
		// We've already passed the renewal point, trigger immediately
		logrus.WithFields(logrus.Fields{
			"upstream_pubkey": t.upstreamPubkey,
			"renewal_point":   renewalPoint,
			"current_usage":   currentUsage,
		}).Info("Already passed renewal point, triggering renewal immediately")

		go t.handleRenewalReached()
		return
	}

	t.timer = time.AfterFunc(duration, func() {
		t.handleRenewalReached()
	})

	logrus.WithFields(logrus.Fields{
		"upstream_pubkey":   t.upstreamPubkey,
		"renewal_point":     renewalPoint,
		"current_usage":     currentUsage,
		"current_increment": t.currentIncrement,
		"total_allotment":   t.totalAllotment,
		"duration_ms":       duration.Milliseconds(),
	}).Debug("Set up renewal timer")
}

// handleRenewalReached is called when the renewal timer fires
func (t *TimeUsageTracker) handleRenewalReached() {
	t.mu.RLock()
	currentUsage := t.GetCurrentUsage()
	upstreamPubkey := t.upstreamPubkey
	totalAllotment := t.totalAllotment
	renewalOffset := t.renewalOffset
	t.mu.RUnlock()

	logrus.WithFields(logrus.Fields{
		"upstream_pubkey": upstreamPubkey,
		"current_usage":   currentUsage,
		"total_allotment": totalAllotment,
		"renewal_offset":  renewalOffset,
	}).Info("Time usage renewal offset reached, triggering renewal")

	// Call the chandler's renewal handler
	go func() {
		err := t.chandler.HandleUpcomingRenewal(upstreamPubkey, currentUsage)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"upstream_pubkey": upstreamPubkey,
				"error":           err,
			}).Error("Failed to handle upcoming renewal")
		}
	}()
}
