package valve

import (
	"fmt"
	"log"
	"os/exec"
	"sync"
	"time"
)

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
		log.Printf("Error authorizing MAC address %s: %v", macAddress, err)
		return err
	}

	log.Printf("Authorization successful for MAC %s: %s", macAddress, string(output))
	return nil
}

// deauthorizeMAC deauthorizes a MAC address using ndsctl
func deauthorizeMAC(macAddress string) error {
	cmd := exec.Command("ndsctl", "deauth", macAddress)
	output, err := cmd.Output()
	if err != nil {
		log.Printf("Error deauthorizing MAC address %s: %v", macAddress, err)
		return err
	}

	log.Printf("Deauthorization successful for MAC %s: %s", macAddress, string(output))
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

	log.Printf("Opening gate for %s until timestamp %d (duration: %d seconds)",
		macAddress, untilTimestamp, durationSeconds)

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
		log.Printf("New authorization for MAC %s", macAddress)
	} else {
		// MAC already in openGates, stop the existing timer
		if existingTimer != nil {
			existingTimer.Stop()
		}
		log.Printf("Extending access for already authorized MAC %s", macAddress)
	}

	// Create a new timer that will call deauthorizeMAC when it expires
	duration := time.Duration(durationSeconds) * time.Second
	timer := time.AfterFunc(duration, func() {
		err := deauthorizeMAC(macAddress)
		if err != nil {
			log.Printf("Error deauthorizing MAC %s after timeout: %v", macAddress, err)
		} else {
			log.Printf("Successfully deauthorized MAC %s after timeout", macAddress)
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
