# Session Tickets and MAC Rotation — Architecture Decision

> **Status: Proposed (2026-09-24) — carry-over held.** The session-ticket
> carry-over proposed in this record is **on hold pending maintainer
> discussion**: see "Amendment - the address is session-scoped" at the end of
> this document. Nothing in this document is in force yet: the module, portal
> and bundle changes below are the PR sequence that would make it true.
> Acceptance is a maintainer action — the drafting account may not accept its
> own proposal, and no PR here claims it has been accepted.

## Context

A client's MAC address is the only thing this module knows about a customer,
and it is used as three different things at once:

1. **the session key** — `m.customerSessions[macAddress]`
   (`src/merchant/merchant.go:196`), the record that says how much time or
   data was bought (`CustomerSession`, `src/merchant/merchant.go:25`);
2. **the meter's key** — the byte meter is the difference between NoDogSplash's
   per-client counters and a baseline captured per MAC
   (`SetDataBaseline`, `src/valve/customer_data_tracker.go:45`;
   `GetDataUsageSinceBaseline`, `src/valve/customer_data_tracker.go:92`), and
   the enforcement comparison is `usage < session.Allotment`
   (`src/merchant/merchant.go:502`);
3. **the delivery address** — the gate is opened and closed for a MAC
   (`src/valve/valve.go:548`), and NoDogSplash authenticates that MAC.

The socket-identity work correctly removed the client's ability to *name* an
identity: five routes now resolve the caller from the connection's source IP
through the DHCP lease file and the ARP table (`clientMACFromSocket`,
`src/main.go:412`; call sites at `src/main.go:502` `/whoami`, `:567` the money
route `POST /`, `:763` `/session-state`, `:915` `/ln-invoice`, `:972` Lightning
quote status). A client-supplied `mac` parameter now selects nothing.

What that work did **not** change is that the resolved MAC *is* the
authorization. Three consequences follow.

**A MAC rotation costs the customer their paid session.** A device that rotates
its private address — which iOS and Android do by default, and which every
re-association can do — arrives as a different customer. The session record is
still filed under the address it left (`src/merchant/merchant.go:196`), so
`/usage` and `/session-state` answer for the old address while the new one looks
like a first-time visitor, and the portal offers a purchase instead of the
access already paid for. A re-purchase under the new MAC lands in
`AddAllotment` (`src/merchant/merchant.go:1588`) and creates a **second**
session record rather than moving the first one (`src/merchant/merchant.go:1599`).

**The obvious rebuild of that record is a metering hole, not a fix.** The
session record carries no meter of its own — the meter is external, per-MAC and
relative (`src/valve/customer_data_tracker.go:92`) — so "bind the session to the
new MAC" is exactly the operation that silently makes usage disappear. Three
specific ways it does so, all of which a naive implementation walks into:

- closing the old attachment and opening the new one calls `OpenGate`
  (`src/valve/valve.go:548`), which records a **fresh baseline** for the new MAC
  (`src/valve/valve.go:588`). Consumption measured against a fresh baseline
  starts at zero: N rotations = N free allotments;
- re-granting the session by calling `AddAllotment` **resets `StartTime` to
  now** for any existing record (`src/merchant/merchant.go:1611`), which
  silently extends a paid time session: every rotation hands back the time
  already spent;
- if the rebind is accepted while the old attachment is still authenticated,
  one session is being delivered to two live clients — the state a copied
  ticket produces.

**A ticket that carries an entitlement is a portable credential.** The point of
this decision is to stop the address from being the identity without inventing
something worse: a bearer token that contains the grant, that a reader can
redeem from anywhere, and that must then be kept in agreement with the session
record it duplicates.

Both halves are live in the field today. The reconciliation of a binding whose
client has left the network is already in review as a fix to the rotation
problem's *symptom* (a stale binding that stays authorized); this decision is
the structural change it does not make.

## Decision

> **Held (2026-09-24):** the carry-over decision below is on hold pending
> maintainer discussion. The amendment at the end of this record (R1-R3)
> governs; the rebind flow in this section is the design it supersedes.

