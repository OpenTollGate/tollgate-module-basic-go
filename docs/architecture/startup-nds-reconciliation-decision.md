# Startup reconciliation: a client NoDogSplash still authorises that this module holds no session for

## Status: Decided and implemented (2026-09-26)

Applies to `src/merchant` (the startup path of the usage monitor and the session
map), `src/valve` (the new client-list read, `ndsctl json` with no argument) and
`main` (nothing changed: the pass runs inside merchant construction, before the
merchant is installed behind the payment API).

It closes the hole that
[`zombie-session-close-reconciliation-decision.md`](zombie-session-close-reconciliation-decision.md)
§5 recorded and deliberately left open, and it is the startup direction of the
drift whose periodic direction that record decides. It does not replace
[`ndsctl-invocation-outcomes-decision.md`](ndsctl-invocation-outcomes-decision.md);
it uses the classification that record defines (every invocation is attributed to
the side that ended it).

## Background: the measured defect

The module's gate map, session map and metering baselines are **process-local**:
nothing loads them from disk, so a restarted module starts from an empty set.
NoDogSplash's client list is **not** process-local — `nodogsplash` is a separate
service, its procd dependency does not restart it with the module, and it keeps
every client it had authorised, with the gate open.

Reproduced on the bench MT3000 (pre17, module pin `2796d96c`, 2026-09-26,
`~/tg-manual/restart-drift-ln.sh`, log
`~/tg-manual/restart-drift-ln-20260926T112454Z.log`): buy one Lightning step
(gate open), then restart **only** `tollgate-wrt`. Measured immediately after:

* `ndsctl json` → `client_length: 2`, the client `state: Authenticated`;
* the freshly restarted module holds **no session at all** (`/balance` →
  `session_active:false`);
* from the client's vantage: `probe=204`, `egress code=200 bytes=2000000`.

The client had free, unmetered internet — with no session, no baseline and no
allotment anywhere in the module's memory — until NoDogSplash's own session
timeout or the next restart cleared it. The inverse case (a session the module
holds whose client NoDogSplash has forgotten, `client_length: 0`, `/balance`
answering "no session" while the module tracks one) is the one the zombie-session
record decides, and its recovery already exists.

Why this is not merely bookkeeping: the operator's release-candidate report is
"after a second purchase the balance shows a new allotment but there is no
internet". A module whose tracking disagrees with the enforcement layer in EITHER
direction produces exactly that class of report — and the free-internet direction
is the one that costs revenue on every restart.

## Decision

1. **The module reads NoDogSplash's client list, once, at startup.** The new
   `valve.ListClients` runs `ndsctl json` with no argument (the measured shape is
   pinned in `src/valve/nds_clients_test.go` against the payload the bench
   printed), and `valve.AuthorisedClients` returns the MACs in state
   `Authenticated`. The read goes through the ordinary invocation path, so it
   inherits the attribution contract: an invocation the MODULE ended (its own
   deadline, or a shutdown) is reported as such and is never escalated as an
   ndsctl failure.

2. **A client NoDogSplash authorises that the module holds no record of has its
   gate CLOSED — fail closed.** The module cannot meter it (no session, no
   baseline, no allotment), it did not open it, and leaving it is unmetered
   internet. The close goes through the ordinary `valve.CloseGate` contract: a
   deauth the module cannot confirm leaves the gate TRACKED and the client's
   access UNVERIFIED, and the escalation says so.

3. **"The module holds no record of it" is measurable, and the ordering makes it
   safe.** `grantSessionAccess` records the session BEFORE it opens the gate (and
   restores the previous record if the open fails), so at any instant a client
   NoDogSplash holds as Authenticated that appears in neither the session map nor
   the valve's tracked gates is not a purchase in flight. That is why the pass may
   close it, and it is why the pass is safe even though it touches the
   enforcement layer.

4. **The pass runs once, synchronously, at startup — never periodically.** The
   drift it repairs is created by exactly one event: this module starting with an
   empty session set while NoDogSplash keeps its list. The periodic direction
   belongs to the stale-binding reconciliation (a device can leave at any time).
   The startup position also runs inside merchant construction — before
   `installMerchant` and before `apiStartup.markReady()` — so it can never race a
   purchase that is being served: while it runs, the payment API still answers
   the explicit "starting" refusal.

5. **A client in any state other than `Authenticated` is left alone.** A
   `Preauthenticated` record cannot pass traffic past the captive portal, so there
   is no access to take away; a state this module does not recognise is treated as
   NOT authorisation (guessing "authorised" would cut off a customer, guessing
   "not authorised" only leaves alone a client NoDogSplash says nothing about).

6. **An unreadable client list changes NOTHING and is reported.** "The module
   could not read the list" is not evidence about any client, so no gate is closed
   on the strength of it, and the operator gets one line naming the residue and
   the check to run (`ndsctl json`). The failure is not silent: the client that
   keeps unmetered access is named as a possibility, not hidden behind a
   comforting "nothing to do".

7. **What the customer loses is named, and it is not silently kept.** The
   purchased remainder lived in the memory of the process that died, so a
   restarted box cannot honour it: the customer must buy again. The pass says so
   in the same line that closes the gate, rather than letting the module pretend
   the session survived. Carrying value across a restart needs entitlement to
   travel with a session ticket — the session-scoped-address work, still open —
   and this record does not claim it.

## Consequences

* A restart is no longer a free-internet window. The measured F1 state (client
  Authenticated in NDS, module holding no session, egress passing traffic) ends
  with the client's gate closed at startup.
* The customer-visible cost is real and deliberately accepted: a box restart ends
  every live session, and the remainder is not transferable. The alternative is
  unmetered internet on every restart of a box that is expected to restart during
  the RC review (upgrades, re-pins, `tollgate-wrt restart`).
* The log now answers the question the operator actually asks after a restart
  ("who was holding access that this module does not know about?") with the MACs,
  the state NoDogSplash reported, and what the module did about each one.
* The tests that pin this: `src/merchant/startup_nds_reconciliation_test.go` (the
  inherited client is closed; the module's OWN live session and a merely
  Preauthenticated record are not; the pass is bounded to one close and one
  escalation; an unreadable list changes nothing and is reported; a refused close
  is an ERROR naming the residue and the operator action, never "now closed") and
  `src/valve/nds_clients_test.go` (the measured payload parses, the states decide
  access, an unreadable or malformed answer is an error and never an empty list,
  and an invocation the module ended is not escalated as an ndsctl failure).
* Residual, stated: while NoDogSplash's control socket is wedged, this pass cannot
  read the list and therefore cannot close anything (decision 6) — the same wedge
  that blocks a paid grant, and the operator action is the same (restart
  `nodogsplash`).
