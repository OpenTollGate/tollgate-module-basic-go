package chandler

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/utils"
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

	d.upstreamPubkey = session.UpstreamTollgate.Advertisement.PubKey
	d.chandler = chandler
	d.upstreamIP = session.UpstreamTollgate.GatewayIP

	// Get initial byte count for the interface (for local tracking)
	initialBytes, err := getInterfaceBytes(d.interfaceName)
	if err != nil {
		return err
	}

	d.startBytes = initialBytes
	d.currentBytes = initialBytes
	d.totalAllotment = session.TotalAllotment
	d.currentIncrement = session.TotalAllotment
	d.renewalInProgress = false
	d.lastInfoLog = time.Now()

	// Poll upstream every 1 second
	d.ticker = time.NewTicker(1 * time.Second)

	go d.monitor()

	logrus.WithFields(logrus.Fields{
		"upstream_pubkey": d.upstreamPubkey,
		"upstream_ip":     d.upstreamIP,
		"interface":       d.interfaceName,
		"start_bytes":     utils.BytesToHumanReadable(d.startBytes),
		"total_allotment": utils.BytesToHumanReadable(d.totalAllotment),
		"renewal_offset":  utils.BytesToHumanReadable(d.renewalOffset),
	}).Info("Data usage tracker started with upstream polling")

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
		"upstream_pubkey": d.upstreamPubkey,
		"total_usage":     utils.BytesToHumanReadable(d.GetCurrentUsage()),
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

// SetRenewalOffset sets the offset for renewal callbacks
func (d *DataUsageTracker) SetRenewalOffset(offset uint64) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.renewalOffset = offset

	return nil
}

// SessionChanged is called when the session is updated
func (d *DataUsageTracker) SessionChanged(session *ChandlerSession) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Calculate the increment from the previous total allotment to the new total allotment
	previousTotalAllotment := d.totalAllotment
	d.totalAllotment = session.TotalAllotment
	d.currentIncrement = d.totalAllotment - previousTotalAllotment

	// Reset renewal trigger for the new increment
	d.renewalInProgress = false

	logrus.WithFields(logrus.Fields{
		"upstream_pubkey":          d.upstreamPubkey,
		"previous_total_allotment": utils.BytesToHumanReadable(previousTotalAllotment),
		"new_total_allotment":      utils.BytesToHumanReadable(d.totalAllotment),
		"current_increment":        utils.BytesToHumanReadable(d.currentIncrement),
	}).Info("Session changed, updating data usage tracker")

	return nil
}

// monitor runs the monitoring loop for data usage
func (d *DataUsageTracker) monitor() {
	for {
		select {
		case <-d.done:
			return
		case <-d.ticker.C:
			// Update local measurements (for reference only)
			d.updateCurrentBytes()

			// Poll upstream for actual usage
			d.pollUpstreamUsage()

			// Check if renewal is needed based on upstream data
			d.checkRenewalOffset()
		}
	}
}

// updateCurrentBytes updates the current byte count from the interface
func (d *DataUsageTracker) updateCurrentBytes() {
	bytes, err := getInterfaceBytes(d.interfaceName)
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

// pollUpstreamUsage polls the upstream gateway's /usage API
func (d *DataUsageTracker) pollUpstreamUsage() {
	d.mu.RLock()
	upstreamIP := d.upstreamIP
	upstreamPubkey := d.upstreamPubkey
	d.mu.RUnlock()

	url := fmt.Sprintf("http://%s:2121/usage", upstreamIP)

	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"upstream_ip": upstreamIP,
			"error":       err,
		}).Debug("Failed to poll upstream usage API")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logrus.WithFields(logrus.Fields{
			"upstream_ip": upstreamIP,
			"status_code": resp.StatusCode,
		}).Debug("Upstream usage API returned non-OK status")
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"upstream_ip": upstreamIP,
			"error":       err,
		}).Debug("Failed to read upstream usage response")
		return
	}

	// Parse plaintext response in format "usage/allotment"
	bodyStr := strings.TrimSpace(string(body))
	parts := strings.Split(bodyStr, "/")
	if len(parts) != 2 {
		logrus.WithFields(logrus.Fields{
			"upstream_ip": upstreamIP,
			"body":        bodyStr,
		}).Debug("Invalid upstream usage response format (expected usage/allotment)")
		return
	}

	usage, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"upstream_ip": upstreamIP,
			"error":       err,
			"body":        bodyStr,
		}).Debug("Failed to parse usage value")
		return
	}

	allotment, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"upstream_ip": upstreamIP,
			"error":       err,
			"body":        bodyStr,
		}).Debug("Failed to parse allotment value")
		return
	}

	// Check if session has ended (indicated by -1/-1)
	if usage == -1 && allotment == -1 {
		logrus.WithFields(logrus.Fields{
			"upstream_pubkey": upstreamPubkey,
			"upstream_ip":     upstreamIP,
		}).Info("Session ended (upstream returned -1/-1), triggering new session")

		// Trigger a new session by calling renewal handler
		go func() {
			err := d.chandler.HandleUpcomingRenewal(upstreamPubkey, 0)
			if err != nil {
				logrus.WithFields(logrus.Fields{
					"upstream_pubkey": upstreamPubkey,
					"error":           err,
				}).Error("Failed to handle session end renewal")
			}
		}()
		return
	}

	d.mu.Lock()
	d.upstreamUsage = uint64(usage)
	d.upstreamAllotment = uint64(allotment)

	// Calculate remaining
	remaining := uint64(0)
	if d.upstreamAllotment > d.upstreamUsage {
		remaining = d.upstreamAllotment - d.upstreamUsage
	}

	// Log at DEBUG level every second
	logrus.WithFields(logrus.Fields{
		"upstream_pubkey": upstreamPubkey,
		"usage":           fmt.Sprintf("%s/%s (%s left)", utils.BytesToHumanReadable(d.upstreamUsage), utils.BytesToHumanReadable(d.upstreamAllotment), utils.BytesToHumanReadable(remaining)),
	}).Debug("Upstream usage polled")

	// Log at INFO level every 10 seconds
	now := time.Now()
	if now.Sub(d.lastInfoLog) >= 10*time.Second {
		d.lastInfoLog = now
		d.mu.Unlock()

		logrus.WithFields(logrus.Fields{
			"upstream_pubkey": upstreamPubkey,
			"usage":           fmt.Sprintf("%s/%s (%s left)", utils.BytesToHumanReadable(d.upstreamUsage), utils.BytesToHumanReadable(d.upstreamAllotment), utils.BytesToHumanReadable(remaining)),
		}).Info("Upstream usage status")
		return
	}
	d.mu.Unlock()
}

