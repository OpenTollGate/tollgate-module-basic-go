package merchant

import (
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
)

// Periodic reconciliation of NoDogSplash's client list against this module's
// bookkeeping: the SAME question the startup pass asks, asked again, because the
// startup pass can only ever see the drift that exists at the instant it runs.
//
// The startup pass (startup_reconciliation.go) closes the window a RESTART
// creates: the module's session set starts empty while NoDogSplash — a separate
// service its procd dependency does not restart — keeps every client it had
// authorised. Nothing else reads the client list, so a client NoDogSplash
// authorises that this module never opened stays authorised until NoDogSplash's
// own session timeout: unmetered access, with no session, no baseline, no
// allotment and no /balance record. That is the same free-internet state the
// startup pass exists to remove, reached through a different door. The doors
// that never pass through the module's purchase path:
//
//   - anything that authorises a client without the module (`ndsctl auth` run by
//     hand or by a script, a second process, or a record restored from
//     NoDogSplash's own state file when the SERVICE restarts);
//   - a module restart on a box whose merchant came up DEGRADED (no reachable
//     mint). MerchantDegraded.StartDataUsageMonitoring is a no-op, so the
//     startup pass does not run at all until a mint becomes reachable and the
//     upgrade constructs a full merchant. That upgrade is a RUNTIME
//     construction, so what the startup pass closes at the upgrade, this pass
//     closes on its own cadence instead.
//
// The decision is the one the startup pass already took, and the alternatives
// are rejected in the decision record: a client NoDogSplash reports as
// Authenticated that this module holds no record of — no session, no tracked
// gate, no unconfirmed close in flight — cannot be metered and was not opened by
// this module, so its gate is CLOSED (fail closed). Adopting it into a session
// would have to invent an allotment nobody paid for, which is the dangerous
// direction this card is about; leaving it alone is the defect itself.
//
// What makes it safe to run while the box is serving customers is the same
// ordering invariant the startup pass relies on: `grantSessionAccess` records
// the session BEFORE it opens the gate (and restores the previous record if the
// open fails), so a purchase in flight is never a client this module "does not
// know". A gate whose close is still UNCONFIRMED is also a client this module
// knows it owns — `knowsClient` reads the valve's tracked gates, which is where
// such a gate stays until ndsctl confirms the close — so this pass leaves it to
// the close machinery rather than fighting it. Those two properties are
// load-bearing: anything that removes them turns this pass into one that closes
// a paying client's gate.
//
// A FAILED READ CHANGES NOTHING. "The module could not read the list" is not
// evidence about any client, so nothing is closed on the strength of it — the
// property #545, #595 and the startup pass all preserve.

// defaultNdsClientReconcileEvery is how many usage-monitor sweeps (2 s each)
// pass between two client-list reconciliations by default — the same ~30 s
// cadence as the stale-binding reconciliation, because both spend ndsctl calls
// on the sweep goroutine and both answer a question the meter cannot.
const defaultNdsClientReconcileEvery = 15

// ndsClientReconcileReportInterval is how often the pass REPEATS an unchanged
// finding. The sweep runs every 2 s, so a line per pass would be the storm #596
// removed from the close path; a residue that is still there a minute later is
// worth saying again. A CHANGE is reported at once — that is news the operator
// has not been told. It is a var so tests can shrink it.
var ndsClientReconcileReportInterval = time.Minute

// ndsClientReconciler is the pass's per-merchant bookkeeping.
//
// The cadence, the read seam and the reporting state are fields rather than
// package variables for the reason staleBindingJanitor documents: the pass runs
// on the usage monitor's own goroutine, so a policy a test writes while that
// goroutine reads it would be a data race.
type ndsClientReconciler struct {
	mu          sync.Mutex
	sweeps      int
	everySweeps int

	// list overrides the client-list read for this merchant only; nil means
	// valve.ListClients, which is what production uses.
	list func() ([]valve.ClientRecord, error)

	// lastFingerprint is the finding of the last pass that had something to
	// report, so a repeat of the SAME finding is throttled while a change is
	// reported at once. hasResidue says whether that finding is still standing,
	// which is what lets the pass say once that the condition ended.
	lastFingerprint string
	hasResidue      bool
	reported        time.Time
	reportEvery     time.Duration

	// readFailureReported says the operator has already been told that the
	// client list cannot be read; it is reset by the first read that succeeds.
	readFailureReported bool
}

// ndsClientListProbeLocked returns the client-list read this merchant uses.
// Caller must hold j.mu.
func (j *ndsClientReconciler) ndsClientListProbeLocked() func() ([]valve.ClientRecord, error) {
	if j.list != nil {
		return j.list
	}
	return valve.ListClients
}

