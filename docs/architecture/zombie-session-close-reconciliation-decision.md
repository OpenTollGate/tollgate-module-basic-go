# A client NoDogSplash has forgotten: close it, bound the retry, reconcile

## Status: Decided (2026-09-26)

Applies to `src/valve` (the `ndsctl` gate) and the usage monitor's reconciliation
in `src/merchant`.

## Background: the measured defect

On the bench MT3000 (pre17, module pin `2796d96c`, clean wireless e2e
`e2e-20260926T103636Z.log`) a session for a real Wi-Fi client that had left the
network (`a8:a0:92:a5:39:7a`) became **unretirable**. Twice a second, for ever:

```
ERROR: the usage of the bytes session of a8:a0:92:a5:39:7a has been unreadable for 170 sweeps
  (client counters unavailable for MAC a8:a0:92:a5:39:7a: client with MAC a8:a0:92:a5:39:7a
   not found in ndsctl)
Error deauthorizing MAC address            error="exit status 1"   mac_address="a8:a0:92:a5:39:7a"
Gate close NOT confirmed for client: the gate stays tracked and the close is retried — until
  ndsctl confirms it, this client may still hold open, unmetered access
  attempt=3 error="exit status 1" retry_in=2s unconfirmed_closes=193
```

while `ndsctl deauth` was answering, verbatim:

```
Client a8:a0:92:a5:39:7a not found.
```

Three separate failures were stacked in that one path:

1. **`exit status 1` was misread.** For `ndsctl deauth`, rc=1 with
   `Client <mac> not found.` is a TERMINAL, SUCCESSFUL state: the enforcement
   layer holds no such client, so there is nothing left to close. The module
   treated it as an *unconfirmed* close and retried it without bound
   (`unconfirmed_closes` 113 → 193 → 195 — monotonic, so the loop never cleared).
2. **The session was never retired.** "The session is retained and the close is
   retried" — a session whose client is gone can be neither closed nor metered,
   and nothing ever gave up on it.
3. **The warning was false.** "this client may still hold open, unmetered access"
   is wrong when NoDogSplash does not know the MAC at all: there is no access for
   it to hold. The TRUE unmeterable case (a client NDS knows but whose counters
   cannot be read) is different and is handled elsewhere by closing the gate.

The consequence was not cosmetic. The loop drove `ndsctl` at the sweep cadence
until the socket died (`nodogsplash: Socket is not ready for communication : Bad
file descriptor` every ~5 s from 10:20:34Z), and with a dead socket **a paid
purchase could no longer be authorised at all**: `state=PAID`, merchant wallet
+1 sat, `access_granted` never true. `nodogsplash restart` + `tollgate-wrt
restart` was the only thing that restored the box. That is the operator's
"the second purchase showed a new allotment but no internet", with the money
moving.

## Decision

1. **"Client not found" is a completed close.** `ndsctl deauth` answering that
   NoDogSplash does not know the MAC is treated as success by `deauthorizeMAC`
   (matched narrowly on "not found" plus the MAC or the word "client", so
   unrelated ndsctl failures can never be mistaken for it). The gate is retired,
   **no retry is armed**, and the operator gets an INFO line naming the verified
   state: *"Client already gone from NoDogSplash: … there is nothing left to
   deauthorize — the gate is closed by definition and the session is retired"*.

2. **Two independent confirmation channels, and a failed probe is never
   evidence.** Besides ndsctl's own answer, a definitive "no client record for
   this MAC" (`ndsctl json <mac>` → `{}`) also completes a close. A probe that
   *errors* changes nothing: the close stays unconfirmed and the budget is
   untouched. (The C1-2 audit of #545 deliberately kept the deauth exit status as
   the primary confirmation to avoid "a probe error ⇒ never retire"; that
   property is preserved — the probe channel only ever *adds* a way to complete a
   close that is already failing, never a way to leave a gate open.)

3. **The close retry is BOUNDED per gate generation** (`closeAttemptBudget = 8`).
   At the budget the module stops driving ndsctl about that gate, **keeps the gate
   tracked** (the record that says the client must be closed is never dropped),
   and escalates the abandonment **exactly once** with what an operator has to do
   (`Gate close UNRESOLVED: …`). `unconfirmed_closes` therefore cannot grow
   monotonically, and one stuck session cannot reach the storm rate again.

