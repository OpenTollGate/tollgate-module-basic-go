package valve

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

// ndsctlTimeout is the maximum time to wait for an ndsctl command to complete.
// ndsctl typically responds in 230-350ms; this guards against NoDogSplash
// deadlocks (issue #387) that can cause ndsctl to hang indefinitely.
const ndsctlTimeout = 5 * time.Second

// authMaxAttempts bounds the number of authorizeMAC attempts.
//
// NoDogSplash does not always have a client session registered the instant we
// try to authorize it — most notably in the two-router reseller flow, where the
// upstream's NDS creates the client session asynchronously (see
// upstream_session_manager/tollgate_prober.go's captive-portal trigger). Without
// a retry the first payment attempt fails with "failed to open gate" and only
// recovers via the token-recovery path ~60-90s later. The auth operation is
// idempotent, so a bounded retry is safe.
const authMaxAttempts = 5

// authRetryDelay is the wait between authorizeMAC retries. It is a var so tests
// can shrink it to keep the suite fast.
var authRetryDelay = 400 * time.Millisecond

// deauthMaxAttempts bounds the immediate retries of one gate close. A deauth
// that fails is NOT a close: the client is still Authenticated in NoDogSplash
// and still holds the open, unmetered gate the customer stopped paying for, so
// the attempt is retried instead of being reported as done (C1-2).
const deauthMaxAttempts = 3

// deauthRetryDelay is the wait between those immediate retries. A var so tests
// can shrink it.
var deauthRetryDelay = 400 * time.Millisecond

// closeRetryBackoff is the schedule for re-attempting a close that is still
// unconfirmed. The last entry repeats: a gate whose close keeps failing stays
// tracked and keeps being retried, because the alternative — dropping the
// tracking — is exactly the free, unmetered internet this module must never
// hand out. A var so tests can shrink it.
var closeRetryBackoff = []time.Duration{
	2 * time.Second,
	5 * time.Second,
	15 * time.Second,
	30 * time.Second,
	time.Minute,
}

// openRetryBackoff is the schedule for re-attempting an authorisation ndsctl
// did not confirm, i.e. a gate the module owes a customer who has ALREADY PAID
// but has not managed to open. The last entry repeats: the gate stays tracked
// and the auth keeps being retried, because the alternative — forgetting it — is
// a charged customer with no internet and nothing left that would ever open the
// gate (the mirror of the close contract, C1-2). A var so tests can shrink it.
var openRetryBackoff = []time.Duration{
	2 * time.Second,
	5 * time.Second,
	15 * time.Second,
	30 * time.Second,
	time.Minute,
}

// runNdsctl executes an ndsctl command with a timeout.
// It returns the combined stdout+stderr output and any error.
// It is a var (not a func) so tests can stub it without a real ndsctl binary.
var runNdsctl = func(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ndsctlTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ndsctl", args...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// isValidMAC checks that the input is a well-formed MAC address (e.g. "aa:bb:cc:dd:ee:ff").
// Rejects malformed strings before they reach ndsctl.
func isValidMAC(mac string) bool {
	_, err := net.ParseMAC(mac)
	return err == nil
}

// Module-level logger with pre-configured module field
var logger = logrus.WithField("module", "valve")

// AuthDelay controls a delay before ndsctl auth, giving the captive portal
// time to load a redirect page before Android detects connectivity and
// closes the WebView. Set to 0 (default) for immediate auth.
var AuthDelay time.Duration

// stopCh is closed by Stop() to cancel all in-flight delayed auth goroutines.
var stopCh = make(chan struct{})

// openGates keeps track of MAC addresses that have been authorized.
// pendingUntil stores target deauth timestamps for MACs awaiting delayed auth,
// so extensions during the delay window are preserved (fixes concurrent payment bug).
var (
	openGates    = make(map[string]*time.Timer)
	gatesMutex   = &sync.Mutex{}
	pendingUntil = make(map[string]int64)

	// gateEpochs says WHICH gate of a MAC the module is tracking. Every path
	// that opens, extends, replaces or retires a gate bumps it, and a close
	// retry remembers the epoch it was armed under. When the epoch has moved on
	// the retry belongs to a gate that no longer exists, so it abandons itself
	// instead of deauthorizing a client who has since paid again.
	gateEpochs = make(map[string]uint64)

	// pendingCloseRetries holds the in-flight retry timers of unconfirmed
	// closes, at most one per MAC.
	pendingCloseRetries = make(map[string]*time.Timer)

	// pendingAuthRetries holds the in-flight retry timers of unconfirmed
	// OPENS, at most one per MAC. An open that ndsctl has not confirmed is a
	// customer who has paid for access it does not have, so like a close it
	// is retried until it is confirmed.
	pendingAuthRetries = make(map[string]*time.Timer)
)

// gateOpenFailures counts opens ndsctl has not confirmed. It backs
// OpenFailures, the counter the module's operator-visible surfaces use to report
// how many paying customers may be sitting behind a shut gate.
var gateOpenFailures uint64

// OpenFailures reports how many gate opens have gone unconfirmed since the
// process started. A non-zero value means a customer's payment was granted and
// ndsctl has not accepted the authorisation that opens the gate: the gate is
// still tracked, the auth is still being retried, and that customer has paid for
// access it does not have right now.
func OpenFailures() uint64 {
	return atomic.LoadUint64(&gateOpenFailures)
}

// openRetryDelay returns the backoff before the given 1-based open-retry
// attempt. Mirror of closeRetryDelay: the last entry repeats.
func openRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	index := attempt - 1
	if index >= len(openRetryBackoff) {
		index = len(openRetryBackoff) - 1
	}
	return openRetryBackoff[index]
}