// reconcileEveryLocked returns how many sweeps pass between two passes.
// Caller must hold j.mu.
func (j *ndsClientReconciler) reconcileEveryLocked() int {
	if j.everySweeps < 1 {
		return defaultNdsClientReconcileEvery
	}
	return j.everySweeps
}

// reportEveryLocked returns how often an unchanged finding is repeated.
// Caller must hold j.mu.
func (j *ndsClientReconciler) reportEveryLocked() time.Duration {
	if j.reportEvery > 0 {
		return j.reportEvery
	}
	return ndsClientReconcileReportInterval
}

// setNdsClientListProbe replaces the client-list read for this merchant only. It
// exists for tests; production leaves it nil and reads NoDogSplash through
// valve.ListClients.
func (m *Merchant) setNdsClientListProbe(list func() ([]valve.ClientRecord, error)) {
	m.ndsClients.mu.Lock()
	m.ndsClients.list = list
	m.ndsClients.mu.Unlock()
}

// tuneNdsClientReconciliation replaces the pass's cadence and report throttle
// for this merchant. It exists for tests: production keeps the defaults (~30 s
// cadence, one repeat a minute). Per merchant rather than package-level because
// the pass runs on the usage monitor's own goroutine.
func (m *Merchant) tuneNdsClientReconciliation(everySweeps int, reportInterval time.Duration) {
	m.ndsClients.mu.Lock()
	m.ndsClients.everySweeps = everySweeps
	m.ndsClients.reportEvery = reportInterval
	m.ndsClients.mu.Unlock()
}

// ndsClientReconcileSweep runs the pass when its cadence is due.
func (m *Merchant) ndsClientReconcileSweep() {
	m.ndsClients.mu.Lock()
	m.ndsClients.sweeps++
	due := m.ndsClients.sweeps%m.ndsClients.reconcileEveryLocked() == 0
	m.ndsClients.mu.Unlock()

	if due {
		m.reconcileNdsClientList()
	}
}

// reconcileNdsClientList is one pass: read NoDogSplash's client list, close the
// gate of every client it reports as Authenticated that this module holds no
// record of, and report the residue — once per distinct finding.
func (m *Merchant) reconcileNdsClientList() {
	m.ndsClients.mu.Lock()
	list := m.ndsClients.ndsClientListProbeLocked()
	m.ndsClients.mu.Unlock()

	records, err := list()
	if err != nil {
		m.noteNdsClientListUnreadable(err)
		return
	}

	m.ndsClients.mu.Lock()
	m.ndsClients.readFailureReported = false
	m.ndsClients.mu.Unlock()

	var unknown, knownAuthorised []valve.ClientRecord
	for _, record := range records {
		if !record.Authorised() {
			// A Preauthenticated record cannot pass traffic past the captive
			// portal, so there is no access to take away, and it is a LEGITIMATE
			// record: a client on its way through the splash page is
			// Preauthenticated by definition. Closing it would tear up a
			// customer's session in progress. A Trusted or Blocked client is the
			// operator's own configuration (nodogsplash's `trustedmaclist` /
			// `blockedmaclist`), not this module's authorisation to manage.
			continue
		}
		if m.knowsClient(record.MAC) {
			knownAuthorised = append(knownAuthorised, record)
			continue
		}
		unknown = append(unknown, record)
	}

	if len(unknown) == 0 {
		m.noteNdsClientListHealthy(len(knownAuthorised))
		return
	}

	closed := make([]string, 0, len(unknown))
	refused := make([]string, 0, len(unknown))
	for _, record := range unknown {
		// The MAC goes back to ndsctl exactly as NoDogSplash reported it:
		// ndsctl's own lookup is a case-sensitive strcmp on the MAC
		// (`client_list_find_by_any`, nodogsplash 5.0.2), so handing it the
		// module's normalised spelling would not necessarily match the record it
		// is trying to close. Only the "does this module know it" test is
		// case-insensitive.
		if err := valve.CloseGate(record.MAC); err != nil {
			refused = append(refused, record.MAC)
			continue
		}
		closed = append(closed, record.MAC)
	}

	m.reportNdsClientResidue(closed, refused, len(knownAuthorised))
}

