# One-way channels as non-custodial bearer instruments for TollGate — spike report

**Status:** spike complete, 2026-10-14. **Verdict: the strong thesis is DISPROVED.**
Module revision read throughout: `OpenTollGate/tollgate-module-basic-go` @
`cf7cad34677e87e775e7f16786b1bbf18c4bc8bc` (`main`).

**The research question, as asked:**

> Can a **one-way payment channel** be constructed such that a payer who has **no
> internet gateway** and **no custodian** can buy internet time from a TollGate —
> and does TollGate benefit?

**Answer, in one sentence:** No — no construction among the four built and run is
both non-custodial and payer-offline, and against this product's own advertised
price (1 sat / 6.12 h) the on-chain settlement layer is 3–4 orders of magnitude
too expensive. The one construction that does hold (`V1`) is not the question
that was asked, needs interactivity on the *local* link, and is dominated by the
Cashu path TollGate already ships.

**Contents**

1. How to reproduce this verdict
2. The disproof, in four independent parts
3. What *is* true, and where the real product opportunity is
4. Track B — the customer-held LN-backed mint
5. LNURLcash (LUD-25/LUD-26) — the container, and a custody trap
6. Prior art: the Decker thesis question, answered
7. What this means for TollGate — recommendations
8. Honest limits — what this spike did **not** establish
9. Errata — what running the rig against consensus caught

---

## 1. How to reproduce

Everything numeric in this report comes from a script. Nothing is typed from a
REPL.

```bash
git clone <fork> && cd tollgate-module-basic-go
bash research/one-way-channel-spike/rig/run-rig.sh    # writes rig/out/
```

Declared network needs — the rig is built so the offline claim is checkable by
the rig itself:

| Step | Network need |
|---|---|
| `sim_economics.py` | **none.** Pure arithmetic. |
| `demo_channel.py` | **none.** A local `regtest` `bitcoind` on `127.0.0.1:18443`. |
| `run-rig.sh` step 1 | once, only if `bitcoind` is absent (checksum-verified download). Supply `BITCOIND=/path/to/bitcoind` to skip entirely. |

Artefacts, all committed: `ENV.txt`, `GO-TOOLCHAIN.txt`, `BITCOIND-VERSION.txt`,
`SIM-OUTPUT.txt`, `RED.txt`, `GREEN.txt`, `sim_economics.json`.

**The construction is validated against Bitcoin Core v31.1 consensus, not against
our own signature code.** A transaction the rig signs is only accepted if the
sighash and DER encoding are byte-correct. That cross-check is the reason to use
Core rather than a simulator — and §9 is the proof it was load-bearing: four
separate bugs in the rig's own cryptography were caught by Core rejecting, not by
self-consistency. A simulator would have passed all four silently and the report
would have contained four false claims.

**The falsification condition is executable.** `GREEN.txt` carries two deliberate
`[FAIL]` lines:

- *"named a construction that is BOTH payer-chain-blind AND non-custodial"*
- *"the ladder makes the construction non-custodial AND payer-offline"*

If a contributor makes either pass, this report is disproved and must be revised.

---

## 2. The disproof, in four independent parts

The thesis is disproved if **any** of five conditions holds. Four are met.

### D1 — Every custodian-free variant requires the payer per granule → **MET**

The canonical Spilman script (`rig/demo_channel.py:build_script`):

```
OP_IF  <CSV> OP_CHECKSEQUENCEVERIFY OP_DROP <payerPub> OP_CHECKSIG     # payer refund
OP_ELSE OP_2 <payerPub> <payeePub> OP_2 OP_CHECKMULTISIG OP_ENDIF      # payments
```

The `ELSE` branch does the custody work: **every payment state is a 2-of-2
spend**, so a state exists only if the payer signed that exact output set. The
payee cannot mint a state with a larger payee output. `GREEN.txt` T3 broadcasts a
redeem-100% attempt and Core **rejects** it
(`Signature must be zero for failed CHECK(MULTI)SIG operation`).

That is the genuine non-custodial result — and it requires the payer's
co-signature per granule. Co-signing needs the **local** link, not the internet;
the local link exists by definition of the product. **The brief conflated
interactivity with connectivity, and they are different requirements.**

### D2 — Every payer-offline variant hands a custodian extractable power → **MET, quantified**

