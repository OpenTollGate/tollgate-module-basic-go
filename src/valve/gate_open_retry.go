package valve

import (
	"time"

	"github.com/sirupsen/logrus"
)

// The gate-OPEN contract: an unconfirmed open is not an open (the mirror of
// "a failed close is not a close", C1-2).
//
// Both sides of the valve owe the customer something, and both used to forget it
// when ndsctl did not confirm:
//
//   - a close that fails leaves access the customer stopped paying for, so the
//     gate stays tracked and the close is retried until ndsctl confirms it;
//   - an OPEN that fails leaves a customer who HAS PAID with no internet. The
//     merchant has already granted the session (that is what the portal and
//     /balance show), so the module owes a LATE GRANT: the paid session stays
//     tracked and the auth is retried until ndsctl confirms it.
//
// The deferred-auth path (AuthDelay > 0, the shape the shipped portal uses so
// the browser can render the splash before the client is let through) used to
// delete the gate tracking and return as soon as the bounded retries inside
// authorizeMAC were used up. Nothing then knew about the client, nothing
// retried, and the customer sat behind a shut gate while every module-visible
// surface said the session was healthy — reported from hardware as "the balance
// came back but the gate stayed shut" (GL-MT3000, pre17, 2026-09-25).

// forgetPendingGate drops the tracking of a pending gate whose deferred-auth
// goroutine found nothing to authorise (no pending entry: the gate was retired
// by a confirmed close, or the process is shutting down).
//
// It refuses to drop a gate that has been replaced, extended or re-opened since
// the goroutine was spawned: `epoch` names the gate the goroutine belongs to, and
// deleting the tracking of a NEWER gate is how a customer's fresh purchase would
// be silently forgotten.
func forgetPendingGate(macAddress string, epoch uint64) {
	gatesMutex.Lock()
	raced := gateEpochs[macAddress] != epoch
	if !raced {
		delete(openGates, macAddress)
	}
	gatesMutex.Unlock()

	if raced {
		logger.WithField("mac_address", macAddress).Warn("A pending gate had nothing to authorise, but the client has a newer gate: leaving it alone")
	}
}

// scheduleAuthRetry arms the retry of an unconfirmed OPEN: after the backoff for
// this attempt the auth is re-attempted, and if it fails again the next attempt
// is armed, for ever (the backoff stops growing). The gate stays in openGates
// throughout, so the state that says "this customer paid and has no access" is
// never lost.
//
// `until` is the deadline of a TIMED gate, or 0 for an indefinite (bytes) gate:
// a retry that finally succeeds must open the gate the gate was bought for, not
// an unbounded one.
func scheduleAuthRetry(macAddress string, epoch uint64, until int64, attempt int) {
	delay := openRetryDelay(attempt)

	gatesMutex.Lock()
	if _, tracked := openGates[macAddress]; !tracked {
		// Nothing left to open: the gate was retired by a confirmed close (or
		// the session is gone), so there is nothing to retry.
		gatesMutex.Unlock()
		return
	}
	if previous, ok := pendingAuthRetries[macAddress]; ok {
		previous.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(delay, func() {
		gatesMutex.Lock()
		if pendingAuthRetries[macAddress] == timer {
			delete(pendingAuthRetries, macAddress)
		}
		gatesMutex.Unlock()

		retryGateOpen(macAddress, epoch, until, attempt)
	})
	pendingAuthRetries[macAddress] = timer
	gatesMutex.Unlock()

	logger.WithFields(logrus.Fields{
		"mac_address": macAddress,
		"attempt":     attempt,
		"retry_in":    delay.String(),
	}).Warn("Retrying an unconfirmed gate open")
}

// retryGateOpen re-attempts an unconfirmed open. It abandons the retry when the
// gate it was armed for is gone or has been replaced — a retry must never open a
// gate for a client whose session was closed or replaced in the meantime — and
// on success it records the metering baseline, so the access it just opened is
// measured like any other.
func retryGateOpen(macAddress string, epoch uint64, until int64, attempt int) {
	gatesMutex.Lock()
	_, tracked := openGates[macAddress]
	currentEpoch := gateEpochs[macAddress]
	gatesMutex.Unlock()

	if !tracked {
		logger.WithField("mac_address", macAddress).Info("Gate open retry abandoned: the gate is no longer tracked")
		return
	}
	if currentEpoch != epoch {
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"gate_epoch":  currentEpoch,
		}).Warn("Gate open retry abandoned: the client has a newer gate (extended, re-opened or closed)")
		return
	}

	if err := authorizeMAC(macAddress); err != nil {
		next := attempt + 1
		gateOpenFailure(macAddress, next, err, openRetryDelay(next))
		scheduleAuthRetry(macAddress, epoch, until, next)
		return
	}

	confirmed, expired := confirmGateOpen(macAddress, epoch, until)
	if expired {
		logger.WithField("mac_address", macAddress).Warn("Session expired while the gate open was being retried, deauthorizing immediately")
		closeTrackedGateNow(macAddress)
		return
	}
	if !confirmed {
		return
	}

	// Same contract as the direct OpenGate path: the baseline is recorded
	// unconditionally (from zero when ndsctl cannot report the client's counters
	// yet), so the session is metered from the moment its gate really opens and
	// the usage monitor re-establishes a missing one instead of skipping the
	// session for ever.
	if err := SetDataBaseline(macAddress); err != nil {
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"error":       err,
		}).Error("Metering baseline recorded from zero: ndsctl could not report the client's counters, so the session is metered from the gate opening")
	}

	logger.WithFields(logrus.Fields{
		"mac_address": macAddress,
		"attempts":    attempt,
	}).Info("Gate open confirmed after retry")
}

// confirmGateOpen records the gate as open — and, for a timed gate, arms its
// expiry timer — in ONE critical section, so the client is never left
// open-but-untimed even for an instant and a close racing the confirmation
// cannot be undone by a late arm.
//
// It reports whether the gate was confirmed as THIS gate's (false means a newer
// gate of the same client took over while the auth was in flight, which keeps
// the client's access) and, for a timed gate, whether the deadline had already
// passed (nothing was opened; the caller closes the gate).
func confirmGateOpen(macAddress string, epoch uint64, until int64) (confirmed bool, expired bool) {
	gatesMutex.Lock()

	if gateEpochs[macAddress] != epoch {
		gatesMutex.Unlock()
		return false, false
	}

	if until == 0 {
		openGates[macAddress] = nil
		markGateOpenedLocked(macAddress)
		gatesMutex.Unlock()
		return true, false
	}

	remaining := time.Until(time.Unix(until, 0))
	if remaining <= 0 {
		gatesMutex.Unlock()
		return false, true
	}

	next := nextGateEpochLocked(macAddress)
	timer := time.AfterFunc(remaining, func() {
		expireTimedGate(macAddress, next)
	})
	openGates[macAddress] = timer
	markGateOpenedLocked(macAddress)
	gatesMutex.Unlock()

	return true, false
}
