package valve

import (
	"sync"
	"testing"
	"time"
)

func TestStaleTimerCallbackDoesNotDeleteReplacement(t *testing.T) {
	mac := "aa:bb:cc:dd:ee:ff"

	gatesMutex.Lock()
	// Clean slate
	for k := range openGates {
		delete(openGates, k)
	}
	gatesMutex.Unlock()

	// Simulate the sentinel pattern from OpenGateUntil:
	// Timer A is created and stored in the map.
	var timerA *time.Timer
	timerA = time.AfterFunc(50*time.Millisecond, func() {
		gatesMutex.Lock()
		if openGates[mac] == timerA {
			delete(openGates, mac)
		}
		gatesMutex.Unlock()
	})

	gatesMutex.Lock()
	openGates[mac] = timerA
	gatesMutex.Unlock()

	// Before timer A fires, replace it with timer B (simulating extension).
	var timerB *time.Timer
	timerB = time.AfterFunc(5*time.Second, func() {
		gatesMutex.Lock()
		if openGates[mac] == timerB {
			delete(openGates, mac)
		}
		gatesMutex.Unlock()
	})

	timerA.Stop()

	gatesMutex.Lock()
	openGates[mac] = timerB
	gatesMutex.Unlock()

	// Fire timer A's callback manually to simulate the race:
	// Timer A was stopped but its callback checks the sentinel.
	// Without the sentinel, this would delete timerB from the map.
	staleCallback := func() {
		gatesMutex.Lock()
		if openGates[mac] == timerA {
			delete(openGates, mac)
		}
		gatesMutex.Unlock()
	}
	staleCallback()

	// Verify: timer B is still in the map.
	gatesMutex.Lock()
	stored := openGates[mac]
	gatesMutex.Unlock()

	if stored != timerB {
		t.Fatal("stale callback deleted the replacement timer — sentinel check failed")
	}

	// Cleanup
	timerB.Stop()
	gatesMutex.Lock()
	delete(openGates, mac)
	gatesMutex.Unlock()
}

func TestExpiredTimerDeletesOwnEntry(t *testing.T) {
	mac := "aa:bb:cc:dd:ee:01"

	gatesMutex.Lock()
	for k := range openGates {
		delete(openGates, k)
	}
	gatesMutex.Unlock()

	var timer *time.Timer
	var wg sync.WaitGroup
	wg.Add(1)

	timer = time.AfterFunc(10*time.Millisecond, func() {
		gatesMutex.Lock()
		if openGates[mac] == timer {
			delete(openGates, mac)
		}
		gatesMutex.Unlock()
		wg.Done()
	})

	gatesMutex.Lock()
	openGates[mac] = timer
	gatesMutex.Unlock()

	// Wait for timer to fire and callback to complete
	wg.Wait()

	gatesMutex.Lock()
	_, exists := openGates[mac]
	gatesMutex.Unlock()

	if exists {
		t.Fatal("expired timer did not delete its own entry")
	}
}

func TestOpenGateUntil_RejectsInvalidMAC(t *testing.T) {
	err := OpenGateUntil("not-a-mac", time.Now().Unix()+60)
	if err == nil {
		t.Fatal("expected error for invalid MAC, got nil")
	}
}

func TestOpenGateUntil_RejectsPastTimestamp(t *testing.T) {
	err := OpenGateUntil("aa:bb:cc:dd:ee:ff", time.Now().Unix()-10)
	if err == nil {
		t.Fatal("expected error for past timestamp, got nil")
	}
}
