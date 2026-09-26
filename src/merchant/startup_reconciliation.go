package merchant

import (
	"log"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
)

// Startup reconciliation of NoDogSplash's authorisations against the module's
// sessions (t_83e6ab0f, deliverable 3; the inverse drift to the stale-binding
// reconciliation in stale_binding_janitor.go).
//
// The module's gate, session and baseline bookkeeping is PROCESS-LOCAL: nothing
// loads it at startup, so a module restart starts from an empty session set.
// NoDogSplash's client list does NOT die with the module — nodogsplash is a
// separate service and keeps every client it had authorised. The result is
// measured, not hypothesised (`~/tg-manual/restart-drift-ln-20260926T112454Z.log`,
// bench MT3000, pre17): buy, gate open, restart ONLY `tollgate-wrt`, and the
// client is still `state=Authenticated` in `ndsctl json` with
// `probe=204`/`egress 200` while the freshly restarted module holds no session
// at all. Nothing on the box meters that client until NoDogSplash's own session
// timeout clears it. That is free, unmetered internet.
//
// The resolution is fail-closed, and the reason it can be decided at all is an
// ordering invariant the purchase path already guarantees: grantSessionAccess
// records the session BEFORE it opens the gate (and restores the previous record
// if the open fails). So at any instant, a MAC NoDogSplash holds as
// Authenticated that appears in NEITHER the session map NOR the valve's tracked
// gates is not a purchase in flight — it is a record this module cannot meter
// and did not make. Its gate is closed.
//
// What that costs, stated plainly: the customer's remaining allotment is NOT
// carried across a module restart (it lived in the memory of the process that
// died), so a paying customer whose box restarted mid-session must buy again.
// The alternative — leaving the gate open — is unmetered internet with the
// module unable to say what was spent, which is the direction C1-2 forbids. The
// honest statement of the limit: entitlement cannot travel with a session until
// it travels with a session ticket (next-release work), and this pass does not
// pretend otherwise.

// ReconcileNdsAuthorisationsOnStartup closes the gate of every client NoDogSplash
// still authorises that this module holds no session for, and reports what it
// found. It is called once, synchronously, from StartDataUsageMonitoring — i.e.
// during merchant construction, before the merchant is installed behind the API
// and before apiStartup opens the gate, so it can never race a purchase that is
// being served.
//
// It is deliberately NOT run periodically. The drift it repairs is created by
// exactly one event — this module starting (its session set starts empty while
// NoDogSplash's list survives) — so a startup pass is the whole of the repair;
// the drift in the other direction (a session the module holds whose client
// NoDogSplash has forgotten, or whose device has left the network) is the
// stale-binding reconciliation's, and that one does run periodically because a
// device can leave at any time.
func (m *Merchant) ReconcileNdsAuthorisationsOnStartup() {
	authorised, err := valve.AuthorisedClients()
	if err != nil {
		// Nothing is changed on an unreadable list: "could not read it" is not
		// evidence about any client, and closing gates on the strength of a
		// failure would cut off paying customers. The operator gets the one
		// line that names the residue and the check to run.
		log.Printf("WARNING: startup reconciliation: could not read NoDogSplash's client list (%v), so the module did NOT change any client's access — it cannot tell which authorised clients it holds no session for. OPERATOR ACTION: `ndsctl json` on the router shows the clients NoDogSplash is holding; a client it authorises that this module has no session for keeps unmetered access until NoDogSplash's own timeout", err)
		return
	}

	if len(authorised) == 0 {
		log.Printf("Startup reconciliation: NoDogSplash holds no authorised client, so there is nothing this module could be out of step with")
		return
	}

	inherited := 0
	for _, macAddress := range authorised {
		if m.knowsClient(macAddress) {
			log.Printf("Startup reconciliation: NoDogSplash authorises %s and this module holds a session (or a tracked gate) for it — leaving it alone", macAddress)
			continue
		}

		if err := valve.CloseGate(macAddress); err != nil {
			log.Printf("ERROR: startup reconciliation: NoDogSplash was still authorising %s when this module started and the module holds no session for it, and its gate could NOT be confirmed closed: %v — the client may still hold open, unmetered access. The gate stays tracked and the valve keeps re-attempting the close; OPERATOR ACTION: inspect nodogsplash (`ndsctl status`) and restart it if its control socket is not answering (unconfirmed gate closes=%d)",
				macAddress, err, valve.GateCloseFailures())
			continue
		}

		inherited++
		log.Printf("WARNING: startup reconciliation: NoDogSplash was still authorising %s when this module started and the module holds no session for it — a module restart loses every session (they are process-local) while NoDogSplash keeps its client list, so that gate held open, UNMETERED access with nothing metering it. Its gate is now CLOSED. The customer's remaining allotment does NOT travel across a restart (it lived in the process that died), so it is not silently kept: the client must buy again",
			macAddress)
	}

	if inherited == 0 {
		log.Printf("Startup reconciliation: all %d client(s) NoDogSplash authorises belong to a session this module holds", len(authorised))
	}
}

// knowsClient reports whether this module has any record of macAddress — a
// session, or a gate the valve is tracking. Either one means the client is the
// module's own and the startup reconciliation must not touch it.
func (m *Merchant) knowsClient(macAddress string) bool {
	m.sessionMu.RLock()
	defer m.sessionMu.RUnlock()

	if _, exists := m.customerSessions[macAddress]; exists {
		return true
	}

	for _, tracked := range valve.TrackedGates() {
		if tracked == macAddress {
			return true
		}
	}
	return false
}
