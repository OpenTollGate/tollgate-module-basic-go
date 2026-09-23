package valve

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// The gate-close contract (C1-2, pre-release security audit 2026-09-23).
//
// An ndsctl deauth that FAILS is not a close: the client is still Authenticated
// in NoDogSplash and still has the open, unmetered gate the customer stopped
// paying for. The module must therefore keep OWNING that gate — it stays in
// openGates, the close is retried until it is confirmed, and the failure is
// escalated so an operator can see it — instead of deleting the tracking and
// leaving a gate open that nothing knows about any more.
//
// The tests below stub runNdsctl (the gate's only seam) and make the deauth fail
// deliberately.

// gateFailNdsctl is a stubbed ndsctl seam whose deauth can be made to fail.
// deauthSuccessAfter = 0 means every deauth fails; N > 0 means the first N
// deauth calls fail and later ones succeed.
type gateFailNdsctl struct {
	deauthAttempts     int32
	deauthSuccessAfter int32
}

// allowDeauth makes every subsequent deauth succeed.
func (n *gateFailNdsctl) allowDeauth() {
	atomic.StoreInt32(&n.deauthSuccessAfter, 0)
}

func (n *gateFailNdsctl) attempts() int {
	return int(atomic.LoadInt32(&n.deauthAttempts))
}

// setUpGateTest installs the stubbed ndsctl and guarantees the package's gate
// state is restored afterwards. Retry timers are stopped BEFORE the seam is put
// back, so a retry that outlives the test can never call the restored seam.
func setUpGateTest(t *testing.T, deauthSuccessAfter int) *gateFailNdsctl {
	t.Helper()

	origRunNdsctl, origAuthDelay := runNdsctl, AuthDelay
	n := &gateFailNdsctl{deauthSuccessAfter: int32(deauthSuccessAfter)}

	t.Cleanup(func() {
		gatesMutex.Lock()
		for mac := range openGates {
			delete(openGates, mac)
		}
		for mac := range pendingUntil {
			delete(pendingUntil, mac)
		}
		for mac, timer := range pendingCloseRetries {
			timer.Stop()
			delete(pendingCloseRetries, mac)
		}
		gatesMutex.Unlock()
		runNdsctl = origRunNdsctl
		AuthDelay = origAuthDelay
	})

	AuthDelay = 0
	runNdsctl = func(args ...string) (string, error) {
		if len(args) == 0 {
			return "", fmt.Errorf("ndsctl called without arguments")
		}
		switch args[0] {
		case "auth":
			return fmt.Sprintf("Auth: %s - Granted", args[1]), nil
		case "deauth":
			attempt := int(atomic.AddInt32(&n.deauthAttempts, 1))
			if attempt <= int(atomic.LoadInt32(&n.deauthSuccessAfter)) {
				return "Failed to deauthenticate client", fmt.Errorf("exit status 1")
			}
			return fmt.Sprintf("Auth: %s - Removed", args[1]), nil
		case "json":
			return fmt.Sprintf(`{"id":1,"mac":"%s","state":"Authenticated","downloaded":1024,"uploaded":512}`, args[1]), nil
		}
		return "", fmt.Errorf("unexpected ndsctl call: %v", args)
	}

	return n
}

func gateTracked(macAddress string) bool {
	gatesMutex.Lock()
	defer gatesMutex.Unlock()
	_, tracked := openGates[macAddress]
	return tracked
}

// TestTimedGateStaysTrackedAndIsRetriedWhenDeauthFails is the core contract of
// C1-2(a): the timed gate's expiry deauth fails, and the module must NOT treat
// that as a close. Before the fix the callback logged the error and deleted
// openGates[mac] anyway, so nothing ever tried to close the gate again and the
// client kept an open, unmetered gate for the life of the process.
func TestTimedGateStaysTrackedAndIsRetriedWhenDeauthFails(t *testing.T) {
	ndsctl := setUpGateTest(t, 1<<30) // every deauth fails
	mac := "aa:bb:cc:dd:ee:20"

	if err := OpenGateUntil(mac, time.Now().Unix()+1); err != nil {
		t.Fatalf("OpenGateUntil: %v", err)
	}

	// The gate expires after ~1s and the close fails. Give the retry machinery
	// time to make a second attempt.
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) && ndsctl.attempts() < 2 {
		time.Sleep(20 * time.Millisecond)
	}

	if !gateTracked(mac) {
		t.Fatal("the gate was dropped from tracking after a FAILED close: nothing retries or re-checks it, so the client keeps an open, unmetered gate")
	}
	if got := ndsctl.attempts(); got < 2 {
		t.Fatalf("ndsctl deauth attempts after the failed close = %d, want >= 2: a failed close must be retried", got)
	}
}

// TestTimedGateCloseIsRetriedUntilItIsConfirmed covers the other half: while the
// close keeps failing the gate stays tracked, and the moment ndsctl answers the
// retry the gate is retired — success is what retires the gate, not an attempt.
func TestTimedGateCloseIsRetriedUntilItIsConfirmed(t *testing.T) {
	ndsctl := setUpGateTest(t, 1) // the first deauth fails, the retry succeeds
	mac := "aa:bb:cc:dd:ee:21"

	if err := OpenGateUntil(mac, time.Now().Unix()+1); err != nil {
		t.Fatalf("OpenGateUntil: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && gateTracked(mac) {
		time.Sleep(25 * time.Millisecond)
	}

	if got := ndsctl.attempts(); got < 2 {
		t.Fatalf("ndsctl deauth attempts = %d, want >= 2: the failed close was never retried", got)
	}
	if gateTracked(mac) {
		t.Fatal("the gate is still tracked after a CONFIRMED close: a confirmed close must retire the gate")
	}
}