// gateOpenFailure records an unconfirmed open. This is the escalation: a failed
// OPEN is the customer-facing mirror of a failed close — the money is in the
// operator's wallet and the customer has nothing — so it names the client, the
// attempt, the running total and when the next attempt runs, on the module log
// that is the operator's only surface.
func gateOpenFailure(macAddress string, attempt int, err error, retryIn time.Duration) {
	total := atomic.AddUint64(&gateOpenFailures, 1)

	fields := logrus.Fields{
		"mac_address":       macAddress,
		"attempt":           attempt,
		"unconfirmed_opens": total,
		"error":             err,
	}
	if retryIn > 0 {
		fields["retry_in"] = retryIn.String()
	}
	logger.WithFields(fields).Error("Gate open NOT confirmed for client: the paid session stays tracked and the auth is retried — until ndsctl confirms it, this customer has paid for access it does not have")
}

// gateCloseFailures counts closes ndsctl has not confirmed. It backs
// GateCloseFailures, the counter the module's operator-visible surfaces use to
// report how many gates may still be open after a failed close.
var gateCloseFailures uint64

// GateCloseFailures reports how many gate closes have gone unconfirmed since the
// process started. A non-zero value means the module tried to take a client's
// access away and ndsctl did not confirm it: the gate is still tracked and the
// close is still being retried, and that client may still hold open, unmetered
// access right now.
func GateCloseFailures() uint64 {
	return atomic.LoadUint64(&gateCloseFailures)
}

// ndsctlMutex ensures only one ndsctl command runs at a time
var ndsctlMutex = &sync.Mutex{}

func Stop() {
	close(stopCh)
}

// authorizeMAC authorizes a MAC address using ndsctl.
//
// It retries up to authMaxAttempts because NoDogSplash may not have registered
// the client session yet at the moment of the call (e.g. the reseller client's
// session is still being created by an upstream captive-portal trigger). This
// removes the transient first-attempt "failed to open gate" observed in the
// two-router autopay flow.
func authorizeMAC(macAddress string) error {
	var lastErr error
	for attempt := 1; attempt <= authMaxAttempts; attempt++ {
		ndsctlMutex.Lock()
		output, err := runNdsctl("auth", macAddress)
		ndsctlMutex.Unlock()

		if err == nil {
			logger.WithFields(logrus.Fields{
				"mac_address": macAddress,
				"output":      output,
				"attempts":    attempt,
			}).Info("Authorization successful for MAC")
			return nil
		}

		lastErr = err
		// NDS 5.0.2 exits 1 when the client is already Authenticated; the gate
		// is open in that case, so this auth "failure" is success (issue #403).
		if state, perr := CheckClientState(macAddress); perr == nil && state.Authenticated {
			logger.WithFields(logrus.Fields{
				"mac_address": macAddress,
				"attempt":     attempt,
			}).Info("Client already Authenticated in NDS; treating gate as open")
			return nil
		}
		if attempt == authMaxAttempts {
			break
		}
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"attempt":     attempt,
			"output":      output,
			"error":       err,
		}).Debug("ndsctl auth failed, retrying (NoDogSplash may not have registered the client yet)")
		time.Sleep(authRetryDelay)
	}

	logger.WithFields(logrus.Fields{
		"mac_address": macAddress,
		"error":       lastErr,
	}).Error("Error authorizing MAC address")
	return lastErr
}

