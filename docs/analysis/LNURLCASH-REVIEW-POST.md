# LNURLcash (LUD-25/LUD-26) — review, and how it bears on TollGate

*Review of https://github.com/lnurl/luds/pull/301 — as of 2026-10-05, head of
branch `lnurlcash`. Primary source read in full: `25.md` (256 lines), `26.md`,
`25-vectors.json`, `26-vectors.json`. Nothing here is inferred from the website
or the playground.*

---

## Verdict up front

This is a good spec and I want it to exist. The naming is honest (it is a bearer
asset, not a payment protocol), the endpoint surface is unchanged, and the
decision to delegate script verification to `libbitcoinkernel` rather than
reimplement it is the single best choice in the document — the verification rule
*is* the consensus rule, so a reimplementation cannot drift from it.

I reviewed it because we are evaluating a related construction for TollGate (a
router selling internet access against bearer value). Three findings from that
work bear directly on LUD-25, and one of them I think is a genuine design gap.

---

## 1. `cs1` proves issuance, not ownership — and for point-of-sale that is the whole security question

The spec is admirably explicit:

> A certificate proves **issuance, not that the note is still outstanding**: the
> previous holder still knows the spend. A new holder SHOULD **rotate (or merge)
> the note as soon as it is online**.

That sentence is the entire security model for any *physical* redemption, and it
is currently stated as a warning about an operational detail.

Consider the case LNURLcash most obviously enables: a merchant accepts a note
over NFC or a QR code and hands over goods or a service **immediately**, then
rotates when it next reaches the network. Between acceptance and rotation the
merchant holds a **replayable** instrument — the party who handed it over can
spend it again, to the same service or another. A certificate does not help,
because it carries no consumption state, only issuance.

I do not think this is a bug — proving non-consumption offline is impossible
without a trusted clock or a chain, and LUD-25 does not pretend otherwise. But I
would argue for the marketing to match the mechanism:

- The "as soon as it is online" hedge reads as a *should*; for a
  point-of-sale integration it is a **hard exposure window** and deserves a
  normative statement of what the spec considers acceptable, or a named
  mitigation (`SERVICE` rotating on the merchant's behalf, a short
  `SERVICE`-side hold, or an explicit "this is not a POS protocol" note).
- If the intended answer is "a `cs1` is for genuine offline *verification of
  provenance*, and consumption must be online", that is a good answer — it just
  should be stated as such, up front, rather than left to the reader to derive
  from a warning.

A question worth considering: would a **one-time certificate** be worth
specifying — a `cs1` variant that binds the note to a single named consumption
(an ephemeral recipient key), so that a merchant accepting it offline knows no
*other* merchant can accept the same bytes? That does not close the
double-spend-to-the-same-service window, but it does make the instrument
non-transferable-after-acceptance, which is most of what a POS actually needs.

## 2. The timelock warning is correct and under-promoted

> A timelock honored by `SERVICE` is `SERVICE` asserting its own clock. It is a
> **custodial policy, not a consensus guarantee, and MUST NOT be presented as
> trustless.**

This is the most important sentence in the document, and it is in a
`[!WARNING]` halfway down. The programmable-leaf design invites exactly the
misreading it warns against: a reader sees arbitrary tapscript, sees `OP_CSV` /
`OP_CLTV` in the allowed set, and reasonably concludes the timelock is enforced
by Bitcoin.

It is not. The leaf says `OP_CSV`; the *service* checks the claimed `nSequence`
against its own clock. Those are different guarantees, and the gap is where a
naive "trustless escrow" would be built.

Suggestion: either move this to a top-level **Security Model** section, or restate
it positively at the point where timelocks are introduced — e.g. "timelocks
constrain *when a `SERVICE` will accept a spend*; they do not constrain a
`SERVICE`". Anyone building on this needs that sentence before they build, not
after.

## 3. `ck1` reproducibility and privacy pull in opposite directions

