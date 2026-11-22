# One-way channels, bearer instruments, and TollGate — a handover

**Date:** 2026-10-14 · **From:** Felix · **For:** christoph / `c08r4d0r`

This package contains two things you asked for, in one place:

1. **Part 1 — the spike result.** Whether a one-way payment channel can serve as
   a non-custodial bearer instrument for cold-start TollGate access. **Verdict:
   disproved**, with the reproducible rig that shows it.
2. **Part 2 — the LNURLcash review**, written as a standalone post you can send
   to `dni` (the LUD-25/26 author) as-is.

Everything is version-controlled. Where to find each thing is in §3.

---

# Part 1 — The spike

## The question

> Can a **one-way payment channel** be constructed so a payer who has **no
> internet gateway** and **no custodian** can buy internet time from a TollGate —
> and does TollGate benefit?

## The answer

**No — with the scope of the negative stated honestly.** Among the four
constructions actually built and executed, none is both non-custodial and
payer-offline; and against the module's own advertised price the on-chain
settlement layer is **3–4 orders of magnitude** too expensive.

This is a *failure to find* over a small, specific search space — not a proof
that no construction exists. The one condition that would overturn it (`D5`) is
named explicitly and left open.

## The four constructions, and who the custodian is

| Variant | Live branch | Custodian | Non-custodial? | Payer offline? |
|---|---|---|---|---|
| **V0** redeemer signs alone | `<payeePub> CHECKSIG` | **the payee** | ❌ | ✅ |
| **V1** per-granule cosign | 2-of-2 | **nobody** | ✅ | ✅ *during a session, over the local link* |
| **V2** pre-signed ladder | 2-of-2 | **payee, up to ladder top** | ⚠️ | ✅ |
| **V3** Cashu-native Spilman (`cdk-spilman`) | 2-of-2 at the mint | **the mint** | ❌ | ✅ |
| **V4** duplex + revocation | per-commitment keys | nobody, **needs a watchtower** | ✅ | ❌ |

Two findings are quantified, both measured against real consensus:

- **V0 is one line from V1 and admits a total loss.** The "payer-offline
  simplification" removes the payer's co-signature; Bitcoin Core then **accepts**
  a spend paying the payee 100%. Not hypothetical — a two-line script change.
- **V2 is a 60% theft with no forgery.** A 5-state ladder at a 100,000-sat
  granule lets a router that served **2** granules submit the **fifth** state and
  extract **300,000 sats**. Every state is validly signed and mutually exclusive;
  the router simply submits the largest. Unfixable without per-granule
  interactivity (i.e. V1).

**V3 is worth naming because it is the project's own committed direction.**
`session-ticket-decision.md` adopts Cashu-native Spilman for granularity. It does
not deliver custodian-freedom: the mint adjudicates, and that document's own
accepted-risks paragraph records that the balance sits at a third-party mint and
that the receiver must claim before expiry. A deliberate trade — just don't let
the custody be claimed away later.

## The three findings that change the framing

**1. Interactivity is not connectivity — the question conflates them.**
Co-signing a state needs the payer reachable on the **local** link. It needs **no
internet** and **no chain state**. The customer is attached to the router's Wi-Fi,
so a local link exists by definition of the product. "No internet gateway" was
never the blocker it was assumed to be.

**2. `OP_CSV` is a minimum delay, not a deadline — so the expiring asset is the
fee budget.** Once the input ages, a refund stays spendable *forever*, which
removes the reorg/watchtower time-pressure class. What does expire is the
fixed-fee commitment: a pre-signed refund relays at up to 2 sat/vB and is
**stranded from 3 sat/vB**, and the payer — who has no internet — **cannot CPFP
it**, while the payee can. **Every asymmetry (fee bumping, rebroadcast, claim)
favours the router.**

**3. It does not pay — for metered micropayments.** On-chain setup+close is a
fixed **716 sats**, so a 1-sat purchase is **71,600%** overhead.

> **Correction (§10 of the design record).** The live router
> (`tollgate-A04E`, `tollgate-wrt v0.6.0-rc4-gcf7cad3`) reports
> `metric = "bytes"`, `step_size = 22020096` (**21 MiB** exactly),
> `price_per_step = 1 sat`. So **1 sat buys 21 MiB of data** — not "6.12 hours",
> which was an artefact of reading `step_size` as milliseconds. The overhead
> ratios below stand, but the break-even moves a lot: a **300 GB/month** user
> pays ~13,624 sat, so a channel reaches **5% overhead in ~1 month** and 1% in
> ~5 months. **A prepaid subscription is economically plausible; a per-session
> micropayment is not.**

