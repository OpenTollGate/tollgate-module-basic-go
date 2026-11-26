<!-- markdownlint-disable MD013 -->

# LNURLcash as an opt-in payment method — Architecture Decision

**Status:** Proposed (2026-11-26). Implemented behind a config flag that
defaults to disabled; the flag's existence is the proposal, not an enablement.

## Context

TollGate has one payment path today: the customer posts a Cashu token to
`POST /`, `HandleRootPost` reads the body as the bearer instrument
(`extractCashuToken` returns it verbatim unless it is a kind-21000 event),
resolves the client identity **from the socket** (`clientIdentity`, with a
`mac` parameter reported back as ignored), and calls
`Merchant.PurchaseSession(token, mac)`.

Two neighbouring facts matter here:

1. The gate runs **zero LNURL server endpoints**. The only LNURL code in the
   tree is client-side `payRequest` logic in `src/lightning/lightning.go`, used
   for profit-share payouts, and it already carries the SSRF defences we would
   need to reuse (`ssrfSafeClient`, `validateCallbackURL`).
2. `docs/architecture/payment-backend-disclosure-decision.md` (Decided
   2026-11-16) already separates **bearer ecash** from **account-based
   Lightning** and requires the operator to disclose which model is in play.
   An LNURLcash note is a third case that record does not cover: the customer
   presents a claim, and a **third party** decides whether it is still good.

### What LNURLcash is, for the purpose of this decision

A note is a withdraw link carrying a secret (`k1`). Handing the note to the gate
is not payment; **redemption is payment**. The gate performs the LUD-03
withdraw sequence against the issuer: an informational `withdrawRequest` GET,
then the callback GET carrying `k1`. The issuer answers with a BOLT11 invoice
that it has already paid, or with an error that means the note is spent,
expired or unknown.

That single sentence is the whole integration argument. It also states the
thing this ADR refuses to blur: **the gate is not accepting a timestamped
certificate as value.** It is asking an issuer, at payment time, whether value
exists and is now being transferred to us.

### Why this is acceptable despite issuer custody

A Cashu mint is also a custodian of unswapped proofs, and TollGate already
accepts that. The relevant question is not "is this trustless" but "is the
failure mode bounded and visible". For an LNURLcash note:

* the issuer can refuse to pay, or be offline — the customer's note then buys
  nothing, and the gate must say so before the customer is granted access;
* the issuer can pay a **different** invoice than the one we asked for — that
  is a refusal to complete our redemption, and it is detectable because the
  preimage must match the invoice we supplied;
* the issuer learns that this gate redeemed this note — a linkability cost
  that belongs in the operator disclosure, next to the mint's.

None of those is a silent fund loss on the gate's side. That is the standard
we hold Cashu to as well.

## Decision

Ship LNURLcash as a **payment method**, not as a payment rail replacement:

1. **Opt-in, disabled by default.** Absent config means the endpoints do not
   exist (404, not 403). Enabling is an operator action, disclosed in the
   operator-facing config schema with the issuer-custody caveat in the field
   description.
2. **Redemption is the authorization.** Access is granted only after the issuer
   has paid **our** invoice: preimage recovered from the callback response must
   match the payment hash we supplied. A note that the issuer refuses, or for
   which redemption is ambiguous, grants nothing.
3. **The customer may be offline; the gate may not.** The customer hands over
   the note; the gate does the issuer interaction. If the gate is offline, the
   method is unavailable — this is a stated limitation, not a bug, and it is
   exactly the reverse of the property an offline bearer instrument would have.
4. **A note is single-use, and we treat it as a distributed transaction.**
   Redemption against the issuer is the point of no return. Everything that
   makes it idempotent — a durable redemption record keyed by a stable
   fingerprint of the note — must be written **before** the callback is sent,
   so a crash mid-redemption is reconciled rather than repeated.
5. **Ambiguity is reconciled, never retried.** A callback timeout does not mean
   the issuer did not pay. The recovery path queries the issuer (or, failing
   that, records the note as `ambiguous` and refuses a second grant) rather
   than re-posting `k1`.
6. **Reuse the SSRF defences.** Every issuer URL comes from an untrusted note.
   Requests go through `ssrfSafeClient` and `validateCallbackURL`, and the
   callback host must be the same host the note's own `withdrawRequest`
   advertised.

Out of scope, deliberately: issuing notes (Role A), offline verification, and
any claim that a certificate's timestamp substitutes for redemption. A
`cs1`-style certificate is **at most** an input to a staleness policy, never
the grant condition.

## Consequences

### Positive

* One more way for a customer to pay without the gate learning a seed or
  holding custody of the note between payment and redemption.
* Reuses the identity, notice, session-grant and journal machinery already
  exercised by the Cashu path, so the grant side does not fork.
* Gives the reseller/one-way-channel dead end a cheaper neighbour: no chain
  interaction at all.

### Costs

* The gate now depends on a **second** class of third party at payment time;
  an issuer outage is a customer-visible failure with no local workaround.
* The issuer observes redemption (privacy cost to disclose).
* A new durable record class (redemption intents) with its own retention and
  reconciliation rules — this is the part that will produce incidents if it is
  treated as a local edit.
* Testing is not free: the happy path needs a live-ish issuer, and the failure
  paths (spent note, refused payment, timeout, crash between intent and
  callback) each need a planted case.

## Invariants the implementation must satisfy

1. Disabled by default; no endpoint, no listener, no background poller when
   off.
2. No grant without a preimage matching **our** payment hash.
3. Redemption intent is durable before the callback is issued.
4. A second presentation of the same note never grants twice — whether the
   first attempt succeeded, failed, or died mid-flight.
5. The note secret is never logged. Log a salted fingerprint, matching what
   `HandleRootPost` already does for tokens (`utils.TokenFingerprint`).
6. Reconciliation, not retry, on ambiguous outcomes.
7. The access session, once granted, outlives the payment record's retention.

## Alternatives considered

* **Accept a `cs1` certificate without redemption.** Rejected: it moves the
  grant condition from "the issuer paid us" to "the holder had a string the
  issuer once signed", which is forgeable-by-replay across gates and free to
  obtain. It would also make the gate's revenue depend on a signature rather
  than a payment.
* **Non-custodial bearer instrument (LNURLcash Role A / one-way channels).**
  Rejected on cost, not purity: the previous spike showed channel setup
  overhead dwarfing a single purchase, while this path has zero chain cost.
  The instrument's *purity* was never the blocker.
* **Do nothing until the LUD draft stabilises.** Rejected: redemption is LUD-03
  behaviour, frozen since 2018, and does not depend on the draft's certificate
  format. The unstable part is the thing we are not implementing.