> `WALLET` SHOULD sign with an all-zero `aux_rand`, so the `ck1` is reproducible
> from `sk` and the domain alone.

Reproducibility is a real virtue — recovery, watch-only export, no nonce state to
lose. But a signature that is a deterministic function of `(sk, domain)` is also
a **constant**: the same key at the same service produces the same bytes every
time. That makes redemptions **linkable**, and it means a `ck1` observed once
identifies the note-holder's key at that domain thereafter.

I expect this is understood — it is the same tradeoff deterministic nonces always
carry — but it is not stated. A short **Privacy Considerations** paragraph
covering "is a `ck1` intended to be correlatable across redemptions at one
service, and if not, what breaks determinism?" would settle it for implementers.

## 4. Two smaller notes

**`mintPubkey` SHOULD be the BOLT-11 node id.** If the certificate key and the
node identity key are the same, a node migration invalidates every certificate in
circulation, and a compromised node key forges certificates. Worth saying what
breaks when they *aren't* the same, and whether they should be separable.

**BIP-68 direction.** LUD-25 mandates the time-type flag for relative locks and
refuses block heights and counts. We implemented a block-based relative lock
first, in the same area, and got the flag wrong — the failure is silent (it
becomes a 512-second unit rather than a block count). A one-line rationale for
time-only would spare the next implementer that afternoon.

---

## Where this connects to our work

We are evaluating whether a **one-way payment channel** can serve as a
non-custodial bearer instrument for a TollGate — a router that sells internet
access to a customer who may have no working internet yet. Two observations from
that work:

**LNURLcash is the container. A one-way channel is a leaf.** LUD-25 already
defines the programmable bearer note we would otherwise have had to invent: a
taproot output key `Q` spent by arbitrary tapscript, with timelocks and
multi-party leaves explicitly in scope. A one-way channel
(`OP_IF <expiry> CSV DROP <payerPub> CHECKSIG OP_ELSE 2 <payerPub> <payeePub> 2
CHECKMULTISIG OP_ENDIF`) is one such leaf. So the useful framing is not a
competing protocol but a question: *can a channel leaf be expressed as an
LNURLcash note?* We think it can, and that the combination is interesting.

**The interesting composition uses your two halves separately.** LNURLcash
supplies *existence* — `cs1` gives an offline, no-network proof of issuance.
A channel supplies *consumption* — consensus-enforced non-double-spend, with the
spend conditioned on a deadline rather than on the service being up. Reading them
together: `cs1` answers "was this issued?", a channel leaf answers "has this been
consumed?", and neither pretends to answer the other's question.

That composition matters to us because our router may have to accept value
*before* it can reach a mint, and settle afterwards. Today that means a
30-second online round-trip in the customer-facing path, or accepting a
replayable instrument. A channel leaf is the first thing we have found that
plausibly removes both.

**One direct conflict to flag**, since it bit us: LUD-25's time-only BIP-68
mandate (item 4 above) is the opposite of what a chain-anchored timestamp needs.
A channel's relative lock is anchored to a funding transaction at a block height,
not to "when the service credited the note". If anyone downstream builds a
channel-shaped leaf on LUD-25, "service-relative" and "chain-absolute" time will
need reconciling explicitly.

---

## Summary

| Point | Weight |
|---|---|
| `cs1` proves issuance, not ownership — state the POS exposure normatively | **High** |
| Timelock custody warning deserves a Security Model section | **High** |
| `ck1` reproducibility vs linkability — add Privacy Considerations | Medium |
| `mintPubkey` / node-id coupling — say what breaks | Low |
| BIP-68 time-only rationale | Low |
| **Keep**: no new endpoint; `libbitcoinkernel` delegation | — |

Interoperability is the reason this spec is worth the effort: "a wallet that does
not know LNURLcash sees an ordinary withdraw link" means this can be deployed
without a flag day, and the `k1` short form means a wallet that knows nothing of
taproot can still use notes. Those two properties are what will make it adopted.