// deauthorizeMAC deauthorizes a MAC address using ndsctl
func deauthorizeMAC(macAddress string) error {
	ndsctlMutex.Lock()
	output, err := runNdsctl("deauth", macAddress)
	ndsctlMutex.Unlock()

	if err != nil {
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"error":       err,
		}).Error("Error deauthorizing MAC address")
		return err
	}

	logger.WithFields(logrus.Fields{
		"mac_address": macAddress,
		"output":      output,
	}).Debug("Deauthorization successful for MAC")
	return nil
}

// ---------------------------------------------------------------------------
// Gate close: a failure to close is not a close (C1-2)
//
// ndsctl deauth is the ONLY way this module takes a customer's access away, so
// every path that ends a session has to keep OWNING the gate until ndsctl
// confirms the close:
//
//   * a failed deauth leaves the gate tracked — the deletion of the tracked
//     entry is what the old code did, and it left a client online through an
//     open gate that nothing would ever close again;
//   * the close is retried, at once and then on a backoff, until it succeeds;
//   * every unconfirmed close is escalated (an ERROR line carrying the client
//     identity plus the GateCloseFailures counter), because the module's log is
//     its operator surface and a silent failure here is an invisible one;
//   * a retry abandons itself when the gate it was armed for has been replaced
//     or extended: the failure direction must never be "a customer who paid
//     loses access", only "the module keeps trying to close what it owns".
// ---------------------------------------------------------------------------

// closeGateConfirmed deauthorizes macAddress and reports whether the gate is
// closed. Confirmation is ndsctl's own answer: a deauth that fails is not a
// close, so it is retried up to deauthMaxAttempts with a short delay. A nil
// error means the client is deauthorized; a non-nil error means the close is
// UNCONFIRMED and the caller must keep the gate tracked.
func closeGateConfirmed(macAddress string) error {
	var lastErr error
	for attempt := 1; attempt <= deauthMaxAttempts; attempt++ {
		if err := deauthorizeMAC(macAddress); err != nil {
			lastErr = err
		} else {
			if attempt > 1 {
				logger.WithFields(logrus.Fields{
					"mac_address": macAddress,
					"attempts":    attempt,
				}).Info("Gate close confirmed after retry")
			}
			return nil
		}
		if attempt < deauthMaxAttempts {
			time.Sleep(deauthRetryDelay)
		}
	}
	return lastErr
}

// closeRetryDelay returns the backoff before the given 1-based retry attempt.
// The schedule's last entry repeats, so a close that keeps failing keeps being
// retried instead of being abandoned.
func closeRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	index := attempt - 1
	if index >= len(closeRetryBackoff) {
		index = len(closeRetryBackoff) - 1
	}
	return closeRetryBackoff[index]
}

// gateCloseFailure records an unconfirmed close. This is the escalation: the
// operator's surface is the module log, so the failure is an ERROR line naming
// the client, the attempts made, the running total of unconfirmed closes and
// when the next attempt runs. The count is also readable through
// GateCloseFailures.
func gateCloseFailure(macAddress string, attempt int, err error, retryIn time.Duration) {
	total := atomic.AddUint64(&gateCloseFailures, 1)

	fields := logrus.Fields{
		"mac_address":        macAddress,
		"attempt":            attempt,
		"unconfirmed_closes": total,
		"error":              err,
	}
	if retryIn > 0 {
		fields["retry_in"] = retryIn.String()
	}
	logger.WithFields(fields).Error("Gate close NOT confirmed for client: the gate stays tracked and the close is retried — until ndsctl confirms it, this client may still hold open, unmetered access")
}

// markGateOpenedLocked records that the gate of macAddress has been opened,
// extended, replaced or re-opened, and returns the epoch of the new gate.
// Caller must hold gatesMutex.
func markGateOpenedLocked(macAddress string) uint64 {
	gateEpochs[macAddress]++
	return gateEpochs[macAddress]
}

// nextGateEpochLocked returns the epoch that markGateOpenedLocked will assign
// when the caller installs the gate it is about to open. It exists so a timer
// callback can capture the identity of its gate as an immutable value BEFORE the
// timer is armed — capturing the timer pointer instead would be a data race, and
// the epoch also invalidates the callback when the gate is extended, replaced or
// retired afterwards. Caller must hold gatesMutex.
func nextGateEpochLocked(macAddress string) uint64 {
	return gateEpochs[macAddress] + 1
}