**Sessions are addressed by a server-signed, memory-only ticket that carries a
session HANDLE and nothing else. The MAC stops being the identity and becomes
the socket-resolved delivery address.**

### The ticket

- On request, the module issues a ticket bound to the session the requesting
  (socket-resolved) MAC currently owns. Nothing is issued for a client with no
  session — the ticket is a handle onto an existing record, never a grant.
- The envelope is `v1.<base64url(payload)>.<base64url(hmac)>`, where `hmac` is
  HMAC-SHA256 over `v1.<payload>` under a **per-process 256-bit key drawn from
  `crypto/rand` at startup and never written anywhere**. The payload is
  `{handle, issued_at, expires_at}` — no metric, no allotment, no MAC.
- The handle is random and opaque; the mapping `handle → attachment` lives in
  process memory only.
- Restart invalidates every ticket by construction (new key, empty store): the
  signature no longer verifies and the handle is unknown. That is the intended
  failure mode, and the portal's resume path is what covers it — the customer's
  *access* is unaffected, because the gate, the session and the meter are not
  in the ticket.

### The attachment

An **attachment** is the binding of one session handle to the one MAC its access
is delivered to:

```go
type sessionAttachment struct {
    Handle     string // opaque, random, appears in the ticket
    MacAddress string // socket-resolved; the delivery address
    IssuedAt   int64
    ExpiresAt  int64
}
```

Two tiers, only the first of which this release implements:

- **Tier 1 (in scope).** The ticket is bound to the socket-resolved MAC. A
  neighbour who copies the ticket and presents it from their own address is
  refused; presenting it *is not* proof of anything, because presenting it from
  the legitimate address is indistinguishable from the legitimate device.
- **Tier 2 (decided, not implemented).** Proof of possession: the ticket
  carries a per-session HMAC key the client must use to sign a fresh nonce the
  module returns, single-use. This is the only variant in which a same-LAN
  copier gets nothing — and the only one that survives the honest gap in Tier 1
  recorded under Notes.

### The rebind

A rotation is `POST /session/rebind` with the ticket, from the new address:

1. the ticket's signature and expiry are verified;
2. the handle resolves to an attachment; if the requesting MAC already *is* the
   attached address the call is a no-op that reports the current session;
3. the **old attachment must no longer be authenticated**
   (`valve.CheckClientState`, `src/valve/valve.go:728` — read-only, it changes
   nothing). If it still is, the rebind is refused: that refusal is the only
   thing separating a rotation from a second delivery of one session;
4. the meter is carried over (Invariant 1) and `StartTime` is preserved
   (Invariant 2);
5. the session record moves to the new MAC and its baseline is captured for the
   new attachment only.

## Invariants

These are the parts a plausible implementation omits. Each one is asserted by a
test, not by prose.

> **Held (2026-09-24):** Invariants 1 and 2 exist only to make a rebind safe,
> and the amendment at the end of this record holds the rebind. A rebind that
> is never issued has no meter to carry and no `StartTime` to preserve.

**Invariant 1 — the byte meter carries.** A session record carries `Consumed`,
the byte total of the attachments it has already left behind. It is
**monotonic** and **never** re-based on a rebind: a fresh attachment's own
counters measure only that attachment, and

    effective usage = Consumed + (counters of the current attachment since its
                               own baseline)

is what both `/usage` and the enforcement comparison
(`src/merchant/merchant.go:502`) must use. Because the counters of a left
attachment may already be gone from NoDogSplash by the time the rebind happens,
the session also remembers the **highest** usage observed for the current
attachment, and a rebind carries that rather than zero when the live read fails.
The bound on the loss is one usage-monitor sweep (2 s), and it is in the
customer's favour — never the whole allotment.

**Invariant 2 — paid time is preserved.** A rebind must not extend a paid
session: `StartTime` is copied, never recomputed, and a rebind must never be
implemented as an `AddAllotment` — the `session.StartTime = time.Now().Unix()`
on the existing-record path (`src/merchant/merchant.go:1611`) is exactly the
line that would turn a rotation into free time.

