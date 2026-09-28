package valve

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// These tests cover the retry machinery of the gate-close contract: the identity
// a retry is armed for, what happens when the customer pays again while a close
// is being retried, and the fact that an unconfirmed close is OBSERVABLE. The
// contract tests (a failed close keeps the gate tracked and is retried) are in
// gate_close_failopen_test.go.

// captureValveLog redirects the package logger (logrus) into a buffer so a test
// can assert what the operator would see.
func captureValveLog(t *testing.T) *bytes.Buffer {
	t.Helper()

	buffer := &bytes.Buffer{}
	previousOut, previousLevel := logrus.StandardLogger().Out, logrus.GetLevel()
	logrus.SetOutput(buffer)
	logrus.SetLevel(logrus.ErrorLevel)
	t.Cleanup(func() {
		logrus.SetOutput(previousOut)
		logrus.SetLevel(previousLevel)
	})
	return buffer
}

// TestCloseGateKeepsTrackingAndReportsFailure: CloseGate is the seam the
// merchant drives. On a failed deauth it must return an error, keep the gate
// tracked, arm a retry, and make the failure visible — a caller that treats the
// errored return as "closed" is the C1-2(b) defect, so the error itself has to
// say that the gate is not confirmed closed.
func TestCloseGateKeepsTrackingAndReportsFailure(t *testing.T) {
	ndsctl := setUpGateTest(t, 1<<30) // every deauth fails
	log := captureValveLog(t)
	mac := "aa:bb:cc:dd:ee:30"

	if err := OpenGateUntil(mac, time.Now().Unix()+3600); err != nil {
		t.Fatalf("OpenGateUntil: %v", err)
	}
	failuresBefore := GateCloseFailures()

	err := CloseGate(mac)
	if err == nil {
		t.Fatal("CloseGate returned nil after a failed deauth: a failed close must not look like a close")
	}
	if !strings.Contains(err.Error(), "NOT confirmed closed") {
		t.Fatalf("CloseGate error = %q, want it to say the close is not confirmed", err)
	}
	if !gateTracked(mac) {
		t.Fatal("CloseGate dropped the gate from tracking after a failed close: nothing retries or re-checks it")
	}
	if got := GateCloseFailures() - failuresBefore; got != 1 {
		t.Fatalf("GateCloseFailures increased by %d, want 1", got)
	}
	if got := ndsctl.attempts(); got < deauthMaxAttempts {
		t.Fatalf("ndsctl deauth attempts = %d, want the close to be retried immediately (%d attempts)", got, deauthMaxAttempts)
	}

	gatesMutex.Lock()
	_, retryArmed := pendingCloseRetries[mac]
	gatesMutex.Unlock()
	if !retryArmed {
		t.Fatal("no close retry was armed: an unconfirmed close must keep being retried")
	}

	if logged := log.String(); !strings.Contains(logged, mac) {
		t.Fatalf("the unconfirmed close was not escalated with the client identity; log was %q", logged)
	}
	if logged := log.String(); !strings.Contains(logged, "Gate close NOT confirmed") {
		t.Fatalf("the unconfirmed close was not escalated; log was %q", logged)
	}
}

// TestCloseRetryAbandonedWhenTheGateWasReopened: a retry may only close the gate
// it was armed for. If the customer buys again while the close is being retried,
// the new gate's epoch differs and the retry must abandon itself — otherwise the
// retry would deauthorize a customer who has just paid.
func TestCloseRetryAbandonedWhenTheGateWasReopened(t *testing.T) {
	ndsctl := setUpGateTest(t, 1<<30) // every deauth fails
	mac := "aa:bb:cc:dd:ee:31"

	if err := OpenGateUntil(mac, time.Now().Unix()+3600); err != nil {
		t.Fatalf("OpenGateUntil: %v", err)
	}

	gatesMutex.Lock()
	epochAtFailure := gateEpochs[mac]
	gatesMutex.Unlock()

	if err := CloseGate(mac); err == nil {
		t.Fatal("expected the close to fail")
	}
	attemptsAtFailure := ndsctl.attempts()

	// The customer buys more time (an extension of the tracked gate).
	if err := OpenGateUntil(mac, time.Now().Unix()+7200); err != nil {
		t.Fatalf("extending the gate: %v", err)
	}

	retryGateClose(mac, epochAtFailure, 1)

	if got := ndsctl.attempts(); got != attemptsAtFailure {
		t.Fatalf("the retry made %d further deauth attempts after the customer paid again: it must abandon itself", got-attemptsAtFailure)
	}
	if !gateTracked(mac) {
		t.Fatal("the retry retired the gate the customer had just extended")
	}
}

