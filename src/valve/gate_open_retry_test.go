package valve

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// The gate-OPEN contract — the mirror of the gate-close contract (C1-2).
//
// On hardware (2026-09-25, GL-MT3000, pre17, module pin 2796d96c) the club's
// main loop failed: a customer spent the first allotment, bought again, the
// portal and /balance showed a NEW allotment, and the gate stayed shut — no
// internet, and no OS captive-portal prompt either. The module's own session
// state was healthy, so the purchase itself was granted; what never happened is
// the one thing that opens the gate: `ndsctl auth` (NoDogSplash's mark for the
// MAC is what the firewall enforces — see packaging/files/etc/nftables.d/
// 20-nds-enforce.nft).
//
// The deferred-auth path is the one the shipped portal uses (AuthDelay > 0: the
// browser must be able to render the splash before the client is let through).
// Its failure branch did exactly what the old close path did before C1-2: it
// logged the error, DELETED the gate tracking and returned — so after the
// bounded retries were used up, nothing was tracked, nothing ever retried, and
// the customer kept a paid session with a shut gate. A failed open is not an
// open: it is a late grant, and the module must keep owning it until ndsctl
// confirms it.
//
// These tests stub runNdsctl (the gate's only seam) and make auth fail while
// the client reads back as Preauthenticated — the state in which a shut gate
// must never be mistaken for an open one.

// openFailNdsctl is a stubbed ndsctl seam whose auth can be made to fail.
// authFailures = N means the first N auth calls fail; allowAuth makes every
// subsequent auth succeed.
type openFailNdsctl struct {
	authAttempts int32
	authFailures int32
}

func (n *openFailNdsctl) allowAuth() {
	atomic.StoreInt32(&n.authFailures, 0)
}

func (n *openFailNdsctl) attempts() int {
	return int(atomic.LoadInt32(&n.authAttempts))
}

// setUpOpenGateTest installs a stubbed ndsctl, shrinks the delay and the retry
// backoff so the suite stays fast, and restores the package state afterwards.
func setUpOpenGateTest(t *testing.T, authFailures int) *openFailNdsctl {
	t.Helper()

	origRunNdsctl, origAuthDelay := runNdsctl, AuthDelay
	origAuthRetry, origBackoff := authRetryDelay, openRetryBackoff
	n := &openFailNdsctl{authFailures: int32(authFailures)}

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
		for mac, timer := range pendingAuthRetries {
			timer.Stop()
			delete(pendingAuthRetries, mac)
		}
		gatesMutex.Unlock()
		for _, mac := range openGateTestMACs {
			ClearDataBaseline(mac)
		}
		runNdsctl = origRunNdsctl
		AuthDelay = origAuthDelay
		authRetryDelay = origAuthRetry
		openRetryBackoff = origBackoff
	})

	AuthDelay = 30 * time.Millisecond
	authRetryDelay = 2 * time.Millisecond
	openRetryBackoff = []time.Duration{20 * time.Millisecond}

	// Tear the retry chain down and wait for any attempt already in flight
	// BEFORE the seam above is restored. Cleanups run LIFO, so this one runs
	// first: a retry that outlived the stub would race the restored seam (and
	// the goroutine that owns it would keep calling the real ndsctl).
	t.Cleanup(func() {
		gatesMutex.Lock()
		for mac := range openGates {
			delete(openGates, mac)
		}
		for mac, timer := range pendingAuthRetries {
			timer.Stop()
			delete(pendingAuthRetries, mac)
		}
		gatesMutex.Unlock()

		// A retry that fires after this point finds no tracked gate and
		// abandons before touching the seam; this covers the one attempt that
		// may already be inside authorizeMAC.
		time.Sleep(time.Duration(authMaxAttempts+2) * authRetryDelay * 2)
	})

	runNdsctl = func(args ...string) (string, error) {
		if len(args) == 0 {
			return "", fmt.Errorf("ndsctl called without arguments")
		}
		switch args[0] {
		case "auth":
			attempt := int(atomic.AddInt32(&n.authAttempts, 1))
			if attempt <= int(atomic.LoadInt32(&n.authFailures)) {
				return "Client is not registered yet", fmt.Errorf("exit status 1")
			}
			return fmt.Sprintf("Auth: %s - Granted", args[1]), nil
		case "deauth":
			return fmt.Sprintf("Auth: %s - Removed", args[1]), nil
		case "json":
			// Known to NDS but NOT authorised: the state in which an auth
			// failure must never be read as "the gate is already open".
			return fmt.Sprintf(`{"id":1,"mac":"%s","state":"Preauthenticated","downloaded":1024,"uploaded":512}`, args[1]), nil
		}
		return "", fmt.Errorf("unexpected ndsctl call: %v", args)
	}

	return n
}

var openGateTestMACs = []string{"aa:bb:cc:dd:ee:30", "aa:bb:cc:dd:ee:31"}