| Variant | Live branch | Custodian | Non-custodial? | Payer offline? |
|---|---|---|---|---|
| **V0** redeemer signs alone | `<payeePub> CHECKSIG` | **the payee** | ❌ | ✅ |
| **V1** Spilman, per-granule cosign | 2-of-2 | **nobody** | ✅ | ✅ *during* a session, over the local link |
| **V2** Spilman, pre-signed ladder | 2-of-2 | **payee, up to ladder top** | ⚠️ | ✅ |
| **V3** Cashu-native Spilman (`cdk-spilman`) — *the project's chosen direction* | 2-of-2 **at the mint** | **the mint** | ❌ | ✅ |
| **V4** duplex + revocation (LN-shaped) | per-commitment keys | nobody, **but needs a watchtower** | ✅ | ❌ |

- **V0 is CRITICAL, not hypothetical.** `RED.txt` T3: Core **ACCEPTS** a 100%
  theft. It is a two-line script change from the "payer-offline" simplification,
  and it is exactly the failure the brief's own "chain-blindness is the crux"
  warning points at.
- **V2 is quantified.** A 5-state ladder at a 100,000-sat granule lets a router
  that served **2** granules submit the **fifth** state and extract
  **300,000 sats = 60% of the ladder top** (`GREEN.txt` T9). **No forgery is
  required** — every ladder state is validly signed and the states are mutually
  exclusive (T4); the router simply submits the largest. Unfixable without
  per-granule interactivity.
- **V3 is the project's own committed direction**, and it **reintroduces the
  mint as adjudicator**: `session-ticket-decision.md:418-424` records that the
  operator's balance "sits at a third-party mint", that the receiver "must claim
  before expiry or the sender refunds the whole channel", and that recovery
  belongs to the wallet layer. It answers the early-exit question — its stated
  purpose — but it does **not** deliver "no custodian".

### D3 — The payer needs chain access for a step the thesis claims is chain-free → **MET for recovery, NOT for transacting**

"Chain-blind" is three different claims. Measured per construction in `GREEN.txt` T7:

| Payer activity | Internet? | Chain state? | Interactivity? |
|---|---|---|---|
| Fund the channel | ✅ | ✅ | — |
| **Co-sign one state (V1)** | ❌ **NO** | ❌ **NO** | ✅ local link |
| Detect an inflated claim | ✅ | ✅ | — |
| Publish the CSV refund | ✅ | ✅ **must know the input aged** | — |
| Know whether the refund will relay | ✅ | ✅ mempool fee rate | — |
| CPFP-bump a stranded refund | ✅ | ✅ | — |

So the **payment path is genuinely chain-blind** — a payer holding only a channel
state and a signing device participates fully with zero chain access. But
recovering leftover value needs chain access once. **A stranded deposit is a
donation, not a channel.**

**Two corrections that make the construction stronger than a naive reading:**

1. **`OP_CSV` is a MINIMUM delay, not a deadline.** There is no window the payer
   must hit; the refund stays spendable **forever** once the input ages. A payer
   that reappears after a year can still refund. This *removes* the
   reorg/watchtower time-pressure class. (The rig's own output contradicted its
   first draft of the prose — see §9 E5.)
2. **The real expiring asset is the fee budget, not a clock.** The pre-signed
   refund is **142 vB** committing to a **fixed 400-sat fee**; it relays at up to
   2 sat/vB and is **stranded from 3 sat/vB** (`GREEN.txt` T6). The payer cannot
   CPFP it — building a child needs a funded wallet and mempool visibility, i.e.
   the internet the payer does not have. The *payee* can, and the payee is the
   party the payer does not trust. **The timeliness requirement is a fee-market
   race, and this is the sharpest technical objection to the thesis.**

**The crux is not interactivity — it is prediction.** Pre-signing a ladder removes
the per-granule signature but replaces it with something worse: the payer must
**predict future spend**, and pays for wrong guesses at the ladder top.

### D4 — Cashu already provides the property more cheaply → **MET by 3–4 orders of magnitude**

Cashu already delivers the bearer property today:
`src/tollwallet/port.go` models receive as *redeem-and-reissue* of a bearer
instrument, and `src/tollwallet/cdk_wallet.go:121` `Receive` needs only the
**router's** mint — the customer performs no mint interaction. The customer hands
over a token; the router does the round-trip.

**The unit the channel must amortise against — from the module's own pricing**
(`src/tollgate_protocol/validator_test.go`, `src/config_manager/config_manager_config.go:305`,
`README.md:351`): `step_size = 22020096 ms`, `price_per_step = 1 sat`.