// retireGateLocked drops every trace of a gate whose close is CONFIRMED. The
// gate leaves the active set, its pending delayed auth and pending close retry
// are stopped and forgotten, and its data baseline is cleared. The epoch is
// bumped so a retry still in flight for this gate abandons instead of
// deauthorizing the client of a newer one. Caller must hold gatesMutex.
func retireGateLocked(macAddress string) {
	gateEpochs[macAddress]++
	delete(openGates, macAddress)
	delete(pendingUntil, macAddress)
	if timer, ok := pendingCloseRetries[macAddress]; ok {
		timer.Stop()
		delete(pendingCloseRetries, macAddress)
	}
	if timer, ok := pendingAuthRetries[macAddress]; ok {
		timer.Stop()
		delete(pendingAuthRetries, macAddress)
	}
	ClearDataBaseline(macAddress)
}

// finishGateClose retires the tracking of a gate whose close is confirmed. It
// refuses to retire a gate that has been replaced or extended while the close
// was in flight — and because that close has just taken away access the customer
// bought again, it authorizes the client back. It reports whether the retirement
// happened; false means the close raced a renewal of the gate.
func finishGateClose(macAddress string, epoch uint64) bool {
	gatesMutex.Lock()
	raced := gateEpochs[macAddress] != epoch
	if !raced {
		retireGateLocked(macAddress)
	}
	gatesMutex.Unlock()

	if !raced {
		return true
	}

	logger.WithField("mac_address", macAddress).Warn("A confirmed gate close raced a gate extension or reopening: restoring the client's access")
	if err := authorizeMAC(macAddress); err != nil {
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"error":       err,
		}).Error("Could not restore the client's access after a raced gate close")
	}
	return false
}

// scheduleCloseRetry arms the retry of an unconfirmed close: after the backoff
// for this attempt the close is re-attempted, and if it fails again the next
// attempt is armed, for ever (the backoff stops growing). The gate stays in
// openGates throughout, so the state that says "this client must be closed" is
// never lost.
func scheduleCloseRetry(macAddress string, epoch uint64, attempt int) {
	delay := closeRetryDelay(attempt)

	gatesMutex.Lock()
	if _, tracked := openGates[macAddress]; !tracked {
		// Nothing left to close: the close was confirmed by another path (or the
		// gate was never open), so there is nothing to retry.
		gatesMutex.Unlock()
		return
	}
	if previous, ok := pendingCloseRetries[macAddress]; ok {
		previous.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(delay, func() {
		gatesMutex.Lock()
		if pendingCloseRetries[macAddress] == timer {
			delete(pendingCloseRetries, macAddress)
		}
		gatesMutex.Unlock()

		retryGateClose(macAddress, epoch, attempt)
	})
	pendingCloseRetries[macAddress] = timer
	gatesMutex.Unlock()

	logger.WithFields(logrus.Fields{
		"mac_address": macAddress,
		"attempt":     attempt,
		"retry_in":    delay.String(),
	}).Warn("Retrying an unconfirmed gate close")
}

// retryGateClose re-attempts an unconfirmed close. It abandons the retry when
// the gate it was armed for is gone or has been replaced: a retry must never
// deauthorize a client who has paid again in the meantime.
func retryGateClose(macAddress string, epoch uint64, attempt int) {
	gatesMutex.Lock()
	_, tracked := openGates[macAddress]
	currentEpoch := gateEpochs[macAddress]
	gatesMutex.Unlock()

	if !tracked {
		logger.WithField("mac_address", macAddress).Info("Gate close retry abandoned: the gate is no longer tracked")
		return
	}
	if currentEpoch != epoch {
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"gate_epoch":  currentEpoch,
		}).Warn("Gate close retry abandoned: the gate was extended or reopened since the close failed")
		return
	}

	if err := closeGateConfirmed(macAddress); err != nil {
		next := attempt + 1
		gateCloseFailure(macAddress, next, err, closeRetryDelay(next))
		scheduleCloseRetry(macAddress, epoch, next)
		return
	}

	logger.WithFields(logrus.Fields{
		"mac_address": macAddress,
		"attempts":    attempt,
	}).Info("Gate close confirmed after retry")
	finishGateClose(macAddress, epoch)
}

// closeTrackedGateNow closes a tracked gate whose deadline has passed (the
// delayed-auth expiry path) under the same contract as every other close: keep
// it tracked and retry unless ndsctl confirms the close.
func closeTrackedGateNow(macAddress string) {
	gatesMutex.Lock()
	epoch := gateEpochs[macAddress]
	gatesMutex.Unlock()

	if err := closeGateConfirmed(macAddress); err != nil {
		gateCloseFailure(macAddress, deauthMaxAttempts, err, closeRetryDelay(1))
		scheduleCloseRetry(macAddress, epoch, 1)
		return
	}
	finishGateClose(macAddress, epoch)
}