// noteNdsClientListUnreadable reports a client list the module could not read,
// once per outage, and changes NOTHING: a failed probe is never evidence about a
// client.
func (m *Merchant) noteNdsClientListUnreadable(err error) {
	m.ndsClients.mu.Lock()
	already := m.ndsClients.readFailureReported
	m.ndsClients.readFailureReported = true
	residue := m.ndsClients.hasResidue
	m.ndsClients.mu.Unlock()

	if already {
		return
	}

	// The residue is named rather than hidden behind a comforting "nothing to
	// do": the clients an earlier pass found authorised-and-unknown keep
	// whatever access NoDogSplash gave them for as long as the read fails.
	residueText := "Nothing is known to be outstanding from the last pass that read the list."
	if residue {
		residueText = "The clients the last successful pass found authorised-and-unknown stay exactly as NoDogSplash has them until a read succeeds."
	}
	log.Printf("WARNING: client-list reconciliation: could not read NoDogSplash's client list (%v), so no client's access was changed — the module cannot tell which authorised clients it holds no record of. %s OPERATOR ACTION: `ndsctl json` on the router shows the clients NoDogSplash is holding; restart nodogsplash if its control socket is not answering",
		err, residueText)
}

// noteNdsClientListHealthy records a pass that found no drift: it clears the
// residue and says so ONCE when there was one, so an operator sees the condition
// end rather than only its start.
func (m *Merchant) noteNdsClientListHealthy(knownAuthorised int) {
	m.ndsClients.mu.Lock()
	hadResidue := m.ndsClients.hasResidue
	m.ndsClients.lastFingerprint = ""
	m.ndsClients.hasResidue = false
	m.ndsClients.mu.Unlock()

	if hadResidue {
		log.Printf("Client-list reconciliation: NoDogSplash now holds no authorised client that this module lacks a record of, so the unmetered window is closed (%d authorised client(s) belong to this module's own sessions or gates)", knownAuthorised)
	}
}

// reportNdsClientResidue reports the clients found authorised-but-unknown.
//
// It is written once per distinct finding and then at most once per
// ndsClientReconcileReportInterval: the sweep runs every 2 s, and a line per pass
// is the storm this module spent #596 removing from the close path. A CHANGE — a
// different MAC, or a close that failed where the last one succeeded — is
// reported at once, because that is news the operator has not been told.
func (m *Merchant) reportNdsClientResidue(closed, refused []string, knownAuthorised int) {
	fingerprint := residueFingerprint(closed, refused)

	m.ndsClients.mu.Lock()
	unchanged := fingerprint == m.ndsClients.lastFingerprint && m.ndsClients.hasResidue
	due := time.Since(m.ndsClients.reported) >= m.ndsClients.reportEveryLocked()
	m.ndsClients.lastFingerprint = fingerprint
	m.ndsClients.hasResidue = true
	if unchanged && !due {
		m.ndsClients.mu.Unlock()
		return
	}
	m.ndsClients.reported = time.Now()
	m.ndsClients.mu.Unlock()

	unknownCount := len(closed) + len(refused)
	parts := make([]string, 0, 2)
	if len(closed) > 0 {
		parts = append(parts, "CLOSED: "+strings.Join(closed, ", "))
	}
	if len(refused) > 0 {
		parts = append(parts, "NOT confirmed closed: "+strings.Join(refused, ", "))
	}
	detail := strings.Join(parts, " | ")

	if len(refused) > 0 {
		log.Printf("ERROR: client-list reconciliation: NoDogSplash authorises %d client(s) this module holds no session, gate or in-flight close for (%s), and at least one of their gates could NOT be confirmed closed — that client may still hold open, UNMETERED access until NoDogSplash's own timeout. A gate whose close is unconfirmed stays TRACKED and the valve keeps re-attempting it; OPERATOR ACTION: inspect nodogsplash (`ndsctl status`) and restart it if its control socket is not answering (unconfirmed gate closes=%d). %d authorised client(s) do belong to this module's own sessions or gates and were left alone",
			unknownCount, detail, valve.GateCloseFailures(), knownAuthorised)
		return
	}

	what := "their gates are now CLOSED"
	if len(closed) == 1 {
		what = "its gate is now CLOSED"
	}
	log.Printf("WARNING: client-list reconciliation: NoDogSplash authorises %d client(s) this module holds no session, gate or in-flight close for (%s). A client NoDogSplash authorises that this module never opened is UNMETERED access by construction — no session, no metering baseline, no allotment and no /balance record — so %s, and it must buy again before it has metered access. %d authorised client(s) do belong to this module's own sessions or gates and were left alone",
		unknownCount, detail, what, knownAuthorised)
}

// residueFingerprint is a stable description of one finding, used to decide
// whether a pass has something new to say. Closed and refused are kept apart:
// the same MAC moving from "closed" to "refused" (or back) is a change worth
// reporting at once.
func residueFingerprint(closed, refused []string) string {
	sortedClosed := append([]string{}, closed...)
	sortedRefused := append([]string{}, refused...)
	sort.Strings(sortedClosed)
	sort.Strings(sortedRefused)
	return strings.Join(sortedClosed, ",") + "|" + strings.Join(sortedRefused, ",")
}
