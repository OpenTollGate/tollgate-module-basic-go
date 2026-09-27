package valve

import (
	"context"
	"encoding/json"
	"errors"
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
// It is a var so tests can shrink it.
var ndsctlTimeout = 5 * time.Second

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

// closeAttemptBudget bounds how many times the close of ONE gate is
// re-attempted while ndsctl keeps answering with something other than a
// confirmation (neither "deauthorized" nor "NoDogSplash does not know this
// client"). At the budget the module STOPS driving ndsctl about that gate: it
// keeps the gate tracked and escalates the abandonment once, because the
// alternative is the loop measured on the bench MT3000 on 2026-09-26, where one
// session whose client had left NoDogSplash was re-closed at the sweep cadence
// for ever (unconfirmed_closes 113 -> 193 -> 195), NDSCTL WAS DRIVEN UNTIL ITS
// SOCKET DIED ("Socket is not ready for communication : Bad file descriptor"
// every ~5s) and a PAID purchase could no longer be authorised at all
// (state=PAID, merchant wallet +1 sat, access_granted never true).
//
// A spent budget is not a dead end: the record stays tracked, and the
// reconciliation re-attempts the close under fresh evidence about the client
// (ReconcileGateClose). It is a bound on hammering, not a give-up on the gate.
const closeAttemptBudget = 8

// closeStreak is the unconfirmed-close budget of the current gate of one MAC.
type closeStreak struct {
	attempts  int
	abandoned bool
}

// ErrGateCloseAbandoned reports that the close of a gate has been re-attempted
// closeAttemptBudget times without ndsctl ever answering that the client is
// deauthorized or that NoDogSplash does not know it, so the module's own sweep
// machinery has stopped driving ndsctl about it. The gate is still tracked, and
// the error means precisely "the close is NOT confirmed and no further attempt is
// being made by this path".
var ErrGateCloseAbandoned = errors.New("gate close abandoned after repeated unconfirmed attempts")

// ndsctlStopDrain is how long Stop() waits for the invocations that are in
// flight when the module is told to stop. ndsctl answers in 230-350ms, so a
// healthy invocation finishes well inside it; a child that is still running
// after it is killed deliberately, and the kill is attributed to the shutdown
// (see drainNdsctlChildren). It is a var so tests can shrink it.
var ndsctlStopDrain = 3 * time.Second

// ndsctlTimeoutReportInterval is the shortest interval between two ERROR
// escalations of "ndsctl did not answer within its deadline". A wedged
// NoDogSplash answers nothing, and the module drives ndsctl at the sweep
// cadence (every 2 s per metered session), so an unthrottled escalation repeats
// the same line for ever — that is the 97-line storm measured on the bench
// MT3000 on 2026-09-26. Inside the interval a repeat is logged at DEBUG with the
// running count, and the module says so at INFO once ndsctl answers again. It is
// a var so tests can shrink it.
var ndsctlTimeoutReportInterval = 30 * time.Second

// ErrNdsctlTimeout reports that an ndsctl invocation did not answer within
// ndsctlTimeout, so the MODULE killed the child. It means "NoDogSplash never
// answered on its control socket", which is a different state from "ndsctl
// answered that the operation failed": the client's access is UNVERIFIED, not
// unchanged.
var ErrNdsctlTimeout = errors.New("ndsctl did not answer within its deadline and the module killed the invocation")

// ErrNdsctlStopped reports that an ndsctl invocation was interrupted because the
// module is stopping. It is not a failure of anything: the module ended the
// child on its way out.
var ErrNdsctlStopped = errors.New("ndsctl was interrupted because the module is stopping")

// ndsctlOutcome says which side ended an ndsctl invocation.
type ndsctlOutcome string

const (
	// ndsctlOutcomeTimedOut: the module's own deadline expired and it killed the
	// child, which had not answered.
	ndsctlOutcomeTimedOut ndsctlOutcome = "timeout"
	// ndsctlOutcomeStopping: the module is stopping, so the invocation was not
	// started, or the child was killed on the way out.
	ndsctlOutcomeStopping ndsctlOutcome = "module_stopping"
)

// ndsctlInterruption is the error of an ndsctl invocation the MODULE ended. Its
// message names the side that ended the child, because the alternative — Go's
// `signal: killed`, which is all `exec` reports for a child that died on a
// signal — is indistinguishable from a restart, an OOM kill or a real ndsctl
// refusal, and every state machine reading the log drew its own conclusion.
type ndsctlInterruption struct {
	outcome ndsctlOutcome
	args    []string
	output  string
	reason  error
}

// newNdsctlInterruption builds the error for an invocation the module ended.
func newNdsctlInterruption(outcome ndsctlOutcome, args []string, output string, reason error) *ndsctlInterruption {
	return &ndsctlInterruption{
		outcome: outcome,
		args:    append([]string(nil), args...),
		output:  output,
		reason:  reason,
	}
}

func (e *ndsctlInterruption) Error() string {
	command := "ndsctl " + strings.Join(e.args, " ")
	if e.outcome == ndsctlOutcomeStopping {
		return fmt.Sprintf("%s was interrupted because the module is stopping (%v): the module ended the child on its way out, so this is not an ndsctl failure and says nothing about the client", command, e.reason)
	}
	return fmt.Sprintf("%s did not answer within %s: the module killed the child, so NoDogSplash never answered on its control socket — this is not ndsctl reporting a failure", command, ndsctlTimeout)
}

// Unwrap maps the interruption onto the sentinel callers can test with
// errors.Is, so "the module timed out" and "the module is stopping" stay
// distinguishable without matching on the message.
func (e *ndsctlInterruption) Unwrap() error {
	if e.outcome == ndsctlOutcomeStopping {
		return ErrNdsctlStopped
	}
	return ErrNdsctlTimeout
}

// ndsctlInterruptionOf reports whether err is an invocation the module ended,
// and the invocation's outcome.
func ndsctlInterruptionOf(err error) (*ndsctlInterruption, bool) {
	var interruption *ndsctlInterruption
	if errors.As(err, &interruption) {
		return interruption, true
	}
	return nil, false
}

// ndsctlInterruptedByStop reports whether err is an invocation the module ended
// because it is stopping.
func ndsctlInterruptedByStop(err error) bool {
	return errors.Is(err, ErrNdsctlStopped)
}

// ndsctl invocation bookkeeping. The counters back the accessors below; the
// report bookkeeping is what keeps an unanswered socket from producing one ERROR
// line per invocation.
var (
	// ndsctlStopping is set by Stop() and never cleared: the process is on its
	// way out, so no new ndsctl child may be started.
	ndsctlStopping atomic.Bool

	// ndsctlInFlight is held for READING for the whole life of every ndsctl
	// child, so Stop() can wait for the children that are in flight by taking it
	// for writing. Stop() sets ndsctlStopping first, so a call that arrives
	// during the drain waits for the write lock and then leaves without starting
	// a child.
	ndsctlInFlight sync.RWMutex

	// ndsctlParentCtx is the parent of every invocation's deadline. Cancelling
	// it is how a drain that overran its budget kills the child that is left,
	// deliberately and attributed (rather than letting the process exit and
	// leaving the log to explain a kill that nothing claimed).
	ndsctlParentCtx, cancelNdsctlParent = context.WithCancel(context.Background())

	ndsctlTimeouts            uint64
	ndsctlStoppedInvocations  uint64
	ndsctlTimeoutReportsMu    sync.Mutex
	ndsctlLastTimeoutReport   time.Time
	ndsctlTimeoutsSinceReport int
	ndsctlUnresponsiveSince   time.Time
)

// NdsctlTimeouts reports how many ndsctl invocations did not answer within
// ndsctlTimeout since the process started (the module killed each of them). A
// non-zero value means NoDogSplash's control socket stopped answering at least
// once: the operation's outcome is UNVERIFIED, and a purchase that needs ndsctl
// cannot be applied while it lasts.
func NdsctlTimeouts() uint64 {
	return atomic.LoadUint64(&ndsctlTimeouts)
}

// NdsctlStoppedInvocations reports how many ndsctl invocations were interrupted
// because the module was stopping. These are not failures of NoDogSplash.
func NdsctlStoppedInvocations() uint64 {
	return atomic.LoadUint64(&ndsctlStoppedInvocations)
}

// runNdsctl executes an ndsctl command with a timeout.
// It returns the combined stdout+stderr output and any error.
// It is a var (not a func) so tests can stub it without a real ndsctl binary.
//
// The production implementation classifies how the invocation ended: a child
// that exited on its own is ndsctl answering (or refusing), while one the MODULE
// ended — its deadline, or its shutdown — is reported as such and never as an
// ndsctl failure.
var runNdsctl = func(args ...string) (string, error) {
	return runNdsctlCommand(args...)
}

// runNdsctlCommand runs one ndsctl invocation under the module's own deadline and
// reports whether the module ended it.
func runNdsctlCommand(args ...string) (string, error) {
	if ndsctlStopping.Load() {
		interruption := newNdsctlInterruption(ndsctlOutcomeStopping, args, "", context.Canceled)
		reportNdsctlInterruption(interruption)
		return "", interruption
	}

	ndsctlInFlight.RLock()
	defer ndsctlInFlight.RUnlock()

	// Stop() may have been called between the check above and the lock: a module
	// that is stopping must not start a child it cannot wait for.
	if ndsctlStopping.Load() {
		interruption := newNdsctlInterruption(ndsctlOutcomeStopping, args, "", context.Canceled)
		reportNdsctlInterruption(interruption)
		return "", interruption
	}

	ctx, cancel := context.WithTimeout(ndsctlParentCtx, ndsctlTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ndsctl", args...)
	output, err := cmd.CombinedOutput()
	if err == nil || ctx.Err() == nil {
		// The child exited on its own: ndsctl answered. (A non-nil error here is
		// ndsctl refusing an operation, which the callers report as before.)
		reportNdsctlAnswered()
		return string(output), err
	}

	outcome := ndsctlOutcomeTimedOut
	if errors.Is(ctx.Err(), context.Canceled) {
		outcome = ndsctlOutcomeStopping
	}
	interruption := newNdsctlInterruption(outcome, args, string(output), ctx.Err())
	reportNdsctlInterruption(interruption)
	return string(output), interruption
}

// ndsctlInterruptionFields describes an interruption for the log: which command,
// which client, and which side ended it.
func ndsctlInterruptionFields(interruption *ndsctlInterruption) logrus.Fields {
	fields := logrus.Fields{
		"ndsctl":         strings.Join(interruption.args, " "),
		"ndsctl_outcome": string(interruption.outcome),
	}
	if len(interruption.args) > 1 {
		fields["mac_address"] = interruption.args[1]
	}
	if len(interruption.args) > 0 {
		fields["ndsctl_op"] = interruption.args[0]
	}
	if answer := strings.TrimSpace(interruption.output); answer != "" {
		fields["ndsctl_output"] = answer
	}
	return fields
}

// reportNdsctlInterruption makes an invocation the module ended visible, at a
// rate that cannot become a storm.
//
// An interruption because the module is stopping is INFO: it is the module
// leaving, and escalating it is what made a service restart read like a burst of
// ndsctl failures. A timeout is an ERROR, because a socket that stops answering
// is how a paid purchase ends up with no access at all — but only once per
// ndsctlTimeoutReportInterval, with the running count on every later one at
// DEBUG, because the module drives ndsctl at the sweep cadence and a wedged
// socket answers nothing.
func reportNdsctlInterruption(interruption *ndsctlInterruption) {
	fields := ndsctlInterruptionFields(interruption)

	if interruption.outcome == ndsctlOutcomeStopping {
		total := atomic.AddUint64(&ndsctlStoppedInvocations, 1)
		fields["stopped_invocations"] = total
		logger.WithFields(fields).Info("ndsctl invocation ended because the module is stopping: the module ended the child on its way out, so this is not an ndsctl failure and the client's access is unchanged by it")
		return
	}

	total := atomic.AddUint64(&ndsctlTimeouts, 1)
	fields["ndsctl_timeouts"] = total
	fields["error"] = interruption.Error()

	ndsctlTimeoutReportsMu.Lock()
	now := time.Now()
	if ndsctlUnresponsiveSince.IsZero() {
		ndsctlUnresponsiveSince = now
	}
	ndsctlTimeoutsSinceReport++
	report := ndsctlLastTimeoutReport.IsZero() || now.Sub(ndsctlLastTimeoutReport) >= ndsctlTimeoutReportInterval
	if report {
		ndsctlLastTimeoutReport = now
		unanswered := ndsctlTimeoutsSinceReport
		ndsctlTimeoutsSinceReport = 0
		fields["unresponsive_since"] = ndsctlUnresponsiveSince.Format(time.RFC3339)
		fields["unanswered_invocations"] = unanswered
	}
	ndsctlTimeoutReportsMu.Unlock()

	if report {
		logger.WithFields(fields).Error("NoDogSplash did not answer an ndsctl invocation within its deadline, so the module killed the child and the operation's outcome is UNVERIFIED — the gate is NOT known to be closed, and a purchase cannot be applied while this lasts. This is not ndsctl reporting a failure; it is the control socket not answering. OPERATOR ACTION: check `ndsctl status` / nodogsplash on the router")
		return
	}
	logger.WithFields(fields).Debug("ndsctl still has not answered an invocation within its deadline (already escalated; repeats inside the report interval stay at debug so a wedged socket cannot produce a storm)")
}

// reportNdsctlAnswered closes the unresponsive episode: the socket answered
// again, and the operator is told how long it was silent for.
func reportNdsctlAnswered() {
	ndsctlTimeoutReportsMu.Lock()
	silentSince := ndsctlUnresponsiveSince
	ndsctlUnresponsiveSince = time.Time{}
	ndsctlTimeoutsSinceReport = 0
	ndsctlTimeoutReportsMu.Unlock()

	if silentSince.IsZero() {
		return
	}
	logger.WithFields(logrus.Fields{
		"unresponsive_for": time.Since(silentSince).Round(time.Second).String(),
		"ndsctl_timeouts":  NdsctlTimeouts(),
	}).Info("ndsctl answered again after invocations that did not answer: the NoDogSplash control socket has recovered")
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

	// closeStreaks is the unconfirmed-close budget of the CURRENT gate of a
	// MAC: how many close attempts have gone unconfirmed in a row, and whether
	// the module has stopped re-attempting the close because that budget is
	// spent. A confirmed close, a new gate generation or a retirement forgets
	// it, so a gate that is replaced starts with a clean budget.
	closeStreaks = make(map[string]*closeStreak)
)

// gateCloseFailures counts closes ndsctl has not confirmed. It backs
// GateCloseFailures, the counter the module's operator-visible surfaces use to
// report how many gate closes were escalated after a failed close.
//
// It is bounded per gate: once the close budget of a gate is spent the module
// stops re-attempting that close and stops escalating it (see
// closeAttemptBudget and handleUnconfirmedClose), so one stuck session cannot
// make this climb without bound — which is exactly what it did on the bench
// (113 -> 193 -> 195) while the same session was retried twice a second.
var gateCloseFailures uint64

// gateClosesAbandoned counts the gates whose close the module has STOPPED
// re-attempting after closeAttemptBudget unconfirmed attempts. Unlike a
// per-attempt counter it cannot be driven up by a single stuck session: one gate
// adds at most one.
var gateClosesAbandoned uint64

// GateCloseFailures reports how many gate closes have been escalated after an
// unconfirmed attempt since the process started. A non-zero value means the
// module tried to take a client's access away and ndsctl did not confirm it, so
// the gate is still tracked. Whether that client still has access is UNVERIFIED:
// ndsctl answered with an error, not with an answer about the client. Use
// GateClosesAbandoned to see how many of those closes the module has given up
// re-attempting, and the module log for the reason of each one.
func GateCloseFailures() uint64 {
	return atomic.LoadUint64(&gateCloseFailures)
}

// GateClosesAbandoned reports how many gates the module has stopped
// re-attempting to close because closeAttemptBudget consecutive attempts went
// unconfirmed. Such a gate is still TRACKED (the record that says the client
// must be closed is never dropped) and the reconciliation re-attempts its close
// whenever it has fresh evidence about the client, but the module's own sweep
// machinery no longer drives ndsctl about it.
func GateClosesAbandoned() uint64 {
	return atomic.LoadUint64(&gateClosesAbandoned)
}

// ndsctlMutex ensures only one ndsctl command runs at a time
var ndsctlMutex = &sync.Mutex{}

// Stop tells the module to shut down: it cancels the pending delayed auth and
// DRAINS the ndsctl invocations that are in flight, so a service restart
// (`tollgate-wrt restart`, which is what the bench defect's reproduction does)
// does not kill a child that was about to answer and does not leave the operator
// reading a kill that nothing claimed.
//
// It is idempotent: procd sends SIGTERM, a second signal (or a stop after a stop)
// must not panic on a channel that is already closed.
func Stop() {
	if !ndsctlStopping.CompareAndSwap(false, true) {
		logger.Debug("Stop was called again: the module is already stopping")
		return
	}
	close(stopCh)
	drainNdsctlChildren()
}

// drainNdsctlChildren waits for the ndsctl invocations that are in flight, and
// kills the ones that outlive the drain budget — deliberately, so the kill is
// attributed to the shutdown instead of being reported as an ndsctl failure.
//
// The drain is bounded twice (once before the kill, once after) because a
// shutdown must never hang: procd SIGKILLs the service after its own timeout, and
// a module that refuses to exit is indistinguishable from a hung one.
func drainNdsctlChildren() {
	drained := make(chan struct{})
	go func() {
		// The write lock is granted only once every in-flight invocation has
		// released its read lock, i.e. once every child has finished.
		ndsctlInFlight.Lock()
		ndsctlInFlight.Unlock()
		close(drained)
	}()

	select {
	case <-drained:
		logger.Info("ndsctl: every in-flight invocation finished before the module stopped, so the module killed none of them")
		return
	case <-time.After(ndsctlStopDrain):
	}

	logger.WithField("drain_budget", ndsctlStopDrain.String()).Warn("ndsctl: an invocation is still in flight after the drain budget, so the module is killing its child because it is stopping — the kill is attributed to the shutdown, not to ndsctl")
	cancelNdsctlParent()

	select {
	case <-drained:
	case <-time.After(ndsctlStopDrain):
		logger.WithField("drain_budget", ndsctlStopDrain.String()).Warn("ndsctl: an invocation did not release the valve within the drain budget even after being killed; the module is stopping without waiting for it")
	}
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
		if interruption, ended := ndsctlInterruptionOf(err); ended {
			// The module ended this invocation: the retry above exists for
			// NoDogSplash answering that it does not have the client yet, not for
			// an invocation that never answered. Retrying a socket that is not
			// answering only spends the caller's deadline, so the attributed
			// escalation from the invocation itself is the whole report.
			logger.WithFields(ndsctlInterruptionFields(interruption)).Debug("ndsctl auth was ended by the module; not retrying an invocation that never answered")
			return lastErr
		}
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

// ndsctlUnknownClient reports whether ndsctl's answer says that NoDogSplash does
// not know this client AT ALL.
//
// Measured on the bench MT3000 (pre17, NDS, 2026-09-26) with a real Wi-Fi client,
// a8:a0:92:a5:39:7a: `ndsctl deauth a8:a0:92:a5:39:7a` printed
//
//	Client a8:a0:92:a5:39:7a not found.
//
// and exited 1 — the same exit status a refused deauthorization has. The two are
// NOT the same state, and reading them as one is what made this session
// unretirable: here the enforcement layer holds no client, so there is nothing
// left to close and no retry can ever converge; there the client is still
// Authenticated and still holds the open gate.
//
// The match is deliberately narrow — the phrase "not found" together with the
// MAC or the word "client" — so unrelated ndsctl failures ("Socket is not ready
// for communication : Bad file descriptor", "Could not connect to server") can
// never be mistaken for it.
func ndsctlUnknownClient(macAddress, output string) bool {
	lowered := strings.ToLower(output)
	if !strings.Contains(lowered, "not found") {
		return false
	}
	return strings.Contains(lowered, strings.ToLower(macAddress)) ||
		strings.Contains(lowered, "client")
}

// deauthorizeMAC deauthorizes a MAC address using ndsctl.
//
// It reports one deauthorization failure as a success: ndsctl answering that
// NoDogSplash does not know the client. Nothing is left to deauthorize in that
// state — NoDogSplash cannot be granting access to a client it does not hold —
// so returning an error there only arms a retry that can never converge. The
// operator still gets a line, at INFO, with the verified state (which is what
// distinguishes it from a real failure).
func deauthorizeMAC(macAddress string) error {
	ndsctlMutex.Lock()
	output, err := runNdsctl("deauth", macAddress)
	ndsctlMutex.Unlock()

	if err != nil {
		if ndsctlUnknownClient(macAddress, output) {
			logger.WithFields(logrus.Fields{
				"mac_address": macAddress,
				"ndsctl":      strings.TrimSpace(output),
			}).Info("Client already gone from NoDogSplash: ndsctl reports that NoDogSplash does not know this client, so there is nothing left to deauthorize — the gate is closed by definition and the session is retired (no retry is armed for a client that is not there)")
			return nil
		}

		if interruption, ended := ndsctlInterruptionOf(err); ended {
			// The invocation was ended by the module (its deadline, or the
			// shutdown) and the invocation itself already reported that, naming
			// which side ended it. Escalating it again as "Error deauthorizing
			// MAC address" is exactly the unattributed `signal: killed` line the
			// bench produced 97 times on a box whose socket had stopped
			// answering. The error is still returned: the close is NOT confirmed.
			logger.WithFields(ndsctlInterruptionFields(interruption)).Debug("ndsctl deauth was ended by the module; the close stays unconfirmed (the invocation has already been escalated with its outcome)")
			return err
		}

		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"error":       err,
			"ndsctl":      strings.TrimSpace(output),
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

// clearCloseStreak forgets the unconfirmed-close budget of a MAC. Called for a
// confirmed close (there is nothing left to bound) and for a new gate generation
// (a gate that is replaced or extended starts with a clean budget).
func clearCloseStreak(macAddress string) {
	gatesMutex.Lock()
	delete(closeStreaks, macAddress)
	gatesMutex.Unlock()
}

// closeAttemptAllowed reports whether the module's own machinery may spend an
// ndsctl call on the close of macAddress. Once a gate's budget is spent it may
// not: the close has been escalated once already, and driving ndsctl again at the
// sweep cadence is the storm that wedged the socket on the bench. The
// reconciliation re-attempts such a close under fresh evidence about the client
// (ReconcileGateClose), which is the designated recovery path.
func closeAttemptAllowed(macAddress string) bool {
	gatesMutex.Lock()
	defer gatesMutex.Unlock()

	streak, exists := closeStreaks[macAddress]
	return !exists || !streak.abandoned
}

// handleUnconfirmedClose records one unconfirmed close attempt of macAddress and
// escalates it. It returns true when the caller must re-arm a retry.
//
// Inside the budget the close keeps being retried and the escalation names the
// client, the attempt and the running total. At the budget the module STOPS:
// no further attempt is made by this path, the running total stops growing for
// this gate, and the abandonment is escalated exactly once with what an operator
// has to do. The gate stays tracked throughout.
func handleUnconfirmedClose(macAddress string, attempt int, err error) bool {
	if ndsctlInterruptedByStop(err) {
		// The module is stopping, so this close was not attempted (or its child
		// was ended on the way out). It is not an ndsctl failure and it is not a
		// spent budget: the gate stays TRACKED, this process does not re-attempt
		// it, and the record that says the client must be closed is not lost.
		// A client NoDogSplash still authorises after a restart — the inverse
		// drift — is the reconciliation the architecture decision record leaves
		// open (docs/architecture/zombie-session-close-reconciliation-decision.md
		// §5), and is NOT claimed here.
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"error":       err,
		}).Info("Gate close not confirmed because the module is stopping: the gate stays tracked, this process does not re-attempt it, and the client's access is left exactly as NoDogSplash has it (this is not an ndsctl failure)")
		return false
	}

	gatesMutex.Lock()
	streak, exists := closeStreaks[macAddress]
	if !exists {
		streak = &closeStreak{}
		closeStreaks[macAddress] = streak
	}
	if streak.abandoned {
		gatesMutex.Unlock()
		return false
	}
	streak.attempts++
	attempts := streak.attempts
	spent := attempts >= closeAttemptBudget
	if spent {
		streak.abandoned = true
	}
	gatesMutex.Unlock()

	if !spent {
		next := attempt + 1
		gateCloseFailure(macAddress, next, err, closeRetryDelay(next))
		return true
	}

	total := atomic.AddUint64(&gateClosesAbandoned, 1)
	logger.WithFields(logrus.Fields{
		"mac_address":      macAddress,
		"attempts":         attempts,
		"budget":           closeAttemptBudget,
		"abandoned_closes": total,
		"last_error":       err,
	}).Error("Gate close UNRESOLVED: ndsctl never confirmed the deauthorization of this client and the close budget is spent, so the module stops re-attempting it here — it cannot keep hammering the ndsctl socket, because that storm is what wedges it and stops a PAID purchase from being authorised at all. The gate STAYS TRACKED and the client's access state is UNVERIFIED: ndsctl answered with an error, not with an answer about the client. The reconciliation re-attempts this close whenever it verifies the client's state with ndsctl, and the record is forgotten only when a new session replaces it. OPERATOR ACTION: inspect nodogsplash (ndsctl status) — restart it or reload the module to clear the wedge")
	return false
}

// closeGateConfirmed deauthorizes macAddress and reports whether the gate is
// closed. Confirmation is ndsctl's own answer: a deauth that fails is not a
// close, so it is retried up to deauthMaxAttempts with a short delay. A nil
// error means the client is deauthorized — or that NoDogSplash does not know the
// client at all, which is the same thing from the enforcement layer's side; a
// non-nil error means the close is UNCONFIRMED and the caller must keep the gate
// tracked.
//
// freshEvidence says the caller has just established something about the client
// itself (the reconciliation's probe), which is the one thing that may spend
// ndsctl on a gate whose close budget is already spent.
func closeGateConfirmed(macAddress string, freshEvidence bool) error {
	if !freshEvidence && !closeAttemptAllowed(macAddress) {
		return fmt.Errorf("%w: the module stopped re-attempting the close of %s", ErrGateCloseAbandoned, macAddress)
	}

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
			clearCloseStreak(macAddress)
			return nil
		}
		if attempt < deauthMaxAttempts {
			time.Sleep(deauthRetryDelay)
		}
	}

	// Second, independent confirmation channel: NoDogSplash's own client list.
	// ndsctl's wording can differ between versions, but a definitive "no record
	// for this MAC" (`ndsctl json <mac>` -> "{}") is the enforcement layer
	// stating that the client holds nothing to close. A probe that FAILS is not
	// evidence of anything, so it leaves the close unconfirmed and the budget
	// untouched — a probe error must never retire a gate.
	if gone, err := ndsctlHasNoClient(macAddress); err == nil && gone {
		logger.WithField("mac_address", macAddress).Info("Client already gone from NoDogSplash: ndsctl reports no client record for this MAC, so there is nothing left to deauthorize — the gate is closed by definition and the session is retired")
		clearCloseStreak(macAddress)
		return nil
	}

	return lastErr
}

// ndsctlHasNoClient asks NoDogSplash whether it knows macAddress at all
// (`ndsctl json <mac>`), read-only. A definitive answer is returned as a bool; a
// probe that fails returns an error, because a failed probe is not evidence that
// the client is gone.
func ndsctlHasNoClient(macAddress string) (bool, error) {
	state, err := CheckClientState(macAddress)
	if err != nil {
		return false, err
	}
	return !state.Registered, nil
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
	logger.WithFields(fields).Error("Gate close NOT confirmed for client: the gate stays tracked and the close is retried. The client's access state is UNVERIFIED until ndsctl confirms the deauthorization — ndsctl answered with an error, not with an answer about the client (a client NoDogSplash does not know at all is logged as already gone, and no retry is armed for it)")
}

// markGateOpenedLocked records that the gate of macAddress has been opened,
// extended, replaced or re-opened, and returns the epoch of the new gate.
// Caller must hold gatesMutex.
func markGateOpenedLocked(macAddress string) uint64 {
	gateEpochs[macAddress]++
	// A new gate generation starts with a clean close budget: whatever the
	// previous gate's close went through, THIS gate has not been attempted yet.
	delete(closeStreaks, macAddress)
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
	delete(closeStreaks, macAddress)
	if timer, ok := pendingCloseRetries[macAddress]; ok {
		timer.Stop()
		delete(pendingCloseRetries, macAddress)
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

	if err := closeGateConfirmed(macAddress, false); err != nil {
		if handleUnconfirmedClose(macAddress, attempt, err) {
			scheduleCloseRetry(macAddress, epoch, attempt+1)
		}
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

	if err := closeGateConfirmed(macAddress, false); err != nil {
		if handleUnconfirmedClose(macAddress, 0, err) {
			scheduleCloseRetry(macAddress, epoch, 1)
		}
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

	if err := closeGateConfirmed(macAddress, false); err != nil {
		if handleUnconfirmedClose(macAddress, 0, err) {
			scheduleCloseRetry(macAddress, epoch, 1)
		}
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
			markGateOpenedLocked(macAddress)
			go delayedAuth(macAddress)
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
			markGateOpenedLocked(macAddress)
			go delayedAuthIndefinite(macAddress)
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

	if err := closeGateConfirmed(macAddress, false); err != nil {
		if handleUnconfirmedClose(macAddress, 0, err) {
			scheduleCloseRetry(macAddress, epoch, 1)
		}
		return fmt.Errorf("gate for MAC %s is NOT confirmed closed: %w", macAddress, err)
	}

	if !finishGateClose(macAddress, epoch) {
		return fmt.Errorf("gate for MAC %s was reopened while it was being closed; the client was authorized again", macAddress)
	}
	return nil
}

// ReconcileGateClose re-attempts the close of a gate the module's sweep
// machinery has stopped re-attempting on its own (ErrGateCloseAbandoned), and is
// the recovery path that makes a spent budget converge.
//
// It may spend ndsctl on such a gate because the caller brings FRESH EVIDENCE
// about the client itself: the reconciliation probes NoDogSplash first and only
// calls this for an address whose client it has confirmed is gone. That evidence
// is what makes the attempt different from the hammering the budget bounds.
//
// It is CloseGate's contract otherwise — a failed close is not a close, the gate
// stays tracked and the session is retired only on a confirmed close — with one
// difference: a failure here does NOT escalate or advance the budget. The
// escalation for this gate has already fired once (that is what abandonment is),
// and a caller that keeps bringing the same evidence must not be able to make
// unconfirmed_closes grow monotonically. The failure is logged as a warning, and
// the next reconciliation pass re-attempts it.
func ReconcileGateClose(macAddress string) error {
	if !isValidMAC(macAddress) {
		return fmt.Errorf("invalid MAC address format: %s", macAddress)
	}

	gatesMutex.Lock()
	epoch := gateEpochs[macAddress]
	gatesMutex.Unlock()

	if err := closeGateConfirmed(macAddress, true); err != nil {
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"error":       err,
		}).Warn("The reconciliation could not confirm the close of this binding: it stays tracked and the next reconciliation pass re-attempts it (the reconciliation brings fresh evidence about the client once per pass, so it never hammers ndsctl)")
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
		if interruption, ended := ndsctlInterruptionOf(err); ended {
			// Ended by the module, already reported with its outcome: reading
			// the counters is not an ndsctl refusal, and the caller must treat
			// the usage as UNKNOWN (not as zero) either way.
			logger.WithFields(ndsctlInterruptionFields(interruption)).Debug("ndsctl json was ended by the module; the client's counters stay unknown")
			return 0, 0, fmt.Errorf("failed to execute ndsctl json for MAC %s: %w", macAddress, err)
		}
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
		if interruption, ended := ndsctlInterruptionOf(err); ended {
			logger.WithFields(ndsctlInterruptionFields(interruption)).Debug("ndsctl json for the client's state was ended by the module; the state stays unknown")
			return ClientState{}, fmt.Errorf("failed to execute ndsctl json for MAC %s: %w", macAddress, err)
		}
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

func delayedAuth(macAddress string) {
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
		gatesMutex.Lock()
		delete(openGates, macAddress)
		gatesMutex.Unlock()
		return
	}

	if err := authorizeMAC(macAddress); err != nil {
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"error":       err,
		}).Error("Delayed auth failed")
		gatesMutex.Lock()
		delete(openGates, macAddress)
		gatesMutex.Unlock()
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
	epoch := nextGateEpochLocked(macAddress)
	gatesMutex.Unlock()

	timer := time.AfterFunc(remaining, func() {
		expireTimedGate(macAddress, epoch)
	})

	gatesMutex.Lock()
	openGates[macAddress] = timer
	markGateOpenedLocked(macAddress)
	gatesMutex.Unlock()
}

func delayedAuthIndefinite(macAddress string) {
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
		gatesMutex.Lock()
		delete(openGates, macAddress)
		gatesMutex.Unlock()
		return
	}

	if err := authorizeMAC(macAddress); err != nil {
		logger.WithFields(logrus.Fields{
			"mac_address": macAddress,
			"error":       err,
		}).Error("Delayed auth failed")
		gatesMutex.Lock()
		delete(openGates, macAddress)
		gatesMutex.Unlock()
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