// expireTimedGate is the callback of a timed gate's expiry timer, for the gate
// whose epoch is `epoch`. It acts only if that gate is still the tracked one: a
// callback whose gate was extended, replaced or already closed in the meantime
// must not deauthorize the client of the newer gate. On a confirmed close the
// gate is retired; on an unconfirmed one the gate stays tracked and the close is
// retried.
func expireTimedGate(macAddress string, epoch uint64) {
	gatesMutex.Lock()
	currentEpoch := gateEpochs[macAddress]
	gatesMutex.Unlock()

	if currentEpoch != epoch {
		logger.WithField("mac_address", macAddress).Debug("Stale gate timer fired after the gate was extended, replaced or closed: not deauthorizing")
		return
	}

	if err := closeGateConfirmed(macAddress); err != nil {
		gateCloseFailure(macAddress, deauthMaxAttempts, err, closeRetryDelay(1))
		scheduleCloseRetry(macAddress, epoch, 1)
		return
	}

	logger.WithField("mac_address", macAddress).Debug("Successfully deauthorized MAC after timeout")
	finishGateClose(macAddress, epoch)
}

// OpenGateUntil opens the gate (if not opened yet) and sets a timer until the timestamp.
// If there is already a timer running, it will extend the timer.
// When AuthDelay > 0, auth is deferred to a goroutine that reads the latest
// untilTimestamp from pendingUntil — extensions during the delay are preserved.
func OpenGateUntil(macAddress string, untilTimestamp int64) error {
	if !isValidMAC(macAddress) {
		return fmt.Errorf("invalid MAC address format: %s", macAddress)
	}

	now := time.Now().Unix()

	durationSeconds := untilTimestamp - now

	if durationSeconds <= 0 {
		return fmt.Errorf("timestamp %d is in the past (current time: %d)", untilTimestamp, now)
	}

	logger.WithFields(logrus.Fields{
		"mac_address":      macAddress,
		"until_timestamp":  untilTimestamp,
		"duration_seconds": durationSeconds,
	}).Info("Opening gate until timestamp")

	gatesMutex.Lock()
	defer gatesMutex.Unlock()

	existingTimer, exists := openGates[macAddress]

	if !exists {
		if AuthDelay > 0 {
			pendingUntil[macAddress] = untilTimestamp
			openGates[macAddress] = nil
			epoch := markGateOpenedLocked(macAddress)
			go delayedAuth(macAddress, epoch)
			logger.WithFields(logrus.Fields{
				"mac_address": macAddress,
				"delay":       AuthDelay,
			}).Info("Scheduled delayed auth for redirect")
			return nil
		}

		err := authorizeMAC(macAddress)
		if err != nil {
			return fmt.Errorf("error authorizing MAC: %w", err)
		}
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
		}).Warn("ndsctl re-auth failed for known client, continuing with timer")
	}

	if !exists {
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
		}).Debug("New authorization for MAC")
	} else if _, pending := pendingUntil[macAddress]; pending {
		pendingUntil[macAddress] = untilTimestamp
		logger.WithFields(logrus.Fields{
			"mac_address":     macAddress,
			"until_timestamp": untilTimestamp,
		}).Info("Extended pending delayed auth")
		return nil
	} else {
		if existingTimer != nil {
			existingTimer.Stop()
		}
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
		}).Debug("Extending access for already authorized MAC")
	}

	// The callback captures the epoch of the gate it belongs to (immutable, and
	// bumped by the install below), so a callback that fires after the gate was
	// extended or replaced does not deauthorize the client of the newer gate.
	// gatesMutex is held for this whole function, so the epoch read here is the
	// one markGateOpenedLocked assigns.
	epoch := nextGateEpochLocked(macAddress)
	duration := time.Duration(durationSeconds) * time.Second
	timer := time.AfterFunc(duration, func() {
		expireTimedGate(macAddress, epoch)
	})

	openGates[macAddress] = timer
	markGateOpenedLocked(macAddress)

	return nil
}

