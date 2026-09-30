package valve

import (
	"context"
	"fmt"
	"os"
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

// Timeout-test parameters. The child is given a lifetime two orders of
// magnitude above the deadline, so "the call returned" can only mean "the
// deadline killed the child", never "the child finished".
const (
	timeoutTestDeadline = 1 * time.Second
	// timeoutTestChildSeconds is the child's own lifetime: how long it runs if
	// nothing kills it.
	timeoutTestChildSeconds = 60
	// timeoutTestChildLifetime is that lifetime as a duration.
	timeoutTestChildLifetime = timeoutTestChildSeconds * time.Second
	// timeoutTestLooseBound is the unconditional wall-clock bound, and it is a
	// BACKSTOP, not the contract: half of the child's own lifetime. Every
	// latency measured for this kill on a host under deliberate CPU starvation
	// (up to 15.5s, see the PR) passes it, while a call that was NOT cut short
	// by the 1s deadline — i.e. one that returned when the 60s child exited on
	// its own — cannot. The 3s bound this replaces sat below the delay an
	// ordinarily loaded host produces.
	timeoutTestLooseBound = timeoutTestChildLifetime / 2
	// timeoutTestStrictBound is the promptness bound, asserted only when
	// TOLLGATE_TEST_STRICT_TIMING=1 is set, for a host with no other load.
	timeoutTestStrictBound = 3 * time.Second
)

// TestRunNdsctlTimeout pins the contract the ndsctl timeout relies on: a
// command started under a deadline context is terminated when that deadline
// fires, well before it would have exited on its own, and the context reports
// DeadlineExceeded.
//
// It deliberately does NOT turn "how promptly did the host get around to
// killing the child" into an assertion. That measurement is scheduling
// latency, not this package: measured for a 1s deadline, the kill landed
// 3.2-3.7s late on a loaded host (which is why the previous
// `elapsed > 3*time.Second` bound failed on the release pin with no diff
// involved, reddening unrelated branches) and up to ~15s late under
// deliberate CPU starvation.
//
// The contract is therefore asserted directly — the context expired, the child
// did not exit successfully, the child was killed by a signal — and none of
// that depends on when the host got around to running the kill. The wall-clock
// bound is the backstop below, and the prompt bound is opt-in via
// TOLLGATE_TEST_STRICT_TIMING=1.
func TestRunNdsctlTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timeout test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeoutTestDeadline)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, "sleep", fmt.Sprint(timeoutTestChildSeconds))
	err := cmd.Run()
	elapsed := time.Since(start)

	if ctx.Err() != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", ctx.Err())
	}
	if err == nil {
		t.Errorf("expected the deadline to kill the child, but it exited successfully after %v", elapsed)
	}
	if elapsed >= timeoutTestLooseBound {
		t.Errorf("the call took %v, which is not well before the %v the child needs to exit on its own: the deadline did not kill it",
			elapsed, timeoutTestChildLifetime)
	}

	if cmd.ProcessState == nil {
		// The deadline expired before the child was started, so there was
		// nothing to kill. That is possible on a heavily loaded host and says
		// nothing about the valve, so it is reported without failing.
		t.Logf("child was not started before the deadline expired (returned %v after %v): %v", elapsed, timeoutTestDeadline, err)
	} else {
		if cmd.ProcessState.Success() {
			t.Errorf("child exited successfully after %v: the deadline did not kill it", elapsed)
		}
		if code := cmd.ProcessState.ExitCode(); code != -1 {
			t.Errorf("expected the child to be terminated by a signal (exit code -1), got exit code %d after %v", code, elapsed)
		}
	}

	if os.Getenv("TOLLGATE_TEST_STRICT_TIMING") == "" {
		return
	}
	if elapsed > timeoutTestStrictBound {
		t.Errorf("strict timing: the %v deadline should kill the child within %v on a quiet host, took %v",
			timeoutTestDeadline, timeoutTestStrictBound, elapsed)
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
