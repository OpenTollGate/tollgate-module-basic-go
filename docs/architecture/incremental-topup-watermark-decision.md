# Incremental top-up: the paid-through watermark, and adding to a live session

**Status:** Accepted (2026-11-02). Implementation ships in the same PR as this
record.

**Governing document:** [`session-ticket-decision.md`](session-ticket-decision.md) —
specifically its **Amendment R1–R6** (2026-09-24), which the document itself
declares governs where the body conflicts with it. This record does not amend
R1–R6 and does not reopen the held session-ticket rebind.

**Answers:** the module-side half of the PWA wallet ADR delta's **WS-3** (the
portal repo's `docs/architecture/pwa-client-wallet-adr-delta.md` §6.1), which was
gated on "operator sign-off + ADR question E/D". It answers only the part that
needs neither a client identity fork nor a durable per-address store; the parts
that do are listed in §7 and are deliberately **not** in this PR.

---

## 1. The question

`POST /` treats every purchase as a **fresh session**: `PurchaseSession` receives
the note, computes an allotment, and grants it. A customer who wants more access
than one purchase buys therefore has only one route — wait for the session to run
out, then buy again — and the portal's whole model is all-or-nothing.

Making "top up an existing session" real means answering three questions, and
they are separable:

1. **What is the quantity that says when the paid entitlement ends?** Today it is
   implicit and phrased in three places with two different expressions.
2. **What does adding to it do to the meter's anchor?** This is the live defect.
3. **Where does the top-up enter the system?** A new route, or the existing one?

## 2. Decision 1 — the paid-through watermark is the session's one paid-through quantity

A session's **paid-through watermark** is the value of its own meter at which the
customer has consumed everything paid for. It is stored as `CustomerSession.Allotment`,
and the meter is whatever measures consumption from the session's anchor
(`StartTime`):

| metric | meter | watermark reached when |
|---|---|---|
| `milliseconds` | `now − StartTime`, in ms | that elapsed value ≥ `Allotment` |
| `bytes` | `Consumed` (carried in) + this attachment's usage since its baseline | that total ≥ `Allotment` |

The rule is **one comparison in one place** — `sessionWithinWatermark` in
`src/merchant/merchant.go` — and **strictly below is inside, reached AT is
enforced**. Three sites consume it, and they may not drift:

- `enforceBytesSession` (the 2-second sweep) — the byte meter;
- `sessionHasExpired` (the lookup that retires a spent `milliseconds` record);
- `openGateForSession`, which arms the gate timer at the **paid-through instant**,
  `sessionPaidThroughUnix(session)` = `StartTime + Allotment/1000`.

Before this record the comparison appeared twice with two spellings
(`usage < session.Allotment` in the sweep, `elapsed >= session.Allotment` in the
lookup) and the instant was computed a third time in the timer. Nothing was wrong
with any single one of them; the defect was that "when does this end" had no
single definition, so a change to the semantics of *adding* paid time had no
single place to land. `TestWatermarkIsTheOneEnforcementRule` pins the boundary and
the agreement between the three sites, including the exact-equality case.

### 2.1 What the watermark does NOT change

The mechanism is the same one the ADR already relies on. There is no in-kernel
budget and no new syscall: `ndsctl` offers auth/deauth, and the module is the
meter. Bytes is *poll the counters, compare, deauth*; `milliseconds` is *arm a
timer at the horizon*. Both were already there.

## 3. Decision 2 — a top-up grows the watermark and never moves the anchor

`AddAllotment` is the top-up, and its contract is about the **anchor**, not the
amount:

- **A purchase of a LIVE session** (the watermark is not yet reached) MAY only
  grow `Allotment`. `StartTime` stays where it is.
- **A purchase after the watermark was reached** is not a top-up. The spent record
  is retired and the purchase starts a fresh clock — the behaviour
  `TestRepurchaseAfterExpiryDoesNotResurrectStaleAllotment` already pins, and the
  reason `sessionHasExpired` is consulted first.
- **A purchase whose live record is on a different metric** is also not a top-up.
  The two sides of the comparison would be in different units (bytes measured
  against a millisecond allotment), which is a watermark that can never be
  reached. The record is retired and the purchase starts a fresh clock on the
  configured metric.

