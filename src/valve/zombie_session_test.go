package valve

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// A client NoDogSplash has FORGOTTEN (the zombie-session contract).
//
// Measured on the bench MT3000 (pre17, module pin 2796d96c, 2026-09-26) with a
// real Wi-Fi client, a8:a0:92:a5:39:7a: the module held a session for an address
// NoDogSplash no longer knew at all. `ndsctl deauth <mac>` answered
//
//	Client a8:a0:92:a5:39:7a not found.
//
// and exited 1 — and the module read that exit status as an UNCONFIRMED CLOSE. It
// kept the gate tracked, retried it for ever (unconfirmed_closes 113 -> 193 ->
// 195, driving ndsctl at the sweep cadence) and logged "this client may still
// hold open, unmetered access", which is FALSE in this state: NoDogSplash does
// not know the MAC, so there is no access for it to hold. The retry storm is what
// wedged the ndsctl socket ("Socket is not ready for communication : Bad file
// descriptor" every ~5s) — and with a wedged socket a PAID purchase could not be
// granted either (state=PAID, merchant wallet +1 sat, access_granted never true).
// That is the operator's "second purchase showed a new allotment but no internet".
//
// The three failures this file pins, in order:
//
//	1. "client not found" is a COMPLETED close: retire, do not arm a retry;
//	2. the close retry is BOUNDED: neither the attempts nor unconfirmed_closes
//	   may keep growing once the budget is spent;
//	3. the wording must match the verified state — never "may still hold open,
//	   unmetered access" for a MAC ndsctl does not know.
//
// The budget and the wording assertions are deliberately phrased against the
// *behaviour* (how many deauth calls reach the enforcement layer, whether the
// counter moves, what the operator is told) rather than against a particular
// implementation of the bound.

// zombieNdsctl is a stubbed ndsctl seam whose deauth answers exactly what the
// real one answered on the bench. It counts the deauth calls per MAC, so a test
// can assert how hard the module drives the enforcement layer.
type zombieNdsctl struct {
	mu         sync.Mutex
	deauths    map[string]int
	forgotten  bool // "Client <mac> not found." + exit 1 — NDS does not know the client
	wedged     bool // exit 1 with no answer about the client at all (broken socket)
	registered bool // what `ndsctl json <mac>` answers
}

// forgetClient makes deauth answer the way it did for the bench client
// NoDogSplash had dropped from its client list.
func (z *zombieNdsctl) forgetClient() {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.forgotten, z.wedged = true, false
}

// wedgeSocket makes deauth fail the way a wedged ndsctl socket fails: an error
// with no answer about the client, so nothing about the client is verified.
func (z *zombieNdsctl) wedgeSocket() {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.forgotten, z.wedged = false, true
}

// setRegistered models whether NoDogSplash lists the client at all.
func (z *zombieNdsctl) setRegistered(registered bool) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.registered = registered
}

// attemptsFor returns how many deauth calls reached ndsctl for one address.
func (z *zombieNdsctl) attemptsFor(macAddress string) int {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.deauths[macAddress]
}

// setUpZombieGateTest installs the zombie stub and restores the package's gate
// state afterwards. Retry timers are stopped BEFORE the seam is restored, so a
// retry that outlives the test can never call the restored seam.
func setUpZombieGateTest(t *testing.T) *zombieNdsctl {
	t.Helper()

	origRunNdsctl, origAuthDelay := runNdsctl, AuthDelay
	z := &zombieNdsctl{deauths: make(map[string]int), registered: true}

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
		mac := ""
		if len(args) > 1 {
			mac = args[1]
		}
		switch args[0] {
		case "auth":
			return fmt.Sprintf("Auth: %s - Granted", mac), nil
		case "deauth":
			z.mu.Lock()
			z.deauths[mac]++
			forgotten, wedged := z.forgotten, z.wedged
			z.mu.Unlock()
			if forgotten {
				return fmt.Sprintf("Client %s not found.\n", mac), fmt.Errorf("exit status 1")
			}
			if wedged {
				return "Socket is not ready for communication : Bad file descriptor\n", fmt.Errorf("exit status 1")
			}
			return fmt.Sprintf("Auth: %s - Removed", mac), nil
		case "json":
			z.mu.Lock()
			registered := z.registered
			z.mu.Unlock()
			if !registered {
				return "{}", nil
			}
			return fmt.Sprintf(`{"id":1,"mac":"%s","state":"Authenticated","downloaded":1024,"uploaded":512}`, mac), nil
		}
		return "", fmt.Errorf("unexpected ndsctl call: %v", args)
	}

	return z
}

// captureValveLogAt redirects the package logger into a buffer at the given
// level, so a test can assert what an operator would actually read.
func captureValveLogAt(t *testing.T, level logrus.Level) *bytes.Buffer {
	t.Helper()

	buffer := &bytes.Buffer{}
	previousOut, previousLevel := logrus.StandardLogger().Out, logrus.GetLevel()
	logrus.SetOutput(buffer)
	logrus.SetLevel(level)
	t.Cleanup(func() {
		logrus.SetOutput(previousOut)
		logrus.SetLevel(previousLevel)
	})
	return buffer
}