> **1 satoshi buys 6.12 hours of internet. 64 sats (the module's own `MinBalance`)
> is 16.3 days.**

| Purchase | = internet | On-chain cost (716 sat @ 2 sat/vB) | Overhead |
|---|---|---|---|
| 1 sat | 6.1 h | 716 sat | **71,600%** |
| 64 sat | 16.3 d | 716 sat | **1,118.8%** |
| 1,000 sat | 254.9 d | 716 sat | 71.6% |
| 10,000 sat | 2,548.6 d | 716 sat | 7.2% |
| 100,000 sat | 25,486 d | 716 sat | 0.7% |

To get the on-chain cost under the 0.1% that a mint/Lightning fee costs, the
customer must prepay for **centuries** of internet:

| Target overhead | Channel needed | = internet | = fiat |
|---|---|---|---|
| 10% | 7,160 sat | 5.00 yr | $7 |
| 1% | 71,600 sat | 49.99 yr | $72 |
| 0.1% | 716,000 sat | 499.95 yr | $716 |

Settlement cost per purchase, Cashu vs channel (`SIM-OUTPUT.txt` T3):

| Purchase | Cashu | channel @2 sat/vB | ratio |
|---|---|---|---|
| 64 sat | 0.06 | 718 | **11,218×** |
| 1,000 sat | 1.00 | 746 | **746×** |
| 10,000 sat | 10.00 | 1,020 | **102×** |
| 100,000 sat | 100.00 | 3,757 | **37.6×** |

**Cashu is cheaper at every size the product actually sells.** The one-way
*Bitcoin* channel is a solution to a problem this product does not have, at a
price this product cannot pay.

### D5 — A construction exists that is both non-custodial and chain-blind → **NOT MET**

None was found among the four built. The rig asserts the non-existence as an
executable check (§1). This is the one condition that would rescue the thesis,
and it is left as a runnable falsification target rather than a rhetorical claim.

---

## 3. What *is* true — the real product opportunity

**V1 is a defensible product, and it is not the brief's question.**

> "Non-custodial, no internet needed, works over the local link, but the payer
> must co-sign each granule" — that is real, and it is achievable.

Two things follow, and they are the actionable output of this spike:

1. **The increment is not "a payer with no internet buys from a router with no
   internet."** Code reading (`CODEFEAS.md`) shows that framing collapses: the
   payer only ever needed LAN + a resolvable MAC (`main.go:1092`), and the
   mint-facing work is the *router's*. Nothing in the tree makes a router's WAN
   conditional on it having been paid.
2. **The defensible increment is deferral:** the router accepts a customer's value
   **before** it can reach a mint, and settles later — removing the 30-second
   `Receive` block (`merchant.go:221`, `:1640`, `:1656`) and the
   `payment-outcome-unknown` class (`merchant.go:1691`) from the
   customer-facing leg.

For that, the router must (i) verify the instrument, (ii) open the gate locally —
which it **already can**: `AddAllotment` is pure memory (`merchant.go:2615-2649`)
and `valve.OpenGate*` is local (`lightning.go:753`, `:761`) — and (iii) defer
settlement. **(i) and (iii) are the whole new capability; the grant half is
already free.** So the deliverable is a *verify-and-defer seam plus an
adjudicator*, not a payment path.

**And this is a Cashu-level problem with a Cashu-level solution.** The project has
already committed to Cashu-native Spilman (`session-ticket-decision.md:371`) for
granularity. Granularity is a Cashu concern; an on-chain channel is the wrong
layer for it.

---

## 4. Track B — the customer-held LN-backed mint

*Operator's follow-on topology: the customer runs a mint backed by their own
Lightning node and uses it to buy internet; and the operator's own question —
"the mint would be offline when the customer is offline, so the tollgate could
swap with the mint, but the mint wouldn't be able to check the state of its
lightning channels. Is this a problem?"*

Full analysis: [`CUSTOMER-MINT-AND-INTEROP.md`](CUSTOMER-MINT-AND-INTEROP.md).

**Verdict: Track B survives the two obvious objections, and dies on a third.**

- **Not killed by recursion.** The bootstrapping is **nested, not circular**: the
  operator can pre-position liquidity over a *mobile* link days ahead, on a
  device that is not the LAN being sold to. Needs internet *once, at
  liquidity-acquisition*, not at purchase.
- **Not killed by the mint's LN illiquidity.** A mint is a fractional-reserve
  credit intermediary, not necessarily an LN routing node. **Signing a NUT-03
  swap requires only the keyset private key** — not a live LN node. So credit
  works offline; only debit (melt) fails.
- **The operator's question, answered precisely.** The mint's inability to see
  its LN channel state is **both** a liveness problem (it cannot melt) **and** a
  safety problem (it may credit a note against a channel force-closed while it
  was offline — and on a stale-commitment race, **the customer pays**). The
  safety half is the one that costs money.

**The decisive finding — a one-way channel solves a *different* problem:**

> The construction spans **two disjoint graphs.** Graph 1 = customer's mint ⇄
> TollGate's node (LN channels). Graph 2 = customer ⇄ router (the LAN purchase).
> A Spilman/CSV channel is a **Graph 2** object. **Nothing in it writes, reads,
> or constrains any LN channel in Graph 1.**

So the channel cannot fix the mint's melt failure (an LN liquidity fact) or its
force-close exposure (an LN commitment-race fact). And then the inversion:

> Track B's mint **is a custodian**. A one-way channel's entire purpose is to
> **be** the trustless substitute for a custodian. Deploying both at once keeps
> the custodian **and** pays the channel's deadline cost. The coherent version of
> Track B is not "customer runs a mint *and* the purchase is a channel" — it is
> **"the customer runs no mint, and funds the channel directly."**

**And the mechanism that makes the exchange unfavourable:** a channel does not
*delete* the time obligation, it **relocates it and removes the reconciler** —
from the authority's obligation to answer within 30 s, to the holder's obligation
to act before expiry, with **no reconciliation step** after expiry (whereas #239
routes its ambiguity to an operator).

**Chain-blindness verdict: deferred-online, not blind.** The *payer* can be
chain-blind; the *construction* cannot, whenever a CSV refund is the safety
property — a relative lock is enforced by *confirmation*, so someone must
broadcast at/after height H. The only bridge-less relay path makes **the router a
liveness dependency for the customer's own refund**.

---

## 5. LNURLcash (LUD-25/LUD-26) — the container, and a custody trap

Full review: [`LNURLCASH-REVIEW.md`](LNURLCASH-REVIEW.md). Concise handover post:
[`LNURLCASH-REVIEW-POST.md`](LNURLCASH-REVIEW-POST.md). Source:
<https://github.com/lnurl/luds/pull/301> (branch `lnurlcash`, `25.md` 256 lines +
`26.md` + vectors; author `dni`; status `draft`).