4. **The reconciliation is the recovery path for a spent budget.**
   `ReconcileGateClose` may spend an attempt on a gate whose budget is spent,
   because its caller brings FRESH EVIDENCE about the client (the reconciliation
   probes NoDogSplash first and only calls it for an address it has confirmed is
   gone). A failure on that path does not escalate and does not advance the
   budget — a caller that keeps bringing the same evidence must not be able to
   make `unconfirmed_closes` climb either. A `bytes` session whose client leaves
   NDS therefore still converges: the address is retired within the grace window,
   and the reconciliation's cadence (≈30 s, two consecutive "gone" passes) bounds
   how often an attempt can be made.

5. **Reconcile tracked state against `ndsctl json` — periodically, and state the
   startup position honestly.** The periodic pass already exists (it is the
   stale-binding reconciliation, which asks the one question a meter cannot: *is
   the client that bought this address still on the network?*). Its choice is
   **RETIRE**, never silently re-establish: a binding whose client is gone is torn
   down under the same close contract, and the purchased remainder is explicitly
   not transferable until entitlement travels with a session ticket (the
   session-scoped address decision, #572).

   At **startup** there is nothing of the module's own to reconcile: the gate map,
   the session map and the metering baselines are process-local and nothing loads
   them from disk, so a restart starts from an empty set. No startup pass over the
   module's own bookkeeping is added, because such a pass would iterate an empty
   set. What is *not* empty at startup is NoDogSplash's own client list, which
   survives a module restart (its procd dependency does not restart it): clients
   NDS still holds that the module knows nothing about are unmetered access by
   construction. That is the **inverse drift** — it needs the client list from
   `ndsctl`, which this module did not read at the time, and a decision about what
   to do with a record whose allotment is unknown. It is deliberately out of scope
   here and filed as its own task so it is decided rather than assumed.

   > **Amended 2026-09-26 (implemented):** the task this paragraph filed has been
   > decided and implemented —
   > [`startup-nds-reconciliation-decision.md`](startup-nds-reconciliation-decision.md).
   > The module now reads NoDogSplash's client list once, at startup
   > (`valve.AuthorisedClients`, `ndsctl json` with no argument) and closes the
   > gate of every client NoDogSplash still authorises that it holds no session
   > for; the measured free-internet window this paragraph describes (module
   > restart, `state=Authenticated` in NDS, no session in the module) ends at the
   > next startup. The paragraph above stands as the record of what this decision
   > did NOT do.

6. **The wording matches the verified state.** No line claims a client "may still
   hold open, unmetered access" without evidence that the client could hold
   access. A verified-absent client is reported as *already gone*; an unconfirmed
   close is reported as **UNVERIFIED** ("ndsctl answered with an error, not with
   an answer about the client"), with the ndsctl answer attached as a field. A
   reviewer can no longer read the log and conclude that the release hands out
   free internet — and an operator cannot chase a hole that is not there.

## Alternatives considered

* **Keep retrying until it converges** (the current behaviour). Rejected: it is
  an unbounded, self-inflicted denial of service against the one interface the
  gate depends on, and the measured outcome was a paid customer with no access.
* **Abandon the close with no recovery path.** Rejected: it is the fail-open
  direction — a client whose close could not be confirmed would keep access until
  a reboot. The reconciliation's evidence-driven attempt keeps the gate closable.
* **Use the read-only probe as the PRIMARY confirmation of every close.**
  Rejected (as in #545): one probe error would make a gate unretirable. It is used
  only as a second channel for a *definitive* answer.
* **Have the module restart `nodogsplash` when the socket is wedged.** Rejected
  here: a service the module restarts is indistinguishable in the log from an
  operator (or an unrelated worker) doing it, and wedged-socket recovery is
  already owned by the kill-storm/orphaned-session work. The module's job in that
  state is to stop making it worse and to say exactly what it knows.

## Consequences

* `GateCloseFailures()` now means "escalations recorded", bounded per gate;
  `GateClosesAbandoned()` is the new gauge for gates the module has stopped
  re-attempting. Both are log-visible; the module still has no health endpoint,
  so the log remains the operator surface.
* A gate can now be in three states, and each is named in the log: **closed**
  (confirmed, retired), **unconfirmed** (tracked, retried within budget, state
  UNVERIFIED), **abandoned** (tracked, not retried by the machinery, escalated
  once, closable by the reconciliation or by a new session).
* The reproduction lives in the unit suites: `src/valve/zombie_session_test.go`
  and `src/merchant/zombie_session_test.go` drive a fake ndsctl that answers the
  measured string, and assert convergence plus a non-growing
  `unconfirmed_closes`. The bench lane carries the same assertion end to end
  (`scripts/mt3000-bench/second-purchase-e2e.sh` in the test kit).