From the module's own constants (`step_size = 22020096`, `price_per_step = 1 sat`):

| Purchase | = internet | Overhead at 716 sats setup+close |
|---|---|---|
| 1 sat | 6.1 h | **71,600%** |
| 64 sat | 16.3 d | **1,118.8%** |
| 100,000 sat | 25,486 d | 0.7% |

To bring on-chain overhead under the 0.1% a mint/Lightning fee costs, a customer
must prepay for **centuries** (~500 years). Cashu is cheaper at **every** size the
product sells. The comparison is deliberately asymmetric *in the channel's
favour*: the channel is charged a full setup+close per purchase, Cashu only its
melt fee. Read the numbers as a **lower bound on Cashu's advantage**.

## The increment actually worth doing

Not a new instrument — **deferral**:

> the router accepts a customer's value **before** it can reach a mint, and
> settles later — removing the 30-second `Receive` block and the
> `payment-outcome-unknown` class from the customer-facing leg.

The grant half **already exists** (`AddAllotment` is pure memory; `valve.OpenGate*`
is local). What's missing is *verify-without-mint* + *defer-settlement*. That is a
**Cashu-level** change at the layer the money already lives on.

## Two corrections to the record

**#239's cold-start bootstrap does not currently work.** I set out believing it
"partially answered" cold start. It does not: `ForwardFirstProof` has **no
production caller**; `bootstrap.go` performs **no mint interaction at all** (its
imports are only sha256/hex/fmt/sync/time/logrus — "mint" appears only in
comments); the release notes call it experimental and awaiting router acceptance;
and the merchant cannot reach the machinery (the packages are wired in opposite
directions).

**The Decker thesis is a *semester* thesis, not a master's.** This answers the
question that started all of this. Verified against the archived ETH TIK thesis
database and the live PDF: **SA-2013-49, Sandra Siby, supervisor Christian
Decker, December 2013**, *"Paying your Internet, One Byte at a Time"* — a working
one-way-channel proof-of-concept for **selling WiFi Internet access**, built with
bitcoinj. Degree is **S (semester), not M**; the student is **Siby, not
Burchert**; and the work *applies* Hearn–Spilman rather than analysing one-way
channels. Burchert's work under Decker is duplex/tree funding — a full-text search
of the RSOS paper returns zero hits for "unidirectional" and zero for "Spilman".

