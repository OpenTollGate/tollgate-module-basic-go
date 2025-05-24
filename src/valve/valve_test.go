package valve

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if _, err := exec.LookPath("ndsctl"); err != nil {
		fmt.Println("ndsctl not found, skipping tests")
		os.Exit(0)
	}
	m.Run()
}

func TestOpenGate(t *testing.T) {
	macAddress := "00:11:22:33:44:55"
	durationSeconds := int64(1) // 1 second for quick testing

	err := OpenGate(macAddress, durationSeconds)
	if err != nil {
		t.Errorf("OpenGate failed: %v", err)
	}

	timerMutex.Lock()
	_, timerExists := activeTimers[macAddress]
	timerMutex.Unlock()
	if !timerExists {
		t.Errorf("Timer was not set for MAC %s", macAddress)
	}

	time.Sleep(time.Duration(durationSeconds+1) * time.Second)

	timerMutex.Lock()
	_, timerExists = activeTimers[macAddress]
	timerMutex.Unlock()
	if timerExists {
		t.Errorf("Timer was not removed after expiration for MAC %s", macAddress)
	}
}

func TestMultipleOpenGateCalls(t *testing.T) {
	macAddress := "00:11:22:33:44:56"
	durationSeconds := int64(2)

	err := OpenGate(macAddress, durationSeconds)
	if err != nil {
		t.Errorf("First OpenGate call failed: %v", err)
	}

	err = OpenGate(macAddress, durationSeconds)
	if err != nil {
		t.Errorf("Second OpenGate call failed: %v", err)
	}

	timerMutex.Lock()
	_, exists := activeTimers[macAddress]
	timerMutex.Unlock()
	if !exists {
		t.Errorf("Timer was not reset for MAC %s", macAddress)
	}

	time.Sleep(time.Duration(durationSeconds+1) * time.Second)

	timerMutex.Lock()
	_, exists = activeTimers[macAddress]
	timerMutex.Unlock()
	if exists {
		t.Errorf("Timer was not removed after expiration for MAC %s", macAddress)
	}
}

func TestGetActiveTimers(t *testing.T) {
	initialCount := GetActiveTimers()

	macAddress := "00:11:22:33:44:57"
	durationSeconds := int64(1)

	err := OpenGate(macAddress, durationSeconds)
	if err != nil {
		t.Errorf("OpenGate failed: %v", err)
	}

	newCount := GetActiveTimers()
	if newCount != initialCount+1 {
		t.Errorf("GetActiveTimers returned %d, expected %d", newCount, initialCount+1)
	}

	time.Sleep(time.Duration(durationSeconds+1) * time.Second)

	finalCount := GetActiveTimers()
	if finalCount != initialCount {
		t.Errorf("GetActiveTimers returned %d after timer expiration, expected %d", finalCount, initialCount)
	}
}

func TestGetRemainingTime(t *testing.T) {
	// Clear any existing timers
	timerMutex.Lock()
	for mac, timer := range activeTimers {
		timer.Stop()
		delete(activeTimers, mac)
		delete(timerExpiry, mac)
	}
	timerMutex.Unlock()

	// Test for a MAC address that doesn't exist
	remainingTime, expiryTime, exists := GetRemainingTime("nonexistent")
	if exists {
		t.Errorf("Expected exists to be false for nonexistent MAC address, got true")
	}
	if remainingTime != 0 {
		t.Errorf("Expected remaining time to be 0 for nonexistent MAC address, got %d", remainingTime)
	}
	if !expiryTime.IsZero() {
		t.Errorf("Expected expiry time to be zero for nonexistent MAC address, got %v", expiryTime)
	}

	// Create a mock timer for testing
	macAddress := "00:11:22:33:44:58"
	durationSeconds := int64(30)
	
	// Mock adding a timer - we use OpenGate instead of directly manipulating
	// the timers to ensure proper setup of all data structures
	err := OpenGate(macAddress, durationSeconds)
	if err != nil {
		t.Errorf("OpenGate failed: %v", err)
	}

	// Test for a MAC address that exists
	remainingTime, returnedExpiry, exists := GetRemainingTime(macAddress)
	if !exists {
		t.Errorf("Expected exists to be true for existing MAC address, got false")
	}
	if remainingTime <= 0 || remainingTime > durationSeconds {
		t.Errorf("Expected remaining time to be between 0 and %d seconds, got %d", durationSeconds, remainingTime)
	}
	
	// Verify expiry time is in the future
	now := time.Now()
	if returnedExpiry.Before(now) {
		t.Errorf("Expected expiry time to be in the future, got %v, now is %v", returnedExpiry, now)
	}
	
	// Calculate expected expiry time range (allowing some execution time variance)
	expectedExpiry := now.Add(time.Duration(durationSeconds) * time.Second)
	expectedWindow := 5 * time.Second
	
	// Check if expiry is within expected range
	diff := expectedExpiry.Sub(returnedExpiry)
	if diff < -expectedWindow || diff > expectedWindow {
		t.Errorf("Expiry time outside expected range. Got %v, expected near %v (within %v)",
			returnedExpiry, expectedExpiry, expectedWindow)
	}

	// Clean up (not strictly necessary as other tests will handle cleanup)
	timerMutex.Lock()
	if timer, exists := activeTimers[macAddress]; exists {
		timer.Stop()
		delete(activeTimers, macAddress)
		delete(timerExpiry, macAddress)
	}
	timerMutex.Unlock()
}
