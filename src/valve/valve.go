package valve

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// Module-level logger with pre-configured module field
var logger = logrus.WithField("module", "valve")

// openGates keeps track of MAC addresses that have been authorized
var (
	openGates  = make(map[string]*time.Timer)
	gatesMutex = &sync.Mutex{}
)

// ndsctlMutex ensures only one ndsctl command runs at a time
var ndsctlMutex = &sync.Mutex{}

// authorizeMAC authorizes a MAC address using ndsctl
func authorizeMAC(macAddress string) error {
	ndsctlMutex.Lock()
	cmd := exec.Command("ndsctl", "auth", macAddress)
	output, err := cmd.Output()
	ndsctlMutex.Unlock()

	if err != nil {
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"error":       err,
		}).Error("Error authorizing MAC address")
		return err
	}

	logger.WithFields(logrus.Fields{
		"mac_address": macAddress,
		"output":      string(output),
	}).Info("Authorization successful for MAC")
	return nil
}

// deauthorizeMAC deauthorizes a MAC address using ndsctl
func deauthorizeMAC(macAddress string) error {
	ndsctlMutex.Lock()
	cmd := exec.Command("ndsctl", "deauth", macAddress)
	output, err := cmd.Output()
	ndsctlMutex.Unlock()

	if err != nil {
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"error":       err,
		}).Error("Error deauthorizing MAC address")
		return err
	}

	logger.WithFields(logrus.Fields{
		"mac_address": macAddress,
		"output":      string(output),
	}).Debug("Deauthorization successful for MAC")
	return nil
}

// OpenGateUntil opens the gate (if not opened yet) and sets a timer until the timestamp.
// If there is already a timer running, it will extend the timer.
func OpenGateUntil(macAddress string, untilTimestamp int64) error {
	now := time.Now().Unix()

	// Calculate duration until the target timestamp
	durationSeconds := untilTimestamp - now

	// If the timestamp is in the past, return an error
	if durationSeconds <= 0 {
		return fmt.Errorf("timestamp %d is in the past (current time: %d)", untilTimestamp, now)
	}

	logger.WithFields(logrus.Fields{
		"mac_address":      macAddress,
		"until_timestamp":  untilTimestamp,
		"duration_seconds": durationSeconds,
	}).Info("Opening gate until timestamp")

	gatesMutex.Lock()
	defer gatesMutex.Unlock()

	// Check if the MAC is already in openGates
	existingTimer, exists := openGates[macAddress]

	if !exists {
		// MAC not in openGates, authorize it
		err := authorizeMAC(macAddress)
		if err != nil {
			return fmt.Errorf("error authorizing MAC: %w", err)
		}
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
		}).Debug("New authorization for MAC")
	} else {
		// MAC already in openGates, stop the existing timer
		if existingTimer != nil {
			existingTimer.Stop()
		}
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
		}).Debug("Extending access for already authorized MAC")
	}

	// Create a new timer that will call deauthorizeMAC when it expires
	duration := time.Duration(durationSeconds) * time.Second
	timer := time.AfterFunc(duration, func() {
		err := deauthorizeMAC(macAddress)
		if err != nil {
			logger.WithFields(logrus.Fields{
				"mac_address": macAddress,
				"error":       err,
			}).Error("Error deauthorizing MAC after timeout")
		} else {
			logger.WithFields(logrus.Fields{
				"mac_address": macAddress,
			}).Debug("Successfully deauthorized MAC after timeout")
		}

		// Remove the MAC from openGates once timer expires
		gatesMutex.Lock()
		delete(openGates, macAddress)
		gatesMutex.Unlock()
	})

	// Store the timer in openGates
	openGates[macAddress] = timer

	return nil
}