**Invariant 3 — no second live attachment.** A rebind is refused while the old
attachment is still authenticated, with a distinct error the portal can show.
A probe that cannot answer (NoDogSplash unreachable) does not refuse: the
probe is advisory, and failing it closed would strand a legitimate rotation on
a flaky `ndsctl`. That gap is deliberate and is stated in the ADR rather than
hidden behind a fail-closed rule the module cannot honour.

**Acceptance test (headline).** After a rotation, the remaining allotment is

    remaining == allotment - consumed        (not `allotment`)

i.e. a bytes session that consumed 40 MB of a 100 MB allotment reports 40/100
against the new attachment and is closed at 100 MB total — across any number of
rotations. The same rotation on a milliseconds session reports the elapsed time
it had already run, not zero.

## Consequences

### Positive

- A MAC rotation becomes harmless: the customer keeps the session they paid for,
  under a new delivery address, with no operator involvement.
- The MAC is demoted from credential to address. A copy of the ticket is inert
  while the legitimate attachment is live, and the module no longer treats
  "I claim this address" as proof of anything.
- The money path is unchanged: a purchase still resolves the buyer from the
  socket (`src/main.go:567`), so this change cannot misgrant a payment.
- Metering becomes a property of the session rather than of the MAC, which is
  what it always was conceptually — the per-MAC baseline is a measurement
  detail, not the ledger.

### Costs

- **New API surface.** Two routes, and a portal that must adopt the resume flow
  to benefit. Until it does, behaviour is the status quo, not worse.
- **Memory-only by design.** Every restart invalidates all tickets. A client
  that was mid-rotation falls back to its address (and to the customer-visible
  session state the module already reports), and the portal's resume path
  re-issues. Persisting tickets was considered and rejected (Notes).
- **In-memory handle store.** Bounded like the session history: TTL sweep plus
  a hard entry cap, so a busy router cannot grow it without bound.
- **Tier 1 leaves a real gap** (Notes), and closing it is a further API change
  for the client. That is the price of shipping the structural fix without
  asking every portal implementation for a proof-of-possession lane at once.
- Rotation still requires the old attachment to be *observed* gone. Overlapping
  MACs (the device changed address while still associated) are refused rather
  than silently double-granted, which means a customer who rotates while
  authenticated may need one retry once the old attachment is reaped.

## Rollout / PR sequence

### PR 1 — this document (docs only)

Lands the decision, the three invariants and the acceptance test on `main`.

### PR 2 — module: issue / verify / rebind with meter carry-over

`src/merchant/session_ticket.go` (ticket signer, attachment store,
`IssueSessionTicket`, `VerifySessionTicket`, `RebindSession`), the `Consumed`
carry-over in `CustomerSession` and its use in `GetUsage` and
`enforceBytesSession`, and the two routes (`POST /session/ticket`,
`POST /session/rebind`) resolving identity from the socket like every other
MAC route. Tests: signature verification, a tampered ticket, an unknown key
(restart), the headline acceptance test, Invariant 2 on a milliseconds session,
Invariant 3 refusals, and a route-level round trip.

### PR 3 — portal: resume flow (separate repo)

The portal asks for a ticket after a purchase, keeps it for the page lifetime,
sends it on the session-state/usage reads, and on a refused or unknown ticket
calls `/session/rebind` before falling back to asking the customer to pay
again. The portal must never treat `403`/unknown-ticket as "no session": the
session may be perfectly alive under an address the page has not seen.

### PR 4 — bundle / feed repin

Repin the captive-portal bundle consumed by packaging
(`packaging/build-inputs.json` `portal.commit`) to the portal release that
carries the resume flow, so a shipped router has both halves. Until this lands,
PR 2 is additive and unused in the field — which is the reason the release
sequence ends here rather than at PR 2.

## Notes

### Rejected alternatives

- **Status quo (MAC as identity).** Cheapest, and what every other constraint
  pushes toward; rejected because it makes a privacy feature (private address
  rotation) punish the customer, and because a per-MAC ledger cannot express
  "this customer moved" at all.