// OpenGate authorizes a MAC address without a timer.
// It's used for data-based sessions that are closed by a tracker.
// It captures the current data usage as a baseline for tracking.
func OpenGate(macAddress string) error {
	if !isValidMAC(macAddress) {
		return fmt.Errorf("invalid MAC address format: %s", macAddress)
	}

	gatesMutex.Lock()
	defer gatesMutex.Unlock()

	_, exists := openGates[macAddress]
	if existingTimer, ok := openGates[macAddress]; ok {
		if existingTimer != nil {
			existingTimer.Stop()
		}
		logger.WithField("mac_address", macAddress).Info("Replacing existing timed gate with indefinite data-based gate.")
	}

	if AuthDelay > 0 {
		if !exists {
			pendingUntil[macAddress] = 1
			// The gate this goroutine opens is marked opened by the tail of this
			// function (it is what stores the nil timer), so the epoch it will
			// carry is the NEXT one. Handing the goroutine that epoch — instead
			// of the one a mark here would assign and the tail would immediately
			// supersede — is what lets the deferred auth and its retries act only
			// while THIS gate is still the tracked one, exactly like the timer in
			// OpenGateUntil.
			epoch := nextGateEpochLocked(macAddress)
			go delayedAuthIndefinite(macAddress, epoch)
			logger.WithFields(logrus.Fields{
				"mac_address": macAddress,
				"delay":       AuthDelay,
			}).Info("Scheduled delayed auth for redirect")
		} else if _, pending := pendingUntil[macAddress]; pending {
			logger.WithField("mac_address", macAddress).Info("Extending pending delayed indefinite auth")
		}
	} else {
		err := authorizeMAC(macAddress)
		if err != nil {
			return err
		}

		// A bytes session without a metering baseline cannot be metered, so the
		// baseline is recorded unconditionally — from zero when ndsctl cannot
		// report the client's counters yet. Refusing the grant here would take
		// the access away from a customer who has already paid; the gap is
		// escalated instead, and the usage monitor re-establishes the baseline
		// (merchant.checkDataUsage) rather than skipping the session for ever.
		if err := SetDataBaseline(macAddress); err != nil {
			logger.WithFields(logrus.Fields{
				"mac_address": macAddress,
				"error":       err,
			}).Error("Metering baseline recorded from zero: ndsctl could not report the client's counters, so the session is metered from the gate opening")
		}
	}

	// Store a nil timer to indicate an indefinite gate
	openGates[macAddress] = nil
	markGateOpenedLocked(macAddress)
	return nil
}

// CloseGate deauthorizes a MAC address and, only once that close is CONFIRMED,
// removes it from the active gates.
//
// An error from this function means the gate may still be open: it stays
// tracked, the close is retried (immediately, then on a backoff) and the failure
// is escalated. Callers must NOT treat an errored CloseGate as a closed gate —
// retiring a session on it is how a customer keeps free, unmetered internet.
func CloseGate(macAddress string) error {
	if !isValidMAC(macAddress) {
		return fmt.Errorf("invalid MAC address format: %s", macAddress)
	}

	gatesMutex.Lock()
	_, tracked := openGates[macAddress]
	epoch := gateEpochs[macAddress]
	gatesMutex.Unlock()

	if !tracked {
		logger.WithField("mac_address", macAddress).Warn("Attempted to close a gate that was not open.")
		// still attempt to deauth, in case the state is out of sync
	}

	if err := closeGateConfirmed(macAddress); err != nil {
		gateCloseFailure(macAddress, deauthMaxAttempts, err, closeRetryDelay(1))
		scheduleCloseRetry(macAddress, epoch, 1)
		return fmt.Errorf("gate for MAC %s is NOT confirmed closed: %w", macAddress, err)
	}

	if !finishGateClose(macAddress, epoch) {
		return fmt.Errorf("gate for MAC %s was reopened while it was being closed; the client was authorized again", macAddress)
	}
	return nil
}

// ClientStats represents the statistics for a single client from ndsctl
type ClientStats struct {
	ID           int     `json:"id"`
	IP           string  `json:"ip"`
	MAC          string  `json:"mac"`
	Added        int64   `json:"added"`
	Active       int64   `json:"active"`
	Duration     int64   `json:"duration"`
	Token        string  `json:"token"`
	State        string  `json:"state"`
	Downloaded   uint64  `json:"downloaded"` // in kilobytes
	AvgDownSpeed float64 `json:"avg_down_speed"`
	Uploaded     uint64  `json:"uploaded"` // in kilobytes
	AvgUpSpeed   float64 `json:"avg_up_speed"`
}