### 3.1 The defect this fixes

`AddAllotment` did this:

```go
} else {
    // Add to existing session and reset start time to now
    session.Allotment += amount
    session.StartTime = time.Now().Unix()
}
```

On `milliseconds`, the meter is `now − StartTime`, so rewriting the anchor
**re-bases the meter to zero** and hands back every second the customer had
already spent. Two 600 s purchases on a session with 120 s of use left 1200 s
running from the second purchase — 1080 s of paid time over 1320 s sold. The
customer's second purchase therefore bought its allotment *plus* a refund of the
first one, and the gate timer armed at `newStartTime + total/1000`, i.e. the same
refunded horizon.

On `bytes`, the fresh anchor was harmless for the comparison but the top-up also
re-opens the gate (`openGateForSession` → `valve.OpenGate`), which records a new
metering baseline. The ledger's attachment-local half therefore restarts, and only
`attachmentUsage` (the highest reading observed so far) stands between the customer
and a free re-download of everything already consumed. Note this is the *same*
mechanism the session-ticket rebind exists to make safe, which is why the two
share the carried-`Consumed` design rather than inventing a second one.

The fix is two lines' worth of behaviour and one removed line, and it needs no new
state, route or store: `session.Allotment += amount` stays, `StartTime` is no
longer touched, and the timer already reads the watermark through
`sessionPaidThroughUnix`.

### 3.2 The rule stated as an invariant

> **A top-up of a live session may only increase paid-through time, never
> decrease the consumption already recorded.** Concretely: `StartTime` is
> monotonically non-decreasing across purchases and never changes while the
> session is live; `Allotment` is monotonically non-decreasing for the lifetime of
> a live session; and `Usage` (both metrics) never decreases across a top-up.

`TestLiveTopUpExtendsTheWatermarkWithoutRestartingTheClock` and
`TestLiveTopUpDoesNotResetTheByteMeter` assert exactly this on the two metrics,
including that the horizon moves by **one allotment**, not by the allotment plus
the time already spent.

## 4. Decision 3 — the top-up is `POST /`, not a new route

`POST /` already resolves the client identity from the socket (the
`clientIdentity` contract, `src/main.go`), already runs the full money path, and
already reaches `AddAllotment` through `grantSessionAccess`. A second
`/topup` endpoint would be a **second money path**, and it would have to
re-implement every guard the first one carries:

