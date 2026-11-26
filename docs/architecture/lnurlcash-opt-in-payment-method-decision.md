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
   for profit-share payouts. It is *not* reusable as-is: `ssrfSafeClient` has no
   `CheckRedirect` (so a 302 would carry a bearer secret to whatever host the
   redirect names), its dial guard uses `net.IP.IsPrivate` (which misses CGNAT
   `100.64.0.0/10` — the range a carrier-grade or Tailscale-style network hands
   out), and `validateCallbackURL` is called on no path at all. The LNURLcash
   client therefore carries its own guards rather than inheriting those three
   defects, and the shared client is left alone (fixing it is its own change
   with its own payout-path risk).
2. `docs/architecture/payment-backend-disclosure-decision.md` (Decided
   2026-11-16) already separates **bearer ecash** from **account-based
   Lightning** and requires the operator to disclose which model is in play.
   An LNURLcash note is a third case that record does not cover: the customer
   presents a claim, and a **third party** decides whether it is still good.

### What LNURLcash is, for the purpose of this decision

A note is a withdraw link carrying a secret (`k1`), and a promise from its
**issuer** to pay whoever redeems it. Handing the note to the gate is not
payment; **redemption is payment**. The gate performs the LUD-03 withdraw
sequence against the issuer: an informational `withdrawRequest` GET, then the
callback GET carrying `k1`.

### The settlement rail (corrected — this is the part that matters)

The obvious reading of LUD-03 is: the issuer answers with a BOLT11 invoice that
it has paid, the gate recovers a preimage, and the preimage proves value
arrived. **TollGate cannot do that.** It runs no Lightning node — this repo's
only Lightning code is client-side `payRequest` for payouts — so it has no
invoice of its own to be paid and no place to look up a preimage. A preimage
recovered from an issuer's response proves only that *the issuer says* it paid
*an* invoice.

So the rail inverts, and the inversion is the whole design:

1. The gate asks **its own settlement mint** for a NUT-04 quote for the note's
   `maxWithdrawable`, capped by the operator's ceiling. That quote is a BOLT11
   invoice the gate's wallet already knows how to reconcile.
2. The gate asks the **issuer** to melt the note into that invoice — the
   callback carries `k1` and `pr`.
3. Access is granted only when **our mint** reports the quote `PAID`/`ISSUED`.

The issuer is authoritative for the burn (it decides whether the note is
still good, and it holds the sats). Our mint is authoritative for the value
(we can query it; we cannot query anything the issuer claims). The two
answers together are a settlement. Neither alone is.

This is why **`cs1` is never parsed** and no certificate is ever consulted: a
certificate is an issuer's timestamped claim, and the claim we need is not the
issuer's — it is our mint's. A note the issuer accepts but our mint never
sees paid grants nothing. A note the issuer refuses grants nothing.

That single reversal is also what makes the integration small: no taproot or
`btcd`, no `cgo`, no burn ledger, no Lightning node, no new grant path. The
merchant's existing quote store (`quote_store.go`) and grant path
(`ensureLightningAccessGranted`) already do steps 1 and 3; the new code is
the issuer conversation and its journal.

### Why this is acceptable despite issuer custody

A Cashu mint is also a custodian of unswapped proofs, and TollGate already
accepts that. The relevant question is not "is this trustless" but "is the
failure mode bounded and visible". For an LNURLcash note:

* the issuer can refuse to pay, or be offline — the customer's note then buys
  nothing, and the gate must say so before the customer is granted access;
* the issuer can accept the callback and melt to a **different** invoice than
  ours — it then owes us nothing, and we detect it because our quote stays
  unpaid forever. That is a refusal, not a loss: the gate grants nothing and
  the value stayed with the issuer;
* the issuer learns that this gate redeemed this note — a linkability cost
  that belongs in the operator disclosure, next to the mint's.

None of those is a silent fund loss on the gate's side. The one asymmetric
window — the issuer melts after we grant, or we grant and the settle query
lies — is closed by making the mint's answer, not the issuer's, the grant
condition, and by never granting on a timeout.

## Decision

Ship LNURLcash as a **payment method**, not as a payment rail replacement:

1. **Opt-in, disabled by default.** Absent config means the endpoints do not
   exist (404, not 403). Enabling is an operator action, disclosed in the
   operator-facing config schema with the issuer-custody caveat in the field
   description.
2. **Redemption is the authorization.** Access is granted only after **our
   settlement mint** reports the quote it issued as `PAID`/`ISSUED`. The
   issuer's acceptance is necessary and never sufficient: it is permission to
   expect value, not the value. A note the issuer refuses, or for which the
   mint's answer is missing, grants nothing.
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
6. **Do not inherit the old client's SSRF defects.** Every issuer URL comes from
   an untrusted note, so the LNURLcash client refuses redirects outright, dials
   only public addresses (explicitly refusing CGNAT `100.64.0.0/10`), and
   requires the callback host to be the host the note's own `withdrawRequest`
   advertised. Plain-http loopback is reachable only under a test-only flag, so
   the unit tests can drive a local fake issuer without that being a
   production capability.

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
2. No grant without **our mint** confirming the quote paid. An issuer's OK, a
   `cs1` certificate, or a recovered preimage is never the grant condition.
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
