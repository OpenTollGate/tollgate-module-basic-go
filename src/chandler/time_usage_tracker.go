package chandler

import (
	"sort"
	"time"

	"github.com/sirupsen/logrus"
)

// NewTimeUsageTracker creates a new time-based usage tracker
func NewTimeUsageTracker() *TimeUsageTracker {
	return &TimeUsageTracker{
		done:   make(chan bool),
		timers: make([]*time.Timer, 0),
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

	// Set up timers for each threshold
	t.setupThresholdTimers(0) // Start with 0 usage at initialization

	logrus.WithFields(logrus.Fields{
		"upstream_pubkey": t.upstreamPubkey,
		"total_allotment": t.totalAllotment,
		"thresholds":      t.thresholds,
	}).Info("Time usage tracker started with threshold timers")

	return nil
}

// Stop stops the usage tracker
func (t *TimeUsageTracker) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Stop all active timers
	for _, timer := range t.timers {
		timer.Stop()
	}
	t.timers = nil

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

// SetRenewalThresholds sets the thresholds for renewal callbacks
func (t *TimeUsageTracker) SetRenewalThresholds(thresholds []float64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.thresholds = make([]float64, len(thresholds))
	copy(t.thresholds, thresholds)

	// If we're already started, reset timers
	if t.upstreamPubkey != "" {
		currentUsage := t.GetCurrentUsage()
		t.setupThresholdTimers(currentUsage)
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

	t.setupThresholdTimers(currentUsage)
	return nil
}

// setupThresholdTimers creates timers for each threshold
func (t *TimeUsageTracker) setupThresholdTimers(currentUsage uint64) {
	logrus.WithFields(logrus.Fields{
		"upstream_pubkey":   t.upstreamPubkey,
		"total_allotment":   t.totalAllotment,
		"current_increment": t.currentIncrement,
		"thresholds":        t.thresholds,
		"existing_timers":   len(t.timers),
	}).Info("Setting up threshold timers")

	// Stop existing timers
	for _, timer := range t.timers {
		timer.Stop()
	}
	t.timers = nil

	if t.upstreamPubkey == "" || t.totalAllotment == 0 {
		logrus.WithFields(logrus.Fields{
			"upstream_pubkey": t.upstreamPubkey,
			"total_allotment": t.totalAllotment,
		}).Warn("Cannot setup threshold timers: upstreamPubkey is empty or total allotment is 0")
		return
	}

	// Sort thresholds to ensure we process them in order
	sortedThresholds := make([]float64, len(t.thresholds))
	copy(sortedThresholds, t.thresholds)
	sort.Float64s(sortedThresholds)

	for _, threshold := range sortedThresholds {
		var thresholdPoint uint64

		// Calculate the threshold point based on the current increment
		if t.currentIncrement == t.totalAllotment {
			// First purchase: threshold is based on total allotment
			thresholdPoint = uint64(float64(t.totalAllotment) * threshold)
		} else {
			// Renewal: threshold is at the end of previous allotment + 80% of current increment
			previousAllotment := t.totalAllotment - t.currentIncrement
			thresholdPoint = previousAllotment + uint64(float64(t.currentIncrement)*threshold)
		}

		logrus.WithFields(logrus.Fields{
			"upstream_pubkey":   t.upstreamPubkey,
			"threshold":         threshold,
			"threshold_point":   thresholdPoint,
			"current_usage":     currentUsage,
			"current_increment": t.currentIncrement,
			"total_allotment":   t.totalAllotment,
		}).Info("Timer calculation details")

		// Calculate remaining time until threshold
		var duration time.Duration
		if thresholdPoint > currentUsage {
			duration = time.Duration(thresholdPoint-currentUsage) * time.Millisecond
			logrus.WithFields(logrus.Fields{
				"upstream_pubkey": t.upstreamPubkey,
				"threshold":       threshold,
				"duration_ms":     duration.Milliseconds(),
			}).Debug("❗️ Setting timer for duration")
		} else {
			// We've already passed this threshold, skip it
			logrus.WithFields(logrus.Fields{
				"upstream_pubkey": t.upstreamPubkey,
				"threshold":       threshold,
				"threshold_point": thresholdPoint,
				"current_usage":   currentUsage,
			}).Info("❗️ Skipping threshold timer: already passed")
			continue
		}

		timer := time.AfterFunc(duration, func(th float64) func() {
			return func() {
				t.handleThresholdReached(th)
			}
		}(threshold))

		t.timers = append(t.timers, timer)

		logrus.WithFields(logrus.Fields{
			"upstream_pubkey":   t.upstreamPubkey,
			"threshold":         threshold,
			"threshold_point":   thresholdPoint,
			"current_usage":     currentUsage,
			"current_increment": t.currentIncrement,
			"total_allotment":   t.totalAllotment,
			"duration_ms":       duration.Milliseconds(),
		}).Debug("Set up threshold timer")
	}
}

// handleThresholdReached is called when a threshold timer fires
func (t *TimeUsageTracker) handleThresholdReached(threshold float64) {
	t.mu.RLock()
	currentUsage := t.GetCurrentUsage()
	upstreamPubkey := t.upstreamPubkey
	totalAllotment := t.totalAllotment
	t.mu.RUnlock()

	logrus.WithFields(logrus.Fields{
		"upstream_pubkey": upstreamPubkey,
		"threshold":       threshold,
		"current_usage":   currentUsage,
		"total_allotment": totalAllotment,
	}).Info("Time usage threshold reached, triggering renewal")

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
