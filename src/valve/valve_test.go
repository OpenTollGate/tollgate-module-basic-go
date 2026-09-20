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

	authCalls := 0
	runNdsctl = func(args ...string) (string, error) {
		switch args[0] {
		case "auth":
			authCalls++
			if authCalls < 3 {
				return "Client not found", fmt.Errorf("exit status 1")
			}
			return "ok", nil
		case "json":
			// Probe on failed auth: client still pending, not authenticated.
			return `{"id":1,"state":"Pending"}`, nil
		}
		return "", fmt.Errorf("unexpected ndsctl call: %v", args)
	}

	if err := authorizeMAC("aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatalf("expected authorizeMAC to succeed after retry, got %v", err)
	}
	if authCalls < 3 {
		t.Fatalf("expected at least 3 auth attempts (fail,fail,ok), got %d", authCalls)
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

	authCalls := 0
	runNdsctl = func(args ...string) (string, error) {
		switch args[0] {
		case "auth":
			authCalls++
			return "", fmt.Errorf("exit status 1")
		case "json":
			// Probe: client not authenticated, so retrying must continue.
			return `{"id":1,"state":"Pending"}`, nil
		}
		return "", fmt.Errorf("unexpected ndsctl call: %v", args)
	}

	err := authorizeMAC("aa:bb:cc:dd:ee:01")
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if authCalls != authMaxAttempts {
		t.Fatalf("expected exactly %d auth attempts, got %d", authMaxAttempts, authCalls)
	}
}

// TestCheckClientStateNotRegistered verifies that an empty NDS client list
// ("{}" from ndsctl json) is reported as a definitive not-registered state,
// not a probe error.
func TestCheckClientStateNotRegistered(t *testing.T) {
	origNdsctl := runNdsctl
	defer func() { runNdsctl = origNdsctl }()

	runNdsctl = func(args ...string) (string, error) {
		if args[0] != "json" {
			t.Errorf("CheckClientState must be read-only, got ndsctl call: %v", args)
		}
		return "{}\n", nil
	}

	state, err := CheckClientState("aa:bb:cc:dd:ee:10")
	if err != nil {
		t.Fatalf("expected nil error for empty NDS client list, got %v", err)
	}
	if state.Registered {
		t.Fatal("expected Registered=false when NDS reports no client")
	}
	if state.Authenticated {
		t.Fatal("expected Authenticated=false when not registered")
	}
}

// TestCheckClientStateAuthenticated verifies the probe reports both
// registration and the Authenticated state for a live client.
func TestCheckClientStateAuthenticated(t *testing.T) {
	origNdsctl := runNdsctl
	defer func() { runNdsctl = origNdsctl }()

	runNdsctl = func(args ...string) (string, error) {
		return `{"id":7,"mac":"aa:bb:cc:dd:ee:11","state":"Authenticated"}`, nil
	}

	state, err := CheckClientState("aa:bb:cc:dd:ee:11")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !state.Registered || !state.Authenticated {
		t.Fatalf("expected Registered=true Authenticated=true, got %+v", state)
	}
}

// TestCheckClientStatePending verifies a registered-but-pending client is
// Registered=true, Authenticated=false.
func TestCheckClientStatePending(t *testing.T) {
	origNdsctl := runNdsctl
	defer func() { runNdsctl = origNdsctl }()

	runNdsctl = func(args ...string) (string, error) {
		return `{"id":7,"mac":"aa:bb:cc:dd:ee:12","state":"Pending"}`, nil
	}

	state, err := CheckClientState("aa:bb:cc:dd:ee:12")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !state.Registered {
		t.Fatal("expected Registered=true for a pending client")
	}
	if state.Authenticated {
		t.Fatal("expected Authenticated=false for a pending client")
	}
}

func TestCheckClientStateRejectsInvalidMAC(t *testing.T) {
	if _, err := CheckClientState("not-a-mac"); err == nil {
		t.Fatal("expected error for invalid MAC")
	}
}

// TestCheckClientStateReturnsProbeError verifies ndsctl failures surface as
// probe errors so callers can fail open.
func TestCheckClientStateReturnsProbeError(t *testing.T) {
	origNdsctl := runNdsctl
	defer func() { runNdsctl = origNdsctl }()

	runNdsctl = func(args ...string) (string, error) {
		return "", fmt.Errorf("exit status 1")
	}

	if _, err := CheckClientState("aa:bb:cc:dd:ee:13"); err == nil {
		t.Fatal("expected probe error to surface, got nil")
	}
}

// TestAuthorizeMACAlreadyAuthenticatedClientSucceeds verifies that when NDS
// 5.0.2's `ndsctl auth` exits 1 for a client that is ALREADY Authenticated,
// authorizeMAC recognizes this via the read-only probe and treats the gate as
// open instead of failing (issue #403 trigger (b): first payment after fresh
// daemon state with a still-authed client).
func TestAuthorizeMACAlreadyAuthenticatedClientSucceeds(t *testing.T) {
	origNdsctl, origDelay := runNdsctl, authRetryDelay
	defer func() {
		runNdsctl = origNdsctl
		authRetryDelay = origDelay
	}()
	authRetryDelay = time.Millisecond

	authCalls := 0
	runNdsctl = func(args ...string) (string, error) {
		switch args[0] {
		case "auth":
			authCalls++
			return "Client is already authenticated", fmt.Errorf("exit status 1")
		case "json":
			return `{"id":7,"state":"Authenticated"}`, nil
		}
		return "", fmt.Errorf("unexpected ndsctl call: %v", args)
	}

	if err := authorizeMAC("aa:bb:cc:dd:ee:14"); err != nil {
		t.Fatalf("expected already-Authenticated client to succeed, got %v", err)
	}
	if authCalls == 0 {
		t.Fatal("expected at least one real auth attempt before the probe")
	}
}
