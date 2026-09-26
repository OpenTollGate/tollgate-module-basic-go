# An ndsctl invocation the module ended is not an ndsctl failure

## Status: Decided (2026-09-26)

Applies to `src/valve` (the `ndsctl` seam, the close contract and the shutdown
path), the usage monitor's force-close in `src/merchant`, and to `main` (the
process shutdown path). It amends, and does not replace,
`docs/architecture/zombie-session-close-reconciliation-decision.md`, which
decides what the module does with a client NoDogSplash has forgotten.

## Background: the measured defect

On the bench MT3000 (pre17, module pin `2796d96c`) a window of the router log
carried, among the close-retry storm:

```
10:20:44 ERRO Error executing ndsctl json        error="signal: killed" mac_address="02:11:22:33:44:77" module=valve
10:20:49 ERRO Error deauthorizing MAC address    error="signal: killed" mac_address="8c:16:45:0d:6f:c5" module=valve
10:20:54 ERRO Error executing ndsctl json        error="signal: killed" mac_address="a8:a0:92:a5:39:7a" module=valve
10:21:14 ERRO Error deauthorizing MAC address    error="signal: killed" mac_address="02:11:22:33:44:55" module=valve
```

97 such lines accumulated in that incident, and they were read as "a shutdown
turns every in-flight `ndsctl` into a logged failure". They were not. What the
log actually shows is the shape of the defect:

* **The message is Go's, not the module's.** `exec` reports every child that died
  on a signal as `signal: killed`. The module runs `ndsctl` under a deadline it
  owns (`ndsctlTimeout`, 5 s, guarding against the NoDogSplash deadlock of issue
  #387), and `exec.CommandContext` kills the child when that deadline fires. So
  the same four words describe "the module's own deadline killed a call that
  never answered", "procd killed the child with the process", "the OOM killer did
  it" and "someone did it by hand" — and the module never asked which.
* **The spacing is the deadline.** The four lines are 5 s apart on a box that was
  alive and logging for 30 s across them, so each one is the module's own timeout
  firing on an invocation that never answered. Congruent with the same incident's
  `nodogsplash: Socket is not ready for communication : Bad file descriptor`
  every ~5 s.
* **There was no shutdown path at all.** `valve.Stop()` existed and closed a
  channel the delayed-auth goroutines watch, but nothing called it: no signal
  handler, no drain. A `tollgate-wrt restart` therefore killed the process with
  every invocation that was in flight, and those kills landed in the log as
  ndsctl failures that nothing had claimed. `Stop()` also panicked if it was ever
  called twice (a second signal, or a stop after a stop, on a closed channel).
* **The attribution defect outlived its cause.** The storm's *source* was the
  unbounded close retry, fixed by the zombie-session decision and its
  `closeAttemptBudget`. What remained is what this record decides: the module
  reported the outcome of an invocation it ended itself as an ndsctl failure,
  with no way for an operator or a state machine to tell the difference.

The consequence is not cosmetic. A socket that stops answering is the state in
which a **paid** purchase cannot be applied (`state=PAID`, wallet +1 sat,
`access_granted` never true), and the log attributed that state to "ndsctl
failed" — pointing an operator at the wrong thing, and letting every reader of
the log draw their own conclusion about whether a client holds unmetered access.

## Decision

1. **Every production invocation is classified by the side that ended it.** A
   child that exits on its own is ndsctl answering — successfully or with a
   refusal — and keeps the existing reporting. A child the module ended returns
   an `ndsctlInterruption` whose message names the module and the reason, and
   which unwraps to `ErrNdsctlTimeout` (the deadline fired; NoDogSplash never
   answered) or `ErrNdsctlStopped` (the module is stopping). The call sites
   (`deauthorizeMAC`, `authorizeMAC`, `GetClientStats`, `CheckClientState`) no
   longer re-escalate such an invocation as an ndsctl failure.

2. **A timeout is an ERROR, throttled; a shutdown is INFO, never escalated.** A
   socket that does not answer is a real operator problem and stays an ERROR —
   once per `ndsctlTimeoutReportInterval`, with the running count, the client and
   `ndsctl_outcome=timeout` on every repeat at DEBUG, and an INFO line when the
   socket answers again. The module drives ndsctl at the sweep cadence (every 2 s
   per metered session), so an unthrottled escalation *is* a storm; throttling
   changes what is logged, never what is attempted.

3. **An invocation the module ends leaves the state machine exactly where it
   was.** "ndsctl never answered" is not evidence about the client, so a close
   interrupted by a timeout stays UNCONFIRMED, the gate stays tracked, and the
   close keeps being retried inside `closeAttemptBudget` — the fail-safe
   direction of the close contract is unchanged.

4. **`authorizeMAC` stops retrying an invocation that never answered.** Its retry
   loop exists for NoDogSplash answering that it does not have the client yet
   (the two-router autopay race); retrying a silent socket only spends the
   caller's deadline. The purchase path a wedged socket blocks is reported
   loudly by the paid-grant path, not by five identical timeouts.

5. **The module has a shutdown path, and it is idempotent and bounded.** `Stop()`
   sets the stopping flag (a new invocation is refused rather than started and
   abandoned), closes the delayed-auth channel, and DRAINS the invocations in
   flight: it waits `ndsctlStopDrain` for them to finish (ndsctl answers in
   230–350 ms, so a healthy one does), then cancels their parent context, which
   kills what is left — deliberately, and attributed to the shutdown rather than
   to ndsctl. The wait is bounded twice, because a shutdown may never hang: procd
   SIGKILLs a service that does not exit, and a module that refuses to exit is
   indistinguishable from a hung one. `main()` installs the SIGTERM/SIGINT
   handler that calls it. A close interrupted by the shutdown is reported at
   INFO and does not spend the close budget; the gate stays tracked.

6. **"The session cannot be metered" has one answer, and it is written once.**
   A `bytes` session whose usage the module cannot read is given
   `usageMonitorGraceSweeps` (30 sweeps ≈ 60 s) of grace — long enough for a
   NoDogSplash restart or a transient failure to clear — and then its gate is
   **closed**, because an unmeasurable session left open is unmetered internet.
   The close follows the ordinary contract: the session is retired only when the
   close is confirmed, and the gate stays tracked otherwise, so the address
   cannot be handed to a later holder of the MAC and the record that says
   "close this" is never dropped. What changes is the reporting cadence: the
   escalation is written **once**, a change of state (the close being abandoned
   by the close budget, or coming back) is reported at once because a transition
   is not a repeat, and the same state repeating is reported at most once per
   `unmeterableSessionReportInterval` (a minute), at WARNING. Previously the two
   ERROR lines repeated every 2 s for as long as NoDogSplash refused, which is how
   the escalation itself became unreadable.

7. **What happens after the close budget is spent is unchanged, and it is now
   reported in the same breath.** The valve stops driving ndsctl about that gate
   (`Gate close UNRESOLVED`, once), keeps it tracked and escalates the
   abandonment, and the reconciliation re-attempts the close under fresh evidence
   about the client (`ReconcileGateClose`). The operator action is named on the
   line. This record adds only the requirement that the merchant's own line about
   that state is bounded and derives its claim from the error
   (`closeRetryStateClause`), so it cannot describe an abandoned close as one that
   is still being retried.

## Out of scope, and stated so

The **inverse** drift — a client NoDogSplash still authorises and the module has
never heard of, which a module restart creates for every client NDS was holding
— is *not* addressed here and was not claimed to be. The module's gate, session
and baseline bookkeeping is process-local, so a startup pass over it would
iterate an empty set; the reconciliation that owns this direction needs the NDS
client list (`ndsctl json` with no argument), which the module did not read at
the time, plus a decision about what to do with a record whose allotment is
unknown. That is recorded as the remaining hole in
`docs/architecture/zombie-session-close-reconciliation-decision.md` §5, and it has
since been decided and implemented as its own work item —
`docs/architecture/startup-nds-reconciliation-decision.md` (the client list is
read once at startup and a client NoDogSplash authorises that the module holds no
session for has its gate CLOSED). This record is unchanged by that: it makes the
module honest about what it did with its own invocations, and the startup read
uses the status quo of this record — it inherits the classification of an
invocation the module ended, and it never escalates one as an ndsctl failure.

## Alternatives considered

* **Report the kill by inspecting the child's `ProcessState` only** (i.e. print
  `signal: killed` plus which signal). Rejected: it says a signal arrived, not who
  sent it, which is the ambiguity that mattered.
