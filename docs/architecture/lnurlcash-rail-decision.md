# LNURLcash redemption rail — opt-in decision record

## Status: Proposed (implementation in progress; the rail ships OFF)

This record governs a second payment instrument beside Cashu: **LNURLcash**
notes ([LUD-25](../README.md), [LUD-26](../README.md)) presented by a guest and
redeemed with the note's issuer before this router grants access. It is a
*redemption* rail only. This router does not mint notes, does not run a SERVICE
endpoint, and does not become an LNURLcash issuer.

The rail ships **disabled**, and an upgrade cannot enable it. That is the same
contract as
[`payment-backend-disclosure-decision.md`](payment-backend-disclosure-decision.md)
applies to account-based Lightning backends, applied to a bearer note.

## The claim being bought, stated narrowly

A LNURLcash note is a taproot output key `Q` that a SERVICE has credited with
value, plus a *spend* (`k1`/`cw1`/`ck1`) that opens `Q`. The value lives in the
issuer's book, not on chain: no transaction is broadcast, and the note is worth
what the issuer says it is worth.

That means one thing this router can rely on and one thing it cannot:

| Question | Answer for this rail |
|---|---|
| Can the gate learn a note's value without trusting the customer? | **Yes** — by asking the issuer (`GET <withdrawLink>?p=<cp1>`) and verifying the issuer's own answer. |
| Can a certificate (`cs1`) prove a note is still *unspent*? | **No** — `cs1` attests that `Q` was outstanding at `t`, and the previous holder still knows the spend. LUD-25 says so itself. |

Therefore: **a certificate is never sufficient to grant access.** `cs1` is
accepted only as an input to a decision the issuer makes, never as the decision.

## Required flow (first version)

1. The guest submits the note to the gate over the LAN. The customer may be
   offline in the sense that they need no wallet, no Lightning node and no
   internet: they talk to the gate, and the gate talks to the issuer.
2. The gate decodes the note locally: canonicalise the issuer domain, check it
   is on the operator's allowlist, decode the spend to `Q`, refuse unsupported
   leaf versions and `OP_SUCCESSx` payloads, and sanity-check the claimed amount.
   No network call happens before these checks pass.
3. The gate durably binds **intent**: operation id, client, issuer, `Q`, amount,
   and the canonical note URL — through `src/paymentjournal`
   (`Prepare` → `Submitted` → `BindInvoice`). The bind is the point of record:
   after it, a restart can find the operation and reconcile it.
4. The gate redeems with the issuer, online. Two shapes are possible and the
   first version implements the one the issuer actually offers: a **melt**
   (`k1=<spend>&pr=<invoice>`), where the note is burned and the gate is paid;
   or a **rotate** (`k1=<spend>&p1=<cp1>`), where the note is burned and reissued
   to a key the gate holds. A rotate is only usable if the gate can spend the
   reissued note, which requires issuer-side support this version does not
   assume.
5. The gate records `paid` only on the issuer's own confirmation of settlement
   or ownership, then `grant_pending`, then grants exactly once and records
   `completed`.
6. If the grant fails after the issuer confirmed, the value is the operator's
   and the customer has nothing: the operation stays `grant_pending` and is
   retried, never reported as a success and never dropped.

## Fund-safety invariants this rail must satisfy

These are the `AGENTS.md` distributed-state-machine rules instantiated for this
rail. Each one has a test that names it.

| Invariant | Rule |
|---|---|
| Persist before irreversible work | The intent, the bound note and the session/operation id are durable **before** the redemption request leaves the router. A persistence failure aborts the purchase; it is never logged and ignored (`persistLightningQuotes` is explicitly *not* the model here). |
| No blind retry after a submit timeout | A redemption whose response never arrived is `unknown`, not `failed`. The gate re-reads the note from the issuer (`?p=`) and only then decides. Retrying a melt with the same spend is how a note is burned twice or lost. |
| Idempotent grant | Granting is keyed on the operation id and recorded in the journal's `completed` state; a replayed request, a concurrent duplicate, or a restart between `paid` and `granted` grants once. |
| No grant on `cs1` alone | A certificate with a valid signature and a plausible `t` grants nothing. The issuer's confirmation of settlement is the only evidence that admits a note. |
| One note, one value | `paymentjournal` refuses a second incoming operation bound to the same payment hash; the rail additionally refuses a replayed `Q` already recorded as paid, burned or pending. |
| Allowlisted issuers only | The allowlist is enforced on the canonical host **before** any network call, so the gate cannot be pointed at an arbitrary host by a note URL. |

## Threat model, and what this record does not claim

- **The issuer is trusted for value and consumption.** A malicious issuer can
  lie about a note's value, refuse a valid note, or double-spend its own book.
  LNURLcash's answer is that the note is a claim on one issuer; the gate's answer
  is an operator-maintained allowlist and a per-note value ceiling.
