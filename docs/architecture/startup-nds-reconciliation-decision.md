# Startup reconciliation: a client NoDogSplash still authorises that this module holds no session for

## Status: Decided and implemented (2026-09-26; decision 4 amended 2026-09-28 by
[t_53b738bb](#amendment-2026-09-28-the-same-question-asked-periodically))

Applies to `src/merchant` (the startup path of the usage monitor and the session
map), `src/valve` (the new client-list read, `ndsctl json` with no argument) and
`main` (nothing changed: the pass runs inside merchant construction, before the
merchant is installed behind the payment API). The amendment extends it to
`src/merchant`'s usage sweep (`checkDataUsage` →
`reconcileNdsClientList`, `nds_client_reconciliation.go`).

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

4. **The pass runs once, synchronously, at startup — and, since the 2026-09-28
   amendment, the SAME question is asked periodically by the usage sweep.** The
   drift a restart creates is created by exactly one event, and the startup
   position repairs it (and runs inside merchant construction — before
   `installMerchant` and before `apiStartup.markReady()` — so it can never race a
   purchase that is being served: while it runs, the payment API still answers
   the explicit "starting" refusal). But a startup pass can only ever see the
   drift that exists at the instant it runs, and there are authorisations that
   never pass through this module at all. The amendment
   ([below](#amendment-2026-09-28-the-same-question-asked-periodically)) decides
   the periodic half: the usage sweep reads the client list on its own slower
   cadence (~30 s) and closes the same drift there. The paragraph this decision
   originally wrote — *never periodically* — is **withdrawn**; the reasoning that
   produced it (a startup pass is the whole of the repair) was wrong, because it
   assumed the module is the only thing that can authorise a client.

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

## Amendment 2026-09-28: the same question, asked periodically

The startup pass answers the question once. `zombie-session-close-reconciliation-decision.md`
§5 named this task as its own, and the card that carried it (t_53b738bb) records
that *nothing* enumerated the client list, so a client NoDogSplash held that this
module had never heard of stayed authorised — with no session, no metering
baseline, no allotment and no `/balance` record — for as long as NoDogSplash's own
session timeout. The module deliberately sets that timeout to a ceiling
(`sessiontimeout='86400'`, `packaging/files/etc/uci-defaults/99-tollgate-setup`,
so the Go backend is the sole authority on session end): the window is a day, not
20 minutes.

### The doors a startup pass cannot close

1. **An authorisation the module did not make.** `ndsctl auth` run by hand, by an
   operator script, or by a second process authorises a client that never enters
   this module's session map at all. There is no restart, so no startup pass runs.
2. **A client restored by NoDogSplash's own state file.** nodogsplash 5.0.2
   exports its client list on SIGTERM and imports it at startup
   (`src/main.c:147`/`:304`, `src/state_file.c`), and `state_file_import_client`
   re-applies the recorded state through `auth_change_state`. A restart of the
   **service** (an OpenWrt upgrade, a crash, `opkg` reinstalling it) therefore
   restores Authenticated clients the module has never seen — and because
   nodogsplash is not restarted with the module, the module's own startup pass may
   not even run in the same event.
3. **A box whose merchant came up degraded.** `MerchantDegraded.StartDataUsageMonitoring`
   is a no-op, so `ReconcileNdsAuthorisationsOnStartup` does not run at all until a
   mint becomes reachable and the upgrade constructs a full merchant
   (`startup_reconciliation.go` states this). Until then, every client NoDogSplash
   authorised before the restart keeps an open, unmetered gate — and if no mint
   ever becomes reachable, that state persists for the whole session timeout.

### Decision

1. **A client NoDogSplash reports as `Authenticated` that this module has no
   record of has its gate CLOSED — fail closed.** This is the same decision the
   startup pass took, for the same reason: the module cannot meter it (no
   session, no baseline, no allotment), it did not open it, and leaving it is
   unmetered internet. The alternatives are rejected explicitly:

   * **(b) Adopt it into a session with a zero or unknown allotment** — rejected.
     An adopted session needs an allotment from somewhere, and the only honest
     values are zero (the client is metered and immediately cut off by the meter,
     i.e. deauthorisation with extra steps) or unknown (free bytes with a
     `/balance` record that claims a customer paid when they did not). Neither
     closes the hole; the second one hides it.
   * **(c) Leave it and document the window** — rejected. That *is* the defect,
     and the window is a day long by configuration.

2. **The test for "this module has no record of it" is the module's own
   bookkeeping, and it does not race a purchase.** A client is known if it has a
   session in `customerSessions`, **or** a gate in `valve.TrackedGates()`. The
   ordering that makes this sound is the one the purchase path already
   guarantees: `grantSessionAccess` → `AddAllotment` inserts the session BEFORE
   `openGateForSession` opens the gate (and `restoreSession` puts the previous
   record back if the open fails), so a purchase in flight is never a client this
   module "does not know". A gate whose close is still UNCONFIRMED also stays in
   `TrackedGates()` until ndsctl confirms it — so the pass leaves such a gate to
   the close machinery rather than fighting it, which is what stops a pass that
   runs every cadence from re-driving a close the budget has already spent.

   The membership test folds case on every side. That is load-bearing: the
   session map is written by `NormalizeMACAddress` (lower-case) and ndsctl's own
   MAC lookup is a case-sensitive `strcmp` (`client_list_find_by_any`), so a
   payload reporting the module's OWN customer with an upper-case MAC still
   classifies as `Authorised()` (`EqualFold`) and would, under a bare `==`, be
   read as an inherited authorisation and have the paying customer's gate closed.
   The startup pass would make that mistake once per boot; the periodic pass makes
   it every cadence. The MAC is nevertheless handed back to ndsctl **exactly as
   NoDogSplash reported it**, because ndsctl's lookup is case-sensitive and
   normalising it could miss the very record being closed.

3. **Every state other than `Authenticated` is left alone — the FAS / pre-auth
   question the card asked to be checked rather than assumed.** Measured in
   nodogsplash 5.0.2: the state is the firewall mark
   (`src/fw_common.c`; `Preauthenticated` = `FW_MARK_PREAUTHENTICATED`, which is
   0), and a client reaches `Authenticated` only through
   `auth_client_auth`/`authenticate_client` — i.e. through the
   `nodogsplash_auth` token exchange or `ndsctl auth`. A client on its way through
   the captive portal (splash page, `/nodogsplash_auth/`, a FAS redirect) is
   `Preauthenticated`, cannot pass traffic past the portal (its packets are
   intercepted and redirected, `src/http_microhttpd.c:551-581`), and is a
   **legitimate** record: deauthorising it would tear up a customer's session in
   progress. It is therefore never a candidate, and no pre-auth record is ever
   closed. `Trusted` and `Blocked` are the operator's own configuration
   (`trustedmaclist` / `blockedmaclist`, `src/conf.c`) and are likewise not this
   module's authorisation to manage; a state this package does not recognise is
   treated as NOT authorisation (guessing "authorised" would cut off a customer).

4. **The periodic pass runs on the usage sweep's slower cadence (~30 s), never on
   the 2 s sweep.** It spends an ndsctl invocation on the monitor goroutine, so it
   shares the stale-binding reconciliation's cadence
   (`defaultNdsClientReconcileEvery = defaultStaleBindingReconcileEvery = 15
   sweeps`) rather than adding a second per-sweep call.

5. **The point of no return, and the crash behaviour.** The only irreversible
   action is the deauthorisation, and it is the same action the startup pass
   already takes: `valve.CloseGate`. Before it, the module writes nothing durable
   — the pass reads the client list, then closes. Consequences:

   * **A close that cannot be confirmed is not a close.** The gate stays TRACKED,
     the close is retried under the existing contract, and the pass reports the
     residue as an ERROR naming the client and the operator action. It never
     reports a refused close as done.
   * **A crash between the read and the close** leaves the client authorised and
     unmetered, which is exactly the state the pass started from; the next cadence
     re-reads and re-closes. There is no partial state to recover, because the
     pass owns no state across calls.
   * **A crash after a confirmed close** costs the client the remainder they paid
     for — the same deliberate, accepted cost the startup pass records: entitlement
     does not travel across a process death until it travels with a session ticket
     (the session-scoped address work, #572). The pass says so in the line that
     closes the gate rather than pretending the session survived.
   * **The residue must not be re-driven into a storm.** A client whose gate
     NoDogSplash re-authorises is closed again, but the reporting is throttled
     (once per distinct finding, then at most once a minute), and the close itself
     is bounded by `closeAttemptBudget` — so this pass cannot reproduce the
     `unconfirmed_closes=113` loop of the degradation-path incident. The pass also
     drives `CloseGate`, not `ReconcileGateClose`: `ReconcileGateClose` exists for
     a caller that brings FRESH EVIDENCE about the client (the stale-binding
     probe), and the client-list read alone is not that evidence.

6. **A failed read changes NOTHING, and is reported once per outage.** "The module
   could not read the list" is not evidence about any client, so no gate is closed
   on the strength of it. The operator gets one line naming the residue (whether a
   previous pass left one standing) and the check to run (`ndsctl json`), not a
   line per sweep for the length of the outage. A pass that finds no drift clears
   the residue, and says once that the window is closed — an operator has to be
   able to see the condition end, not only its start.

### Consequences

* The inverse drift is now bounded by the reconciliation cadence (~30 s) rather
  than by NoDogSplash's session timeout (configured here at 86400 s). An
  authorisation this module never made is closed within a pass.
* The startup pass becomes the FIRST pass of this reconciliation rather than the
  only one; the two share the decision, the test for "the module knows it", and
  the fail-closed direction. The startup pass keeps its distinct value — it runs
  before the payment API is open, so it cannot race a purchase at all.
* A client that legitimately holds a gate but has no session (`TrackedGates` only)
  is left alone by both passes. That is what keeps the degraded → full upgrade
  from closing a paying client's gate, and it is load-bearing in both: a change
  that makes `TrackedGates()` forget a gate with an unconfirmed close turns these
  passes into ones that can cut off a paying customer.
* Residual, stated: three conditions keep the door open, and none of them is
  hidden: (a) a wedged NoDogSplash control socket blocks the read, so the pass
  cannot close anything (the same wedge that blocks a paid grant; operator action
  is to restart `nodogsplash`); (b) the pass only closes clients NoDogSplash
  reports as `Authenticated`, so a client whose authorisation the module cannot
  read is not touched (fail-open by design — a failed probe is never evidence);
  (c) entitlement still does not travel across a restart, so a closed client must
  buy again.
* The tests that pin this: `src/merchant/nds_client_reconciliation_test.go` — the
  sweep closes an authorisation the module never made; a Preauthenticated record,
  the module's own live session and a tracked-gate-only client are NOT closed;
  an unreadable list changes nothing and names the check to run; a refused close
  is an ERROR and never "now closed"; the residue clearing is reported; a clean
  box produces no close and no report. The full suite is
  `go test -race -count=1 -tags testenv ./...` in `src/merchant` and `src/valve`.
