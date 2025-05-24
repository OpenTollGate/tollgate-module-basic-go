package valve

import (
	"fmt"
	"log"
	"os/exec"
	"sync"
	"time"
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