* **Log every timeout at ERROR, unthrottled.** Rejected: measured, that is 30
  ERROR lines a minute per unmeterable session, and the escalation stops being
  readable. The throttle is on the *repeat*, not on the first report, and a state
  change is always reported at once.
* **Treat a timeout as a failed close and drop the tracking.** Rejected outright:
  it is the fail-open direction (C1-2). A timeout means the close is UNVERIFIED,
  never that it happened.
* **Have `Stop()` close the stop channel without draining** (the previous
  behaviour, made idempotent). Rejected: the point of a shutdown path is that the
  invocation that was about to answer is allowed to answer; a restart that kills
  it produces exactly the indistinguishable log this record removes.
* **Restart nodogsplash from the module when the socket stops answering.**
  Rejected here: a service the module restarts is indistinguishable in the log
  from an operator (or an unrelated worker) doing it, and wedged-socket recovery
  belongs to the operator's action, which the escalation names.

## Consequences

* `NdsctlTimeouts()` and `NdsctlStoppedInvocations()` expose the same facts to
  code as the log does to the operator; the module still has no health endpoint,
  so the log remains the operator surface.
* The log can no longer say "Error deauthorizing MAC address error=signal:
  killed". An operator reading a timeout now sees which command, which client,
  that NoDogSplash never answered, that the outcome is UNVERIFIED, and what to
  check.
* A restart is quiet unless something is actually wrong: every in-flight
  invocation either finishes inside the drain budget or is reported as ended by
  the shutdown.
* The tests that pin this are `src/valve/ndsctl_invocation_contract_test.go`
  (a fake `ndsctl` on PATH: a hung child, a slow child, a child that outlives the
  drain budget) and `src/merchant/unmeterable_session_cadence_test.go` (the
  escalation writes once, a state change is reported, and enforcement still
  converges when ndsctl recovers).
* The residual risk this does not remove: while NoDogSplash does not answer, a
  purchase cannot be applied and a close cannot be confirmed. The module now says
  so precisely; recovering the socket is the operator's action.
