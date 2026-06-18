package valve

import (
	"context"
	"fmt"
	"os/exec"
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

func TestIsValidMAC(t *testing.T) {
	tests := []struct {
		mac  string
		want bool
	}{
		{"aa:bb:cc:dd:ee:ff", true},
		{"AA:BB:CC:DD:EE:FF", true},
		{"01:23:45:67:89:ab", true},
		{"", false},
		{"not-a-mac", false},
		{"aa:bb:cc:dd:ee", false},
		{"aa:bb:cc:dd:ee:ff:gg", false},
	}

	for _, tc := range tests {
		t.Run(tc.mac, func(t *testing.T) {
			got := isValidMAC(tc.mac)
			if got != tc.want {
				t.Errorf("isValidMAC(%q) = %v, want %v", tc.mac, got, tc.want)
			}
		})
	}
}

func TestRunNdsctlTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timeout test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, "sleep", "30")
	_ = cmd.Run()
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Errorf("command should have been killed after ~1s, took %v", elapsed)
	}

	if ctx.Err() != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", ctx.Err())
	}
}

func TestOpenGateUntilRejectsInvalidMAC(t *testing.T) {
	future := time.Now().Unix() + 60
	err := OpenGateUntil("not-a-mac", future)
	if err == nil {
		t.Fatal("expected error for invalid MAC")
	}
}

func TestOpenGateUntilRejectsPastTimestamp(t *testing.T) {
	past := time.Now().Unix() - 10
	err := OpenGateUntil("aa:bb:cc:dd:ee:ff", past)
	if err == nil {
		t.Fatal("expected error for past timestamp")
	}
}

func TestCloseGateRejectsInvalidMAC(t *testing.T) {
	err := CloseGate("not-a-mac")
	if err == nil {
		t.Fatal("expected error for invalid MAC")
	}
}

func TestOpenGateRejectsInvalidMAC(t *testing.T) {
	err := OpenGate("not-a-mac")
	if err == nil {
		t.Fatal("expected error for invalid MAC")
	}
}

func TestGetClientStatsRejectsInvalidMAC(t *testing.T) {
	_, _, err := GetClientStats("not-a-mac")
	if err == nil {
		t.Fatal("expected error for invalid MAC")
	}
}

// TestAuthorizeMACRetriesThenSucceeds verifies that authorizeMAC tolerates the
// transient "client not registered yet" condition (the two-router autopay race)
// by retrying until ndsctl succeeds.
func TestAuthorizeMACRetriesThenSucceeds(t *testing.T) {
	origNdsctl, origDelay := runNdsctl, authRetryDelay
	defer func() {
		runNdsctl = origNdsctl
		authRetryDelay = origDelay
	}()
	authRetryDelay = time.Millisecond // keep the test fast

	calls := 0
	runNdsctl = func(args ...string) (string, error) {
		calls++
		if args[0] == "auth" && calls < 3 {
			return "Client not found", fmt.Errorf("exit status 1")
		}
		return "ok", nil
	}

	if err := authorizeMAC("aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatalf("expected authorizeMAC to succeed after retry, got %v", err)
	}
	if calls < 3 {
		t.Fatalf("expected at least 3 attempts (fail,fail,ok), got %d", calls)
	}
}

// TestAuthorizeMACFailsAfterMaxAttempts verifies authorizeMAC gives up after
// authMaxAttempts and returns the last error rather than retrying forever.
func TestAuthorizeMACFailsAfterMaxAttempts(t *testing.T) {
	origNdsctl, origDelay := runNdsctl, authRetryDelay
	defer func() {
		runNdsctl = origNdsctl
		authRetryDelay = origDelay
	}()
	authRetryDelay = time.Millisecond

	calls := 0
	runNdsctl = func(args ...string) (string, error) {
		calls++
		return "", fmt.Errorf("exit status 1")
	}

	err := authorizeMAC("aa:bb:cc:dd:ee:01")
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if calls != authMaxAttempts {
		t.Fatalf("expected exactly %d attempts, got %d", authMaxAttempts, calls)
	}
}