- **Entitlement in the ticket** (allotment, metric, expiry carried as claims).
  Rejected: it duplicates the session record in a place the module cannot
  amend, makes the ticket worth stealing for its content, and turns every
  allotment change (`AddAllotment` renewals) into a ticket-invalidation problem.
- **Persistent tickets** (`identities.json` or a flash store). Rejected for this
  release: it introduces a second crash-consistent store on a router that loses
  power, and a ticket that survives a restart is a ticket that survives a power
  cycle into another network. The module's persistent-identity discipline
  (AGENTS.md) is reserved for the wallet.
- **Signing with the merchant key** (`identities.json`). Rejected: the merchant
  key is the payment identity. A ticket signed by it becomes verifiable by third
  parties (who then hold a stable customer handle) and couples ticket validity
  to an operator key rotation that must not touch access.
- **Fixing rotation inside `AddAllotment`.** Rejected: `AddAllotment` is the
  renewal path and must keep resetting `StartTime` for a renewal. Teaching it to
  sometimes not reset would put the rotation semantics — an identity decision —
  inside the money path, where every future reader has to re-derive them.

### Honest gap in Tier 1

A neighbour on the same LAN can read the ticket (it crosses the network in
plaintext over HTTP, on a captive portal) and, once the legitimate device has
left the network, present it from their own address: the rebind succeeds,
because the old attachment is not authenticated and the ticket is valid. Tier 1
therefore protects against a copy made **while the legitimate device is online**
and against casual replay, not against a patient attacker on the same segment.
Tier 2 is the tier that closes this; it is decided here and implemented later,
and nothing in PR 2 should be read as claiming otherwise.

### What this decision does not change

- The purchase flow, the wallet, the money path and the socket-derived buyer
  identity.
- `/usage` and `/session-state` semantics for a client that never rotates.
- The stale-binding reconciliation for clients that leave the network (already
  in review): a rotation is the same event seen from the new address, and both
  paths must agree that a session is delivered to exactly one address at a time.

## Amendment - the address is session-scoped (2026-09-24)

**Update 2026-09-24 (operator decision).** This amendment governs the record
above. The session-ticket carry-over it proposes — the ticket, the rebind flow,
and Invariants 1-2 which exist only to make a rebind safe — is **held, not
withdrawn**; R4-R6 extend the same rules to the Spillman channels the operator
intends as the payment rail. The governing principle is rotation *between*
purchases, and the open question is whether any opt-in escape hatch is
warranted. Where the body of this record conflicts with R1-R6, R1-R6 govern.

**R1 - the address is session-scoped.** Entitlement belongs to one address for
the lifetime of the purchase that paid for it. Nothing carries across
addresses: no meter ledger, no `StartTime`, no session record, no credit.

**R2 - no join, in state or in logs.** The router must never create or keep a
record that names two customer addresses together. No log line or log field may
pair an old and a new address: the carry-over design's
`Session rebind: handle %s moved from %s to %s` info line, and its two WARNING
lines that name the previous attachment, are exactly what R2 forbids. No
durable (or long-lived in-memory) table keyed by address may outlive the
session.

**R3 - no automatic rotation handling.** The portal must never auto-issue a
session handle or auto-rebind a session without an explicit customer action and
plain disclosure. If an escape hatch is ever shipped, it is opt-in per
purchase, short-lived (order of a minute, single use) and MAC-blind in its
logging.

The reasoning is short. MAC rotation between purchases is the product's privacy
primitive; the router cannot command a device to rotate, because that is the
customer's device setting, and a spoofable MAC makes any "one purchase per
address" enforcement both unenforceable and a tax on honest users. The design
rule is therefore *encourage rotation, never require it, and never link two
addresses*.

What makes the model work instead of carry-over:

- small, granular purchases bound the loss from a rotation mid-purchase;
- the customer's own remaining counter (`GET /usage`, shown in the portal) is
  their detection surface;
- client isolation of guest clients removes the cheap discovery path for
  address takeover (tracked as follow-up work).

