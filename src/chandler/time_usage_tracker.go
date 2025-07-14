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

	t.session = session
	t.chandler = chandler
	t.startTime = time.Now()
	t.pausedTime = 0
	t.currentIncrement = session.TotalAllotment

	// Set up timers for each threshold
	t.setupThresholdTimers()

	logrus.WithFields(logrus.Fields{
		"upstream_pubkey": session.UpstreamTollgate.Advertisement.PubKey,
		"total_allotment": session.TotalAllotment,
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
		"upstream_pubkey": t.session.UpstreamTollgate.Advertisement.PubKey,
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
	if t.session != nil {
		t.setupThresholdTimers()
	}

	return nil
}

// UpdateAllotment is called when a renewal payment is made
func (t *TimeUsageTracker) UpdateAllotment(newIncrement uint64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.currentIncrement = newIncrement
	t.setupThresholdTimers()
	return nil
}

// setupThresholdTimers creates timers for each threshold
func (t *TimeUsageTracker) setupThresholdTimers() {
	// Stop existing timers
	for _, timer := range t.timers {
		timer.Stop()
	}
	t.timers = nil

	if t.session == nil || t.session.TotalAllotment == 0 {
		return
	}

	// Sort thresholds to ensure we process them in order
	sortedThresholds := make([]float64, len(t.thresholds))
	copy(sortedThresholds, t.thresholds)
	sort.Float64s(sortedThresholds)

	for _, threshold := range sortedThresholds {
		var duration time.Duration

		// If it's the first purchase, the renewal is based on the total allotment.
		// For subsequent renewals, it's based on the increment from the current usage.
		if t.currentIncrement == t.session.TotalAllotment {
			duration = time.Duration(uint64(float64(t.session.TotalAllotment)*threshold)) * time.Millisecond
		} else {
			thresholdFromNow := uint64(float64(t.currentIncrement) * threshold)
			duration = time.Duration(thresholdFromNow) * time.Millisecond
		}

		// we already passed this
		if duration <= 0 {
			continue
		}

		timer := time.AfterFunc(duration, func(th float64) func() {
			return func() {
				t.handleThresholdReached(th)
			}
		}(threshold))

		t.timers = append(t.timers, timer)

		logrus.WithFields(logrus.Fields{
			"upstream_pubkey": t.session.UpstreamTollgate.Advertisement.PubKey,
			"threshold":       threshold,
			"duration_ms":     duration.Milliseconds(),
		}).Debug("Set up threshold timer")
	}
}

// handleThresholdReached is called when a threshold timer fires
func (t *TimeUsageTracker) handleThresholdReached(threshold float64) {
	t.mu.RLock()
	currentUsage := t.GetCurrentUsage()
	upstreamPubkey := t.session.UpstreamTollgate.Advertisement.PubKey
	totalAllotment := t.session.TotalAllotment
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