// TestNdsForgettingAClientIsACompletedClose is failure 1 of the zombie session.
//
// ndsctl's exit status is not a yes/no answer about the gate: for `deauth` it
// exits 1 BOTH when the deauthorization failed and when NoDogSplash does not know
// the client at all. Only the second answer is terminal — the enforcement layer
// holds nothing for this MAC, so there is nothing left to close, and retrying it
// is a retry that can never converge.
func TestNdsForgettingAClientIsACompletedClose(t *testing.T) {
	ndsctl := setUpZombieGateTest(t)
	ndsctl.forgetClient()
	log := captureValveLogAt(t, logrus.InfoLevel)
	mac := "aa:bb:cc:dd:ee:50"

	if err := OpenGateUntil(mac, time.Now().Unix()+3600); err != nil {
		t.Fatalf("OpenGateUntil: %v", err)
	}

	failuresBefore := GateCloseFailures()

	if err := CloseGate(mac); err != nil {
		t.Fatalf("CloseGate = %v for a client NoDogSplash does not know (ndsctl answered %q): the enforcement layer holds no such client, so the close is COMPLETE — reading this as an unconfirmed close is what keeps the session unretirable and retries for ever",
			err, fmt.Sprintf("Client %s not found.", mac))
	}

	if gateTracked(mac) {
		t.Fatal("the gate of a client NoDogSplash does not know is still tracked: its close will be retried for ever (measured: unconfirmed_closes 113 -> 193)")
	}

	if got := GateCloseFailures() - failuresBefore; got != 0 {
		t.Fatalf("unconfirmed_closes grew by %d for a client NoDogSplash does not know: this state is not a failure at all, it is a completed close", got)
	}

	gatesMutex.Lock()
	_, retryArmed := pendingCloseRetries[mac]
	gatesMutex.Unlock()
	if retryArmed {
		t.Fatal("a close retry was armed for a client NoDogSplash does not know: it can only ever fail, so the retry is the loop that wedges the ndsctl socket")
	}

	if got := ndsctl.attemptsFor(mac); got > 1 {
		t.Fatalf("ndsctl deauth calls for a client NoDogSplash does not know = %d, want 1: trying harder cannot close a client that is not there, and the storm is what breaks authorisation for NEW purchases", got)
	}

	logged := log.String()
	if !strings.Contains(logged, "already gone") {
		t.Fatalf("the operator was never told the session was retired because its client is gone from NoDogSplash (the required wording is an explicit INFO line): log = %q", logged)
	}
	if strings.Contains(logged, "unmetered access") {
		t.Fatalf("the log claims the client may hold open, unmetered access although ndsctl said NoDogSplash does not know the MAC at all — a reviewer reading that line concludes the release hands out free internet: log = %q", logged)
	}
}

// TestUnconfirmedCloseAttemptsAreBounded is failure 2: whatever the module does
// about a close it cannot confirm, it must not be able to do it for ever. The
// measured loop drove ndsctl twice per second per stuck address and its counter
// climbed without bound (113 -> 193 -> 195); a session that needs an operator
// must stop consuming the router instead.
func TestUnconfirmedCloseAttemptsAreBounded(t *testing.T) {
	ndsctl := setUpZombieGateTest(t)
	ndsctl.wedgeSocket()
	log := captureValveLogAt(t, logrus.ErrorLevel)
	mac := "aa:bb:cc:dd:ee:51"

	if err := OpenGateUntil(mac, time.Now().Unix()+3600); err != nil {
		t.Fatalf("OpenGateUntil: %v", err)
	}

	// The merchant drives CloseGate on every sweep (twice a second) while the
	// close is unconfirmed, so this is the shape of the production loop.
	for i := 0; i < 20; i++ {
		_ = CloseGate(mac)
	}
	attemptsAt20 := ndsctl.attemptsFor(mac)
	failuresAt20 := GateCloseFailures()
	if attemptsAt20 == 0 {
		t.Fatal("no close was attempted at all: this test needs a failing close to bound")
	}

	for i := 0; i < 20; i++ {
		_ = CloseGate(mac)
	}

	if got := ndsctl.attemptsFor(mac); got != attemptsAt20 {
		t.Fatalf("ndsctl deauth calls kept growing after the close budget was spent (%d -> %d): the module keeps hammering the one interface the gate depends on", attemptsAt20, got)
	}
	if got := GateCloseFailures(); got != failuresAt20 {
		t.Fatalf("unconfirmed_closes kept growing after the close budget was spent (%d -> %d): the counter must not be able to grow monotonically", failuresAt20, got)
	}
	if !gateTracked(mac) {
		t.Fatal("the module stopped retrying the close AND dropped the gate: the record that says this client must be closed is gone, which is the fail-open direction (a client who stopped paying keeps internet)")
	}

	if got := strings.Count(log.String(), "UNRESOLVED"); got != 1 {
		t.Fatalf("the abandonment of an unconfirmable close was escalated %d times, want exactly 1: an operator needs one loud, specific line — not a line per sweep", got)
	}
}