Worth noting: that thesis's two self-reported limits are exactly this spike's live
questions — funding confirmation deliberately skipped ("leaves open a small window
… for a double-spending attack") and a 24-hour refund lock.

## The customer-held-mint topology

Your follow-on. **It survives recursion and illiquidity, and dies on an
inversion.**

- **Not killed by recursion:** the bootstrapping is **nested, not circular** — the
  operator can pre-position liquidity over a *mobile* link days ahead, on a device
  that is not the LAN being sold to. Internet is needed *once, at
  liquidity-acquisition*, not at purchase.
- **Not killed by the mint's LN illiquidity:** a mint is a fractional-reserve
  credit intermediary, not necessarily an LN routing node. **Signing a NUT-03
  swap needs only the keyset private key** — not a live LN node. So credit works
  offline; only debit (melt) fails.
- **Your question, answered precisely:** the mint's inability to see its LN
  channel state is **both** a liveness problem (it cannot melt — *the seller*
  holds unswappable value) **and** a safety problem (it may credit a note against
  a channel force-closed while it was offline — on a stale-commitment race, *the
  customer* loses coin, because being offline by design means missing
  `to_self_delay`). **Liveness costs the seller; safety costs the buyer.**
- **Killed by the inversion:** the construction spans **two disjoint graphs** —
  mint⇄node (LN channels) and customer⇄router (the LAN purchase). A Spilman
  channel is a customer⇄router object and **touches no LN channel** in the mint's
  graph. So it solves a different problem. And then:

> Track B's mint **is a custodian**. A one-way channel's entire purpose is to
> **be** the trustless substitute for a custodian. Deploying both at once keeps
> the custodian **and** pays the channel's deadline cost. The coherent version is
> **"the customer runs no mint, and funds the channel directly."**

- **And the mechanism:** a channel does not *delete* the time obligation — it
  **relocates it and removes the reconciler**: from the authority's obligation to
  answer within 30 s, to the holder's obligation to act before expiry, with **no
  reconciliation step** after expiry.
- **Chain-blindness: deferred-online, not blind.** The *payer* can be chain-blind;
  the *construction* cannot whenever a CSV refund is the safety property — a
  relative lock is enforced by *confirmation*, so someone must broadcast at/after
  height H. The only bridge-less relay path makes **the router a liveness
  dependency for the customer's own refund**.

---

# Part 2 — LNURLcash review, for `dni`

*Send the section below as-is, or point them at
`docs/analysis/LNURLCASH-REVIEW-POST.md` in the PR.*

---

## LNURLcash (LUD-25/LUD-26) — review, and how it bears on TollGate

*Review of https://github.com/lnurl/luds/pull/301 — as of 2026-10-05, head of
branch `lnurlcash`. Primary source read in full: `25.md` (256 lines), `26.md`,
`25-vectors.json`, `26-vectors.json`. Nothing here is inferred from the website or
the playground.*

### Verdict up front

This is a good spec and I want it to exist. The naming is honest (it is a bearer
asset, not a payment protocol), the endpoint surface is unchanged, and the
decision to delegate script verification to `libbitcoinkernel` rather than
reimplement it is the single best choice in the document — the verification rule
*is* the consensus rule, so a reimplementation cannot drift from it.

I reviewed it because we are evaluating a related construction for TollGate (a
router selling internet access against bearer value). Three findings from that
work bear directly on LUD-25, and one of them I think is a genuine design gap.

### 1. `cs1` proves issuance, not ownership — and for point-of-sale that is the whole security question

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

- The "as soon as it is online" hedge reads as a *should*; for a point-of-sale
  integration it is a **hard exposure window** and deserves a normative statement
  of what the spec considers acceptable, or a named mitigation (`SERVICE`
  rotating on the merchant's behalf, a short `SERVICE`-side hold, or an explicit
  "this is not a POS protocol" note).
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

### 2. The timelock warning is correct and under-promoted

> A timelock honored by `SERVICE` is `SERVICE` asserting its own clock. It is a
> **custodial policy, not a consensus guarantee, and MUST NOT be presented as
> trustless.**

This is the most important sentence in the document, and it is in a `[!WARNING]`
halfway down. The programmable-leaf design invites exactly the misreading it warns
against: a reader sees arbitrary tapscript, sees `OP_CSV` / `OP_CLTV` in the
allowed set, and reasonably concludes the timelock is enforced by Bitcoin.

It is not. The leaf says `OP_CSV`; the *service* checks the claimed `nSequence`
against its own clock. Those are different guarantees, and the gap is where a
naive "trustless escrow" would be built.

Suggestion: either move this to a top-level **Security Model** section, or restate
it positively at the point where timelocks are introduced — e.g. "timelocks
constrain *when a `SERVICE` will accept a spend*; they do not constrain a
`SERVICE`". Anyone building on this needs that sentence before they build, not
after.

### 3. `ck1` reproducibility and privacy pull in opposite directions

> `WALLET` SHOULD sign with an all-zero `aux_rand`, so the `ck1` is reproducible
> from `sk` and the domain alone.

Reproducibility is a real virtue — recovery, watch-only export, no nonce state to
lose. But a signature that is a deterministic function of `(sk, domain)` is also a
**constant**: the same key at the same service produces the same bytes every time.
That makes redemptions **linkable**, and it means a `ck1` observed once identifies
the note-holder's key at that domain thereafter.

I expect this is understood — it is the same tradeoff deterministic nonces always
carry — but it is not stated. A short **Privacy Considerations** paragraph
covering "is a `ck1` intended to be correlatable across redemptions at one
service, and if not, what breaks determinism?" would settle it for implementers.

### 4. Two smaller notes

**`mintPubkey` SHOULD be the BOLT-11 node id.** If the certificate key and the
node identity key are the same, a node migration invalidates every certificate in
circulation, and a compromised node key forges certificates. Worth saying what
breaks when they *aren't* the same, and whether they should be separable.

**BIP-68 direction.** LUD-25 mandates the time-type flag for relative locks and
refuses block heights and counts. We implemented a block-based relative lock
first, in the same area, and got the flag wrong — the failure is silent (it
becomes a 512-second unit rather than a block count). A one-line rationale for
time-only would spare the next implementer that afternoon.

### Where this connects to our work

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
LNURLcash note?* We think it can — **with one correction: `BIP-342` disables
`OP_CHECKMULTISIG`, so the leaf needs the `OP_CHECKSIGADD` form before it can be
encoded at all.**

**The interesting composition uses your two halves separately.** LNURLcash
supplies *existence* — `cs1` gives an offline, no-network proof of issuance. A
channel supplies *consumption* — consensus-enforced non-double-spend, with the
spend conditioned on a deadline rather than on the service being up. Reading them
together: `cs1` answers "was this issued?", a channel leaf answers "has this been
consumed?", and neither pretends to answer the other's question.

That composition matters to us because our router may have to accept value
*before* it can reach a mint, and settle afterwards. Today that means a 30-second
online round-trip in the customer-facing path, or accepting a replayable
instrument. A channel leaf is the first thing we have found that plausibly removes
both — **though we should be honest that our own spike concluded the on-chain
version is uneconomic for a per-session micropayment at our price point (1 sat
buys 21 MiB — see §10 of the design record for the corrected break-even, which
makes a prepaid subscription plausible), so this
is a direction, not a plan.**

**One direct conflict to flag**, since it bit us: LUD-25's time-only BIP-68
mandate is the opposite of what a chain-anchored timestamp needs. A channel's
relative lock is anchored to a funding transaction at a block height, not to "when
the service credited the note". If anyone downstream builds a channel-shaped leaf
on LUD-25, "service-relative" and "chain-absolute" time will need reconciling
explicitly.

**Also worth adopting regardless:** LUD-25's two stated footguns — `SERVICE` MUST
reject any leaf containing `OP_SUCCESSx` outside pushed data, and MUST NOT use a
public key that is neither empty nor 32 bytes in a signature check (BIP-342's
upgradable key types let any signature pass against one). Both are real
script-builder assertions and we have added them to our own rig.

### Summary

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

---

# 3 — Where everything lives

**The deliverable:**
https://github.com/OpenTollGate/tollgate-module-basic-go/pull/944
(draft · 26 files · +6798/-0 · head `4fd818a`)

| Path | What |
|---|---|
| `docs/architecture/one-way-channel-spike.md` | the result as a design record |
| `docs/analysis/SPIKE-REPORT.md` | master report |
| `docs/analysis/PRIOR-ART.md` | construction, trust assumptions, chain requirements, cited |
| `docs/analysis/ARCH-FIT.md` | can a channel be a `WalletPort` backend? (**beside it, not in it**) |
| `docs/analysis/CODEFEAS.md` | the module's actual purchase flow, per-step reachability |
| `docs/analysis/RISK-AND-IAC.md` | custody, attacks, Cashu-dominance, D1–D5 |
| `docs/analysis/CUSTOMER-MINT-AND-INTEROP.md` | the customer-mint topology + LNURLcash encoding |
| `docs/analysis/LNURLCASH-REVIEW.md` | full LUD-25/26 review |
| `docs/analysis/LNURLCASH-REVIEW-POST.md` | the post in Part 2 |
| `docs/analysis/CROSS-FAMILY-REVIEW.md` | the cold review, verbatim |
| `research/one-way-channel-spike/` | **the reproducible rig** — `run-rig.sh`, `ERRATA.md`, `out/` |

**Run the rig:**

```bash
bash research/one-way-channel-spike/rig/run-rig.sh    # writes rig/out/
```

No network needed except a one-time checksum-verified `bitcoind` download; the
simulator and channel demo are local-only (a `regtest` node on `127.0.0.1:18443`).

**Evidence quality:** every channel assertion runs against **Bitcoin Core v31.1
consensus** — a signed transaction is accepted only if the sighash and DER
encoding are byte-correct. That cross-check was load-bearing: **four** bugs in the
rig's own cryptography were caught by Core *rejecting*, not by self-consistency,
and are recorded in `ERRATA.md`. A simulator would have passed all four silently.

**Falsification:** `GREEN.txt` carries two deliberate `[FAIL]` lines. Making
either pass disproves the work. Be precise — they are **hardcoded assertions, a
target a human must implement**, not a proof procedure; no script can decide
whether some other construction exists.

**The cold review found five real defects in this work** (an internal bug-count
contradiction, an overreaching universal negative, an asymmetric cost comparison,
an undeclared BTC price assumption, and a non-reproducible ratio). All are fixed
and the fix is documented in `CROSS-FAMILY-REVIEW.md`. Its most useful output:
the reviewer independently attempted the counterexample and could not build one.

# 4 — Open items

1. **PR #944 is a draft.** Flip it live on request.
2. **The verify-and-defer seam** is the one actionable, non-duplicative
   increment. Grant half already exists; missing half is verify-without-mint +
   defer-settlement. Worth its own issue.
3. **LNURLcash feedback** — Part 2 is ready to send to `dni`. Not sent; your call.
4. **`session-ticket-decision.md`** already commits to Cashu-native Spilman for
   granularity, with its own migration gate. Nothing here argues against that —
   only that it should not be described as custodian-free.