- **A `cs1` ages.** Age policy is undefined by LUD-25. This rail therefore does
  not use `t` for anything except rejecting a certificate signed in the future
  by more than the allowed clock skew.
- **Spends travel in URLs.** The gate must not log the query string of the
  redemption request, must answer with `Cache-Control: no-store`, and must never
  put a spend, `Q`, MAC address or session id in an error message, a wallet memo
  or a Lightning invoice.
- **Not offline-first.** Gate-offline optimistic access on a certificate alone is
  explicitly out of scope: it is the one shortcut that would turn "a certificate
  is not proof of possession" into a free-internet bug.

## Operator disclosure (the acknowledgement this rail requires)

Enabling the rail requires **both** `lnurlcash.enabled = true` and
`lnurlcash.acknowledgement = "lnurlcash-issuer-redeem-v1"` — the version of the
words below that this build knows. Changing the words bumps the version and a
router that acknowledged an older version stays off until the operator
acknowledges the new one.

> **LNURLcash note redemption**
> This router will accept LNURLcash notes as payment and will redeem each note
> with the issuer you allowlist, over the internet, before granting access. The
> issuer sees a request naming the note; it does not receive the customer's MAC
> address, session id or name from this router.
>
> The value of a note is the issuer's promise, not a Cashu proof and not an
> on-chain output. If the issuer is unavailable, notes cannot be redeemed and
> the customer cannot pay with one. If the issuer is dishonest, it can refuse or
> devalue a note.
>
> This router stores a durable payment record (note value, issuer, operation id,
> grant state and timestamps) so a restart cannot lose a paid purchase. No note
> spend, bearer secret or issuer key is stored.

Required acknowledgements (recorded as the single `acknowledgement` value):

- [ ] I understand the issuer decides a note's value and can refuse it.
- [ ] I understand the gate contacts the issuer on every redemption, so the rail
      needs working internet and a reachable issuer.
- [ ] I allowlist the issuer domains I accept and set a per-note ceiling.

## Configuration

```json
{
  "lnurlcash": {
    "enabled": true,
    "issuers": ["mint.example"],
    "max_note_msat": 21000000,
    "acknowledgement": "lnurlcash-issuer-redeem-v1"
  }
}
```

`issuers` entries are **bare canonical hosts** — lowercase, A-label, no scheme,
port, path or userinfo. A URL, an IP literal or a non-ASCII label is refused at
activation time rather than silently dropped, because an operator who wrote a
URL believes that issuer is allowed.

## Non-goals

- Minting notes, operating a SERVICE, or publishing a `withdrawLink`.
- Accepting a `cs1` alone, and any offline/optimistic grant.
- Storing issuer signing keys, note spends or bearer secrets in config.
- Replacing the Cashu purchase path, the Lightning top-up path, or their records.
- Any change to the portal's visual design; the portal repo owns that surface.

## Known limitation: the journal has a hard record cap

`paymentjournal` refuses `Prepare` once it holds `maxRecords` (1024) operations, and it counts
**every** record whatever its state — a settled sale occupies a slot exactly as much as a
failed one. There is no prune, no expiry, and no delete path anywhere in the store; it is
append-only by design.

The consequence for this rail, measured rather than reasoned:

```
sale 1024 refused: lnurlcash rail: the payment journal refused a record
```

A busy gate therefore stops accepting LNURLcash after 1024 sales, and stays stopped. This is
NOT an attack precondition: it needs no malformed note, no ambiguity and no adversary, only
customers paying.

The rail's own `Settle` (which marks a sold record `paid` when the quote settles) does NOT
change this. It corrects the OPERATOR's view — before it, every sale a gate ever made was
reported as "the melt was attempted and the outcome is unknown", which is a lie that would
send someone chasing nothing — but the cap counts records, not live redemptions, so retirement
frees no capacity.

**Not fixed here, deliberately.** The remedy is a retention policy for terminal records
(`paid`, `failed`, `completed`) in `paymentjournal` itself: prune past a bound, or keep a
separate small reservation table sized for concurrent redemptions rather than sales. That is
shared code with its own crash-consistency obligations, and it belongs in its own change with
its own review rather than being smuggled into this one.

Until it lands, the rail must not be enabled on a gate that expects more than ~1000 lifetime
LNURLcash sales. That is a real product limit and it is stated in the PR body.

## References

- LUD-25 / LUD-26 (draft): notes, canonical spend transaction, certificates,
  retries, mint fees.
- `docs/architecture/payment-backend-disclosure-decision.md` — the consent and
  session-record contract this record inherits.
- `src/paymentjournal/README.md` — the durable journal whose states this rail
  uses, and the caller's remaining obligations (authenticated settlement,
  exactly-once grant).
- `AGENTS.md` — fund-safety and crash-consistency rules.