- the in-flight duplicate guard, keyed by the note's receive reference
  (`beginReceive`), which is what stops a double-click or two tabs from granting
  twice (#639);
- the **#502 receive-intent journal** — the attempt is durable *before* the money
  moves, and resubmission of a known reference is answered
  `payment-received-grant-pending` instead of a second spend;
- the owed-grant recording when the gate cannot open after `Receive` (#403);
- the pre-Receive validations (token decode, swap-fee pre-check, mint health).

Duplicating those is precisely the class of defect the repo's fund-safety rules
exist to prevent, and it buys nothing: once "buy while live" tops up instead of
resetting the clock, the existing route *is* the metered claim rail.

**What this means for the portal:** a top-up is an ordinary purchase at `POST /`,
and the client can tell from `/usage` or `/balance` that its entitlement grew
without its consumption being reset. No new client capability is required.

## 5. The restart-durability question — answered NO, for this PR

The session record is memory-only (`customerSessions`), so the watermark does not
survive a restart today. Making it durable means a **durable per-address record of
allotment**, which is exactly what **R2** forbids ("No durable (or long-lived
in-memory) table keyed by address may outlive the session"), and what the refund
analysis in the governing ADR already ruled out as the reason refunds are not the
default path.

This PR therefore **does not** persist the watermark, and a restart remains what
it was: the customer's paid time is lost and `startup_reconciliation` closes the
gate. That is unchanged behaviour, not a regression, and the owed-grant journal
still covers the *money* side (a paid-but-ungranted purchase survives a restart).
If a durable entitlement ledger is ever wanted, it is **its own PR** with its own
ADR — it needs either an amendment to R2 or a story for why an allotment record
keyed by address is not the join R2 forbids.

## 6. Relationship to the Spillman channel rail

Read this before proposing a drip protocol.

The ADR's own migration gate — *"as soon as the wallet runs on CDK and a wallet
that supports the channel protocol is available"* — is **still closed**. Until
both hold, the per-step invoice path stays the shipping one. This record does not
open that gate and does not replace channels: it makes the *existing* per-purchase
rail able to add to a live session, which is a strictly smaller change and is
useful with or without channels.

What it deliberately does **not** do is make one invoice per granule cheap. Every
top-up today is a full `POST /` → `Receive` → mint swap, and the request budget
around that (`maxActiveQuotesPerMAC = 3`, `maxActiveQuotesGlobal = 128`, per-IP
10 rpm, `quoteCreateQuotaMiddleware`) was sized against small frequent payments.
The point of incremental payment is *not* one invoice per granule — that is what
the channel rail is for, and it is tracked separately. A client should therefore
top up in **amounts it is willing to hold**, not in 1-sat granules.

## 7. Out of scope — named so they are not rediscovered

Each of these is a separate PR, and three of them are blocked on a decision that
this record does not make:

1. **Durable entitlement ledger keyed to a client.** Blocked on R2 (see §5).
2. **Client identity continuity for a PWA that cannot present a MAC.** The module
   keys everything on the socket-resolved MAC and explicitly ignores
   client-supplied identifiers. A metered PWA needs either a signed,
   mint-independent client key accepted as a session key (a `clientIdentity`
   security-surface change) or a ticket per top-up (which revives the *held*
   rebind design and contradicts R3). This is **D5**, and it is still OPEN.
3. **Cross-router roaming.** Out of scope entirely: nothing in this module
   federates a customer grant to a peer router, and a rebind only reads the same
   router's session map.
4. **Per-note request budget / billing rhythm.** See §6; belongs with the channel
   rail.
5. **`milliseconds` coverage by the stale-binding janitor.** The janitor excludes
   `milliseconds` sessions on purpose (their gate is bounded by its own timer), so
   a time session whose client leaves is not reconciled against `ndsctl`. That is
   unchanged here and is a separate, small change.
6. **Spillman channels.** Tracked separately; the ADR's migration gate governs.

## 8. Consequences

**Positive.**

- "Top up an existing session" is real, on both metrics, with no new route, no new
  state and no new failure mode on the money path.
- "When does this session end" is now one definition, so the sweep, the lookup and
  the gate timer cannot disagree by a unit — a class of bug that closes a gate
  while it still has paid time, or keeps one open a sweep too long.
- A paid customer is no longer refunded the time (or the data) they already spent
  by buying again.

**Costs and accepted risks.**

- The watermark is still memory-only (§5).
- A top-up re-opens the gate, which on `bytes` re-baselines the attachment meter.
  The carried `Consumed` plus `attachmentUsage` is what keeps the ledger honest,
  and the same mechanism is what the rebind invariant already relies on; the test
  pins it rather than assuming it.
- The top-up is idempotent per *note* (the existing guards), not per *intent*: a
  customer who pays twice buys twice, which is the correct economic answer and is
  what the "no refunds" position implies.

## 9. Test obligations (in this PR)

| Invariant | Test |
|---|---|
| The watermark is one rule, and the three sites agree — including equality | `TestWatermarkIsTheOneEnforcementRule` |
| A live top-up preserves the anchor and grows paid time by exactly one allotment | `TestLiveTopUpExtendsTheWatermarkWithoutRestartingTheClock` |
| The byte meter never goes backwards across a top-up, and the sweep gauges the extended watermark | `TestLiveTopUpDoesNotResetTheByteMeter` |
| A top-up does not leave a second tracked gate for the same MAC | `TestTopUpLeavesExactlyOneTrackedGate` |
| A purchase after the watermark is reached still starts a fresh clock (unchanged) | `TestRepurchaseAfterExpiryDoesNotResurrectStaleAllotment` (existing) |

**Hardware:** the module rail changes the gate, so the real `ndsctl` flip must be
exercised on the serial router (`tollgate-A04E`) — see the PR body for the
evidence, or its absence.