**This is the most constructive finding in the spike.** LNURLcash is not a
competing protocol — it is **the container this spike would otherwise have had to
invent**:

> Every note is a **BIP-341 taproot output key `Q`**, spent by a witness Bitcoin's
> own script rules accept for `Q`: a hashlock for a plain bearer secret, a
> signature for a key-owned note, or **any other tapscript condition (timelocks,
> multi-party leaves)**, which makes the note a *programmable* withdraw link.

A one-way channel **is** a timelock+multisig leaf. So the right framing is not a
new instrument but a question: *can a channel leaf be expressed as an LNURLcash
note?*

**Three findings, and one is a blocker:**

1. **`cs1` gives offline verification of ISSUANCE, and the spec itself bounds it:**
   *"A certificate proves issuance, not that the note is still outstanding: the
   previous holder still knows the spend. A new holder SHOULD rotate (or merge) as
   soon as it is online."* So offline verification is issuance-only;
   non-double-spend **requires going online**. That replay window is exactly what
   a channel's on-chain enforcement would close — the spike's increment, now
   precisely located.
2. **The custody trap, quoted verbatim from the spec:** *"A timelock honored by
   `SERVICE` is `SERVICE` asserting its own clock. It is a **custodial policy, not
   a consensus guarantee, and MUST NOT be presented as trustless**."* LUD-25
   timelocks are **advisory**. A channel implemented as a plain LNURLcash timelock
   leaf is therefore **custodial in precisely the dimension this spike is about**.
   The sharpest form of the question becomes: *can a channel leaf make both the
   double-spend window and the timelock **consensus**-enforced rather than
   `SERVICE`-clock-enforced?*
3. **A hard interop blocker.** `BIP-342` (tapscript) **disables `OP_CHECKMULTISIG`
   (`0xae`)**. The canonical Spilman leaf as written in §2 **cannot** be a
   tapscript leaf; it requires the `OP_CHECKSIGADD` form. Any construction that
   composes the two specs must be re-expressed before it can even be encoded.

