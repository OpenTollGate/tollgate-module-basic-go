package valve

import (
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/nbd-wtf/go-nostr"
)

// activeTimers keeps track of active timers for each MAC address
var (
	activeTimers = make(map[string]*time.Timer)
	timerMutex   = &sync.Mutex{}
)

// OpenGate authorizes a MAC address for network access for a specified duration
func OpenGate(macAddress string, durationSeconds int64) error {
	var durationMinutes int = int(durationSeconds / 60)

	// The minimum of this tollgate is 1 min, otherwise it would default to 24h
	if durationMinutes == 0 {
		durationMinutes = 1
	}

	log.Printf("Opening gate for %s for the duration of %d minute(s)", macAddress, durationMinutes)

	// Check if there's already a timer for this MAC address
	timerMutex.Lock()
	_, timerExists := activeTimers[macAddress]
	timerMutex.Unlock()

	// Only authorize the MAC address if there's no existing timer
	if !timerExists {
		err := authorizeMAC(macAddress)
		if err != nil {
			return fmt.Errorf("error authorizing MAC: %w", err)
		}
		log.Printf("New authorization for MAC %s", macAddress)
	} else {
		log.Printf("Extending access for already authorized MAC %s", macAddress)
	}

	// Cancel any existing timers for this MAC address
	cancelExistingTimer(macAddress)

	// Set up a new timer for this MAC address
	duration := time.Duration(durationSeconds) * time.Second
	timer := time.AfterFunc(duration, func() {
		err := deauthorizeMAC(macAddress)
		if err != nil {
			log.Printf("Error deauthorizing MAC %s after timeout: %v", macAddress, err)
		} else {
			log.Printf("Successfully deauthorized MAC %s after timeout of %d minutes", macAddress, durationMinutes)
		}

		// Remove the timer from the map once it's executed
		timerMutex.Lock()
		delete(activeTimers, macAddress)
		timerMutex.Unlock()
	})

	// Store the timer in the map
	timerMutex.Lock()
	activeTimers[macAddress] = timer
	timerMutex.Unlock()

	return nil
}

// cancelExistingTimer cancels any existing timer for the given MAC address
func cancelExistingTimer(macAddress string) {
	timerMutex.Lock()
	defer timerMutex.Unlock()

	if timer, exists := activeTimers[macAddress]; exists {
		timer.Stop()
		delete(activeTimers, macAddress)
		log.Printf("Canceled existing timer for MAC %s", macAddress)
	}
}

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

// GetActiveTimers returns the number of active timers for debugging
func GetActiveTimers() int {
	timerMutex.Lock()
	defer timerMutex.Unlock()
	return len(activeTimers)
}

// OpenGateForSession authorizes network access based on a session event
func OpenGateForSession(sessionEvent nostr.Event, config *config_manager.Config) error {
	// Extract MAC address from session event
	macAddress, err := extractMACFromSession(sessionEvent)
	if err != nil {
		return fmt.Errorf("failed to extract MAC address from session: %w", err)
	}

	// Extract allotment from session event
	allotmentMs, err := extractAllotmentFromSession(sessionEvent)
	if err != nil {
		return fmt.Errorf("failed to extract allotment from session: %w", err)
	}

	// Convert allotment to duration
	durationSeconds := int64(allotmentMs / 1000)

	log.Printf("Opening gate for session: MAC=%s, allotment=%d ms, duration=%d seconds",
		macAddress, allotmentMs, durationSeconds)

	// Use the existing OpenGate function
	return OpenGate(macAddress, durationSeconds)
}

// extractMACFromSession extracts the MAC address from a session event
func extractMACFromSession(sessionEvent nostr.Event) (string, error) {
	for _, tag := range sessionEvent.Tags {
		if len(tag) >= 3 && tag[0] == "device-identifier" && tag[1] == "mac" {
			return tag[2], nil
		}
	}
	return "", fmt.Errorf("no MAC address found in session event")
}

// extractAllotmentFromSession extracts allotment from a session event
func extractAllotmentFromSession(sessionEvent nostr.Event) (uint64, error) {
	for _, tag := range sessionEvent.Tags {
		if len(tag) >= 2 && tag[0] == "allotment" {
			allotment, err := strconv.ParseUint(tag[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("failed to parse allotment: %w", err)
			}
			return allotment, nil
		}
	}
	return 0, fmt.Errorf("no allotment found in session event")
}

// getStepSizeFromConfig gets the step size from configuration
func getStepSizeFromConfig(config *config_manager.Config) uint64 {
	// For now, return the default step size of 60000ms (1 minute)
	// In a full implementation, this would parse the merchant's advertisement
	return 60000
}
