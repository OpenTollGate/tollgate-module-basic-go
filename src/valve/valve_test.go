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