// GetClientStats retrieves the current data usage statistics for a MAC address from ndsctl
// Returns downloaded and uploaded in bytes (converted from kilobytes)
// Returns error if client not found or command fails
// This function is thread-safe and serializes ndsctl calls
func GetClientStats(macAddress string) (downloaded uint64, uploaded uint64, err error) {
	if !isValidMAC(macAddress) {
		return 0, 0, fmt.Errorf("invalid MAC address format: %s", macAddress)
	}

	// Serialize ndsctl calls to prevent concurrent execution issues
	ndsctlMutex.Lock()
	output, err := runNdsctl("json", macAddress)
	ndsctlMutex.Unlock() // Unlock immediately after command completes

	if err != nil {
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"error":       err,
		}).Error("Error executing ndsctl json")
		return 0, 0, fmt.Errorf("failed to execute ndsctl json for MAC %s: %w", macAddress, err)
	}

	// Check for empty response (client not found)
	trimmed := output
	if trimmed == "{}" || trimmed == "{}\n" {
		return 0, 0, fmt.Errorf("client with MAC %s not found in ndsctl", macAddress)
	}

	var stats ClientStats
	err = json.Unmarshal([]byte(output), &stats)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"error":       err,
			"output":      output,
		}).Error("Error parsing ndsctl json output")
		return 0, 0, fmt.Errorf("failed to parse ndsctl json for MAC %s: %w", macAddress, err)
	}

	// Convert from kilobytes to bytes
	downloadedBytes := stats.Downloaded * 1024
	uploadedBytes := stats.Uploaded * 1024

	logger.WithFields(logrus.Fields{
		"mac_address":      macAddress,
		"downloaded_kb":    stats.Downloaded,
		"uploaded_kb":      stats.Uploaded,
		"downloaded_bytes": downloadedBytes,
		"uploaded_bytes":   uploadedBytes,
		"state":            stats.State,
	}).Debug("Retrieved client stats from ndsctl")

	return downloadedBytes, uploadedBytes, nil
}

// GetClientUsage returns the total data usage (downloaded + uploaded) for a MAC address
// This is a convenience function that calls GetClientStats
func GetClientUsage(macAddress string) (totalBytes uint64, err error) {
	downloaded, uploaded, err := GetClientStats(macAddress)
	if err != nil {
		return 0, err
	}
	return downloaded + uploaded, nil
}

// ClientState is the read-only, NDS-reported view of a client MAC, used by
// payment pre-flight checks (issue #403).
type ClientState struct {
	Registered    bool
	Authenticated bool
}

// TrackedGates returns the MAC addresses of the gates this module currently
// believes it holds open, sorted so a report is stable.
//
// It answers a question about the module's OWN bookkeeping, not about the
// router: a gate stays in this set while its close is unconfirmed (a failed
// deauth is not a close, C1-2) and while it is simply still running. Callers use
// it to find gates nothing else looks at — a gate whose client has left the
// network and whose session record is gone — and must probe NoDogSplash
// (CheckClientState) before acting on the answer. It is read-only: it never
// changes gate state.
func TrackedGates() []string {
	gatesMutex.Lock()
	macs := make([]string, 0, len(openGates))
	for macAddress := range openGates {
		macs = append(macs, macAddress)
	}
	gatesMutex.Unlock()

	sort.Strings(macs)
	return macs
}

// CheckClientState probes NoDogSplash for a client without changing any
// state (`ndsctl json <mac>`). An empty client list ("{}") is reported as a
// definitive not-registered answer, while ndsctl failures surface as errors
// so callers can fail open.
func CheckClientState(macAddress string) (ClientState, error) {
	if !isValidMAC(macAddress) {
		return ClientState{}, fmt.Errorf("invalid MAC address format: %s", macAddress)
	}

	ndsctlMutex.Lock()
	output, err := runNdsctl("json", macAddress)
	ndsctlMutex.Unlock()

	if err != nil {
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"error":       err,
		}).Error("Error executing ndsctl json for client state")
		return ClientState{}, fmt.Errorf("failed to execute ndsctl json for MAC %s: %w", macAddress, err)
	}

	if strings.TrimSpace(output) == "{}" {
		return ClientState{Registered: false}, nil
	}

	var stats ClientStats
	if err := json.Unmarshal([]byte(output), &stats); err != nil {
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"error":       err,
			"output":      output,
		}).Error("Error parsing ndsctl json for client state")
		return ClientState{}, fmt.Errorf("failed to parse ndsctl json for MAC %s: %w", macAddress, err)
	}

	return ClientState{
		Registered:    true,
		Authenticated: strings.EqualFold(stats.State, "Authenticated"),
	}, nil
}