// TestStaleGateTimerDoesNotDeauthorizeAnExtendedGate: a timer callback that fires
// after its gate was extended or replaced belongs to a gate that no longer
// exists, and must not take the client's access away. (Before the fix the
// callback deauthorized unconditionally and only guarded the map deletion, so a
// stale callback could cut off a customer who had just paid for more time.)
func TestStaleGateTimerDoesNotDeauthorizeAnExtendedGate(t *testing.T) {
	ndsctl := setUpGateTest(t, 0) // deauth would succeed, but must not be attempted
	mac := "aa:bb:cc:dd:ee:32"

	if err := OpenGateUntil(mac, time.Now().Unix()+1); err != nil {
		t.Fatalf("OpenGateUntil: %v", err)
	}
	gatesMutex.Lock()
	firstEpoch := gateEpochs[mac]
	gatesMutex.Unlock()

	// The customer extends before the first deadline: a new gate, a new epoch.
	if err := OpenGateUntil(mac, time.Now().Unix()+3600); err != nil {
		t.Fatalf("extending the gate: %v", err)
	}

	// The first gate's callback fires late.
	expireTimedGate(mac, firstEpoch)

	if got := ndsctl.attempts(); got != 0 {
		t.Fatalf("a stale timer made %d deauth attempts against a gate that had been extended", got)
	}
	if !gateTracked(mac) {
		t.Fatal("a stale timer retired the extended gate")
	}
}

// TestSetDataBaselineAlwaysRecordsAndReports: the baseline is what makes a bytes
// session meterable, so it is recorded even when ndsctl cannot report the
// client's counters — and that gap is reported rather than swallowed. (Before the
// fix the failure was a Warn and the caller could not tell.)
func TestSetDataBaselineAlwaysRecordsAndReports(t *testing.T) {
	setUpNdsctlJSON(t, "{}") // NDS has no record of this client
	mac := "aa:bb:cc:dd:ee:33"
	t.Cleanup(func() { ClearDataBaseline(mac) })

	err := SetDataBaseline(mac)
	if err == nil {
		t.Fatal("expected SetDataBaseline to report the counters it could not read")
	}
	if !errors.Is(err, ErrClientCountersUnavailable) {
		t.Fatalf("SetDataBaseline error = %v, want ErrClientCountersUnavailable", err)
	}
	if !HasDataBaseline(mac) {
		t.Fatal("SetDataBaseline did not record a baseline: a bytes session without one can never be metered")
	}
}

// TestGetDataUsageSinceBaselineReportsAnUnreadableClient: usage the module cannot
// read is not zero usage. The old contract answered (0, nil), which made a client
// the module cannot see indistinguishable from an idle one — a session metered
// against a permanent 0 never reaches its allotment, so its gate was never closed
// (C1-2c).
func TestGetDataUsageSinceBaselineReportsAnUnreadableClient(t *testing.T) {
	setUpNdsctlJSON(t, "{}")
	mac := "aa:bb:cc:dd:ee:34"

	if err := SetDataBaseline(mac); err != nil {
		// The zero baseline is recorded; only the counters are unavailable.
		if !errors.Is(err, ErrClientCountersUnavailable) {
			t.Fatalf("SetDataBaseline: %v", err)
		}
	}

	usage, err := GetDataUsageSinceBaseline(mac)
	if err == nil {
		t.Fatalf("GetDataUsageSinceBaseline = %d, nil; want an error for a client whose counters cannot be read", usage)
	}
	if !errors.Is(err, ErrClientCountersUnavailable) {
		t.Fatalf("GetDataUsageSinceBaseline error = %v, want ErrClientCountersUnavailable", err)
	}

	ClearDataBaseline(mac)

	if _, err := GetDataUsageSinceBaseline(mac); !errors.Is(err, ErrDataBaselineMissing) {
		t.Fatalf("GetDataUsageSinceBaseline without a baseline = %v, want ErrDataBaselineMissing", err)
	}
}

// setUpNdsctlJSON installs an ndsctl seam that answers `json` with the given
// payload. Any other subcommand is an error, so a test that reaches one fails
// loudly instead of touching a real ndsctl.
func setUpNdsctlJSON(t *testing.T, jsonPayload string) {
	t.Helper()

	origRunNdsctl := runNdsctl
	t.Cleanup(func() { runNdsctl = origRunNdsctl })

	runNdsctl = func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "json" {
			return jsonPayload, nil
		}
		return "", errors.New("unexpected ndsctl call")
	}
}