// gateOpenIndefinite reports whether the module tracks an indefinite (bytes)
// gate for macAddress — the state a successful paid grant has to reach.
func gateOpenIndefinite(macAddress string) bool {
	gatesMutex.Lock()
	defer gatesMutex.Unlock()
	timer, tracked := openGates[macAddress]
	if !tracked {
		return false
	}
	if _, pending := pendingUntil[macAddress]; pending {
		return false
	}
	return timer == nil
}

// waitFor polls cond until it is true or the deadline passes, and reports
// whether it became true.
func waitFor(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// TestFailedDeferredAuthKeepsTheGateTracked is the contract this fix adds: when
// the deferred auth cannot authorise the client, the gate must NOT be dropped.
// Before the fix the failure branch deleted openGates[mac] after the bounded
// retries were exhausted, so a customer who had already paid was left with a
// shut gate that nothing tracked and nothing would ever retry.
func TestFailedDeferredAuthKeepsTheGateTracked(t *testing.T) {
	ndsctl := setUpOpenGateTest(t, 1<<30) // every auth fails
	mac := openGateTestMACs[0]
	before := OpenFailures()

	if err := OpenGate(mac); err != nil {
		t.Fatalf("OpenGate: %v", err)
	}

	// Let the deferred auth burn its bounded attempts AND arm the retry beyond
	// them: pre-fix the gate is deleted as soon as the bounded pass gives up.
	if waitFor(func() bool { return !gateTracked(mac) || ndsctl.attempts() > authMaxAttempts }, 8*time.Second) {
		// fallthrough: the assertions below name what happened
	}

	if !gateTracked(mac) {
		t.Fatalf("the gate was dropped from tracking after a FAILED open (ndsctl auth attempts=%d): the customer's paid session is now owned by nothing, and nothing will ever open the gate — this is the reported \"balance came back, gate stayed shut\"", ndsctl.attempts())
	}
	if got := ndsctl.attempts(); got <= authMaxAttempts {
		t.Fatalf("ndsctl auth attempts = %d, want > %d: an unconfirmed gate OPEN must keep being retried after the bounded retries are used up, exactly like an unconfirmed close", got, authMaxAttempts)
	}
	if OpenFailures() <= before {
		t.Fatal("the unconfirmed gate open was never escalated: the module log is the operator's surface for it, so a silent failure is an invisible one")
	}
}

// TestLateAuthSuccessFinallyOpensTheGate pins the other half: the retry is not
// busy-work — when the transient condition clears, the gate must actually open,
// and it must open METERED (the bytes session records its baseline when the gate
// opens, which is what keeps the next allotment enforceably finite).
func TestLateAuthSuccessFinallyOpensTheGate(t *testing.T) {
	// The first pass fails entirely AND the first full retry ROUND fails too: the
	// customer's session has to survive more than one round of a transient NDS
	// failure before the grant completes. authorizeMAC retries authMaxAttempts
	// times inside ONE call, so a round is authMaxAttempts auth calls; failing
	// everything (1<<30) and waiting for the SECOND round to burn makes the
	// first pass and the first retry round both fail deterministically, where an
	// exact failure count would race the assertion below (the retry fires from a
	// timer, so the number of calls already made when we look is not fixed).
	ndsctl := setUpOpenGateTest(t, 1<<30)
	mac := openGateTestMACs[1]

	if err := OpenGate(mac); err != nil {
		t.Fatalf("OpenGate: %v", err)
	}

	// The bounded pass and at least one armed retry round have both run out.
	if !waitFor(func() bool { return ndsctl.attempts() >= 2*authMaxAttempts }, 8*time.Second) {
		t.Fatalf("only %d auth attempts were made, want >= %d", ndsctl.attempts(), 2*authMaxAttempts)
	}
	if !gateTracked(mac) {
		t.Fatal("the gate was dropped before the late grant could complete: nothing retries an open the module gave up on")
	}
	if HasDataBaseline(mac) {
		t.Fatal("a metering baseline was recorded for a client that was never authorised: the session would meter against a shut gate")
	}

	// The transient condition clears (NDS registers the client, ndsctl answers).
	ndsctl.allowAuth()

	// The retry must complete the grant: the gate tracked as an indefinite open
	// gate, and metered from the moment it opens. The baseline is recorded right
	// after the gate is confirmed, so it is part of the completion, not a race.
	if !waitFor(func() bool { return gateOpenIndefinite(mac) && HasDataBaseline(mac) }, 8*time.Second) {
		t.Fatalf("the retry never opened the gate (auth attempts=%d, tracked=%v, baseline=%v)", ndsctl.attempts(), gateTracked(mac), HasDataBaseline(mac))
	}
	if !HasDataBaseline(mac) {
		t.Fatal("the late grant opened the gate without recording a metering baseline: the bytes session would be unmetered until the monitor noticed")
	}
}