// delayedAuth performs the authorisation deferred by AuthDelay for a TIMED gate.
// `epoch` is the gate this goroutine belongs to: every step that acts on the
// client checks it, so a goroutine that outlived its gate can never deauthorize
// or forget a newer one.
func delayedAuth(macAddress string, epoch uint64) {
	logger.WithFields(logrus.Fields{
		"mac_address": macAddress,
		"delay":       AuthDelay,
	}).Info("Waiting before delayed auth")

	select {
	case <-time.After(AuthDelay):
	case <-stopCh:
		logger.WithField("mac_address", macAddress).Info("Delayed auth cancelled during shutdown")
		gatesMutex.Lock()
		delete(pendingUntil, macAddress)
		delete(openGates, macAddress)
		gatesMutex.Unlock()
		return
	}

	gatesMutex.Lock()
	untilTimestamp, pending := pendingUntil[macAddress]
	delete(pendingUntil, macAddress)
	gatesMutex.Unlock()

	if !pending {
		logger.WithField("mac_address", macAddress).Warn("Delayed auth has no pending entry, aborting")
		forgetPendingGate(macAddress, epoch)
		return
	}

	if err := authorizeMAC(macAddress); err != nil {
		// A failed auth is not an open: the customer has already paid (the
		// merchant granted the session before this goroutine ran), so the gate
		// stays TRACKED and the auth is retried until ndsctl confirms it. The
		// code this replaces deleted the gate here, which left a paid session
		// with a shut gate that nothing tracked and nothing would ever retry.
		gateOpenFailure(macAddress, authMaxAttempts, err, openRetryDelay(1))
		scheduleAuthRetry(macAddress, epoch, untilTimestamp, 1)
		return
	}

	logger.WithFields(logrus.Fields{
		"mac_address": macAddress,
	}).Info("Delayed auth succeeded")

	remaining := time.Until(time.Unix(untilTimestamp, 0))
	if remaining <= 0 {
		logger.WithField("mac_address", macAddress).Warn("Session expired during auth delay, deauthorizing immediately")
		closeTrackedGateNow(macAddress)
		return
	}

	// Like the timer in OpenGateUntil, the callback captures the epoch of the
	// gate it belongs to and acts only if that gate is still the tracked one.
	gatesMutex.Lock()
	timerEpoch := nextGateEpochLocked(macAddress)
	gatesMutex.Unlock()

	timer := time.AfterFunc(remaining, func() {
		expireTimedGate(macAddress, timerEpoch)
	})

	gatesMutex.Lock()
	openGates[macAddress] = timer
	markGateOpenedLocked(macAddress)
	gatesMutex.Unlock()
}

// delayedAuthIndefinite performs the authorisation deferred by AuthDelay for an
// INDEFINITE (bytes-metered) gate — the shape every paid step allotment uses.
// `epoch` is the gate this goroutine belongs to, as in delayedAuth.
func delayedAuthIndefinite(macAddress string, epoch uint64) {
	logger.WithFields(logrus.Fields{
		"mac_address": macAddress,
		"delay":       AuthDelay,
	}).Info("Waiting before delayed auth")

	select {
	case <-time.After(AuthDelay):
	case <-stopCh:
		logger.WithField("mac_address", macAddress).Info("Delayed indefinite auth cancelled during shutdown")
		gatesMutex.Lock()
		delete(pendingUntil, macAddress)
		delete(openGates, macAddress)
		gatesMutex.Unlock()
		return
	}

	gatesMutex.Lock()
	_, pending := pendingUntil[macAddress]
	delete(pendingUntil, macAddress)
	gatesMutex.Unlock()

	if !pending {
		logger.WithField("mac_address", macAddress).Warn("Delayed indefinite auth has no pending entry, aborting")
		forgetPendingGate(macAddress, epoch)
		return
	}

	if err := authorizeMAC(macAddress); err != nil {
		// A failed auth is not an open. This is the path the reported defect
		// takes: allotment spent -> gate closed -> customer buys again -> the
		// merchant grants a fresh session and /balance shows it -> ndsctl does
		// not accept the auth -> the old code DELETED the tracking here, so
		// nothing retried and the customer kept a paid, shut gate.
		gateOpenFailure(macAddress, authMaxAttempts, err, openRetryDelay(1))
		scheduleAuthRetry(macAddress, epoch, 0, 1)
		return
	}

	logger.WithFields(logrus.Fields{
		"mac_address": macAddress,
	}).Info("Delayed auth succeeded")

	// Same contract as OpenGate: the baseline is recorded unconditionally (from
	// zero when ndsctl cannot report the client's counters yet) so that the
	// session is metered from the moment its gate opens, and the usage monitor
	// re-establishes it rather than skipping the session for ever.
	if err := SetDataBaseline(macAddress); err != nil {
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"error":       err,
		}).Error("Metering baseline recorded from zero: ndsctl could not report the client's counters, so the session is metered from the gate opening")
	}

	gatesMutex.Lock()
	openGates[macAddress] = nil
	markGateOpenedLocked(macAddress)
	gatesMutex.Unlock()
}
