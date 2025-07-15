package valve

import (
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

// authorizeMAC authorizes a MAC address using ndsctl
func authorizeMAC(macAddress string) error {
	cmd := exec.Command("ndsctl", "auth", macAddress)
	output, err := cmd.Output()
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
	cmd := exec.Command("ndsctl", "deauth", macAddress)
	output, err := cmd.Output()
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