### Refunds considered and not adopted (default path)

The operator would consider ecash refunds for a session ended early. The
product, though, wants to encourage small frequent granular payments rather
than enable over-payment: a refund option makes "buy more than you need and
reclaim the rest" free, and rewards *not* rotating, which is the opposite of
the privacy property. Mechanically, a refund is an outbound payment — a swap or
melt at the mint, a Lightning invoice and its routing, operator float — whose
cost can exceed the granule. Decisively, paying a customer back requires a
**durable per-address record of owed value**, which is the exact join R2
forbids, and which would also have to survive a restart to be honoured. Refunds
are therefore not part of the default path.

The trade-offs, the constraints that any future refund must satisfy, and the
open questions for maintainers are set out in a discussion note:
https://njump.me/55a5ca8b0d6ed31b9566451fe513c2ed0a741eac4143b3223d0844b6c1b28fb0 .
That note is the place to argue the decision; this ADR records only the current
position.

The cash-native early exit the operator wants is the channel close described in
the following subsection, which returns the customer's change at the mint
without the router ever holding a record of what it owes.

### Spillman channels as the intended payment rail (2026-09-24)

**Operator decision.** The module migrates to Spillman-style Cashu payment
channels — the reference implementation is SatsAndSports/cashu_spilman_channels
(`cdk-spilman`), with SatsAndSports/MONAD as the production-shaped example —
**as soon as the wallet runs on CDK and a wallet that supports the channel
protocol is available.** That is the migration gate; until both hold, the
per-step invoice path above stays the shipping one.

Why this answers the early-exit question better than a refund does:

- **Closing the channel is the client's own way to end a session early.** Either
  party can close; at close the server submits the latest balance update to the
  mint and receives its share while the client's change returns to the client's
  own outputs. The unspent remainder therefore never passes through the router,
  and no durable per-address record of owed value is needed — which is precisely
  what ruled the refund out in the section above.
- **It is the payment shape this product wants.** One funding transaction
  carries unlimited channel updates, so small, frequent, granular payments stop
  costing an invoice and a mint round-trip each. Granularity was never the
  problem; the per-payment overhead was.
- **It is the better privacy fit.** P2BK (pay-to-blinded-key) prevents mint
  correlation, and the channel is keyed by blinded channel/payment material, not
  by the customer's address.

Channels introduce durable state the router has never had — a close/claim
journal and an expiry watcher — so the amendment's rules extend to them:

- **R4 — one channel, one purchase, one address.** A channel is bound to a
  single session for its lifetime; the binding lives in RAM and is dropped when
  the session ends. A channel identifier is never accepted across a session or
  address boundary.
- **R5 — close before rotating.** Client software must close the previous channel
  (or leave it to expire) before rotating its address, and must never present an
  existing channel after a rotation. The router treats a channel arriving from a
  new address as unrelated and does not resume it: R3 applied to channels.
- **R6 — the journal is MAC-blind.** The durable close/claim journal and any
  expiry watcher are keyed by blinded channel/payment identifiers only and hold
  no address — no MAC, no IP — and no row may pair a channel with an address:
  R2 applied to channels. This is a rule, not a preference, because the journal
  is the one artifact that outlives the session.
- **The gate follows the balance.** The router passes traffic only up to the
  latest signed balance (gate = signed balance ≥ measured usage, with an
  explicit headroom policy for the sampling gap between channel updates and the
  NDS counters), and it never blindly retries a stale or conflicting payment —
  MONAD's `PAYMENT_CONFLICT` behaviour is the model.

Risks accepted with the gate, recorded so they are not rediscovered later: the
reference implementation is **Early Alpha with breaking API changes**; the
receiver must claim before expiry or the sender refunds the whole channel, so a
crash or a forgotten close is lost revenue; the CDK sidecar's flash/RAM fitting
on the target router is unverified; and the operator's balance sits at a
third-party mint, with the recovery paths (`UnknownSpent`, NUT-09 restore)
belonging to the wallet layer, not to this module.