// checkRenewalOffset checks if renewal is needed based on upstream usage
func (d *DataUsageTracker) checkRenewalOffset() {
	d.mu.RLock()
	upstreamUsage := d.upstreamUsage
	upstreamAllotment := d.upstreamAllotment
	renewalOffset := d.renewalOffset
	renewalInProgress := d.renewalInProgress
	upstreamPubkey := d.upstreamPubkey
	d.mu.RUnlock()

	// Skip if no upstream data yet or renewal already in progress
	if upstreamAllotment == 0 || renewalInProgress {
		return
	}

	// Calculate remaining allotment
	remaining := uint64(0)
	if upstreamAllotment > upstreamUsage {
		remaining = upstreamAllotment - upstreamUsage
	}

	// Trigger renewal if remaining is less than or equal to offset
	if remaining <= renewalOffset {
		d.mu.Lock()
		d.renewalInProgress = true
		d.mu.Unlock()

		logrus.WithFields(logrus.Fields{
			"upstream_pubkey": upstreamPubkey,
			"remaining":       utils.BytesToHumanReadable(remaining),
			"renewal_offset":  utils.BytesToHumanReadable(renewalOffset),
			"usage":           utils.BytesToHumanReadable(upstreamUsage),
			"allotment":       utils.BytesToHumanReadable(upstreamAllotment),
		}).Info("Renewal offset reached, triggering renewal")

		// Call the chandler's renewal handler
		go func(pubkey string, usage uint64) {
			err := d.chandler.HandleUpcomingRenewal(pubkey, usage)
			if err != nil {
				logrus.WithFields(logrus.Fields{
					"upstream_pubkey": pubkey,
					"error":           err,
				}).Error("Failed to handle upcoming renewal")
			}
		}(upstreamPubkey, upstreamUsage)
	}
}

// getInterfaceBytes reads the current byte count for the interface from /proc/net/dev
// This function is designed to work on both 32-bit and 64-bit systems
func getInterfaceBytes(interfaceName string) (uint64, error) {
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

		iface := strings.TrimSpace(line[:colonIndex])
		if iface != interfaceName {
			continue
		}

		// Parse the statistics after the colon
		stats := strings.Fields(line[colonIndex+1:])
		if len(stats) < 16 {
			return 0, fmt.Errorf("insufficient statistics for interface %s", interfaceName)
		}

		// /proc/net/dev format:
		// RX: bytes packets errs drop fifo frame compressed multicast
		// TX: bytes packets errs drop fifo colls carrier compressed

		// Get RX bytes (index 0) and TX bytes (index 8)
		rxBytes, err := parseUint64Safe(stats[0])
		if err != nil {
			return 0, fmt.Errorf("failed to parse RX bytes for %s: %w", interfaceName, err)
		}

		txBytes, err := parseUint64Safe(stats[8])
		if err != nil {
			return 0, fmt.Errorf("failed to parse TX bytes for %s: %w", interfaceName, err)
		}

		// Return total bytes (RX + TX)
		totalBytes := rxBytes + txBytes

		logrus.WithFields(logrus.Fields{
			"interface": interfaceName,
			"rx":        utils.BytesToHumanReadable(rxBytes),
			"tx":        utils.BytesToHumanReadable(txBytes),
			"total":     utils.BytesToHumanReadable(totalBytes),
		}).Debug("Summing RX and TX bytes for total usage")

		return totalBytes, nil
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("error reading /proc/net/dev: %w", err)
	}

	return 0, fmt.Errorf("interface %s not found in /proc/net/dev", interfaceName)
}

// parseUint64Safe safely parses a uint64 from string, handling 32-bit system limitations
func parseUint64Safe(s string) (uint64, error) {
	// strconv.ParseUint handles both 32-bit and 64-bit systems correctly
	return strconv.ParseUint(strings.TrimSpace(s), 10, 64)
}
