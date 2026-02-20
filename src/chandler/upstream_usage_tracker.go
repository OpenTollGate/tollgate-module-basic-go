package chandler

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/utils"
	"github.com/sirupsen/logrus"
)

// UpstreamUsageTracker polls the upstream gateway for usage information
// and triggers renewal when needed
// NOTE: Payment throttling and state is now handled in UpstreamSession.HandleRenewal()
type UpstreamUsageTracker struct {
	gatewayIP       string
	renewalOffset   uint64
	renewalCallback func(gatewayIP string, currentUsage uint64) error

	// State (protected by mu)
	totalAllotment     uint64
	lastUsage          uint64
	lastAllotment      uint64
	pollCount          int       // Track poll count for periodic info logging
	lastPaymentTrigger time.Time // Track when we last triggered a payment
	mu                 sync.RWMutex

	// Control
	ticker *time.Ticker
	done   chan struct{}
}

// NewUpstreamUsageTracker creates a new upstream usage tracker
func NewUpstreamUsageTracker(
	gatewayIP string,
	renewalOffset uint64,
	renewalCallback func(string, uint64) error,
) *UpstreamUsageTracker {
	return &UpstreamUsageTracker{
		gatewayIP:       gatewayIP,
		renewalOffset:   renewalOffset,
		renewalCallback: renewalCallback,
		totalAllotment:  0, // Will be set after first poll
		done:            make(chan struct{}),
	}
}

// Start begins polling the upstream gateway
func (u *UpstreamUsageTracker) Start() error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.ticker != nil {
		return fmt.Errorf("tracker already started")
	}

	// Poll every 1 second
	u.ticker = time.NewTicker(1 * time.Second)

	go u.monitor()

	logrus.WithFields(logrus.Fields{
		"gateway":        u.gatewayIP,
		"renewal_offset": u.renewalOffset,
	}).Info("⏱️  Upstream usage tracker started")

	return nil
}

// Stop stops the tracker
func (u *UpstreamUsageTracker) Stop() {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.ticker != nil {
		u.ticker.Stop()
		u.ticker = nil
	}

	select {
	case <-u.done:
		// Already closed
	default:
		close(u.done)
	}

	logrus.WithField("gateway", u.gatewayIP).Info("⏹️  Upstream usage tracker stopped")
}

// monitor polls the upstream gateway and triggers renewal
func (u *UpstreamUsageTracker) monitor() {
	for {
		select {
		case <-u.done:
			return
		case <-u.ticker.C:
			u.poll()
		}
	}
}

// poll fetches current usage from upstream
func (u *UpstreamUsageTracker) poll() {
	usage, allotment, err := u.fetchUpstreamUsage()
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"gateway": u.gatewayIP,
			"error":   err,
		}).Debug("Failed to fetch upstream usage")
		return
	}

	u.mu.Lock()
	u.lastUsage = usage
	u.lastAllotment = allotment
	u.pollCount++
	pollCount := u.pollCount
	previousAllotment := u.totalAllotment

	// Calculate percentage used
	var percentUsed float64
	if allotment > 0 {
		percentUsed = float64(usage) / float64(allotment) * 100
	}

	// Always debug log with human-readable format
	logrus.Debugf("Upstream usage for %s: %s / %s (%.1f%%)",
		u.gatewayIP,
		utils.BytesToHumanReadable(usage),
		utils.BytesToHumanReadable(allotment),
		percentUsed)

	// Info log every 5 seconds (5 polls)
	if pollCount%5 == 0 {
		logrus.Infof("Upstream usage for %s: %s / %s (%.1f%%)",
			u.gatewayIP,
			utils.BytesToHumanReadable(usage),
			utils.BytesToHumanReadable(allotment),
			percentUsed)
	}

	// Detect session state changes
	if allotment == 0 && previousAllotment > 0 {
		// Session expired
		logrus.WithFields(logrus.Fields{
			"gateway":            u.gatewayIP,
			"previous_allotment": previousAllotment,
		}).Info("⚠️  Session expired")
		u.totalAllotment = 0
	} else if allotment > 0 && previousAllotment == 0 {
		// New session created after expiration
		logrus.WithFields(logrus.Fields{
			"gateway":       u.gatewayIP,
			"new_allotment": allotment,
		}).Info("✅ New session created")
		u.totalAllotment = allotment
	} else if allotment > previousAllotment {
		// Allotment increased (renewal completed)
		logrus.WithFields(logrus.Fields{
			"gateway":       u.gatewayIP,
			"new_allotment": allotment,
		}).Info("📈 Allotment increased (renewal completed)")
		u.totalAllotment = allotment
	}
	u.mu.Unlock()

	// Check if we need renewal
	u.checkRenewal(usage, allotment)
}