Also worth carrying: LNURLcash independently arrives at taproot for the same
malleability reasons our rig found, and adds two footguns a script builder should
assert — reject `OP_SUCCESSx` outside pushed data, and never use a public key that
is neither empty nor 32 bytes in a signature check (BIP-342 lets any signature
pass against those).

**Direct spec conflict:** LUD-25 mandates **time-based** BIP-68 relative locks and
**refuses** block heights/counts. A channel's CSV is normally anchored to a
funding block height. Our rig hit exactly this area and got the flag wrong (a
silent 512-second-unit error). Reconciling *service-relative* `nSequence` with
*chain-absolute* CSV is a real design problem for any hybrid.

---

## 6. Prior art: the Decker thesis question, answered

This spike began from a question about a paper, and the prior-art review settled
it against primary sources.

**The claim:** Christian Decker supervised a master's thesis on Spillman-style
one-way channels.

**Verdict: directionally right, wrong on every specific — and the substance is
*stronger* than the claim.**

- **There is no payment-channel paper by Decker about one-way channels.** His
  OpenAlex works list (entity `A5084430723`, 67 works) contains the duplex paper
  and the Burchert funding papers, and nothing one-way.
- **Burchert's work is duplex/tree funding, not one-way.** *"Scalable Funding of
  Bitcoin Micropayment Channel Networks"*, SSS 2017 (`10.1007/978-3-319-69084-1_26`)
  and RS Open Science 2018 (`10.1098/rsos.180089`). Full-text search of the RSOS
  article returns **zero** hits for "unidirectional" and zero for "Spilman".
- **Decker *did* supervise a thesis on precisely this construction — a
  *semester* thesis.** From the archived ETH TIK theses database, row
  `Paying your Internet, One Byte at a Time | S | Christian Decker | HS 13 |
  SA-2013-49`. The PDF is live at
  `pub.tik.ee.ethz.ch/students/2013-HS/SA-2013-49.pdf`; title page: **Sandra
  Siby**, **"Supervisor: Christian Decker"**, **16 December 2013**. Abstract:
  *"This thesis builds a proof-of-concept that allows an access point to provide
  Internet access to untrusted users in exchange for payment in bitcoins, using a
  micropayment channel."* Implemented with bitcoinj; cites Hearn & Spilman as its
  channel reference.

> **Sharpest defensible statement:** Decker supervised an ETH **semester** thesis
> (SA-2013-49, **Sandra Siby**, Dec 2013) that built a working one-way-channel
> proof-of-concept **for selling WiFi Internet access** — the closest existing
> prior art to TollGate. It is not a master's thesis; the student is not
> Burchert; and the work *applies* the construction rather than studying one-way
> channels.

**A caution from the prior-art review that this spike independently confirms:** the
canonical Bitcoin-wiki description defeats the payer's double-spend via
nSequence-based mempool replacement — a mechanism the *same page* warns "was
disabled in 2010, and more recently the code has been removed completely". **Do
not build on it.** Our rig's `V0`/`V2` results are the modern restatement of that
same hazard.

---

## 7. What this means for TollGate — recommendations

Ordered, smallest first:

1. **Do not build an on-chain one-way channel for TollGate.** Not because it is
   impossible, but because no variant is both non-custodial and payer-offline, and
   the settlement layer is 3–4 orders of magnitude too expensive against the
   product's own 1-sat-per-6.12-hours price.
2. **Keep the project's Cashu-native Spilman decision** (`session-ticket-decision.md:371`).
   It addresses granularity — the actual motivation — and it is the same layer as
   the money already is. Record clearly that it does **not** deliver
   custodian-freedom, so the claim is not made later.
3. **If cold start is the goal, fix the deferral seam, not the instrument.**
   The grant half already exists (`AddAllotment` is pure memory;
   `valve.OpenGate*` is local). What is missing is *verify-and-defer*: accept the
   instrument before the mint is reachable and settle afterwards. That is a
   Cashu-level change and it is genuinely non-duplicative with #239.
4. **Note that #239's bootstrap does not currently work.** `ForwardFirstProof`
   has **no production caller**; `bootstrap.go` has **no mint interaction at all**
   (imports are only `crypto/sha256`, `encoding/hex`, `fmt`, `sync`, `time`,
   `logrus`); the release notes call it experimental and awaiting router
   acceptance. Its cold start is an existence proof that a *reseller* can be
   funded by an *upstream* with mint access — a different claim. The report that
   "cold start is partially answered" was **wrong** and is corrected here.
