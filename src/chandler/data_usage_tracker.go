package chandler

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// NewDataUsageTracker creates a new data-based usage tracker
func NewDataUsageTracker(interfaceName string) *DataUsageTracker {
	return &DataUsageTracker{
		interfaceName: interfaceName,
		done:          make(chan bool),
	}
}

// Start begins monitoring data usage for the session
func (d *DataUsageTracker) Start(session *ChandlerSession, chandler ChandlerInterface) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.session = session
	d.chandler = chandler

	// Get initial byte count for the interface
	initialBytes, err := d.getInterfaceBytes()
	if err != nil {
		return err
	}

	d.startBytes = initialBytes
	d.currentBytes = initialBytes
	d.triggered = make(map[float64]bool) // Initialize triggered map

	// Start monitoring with 5-second precision for data usage
	// Data usage needs polling since we can't predict when network traffic occurs
	d.ticker = time.NewTicker(500 * time.Millisecond)

	go d.monitor()

	logrus.WithFields(logrus.Fields{
		"upstream_pubkey": session.UpstreamTollgate.Advertisement.PubKey,
		"interface":       d.interfaceName,
		"start_bytes":     d.startBytes,
		"total_allotment": session.TotalAllotment,
		"thresholds":      d.thresholds,
	}).Info("Data usage tracker started")

	return nil
}

// Stop stops the data usage tracker
func (d *DataUsageTracker) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.ticker != nil {
		d.ticker.Stop()
	}

	select {
	case d.done <- true:
	default:
	}

	logrus.WithFields(logrus.Fields{
		"upstream_pubkey": d.session.UpstreamTollgate.Advertisement.PubKey,
		"total_usage":     d.GetCurrentUsage(),
	}).Info("Data usage tracker stopped")

	return nil
}

// GetCurrentUsage returns current data usage in bytes
func (d *DataUsageTracker) GetCurrentUsage() uint64 {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.currentBytes < d.startBytes {
		return 0 // Interface was reset
	}

	return d.currentBytes - d.startBytes
}

// UpdateUsage manually updates the current byte count
func (d *DataUsageTracker) UpdateUsage(amount uint64) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.currentBytes = amount
	return nil
}

// SetRenewalThresholds sets the thresholds for renewal callbacks
func (d *DataUsageTracker) SetRenewalThresholds(thresholds []float64) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.thresholds = make([]float64, len(thresholds))
	copy(d.thresholds, thresholds)

	return nil
}

// monitor runs the monitoring loop for data usage
func (d *DataUsageTracker) monitor() {
	triggeredThresholds := make(map[float64]bool)

	for {
		select {
		case <-d.done:
			return
		case <-d.ticker.C:
			d.updateCurrentBytes()
			d.checkThresholds(triggeredThresholds)
		}
	}
}

// updateCurrentBytes updates the current byte count from the interface
func (d *DataUsageTracker) updateCurrentBytes() {
	bytes, err := d.getInterfaceBytes()
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"interface": d.interfaceName,
			"error":     err,
		}).Warn("Failed to get interface bytes")
		return
	}

	d.mu.Lock()
	d.currentBytes = bytes
	d.mu.Unlock()
}

// checkThresholds checks if any renewal thresholds have been reached
func (d *DataUsageTracker) checkThresholds(triggered map[float64]bool) {
	d.mu.RLock()
	currentUsage := d.GetCurrentUsage()
	totalAllotment := d.session.TotalAllotment
	upstreamPubkey := d.session.UpstreamTollgate.Advertisement.PubKey
	thresholds := d.thresholds
	d.mu.RUnlock()

	if totalAllotment == 0 {
		return
	}

	usagePercent := float64(currentUsage) / float64(totalAllotment)

	for _, threshold := range thresholds {
		if usagePercent >= threshold && !triggered[threshold] {
			triggered[threshold] = true

			logrus.WithFields(logrus.Fields{
				"upstream_pubkey": upstreamPubkey,
				"threshold":       threshold,
				"usage_percent":   usagePercent * 100,
				"current_usage":   currentUsage,
				"total_allotment": totalAllotment,
			}).Info("Data usage threshold reached, triggering renewal")

			// Call the chandler's renewal handler
			go func(pubkey string, usage uint64) {
				err := d.chandler.HandleUpcomingRenewal(pubkey, usage)
				if err != nil {
					logrus.WithFields(logrus.Fields{
						"upstream_pubkey": pubkey,
						"error":           err,
					}).Error("Failed to handle upcoming renewal")
				}
			}(upstreamPubkey, currentUsage)
		}
	}
}

// getInterfaceBytes reads the current byte count for the interface from /proc/net/dev
// This function is designed to work on both 32-bit and 64-bit systems
func (d *DataUsageTracker) getInterfaceBytes() (uint64, error) {
	file, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, fmt.Errorf("failed to open /proc/net/dev: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	// Skip header lines
	if !scanner.Scan() || !scanner.Scan() {
		return 0, fmt.Errorf("failed to read /proc/net/dev header")
	}

	// Look for our interface
	for scanner.Scan() {
		line := scanner.Text()

		// Parse interface line: "  eth0: bytes packets errs drop..."
		colonIndex := strings.Index(line, ":")
		if colonIndex == -1 {
			continue
		}

		interfaceName := strings.TrimSpace(line[:colonIndex])
		if interfaceName != d.interfaceName {
			continue
		}

		// Parse the statistics after the colon
		stats := strings.Fields(line[colonIndex+1:])
		if len(stats) < 16 {
			return 0, fmt.Errorf("insufficient statistics for interface %s", d.interfaceName)
		}

		// /proc/net/dev format:
		// RX: bytes packets errs drop fifo frame compressed multicast
		// TX: bytes packets errs drop fifo colls carrier compressed

		// Get RX bytes (index 0) and TX bytes (index 8)
		rxBytes, err := parseUint64Safe(stats[0])
		if err != nil {
			return 0, fmt.Errorf("failed to parse RX bytes for %s: %w", d.interfaceName, err)
		}

		txBytes, err := parseUint64Safe(stats[8])
		if err != nil {
			return 0, fmt.Errorf("failed to parse TX bytes for %s: %w", d.interfaceName, err)
		}

		// Return total bytes (RX + TX)
		totalBytes := rxBytes + txBytes

		logrus.WithFields(logrus.Fields{
			"interface":   d.interfaceName,
			"rx_bytes":    rxBytes,
			"tx_bytes":    txBytes,
			"total_bytes": totalBytes,
		}).Debug("Updated interface byte counts")

		return totalBytes, nil
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("error reading /proc/net/dev: %w", err)
	}

	return 0, fmt.Errorf("interface %s not found in /proc/net/dev", d.interfaceName)
}

// parseUint64Safe safely parses a uint64 from string, handling 32-bit system limitations
func parseUint64Safe(s string) (uint64, error) {
	// strconv.ParseUint handles both 32-bit and 64-bit systems correctly
	return strconv.ParseUint(strings.TrimSpace(s), 10, 64)
}