// fetchUpstreamUsage fetches usage from upstream :2121/usage
func (u *UpstreamUsageTracker) fetchUpstreamUsage() (usage, allotment uint64, err error) {
	url := fmt.Sprintf("http://%s:2121/usage", u.gatewayIP)

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, err
	}

	// Parse "usage/allotment" format (e.g., "1048576/10485760" or "-1/-1")
	parts := strings.Split(strings.TrimSpace(string(body)), "/")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid usage response format: %s", string(body))
	}

	usageInt, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid usage value: %s", parts[0])
	}

	allotmentInt, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid allotment value: %s", parts[1])
	}

	// Handle -1/-1 (no session)
	if usageInt == -1 && allotmentInt == -1 {
		return 0, 0, nil // Return 0/0 to trigger initial payment
	}

	return uint64(usageInt), uint64(allotmentInt), nil
}

// checkRenewal checks if renewal is needed and triggers it
// Includes tracker-level throttling to prevent multiple concurrent payment attempts
func (u *UpstreamUsageTracker) checkRenewal(usage, allotment uint64) {
	u.mu.Lock()
	// Throttle at tracker level: don't trigger if we triggered recently (within 10 seconds)
	if time.Since(u.lastPaymentTrigger) < 10*time.Second {
		u.mu.Unlock()
		return
	}
	u.mu.Unlock()

	// If no session exists (0/0), trigger initial payment
	if usage == 0 && allotment == 0 {
		u.mu.Lock()
		u.lastPaymentTrigger = time.Now()
		u.mu.Unlock()

		logrus.WithField("gateway", u.gatewayIP).Info("💳 No session exists, triggering initial payment")

		// Trigger payment in goroutine (non-blocking)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					logrus.WithFields(logrus.Fields{
						"gateway": u.gatewayIP,
						"panic":   r,
					}).Error("🚨 PANIC in payment goroutine!")
				}
			}()

			if err := u.renewalCallback(u.gatewayIP, 0); err != nil {
				logrus.WithFields(logrus.Fields{
					"gateway": u.gatewayIP,
					"error":   err,
				}).Error("❌ Initial payment failed")
			}
		}()
		return
	}

	// Check if we need renewal (usage approaching allotment)
	if allotment > 0 {
		remaining := int64(allotment) - int64(usage)
		if remaining <= int64(u.renewalOffset) {
			u.mu.Lock()
			u.lastPaymentTrigger = time.Now()
			u.mu.Unlock()

			logrus.WithFields(logrus.Fields{
				"gateway":   u.gatewayIP,
				"usage":     usage,
				"allotment": allotment,
				"remaining": remaining,
			}).Info("💳 Renewal threshold reached, triggering renewal")

			// Trigger renewal in goroutine (non-blocking)
			go func() {
				defer func() {
					if r := recover(); r != nil {
						logrus.WithFields(logrus.Fields{
							"gateway": u.gatewayIP,
							"panic":   r,
						}).Error("🚨 PANIC in renewal goroutine!")
					}
				}()

				if err := u.renewalCallback(u.gatewayIP, usage); err != nil {
					logrus.WithFields(logrus.Fields{
						"gateway": u.gatewayIP,
						"error":   err,
					}).Error("❌ Renewal failed")
				}
			}()
		}
	}
}

// GetCurrentUsage returns the last known usage
func (u *UpstreamUsageTracker) GetCurrentUsage() uint64 {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.lastUsage
}

// GetTotalAllotment returns the total allotment
func (u *UpstreamUsageTracker) GetTotalAllotment() uint64 {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.totalAllotment
}