5. **Do not build a channel on a tree whose crash-recovery lane is red.** #719
   shows `main` does not recover value when SIGKILLed between "mint signed the
   swap" and "wallet persisted". Channel state is *more* durable state than the
   router has ever held. Building on it would multiply an existing defect.
6. **Engage on LNURLcash rather than competing with it.** It is the container,
   its interop story is the reason to adopt it ("a wallet that does not know
   LNURLcash sees an ordinary withdraw link"), and its `cs1`/rotate model maps
   onto the bearer port's existence/consumption split. The handover post is
   written and ready to send.
7. **If a LAN mint is deployed, treat it as operator-custodial** — a LAN-hosted
   mint is a legal configuration (no host/scheme validation on mint URLs), but it
   answers "no internet" only by making the *operator* the custodian. It does not
   answer "no custodian", and it should not be presented as doing so.

---

## 8. Honest limits — what this spike did NOT establish

- **No channel was wired into the module.** `ARCH-FIT.md`'s delta **type-checks**
  (`evidence/archfit_delta.go`, `go vet`/`go build`/`gofmt` clean) but proves
  *well-typedness*, not *behaviour*.
- **Reorg behaviour is NOT measured.** `regtest` mine-on-command is not a reorg.
  Two specific hazards are argued from construction and flagged open: a reorg can
  flip which mutually-exclusive state won, and a CSV-age-triggered broadcast can
  be un-confirmed.
- **No LN implementation was built or run.** V4 is argued from the literature.
- **The economics are a deterministic simulator, not a fee-market model.** It does
  not predict fee variance; it shows the *scale* of the mismatch, which is 3–4
  orders of magnitude and cannot be closed by tuning.
- **`sim_economics.py` assumes** a 716-sat setup+close at 2 sat/vB and a 0.1%
  cashu melt; both are stated in `SIM-OUTPUT.txt` and are the only two
  assumptions in the table.
- **The module has no client implementation** in-tree, so what a real customer
  client could do with a channel instrument is not established from code.
- **Prior-art claims are cited to URLs**, with `[inferred]` and `[unverified]`
  marked inline; §6 of `PRIOR-ART.md` collects what could not be verified.

---

## 9. Errata — what running the rig against consensus caught

Recorded because they are the evidence that the GREEN result is a *test result*
and not a self-report, and because each is a trap a contributor will repeat.

| # | What the rig got wrong | How it was caught |
|---|---|---|
| **E1** | DER SEQUENCE length double-counted the tag bytes (`30 46` wrapping 68 bytes) | `mempool-script-verify-flag-failed (Non-canonical DER signature)`; localised by a structural DER check over 400 signatures — **all 400 malformed** |
| **E2** | BIP-68 type flag set for a **block-based** relative lock | Silently became a 512-*second* unit; Core rejected |
| **E3** | `CHECKMULTISIG` witness stack order — dummy in the wrong position | `mempool-script-verification` |
| **E4** | (see `rig/ERRATA.md`) | — |
| **E5** | Prose asserted `OP_CSV` was a *deadline* the payer must hit | The rig's own output contradicted the prose — CSV is a **minimum** delay, spendable forever. Corrected in §2/D3 and it *strengthens* the construction |
| **E6** | (see `rig/ERRATA.md`) | — |

**If this rig had used a simulator for these legs, all of them would have passed
silently** and this report would have contained several false claims.

---

## Appendix — deliverables

| File | What it is |
|---|---|
| `SPIKE-BRIEF.md` | the question as scoped, with the recon that shaped it |
| `PRIOR-ART.md` | one-way channel construction, trust assumptions, chain requirements, cited |
| `ARCH-FIT.md` | can a channel be a `WalletPort` backend? (verdict: beside it, not in it) + a type-checked interface delta |
| `CODEFEAS.md` | the module's actual purchase flow, per-step reachability, and the corrected #239 reading |
| `RISK-AND-IAC.md` | adversarial custody analysis, attacks, Cashu-dominance, disproof conditions, rig design |
| `CUSTOMER-MINT-AND-INTEROP.md` | Track B (customer-held LN-backed mint) + LNURLcash leaf-encoding walk |
| `LNURLCASH-REVIEW.md` | full LUD-25/26 review, divergence table, open questions |
| `LNURLCASH-REVIEW-POST.md` | the concise shareable handover post |
| `rig/` | the reproducible IaC: rig, run-rig.sh, check-env.sh, check-go.sh, ERRATA.md, out/ |
| `evidence/` | demo re-run output, and the type-check of the arch-fit delta |
