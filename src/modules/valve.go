package modules

import (
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// activeTimers keeps track of active timers for each MAC address
// timerExpiry keeps track of when each MAC address's access will expire
var (
	activeTimers = make(map[string]*time.Timer)
	timerExpiry  = make(map[string]time.Time)
	timerMutex   = &sync.Mutex{}
)

// OpenGate authorizes a MAC address for network access for a specified duration
func OpenGate(macAddress string, durationSeconds int64) error {
	var durationMinutes int = int(durationSeconds / 60)

	// The minimum of this tollgate is 1 min, otherwise it would default to 24h
	if durationMinutes == 0 {
		durationMinutes = 1
	}

	fmt.Printf("Opening gate for %s for the duration of %d minute(s)\n", macAddress, durationMinutes)

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
		fmt.Printf("New authorization for MAC %s\n", macAddress)
	} else {
		fmt.Printf("Extending access for already authorized MAC %s\n", macAddress)
	}

	// Cancel any existing timers for this MAC address
	cancelExistingTimer(macAddress)

	// Set up a new timer for this MAC address
	duration := time.Duration(durationSeconds) * time.Second
	timer := time.AfterFunc(duration, func() {
		err := deauthorizeMAC(macAddress)
		if err != nil {
			fmt.Printf("Error deauthorizing MAC %s after timeout: %v\n", macAddress, err)
		} else {
			fmt.Printf("Successfully deauthorized MAC %s after timeout of %d minutes\n", macAddress, durationMinutes)
		}

		// Remove the timer from the map once it's executed
		timerMutex.Lock()
		delete(activeTimers, macAddress)
		timerMutex.Unlock()
	})

	// Store the timer and expiry time in the maps
	expiryTime := time.Now().Add(duration)
	timerMutex.Lock()
	activeTimers[macAddress] = timer
	timerExpiry[macAddress] = expiryTime
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
		delete(timerExpiry, macAddress)
		fmt.Printf("Canceled existing timer for MAC %s\n", macAddress)
	}
}

// authorizeMAC authorizes a MAC address using ndsctl
func authorizeMAC(macAddress string) error {
	cmd := exec.Command("ndsctl", "auth", macAddress)
	output, err := cmd.Output()
	if err != nil {
		fmt.Printf("Error authorizing MAC address %s: %v\n", macAddress, err)
		return err
	}

	fmt.Printf("Authorization successful for MAC %s: %s\n", macAddress, string(output))
	return nil
}

// deauthorizeMAC deauthorizes a MAC address using ndsctl
func deauthorizeMAC(macAddress string) error {
	cmd := exec.Command("ndsctl", "deauth", macAddress)
	output, err := cmd.Output()
	if err != nil {
		fmt.Printf("Error deauthorizing MAC address %s: %v\n", macAddress, err)
		return err
	}

	fmt.Printf("Deauthorization successful for MAC %s: %s\n", macAddress, string(output))
	return nil
}

// GetActiveTimers returns the number of active timers for debugging
func GetActiveTimers() int {
	timerMutex.Lock()
	defer timerMutex.Unlock()
	return len(activeTimers)
}

// GetRemainingTime returns the remaining time in seconds for a MAC address,
// the expiry timestamp, and a boolean indicating whether the MAC address has an active timer
func GetRemainingTime(macAddress string) (int64, time.Time, bool) {
	timerMutex.Lock()
	defer timerMutex.Unlock()

	// Check if the MAC address has an active timer
	_, timerExists := activeTimers[macAddress]
	if !timerExists {
		return 0, time.Time{}, false
	}

	// Get the expiry time
	expiryTime := timerExpiry[macAddress]
	
	// Calculate the remaining time
	remainingTime := time.Until(expiryTime).Seconds()
	if remainingTime < 0 {
		remainingTime = 0
	}

	return int64(remainingTime), expiryTime, true
}