// OpenGate authorizes a MAC address without a timer.
// It's used for data-based sessions that are closed by a tracker.
// It captures the current data usage as a baseline for tracking.
func OpenGate(macAddress string) error {
	gatesMutex.Lock()
	defer gatesMutex.Unlock()

	// If there's an existing timer, stop it.
	if existingTimer, exists := openGates[macAddress]; exists {
		if existingTimer != nil {
			existingTimer.Stop()
		}
		logger.WithField("mac_address", macAddress).Info("Replacing existing timed gate with indefinite data-based gate.")
	}

	err := authorizeMAC(macAddress)
	if err != nil {
		return err
	}

	// Set data baseline for tracking
	err = SetDataBaseline(macAddress)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"error":       err,
		}).Warn("Failed to set data baseline, continuing anyway")
	}

	// Store a nil timer to indicate an indefinite gate
	openGates[macAddress] = nil
	return nil
}

// CloseGate deauthorizes a MAC address and removes it from the active gates.
func CloseGate(macAddress string) error {
	gatesMutex.Lock()
	defer gatesMutex.Unlock()

	if _, exists := openGates[macAddress]; !exists {
		logger.WithField("mac_address", macAddress).Warn("Attempted to close a gate that was not open.")
		// still attempt to deauth, in case the state is out of sync
	}

	err := deauthorizeMAC(macAddress)
	if err != nil {
		return err
	}

	// Clean up from active gates map and data baseline
	delete(openGates, macAddress)
	ClearDataBaseline(macAddress)
	return nil
}

// ClientStats represents the statistics for a single client from ndsctl
type ClientStats struct {
	ID           int     `json:"id"`
	IP           string  `json:"ip"`
	MAC          string  `json:"mac"`
	Added        int64   `json:"added"`
	Active       int64   `json:"active"`
	Duration     int64   `json:"duration"`
	Token        string  `json:"token"`
	State        string  `json:"state"`
	Downloaded   uint64  `json:"downloaded"` // in kilobytes
	AvgDownSpeed float64 `json:"avg_down_speed"`
	Uploaded     uint64  `json:"uploaded"` // in kilobytes
	AvgUpSpeed   float64 `json:"avg_up_speed"`
}

// GetClientStats retrieves the current data usage statistics for a MAC address from ndsctl
// Returns downloaded and uploaded in bytes (converted from kilobytes)
// Returns error if client not found or command fails
// This function is thread-safe and serializes ndsctl calls
func GetClientStats(macAddress string) (downloaded uint64, uploaded uint64, err error) {
	// Serialize ndsctl calls to prevent concurrent execution issues
	ndsctlMutex.Lock()
	cmd := exec.Command("ndsctl", "json", macAddress)
	output, err := cmd.Output()
	ndsctlMutex.Unlock() // Unlock immediately after command completes

	if err != nil {
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"error":       err,
		}).Error("Error executing ndsctl json")
		return 0, 0, fmt.Errorf("failed to execute ndsctl json for MAC %s: %w", macAddress, err)
	}

	// Check for empty response (client not found)
	trimmed := string(output)
	if trimmed == "{}" || trimmed == "{}\n" {
		return 0, 0, fmt.Errorf("client with MAC %s not found in ndsctl", macAddress)
	}

	var stats ClientStats
	err = json.Unmarshal(output, &stats)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"error":       err,
			"output":      string(output),
		}).Error("Error parsing ndsctl json output")
		return 0, 0, fmt.Errorf("failed to parse ndsctl json for MAC %s: %w", macAddress, err)
	}

	// Convert from kilobytes to bytes
	downloadedBytes := stats.Downloaded * 1024
	uploadedBytes := stats.Uploaded * 1024

	logger.WithFields(logrus.Fields{
		"mac_address":      macAddress,
		"downloaded_kb":    stats.Downloaded,
		"uploaded_kb":      stats.Uploaded,
		"downloaded_bytes": downloadedBytes,
		"uploaded_bytes":   uploadedBytes,
		"state":            stats.State,
	}).Debug("Retrieved client stats from ndsctl")

	return downloadedBytes, uploadedBytes, nil
}

// GetClientUsage returns the total data usage (downloaded + uploaded) for a MAC address
// This is a convenience function that calls GetClientStats
func GetClientUsage(macAddress string) (totalBytes uint64, err error) {
	downloaded, uploaded, err := GetClientStats(macAddress)
	if err != nil {
		return 0, err
	}
	return downloaded + uploaded, nil
}
