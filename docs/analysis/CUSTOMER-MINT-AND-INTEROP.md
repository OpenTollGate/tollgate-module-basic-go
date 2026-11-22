# CUSTOMER-MINT-AND-INTEROP — adversarial test of the customer-held LN-backed mint (Track B), and LNURLcash (LUD-25) as a container for the one-way channel

**Date:** 2026-10-14. **Module HEAD read:** `OpenTollGate/tollgate-module-basic-go` @
`cf7cad34677e87e775e7f16786b1bbf18c4bc8bc` (`main`). **Local clone verified present:**
`/tmp/spike/tmbg` (I ran the greps below myself; `git rev-parse HEAD` printed
`cf7cad34677e87e775e7f16786b1bbf18c4bc8bc`).
**Sources read first:** `SPIKE-BRIEF.md`, `CODEFEAS.md`, `ARCH-FIT.md`, `LNURLCASH-REVIEW.md`
(all in this directory). **Spec sources:** `25.md`
(`sha256 0cff63c2…5028a6`, 256 lines) and `26.md` (`sha256 67655944…c3c20f`, 92 lines) from
`https://raw.githubusercontent.com/lnurl/luds/lnurlcash/`, plus `25-vectors.json`, fetched
2026-10-14. **Chain evidence:** live mainnet `bitcoind` v`/Satoshi:31.1.0/` on DQ05,
`-datadir=/mnt`, height **971448**, queried read-only (raw output quoted in §1e and Appendix).

**Evidence classes, used throughout.** `[M]` = I measured it (a command I ran and whose output is
quoted). `[R]` = I reasoned it from cited rules. `[V]` = verified by executing arithmetic/I decoded a
published test vector. Nothing below is fabricated; where I could not answer, §"What would falsify"
names the test.

**Two tracks, kept distinct.**
- **Track A** (the naive framing: "customer has no internet gateway") is already **disproved** by
  `CODEFEAS.md:16-25` — the mint was never on the *customer's* path; it is on the *router's* path in
  every branch, including the #239 bootstrap branch, which in any case has **no production caller**
  (`CODEFEAS.md:328-339`; I re-confirmed: `grep -rn ForwardFirstProof src/` `[M]` returns only the
  definition `bootstrap.go:205`, a wrapper `upstream_session_manager.go:192-209`, an interface
  declaration `types.go:99-101`, a comment `session.go:255`, and tests). I do not revisit Track A
  beyond noting that Track B is a *different* topology, not a rescue of Track A.
- **Track B** (operator-proposed, quoted in the task): *the customer runs a mint backed by the
  customer's own LN node and uses it to buy internet from the TollGate*. This memo is its
  adversarial test.

**Headline.** Track B is **not killed by recursion** (the bootstrapping is nested, not circular) and
**not killed by the mint's own LN illiquidity** (a mint is a fractional-reserve credit
intermediary, not necessarily an LN routing node). It is killed by a subtler fact that the
operator's framing hides: **a mint's custody is precisely the thing a one-way channel exists to
remove.** Track B moves the customer's trust from the TollGate to the customer's own mint — but that
mint is a *third party the customer is not*, is offline exactly when its LN node must act, and a
one-way channel does **not** touch its LN channels. The construction does not justify channel work;
it justifies *deleting the mint* and having the customer fund a channel directly (§1c). On Part 2:
LUD-25 can **carry** the channel's script leaf, but it **cannot enforce** it — LUD-25's own timelock
rule is custodial-by-design and its canonical spend transaction is never broadcast, so a
channel-as-LNURLcash-leaf is custodial in exactly the dimension the spike is about (§2f-§2h). Verdict
§2i: **partial fit, not a container; a bounded experiment on the encoding only.**

---

# PART 1 — Adversarial test of Track B

## 1a. Bootstrap recursion — real, but nested, not circular

**Requirement to *create* the customer's mint (measured against the shape of a Cashu mint).** A mint
that issues notes and later melts them to pay LN invoices needs, on the day it first issues:

1. a **chain backend** (its LN node needs block + filter data to open/close channels and learn
   confirmations);
2. an **LN node** with at least one channel that has **spendable local balance** — i.e. an on-chain
   funding transaction, which itself needs a chain backend and an on-chain UTXO;
3. **inbound liquidity** on the channel facing the *TollGate* (or a route to it) so it can *melt*,
   because a melt is an outgoing payment (`CODEFEAS.md:95` `MintTokens` is the mint-side partner of a
   melt; `lightning.go:669`);
4. reachability of `{mint}/v1/keysets` from the router, because the module's accepted-mint set is
   built from mints that answered at boot (`CODEFEAS.md:79-81`, `merchant.go:494-509`).

**Requirement to *use* the mint (be online at redemption).** At the moment the TollGate's router
performs `Receive` — a NUT-03 swap at *the customer's* mint — the mint must answer an HTTP request
within the module's `defaultReceiveTimeout = 30s` (`CODEFEAS.md:71-74`, `merchant.go:221`, `1656`) and
the mint must be able to **sign the swap outputs** (`tollwallet.go:293-336` `[M]` read of the
`Receive` path). Signing does **not** require the mint's LN node to be up or its channels to be
current; it requires only the keyset's private key. **That distinction is load-bearing and it is not
fatal — it is what makes Track B survivable on the hot path.**

**Where the recursion actually bites: liquidity *acquisition*, not the recursion itself.** To fund
the channel facing the TollGate, the operator must do one of:

- **(i) buy bitcoin on-chain externally** (an exchange / on-ramp), then open a channel — this needs
  *some* internet on *some* device, but **not the LAN being sold to**, and **not at the moment of
  purchase** (the operator can pre-position the channel days ahead on a mobile link). **This is the
  rescue.**
- **(ii) receive LN** from an existing peer (needs inbound liquidity to already exist — circular
  unless the operator already runs an LN node with liquidity, which a customer buying internet
  *from a TollGate* plausibly does not);
- **(iii) be the TollGate's own counterparty** — the customer funds a channel *directly to the
  TollGate's node* and no LN path to the customer exists (the TollGate's inbound is only used when
  melting/swapping with third parties).

**Verdict (1a).** The recursion is real for option (ii) and is *broken* for (i) and (iii): the
customer needs internet **at liquidity-acquisition time (once, pre-positioned by any device)**, not
at redemption time. So the topology is **not self-defeating**; it is **"pre-funded once, then
operated offline."** Quantified, the irreducible requirement is: **one on-chain funding transaction
+ one channel open, established before the customer is offline.** The customer's own words ("the mint
would be offline when the customer is offline") are consistent with this and are the *design intent*,
not a bug. **[Reasoned]**, and the test that would settle it is in the falsification section.

**But note the cost hidden in (i):** if the customer pre-funds on-chain, then **bitcoin's
confirmation delay becomes the Tick's dependency** — a channel open is ~1-6 blocks (~10-60 min at the
measured rates below) and needs a chain connection during that window. So "offline immediately after
arrival" is false: the customer is **online-before** and **online-after**, and the whole construction
buys them only the *middle*. That is a genuine narrowing of what has been solved.

## 1b. The operator's actual question — "the mint can't check the state of its lightning channels"

This must be split into the two cases the operator ran together, because they have different
severities and different losers.

**Case (i) — the mint's LN node is *offline* but its channels are unbreached.**
- The mint **cannot melt** (an outgoing HTLC needs a live node + a connected peer).
- The mint **can still swap/sign** Cashu (keyset key is local). So a customer can still *deposit*
  notes at the mint and swap them at the TollGate.
- **Breakage:** the mint's *credit* side works, its *debit* side does not. That is **a liveness
  problem in the dimension of "can the mint ever discharge value," not "can value be issued."**
- **Who loses:** nobody *immediately* — and this is the crucial asymmetry. The **TollGate** is the one
  holding the swapped Cashu notes; the TollGate's own payout path is `MeltToLightning`
  (`CODEFEAS.md:281`, `merchant.go:1477-1484`), so **the TollGate is the party that needs the
  customer's mint to be *liquid*, not the customer.** The customer's gateway experience is fine; the
  TollGate is the one left holding a claim it cannot convert. **Severity: TollGate revenue risk, not
  customer loss.**
- **This is liveness, and it is exactly the risk class Cashu already has** — a mint is a custodial
  credit intermediary. **[Reasoned]** from NUT-03/NUT-04 semantics and `CODEFEAS.md:281`.

**Case (ii) — the mint's LN node has *stale chain state* / a channel was force-closed on it.**

Read the LN spec on what a force-close actually is. BOLT-05 §"Revoked Transaction Close Handling"
(`https://raw.githubusercontent.com/lightning/bolts/master/05-onchain.md:510-512`, fetched):
*"If any node tries to cheat by broadcasting an outdated commitment transaction …"*, and the
remedy is a **penalty transaction** with its own `to_self_delay`-gated path (`:196-205`, `:259`,
`:345`), fee-bumped until it confirms (`:577-578`). So:

- A force-close is a **race with a deadline on the honest party's side**: the punishing party must
  broadcast (and, post-anchors, **fee-bump**) within the `to_self_delay` window
  (`bolt05.md:568-574`, `:577-578`). If the mint's node is **offline across that window**, the
  *cheating* counterparty — or even an honest-but-outdated counterparty — can take the coin. This is
  **loss, not delay.**
- The mint, being **offline whenever the customer is offline**, is structurally the node *most* likely
  to miss that window. **This is the killer, and it is a SAFETY problem, not a liveness one.**
- **Who loses: the mint — i.e. the customer, in their role as mint operator.** So Track B does not
  move the offline-risk onto a third party. It moves it onto **the customer's own capital**.
- **And the customer cannot hedge it:** watching for a force-close while offline requires a chain
  watcher, which is exactly the "customer holds blockchain state" the spike forbids
  (`SPIKE-BRIEF.md:95-96`). A third-party watchtower restores the safety but **reintroduces a live
  third party** — which is the thing the whole construction was meant to remove.

**Precise verdict (1b): BOTH, and the safety half is the one that matters.** Case (i) is a liveness
problem that costs the *TollGate* and is reachable today with any Cashu mint. Case (ii) is a safety
problem that costs *the customer* and that the construction **creates** by making the mint operator
offline by design. A mint that is offline for hours is not merely slow; if it is offline across a
force-close `to_self_delay`, it loses the channel. **[Reasoned]**, grounded in BOLT-05 §"Revoked
Transaction Close Handling".

## 1c. Does a one-way channel solve that? — Verdict: **No. It solves a DIFFERENT problem.**

This is the crux the task asked me to be precise about. Work the graph literally.

- **The construction spans two disjoint graphs.** Graph 1 = *customer's mint ⇄ TollGate's node*
  (LN channels). Graph 2 = *customer ⇄ router* (the LAN purchase, and whatever instrument carries the
  value). A Spilman/CSV one-way channel is a **Graph 2** object: a pre-signed refund spendable after a
  relative timelock, adjudicated by the chain. **Nothing in the construction writes, reads, or
  constrains any LN channel in Graph 1.**
- Therefore a one-way channel **cannot** fix Case (i) or Case (ii). The mint's inability to melt
  (Case i) is an LN routing/liquidity fact; a channel on the LAN does not create inbound liquidity at
  the customer's node. The mint's force-close exposure (Case ii) is an LN commitment-race fact; a
  Spilman script has no opinion about it. **[Reasoned]; the falsification test is named in the last
  section.**
- **What a one-way channel *does* replace is real but is a different failure mode.** `ARCH-FIT.md`
  states it cleanly: the construction converts "the router must complete a NUT-03 swap within 30 s"
  into "a pre-signed tx is broadcastable eventually" (`ARCH-FIT.md:378-380`, `382-388`). That is a
  **LIVENESS requirement exchanged for a DEADLINE requirement** — the claim under test.
  The claim **is true as stated**, and it is **not** an exchange that helps Track B:

  | | Cashu mint leg (Graph 1) | One-way channel (Graph 2) |
  |---|---|---|
  | what is required | the mint **answers** (liveness, 30 s) | a tx **confirms before expiry** (deadline) |
  | who can act | mint, whenever it is up | redeemer, only inside the window |
  | if the window is missed | note stays unspent; retry | **refund branch wins; value gone** (`ARCH-FIT.md:456-480`, `:521-527`) |
  | whose failure is it | the mint's liveness | **the redeemer's own delay** |

  So the deadline construction **replaces an authority's obligation in time (liveness) with a
  holder's obligation in time (deadline)** — it does not delete the time obligation, it **relocates
  it, and removes the reconciler.** `ARCH-FIT.md:479-480` and `:525-531` already name this as the
  disqualifier: a channel's failure mode has *no reconciliation step* after expiry, whereas #239
  routes its ambiguity to an operator (`bootstrap.go:244-249`).
- **The sharpest form of the verdict, and the one to carry forward:**
  > Track B's mint is a **custodian**. A one-way channel's entire purpose is to **be** the
  > trustless substitute for a custodian. Deploying both at once keeps the custodian *and* pays the
  > channel's deadline cost. The coherent version of Track B is not "customer runs a mint **and**
  > the purchase is a channel" — it is **"the customer runs no mint, and funds the channel directly."**
  > That construction exists, needs no mint, and is the only reading of Track B that justifies any
  > channel work at all.

  In the channel-direct reading, the customer's role collapses to: hold the funding key, sign the
  refund branch (or simply give the TollGate a 2-of-2 claim), go offline. **No mint. No LN node. No
  liquidity to acquire beyond the channel's own funding.** That is strictly simpler *and* it is what
  the operator was reaching for.

**Verdict (1c): a one-way channel solves a different problem (customer↔router) than the one the
operator asked about (the mint's LN channels). It does not rescue Track B. It renders Track B
redundant.** **[Reasoned]** — this is an argument about what the construction touches, and it is
decidable by inspection of the constructions' object sets, not by a measurement.

## 1d. Chain blindness — **deferred-online, not blind.** A "chain-blind payer" is a real category; a
"chain-blind *construction*" that includes a CSV refund is not.

Two distinct claims, and the operator's framing blurs them:

1. **The payer (customer) can be chain-blind.** TRUE and achievable: the customer signs pre-images /
   the funding key and then holds nothing chain-shaped. This is Spilman's actual property
   (`SPIKE-BRIEF.md:46-49`). Nothing in a one-way channel forces the *payer* to watch blocks.
2. **The construction can be chain-blind.** FALSE whenever a CSV refund is the safety property. A
   relative timelock is enforced by *confirmation*, not by *time passing*. To make the refund real,
   **someone must broadcast a transaction, at or after height H, and it must confirm.** Broadcasting
   needs a chain connection; it is only *optionally online at a moment of the holder's choosing* —
   which is the definition of **deferred-online**, not blind.

So: is there a live, bridge-less broadcast path? Only via a **mempool relay** (a third party that
carries the tx), which must itself be online and must be reachable by a party who, by hypothesis, can
reach only the router. Options: (a) the router relays after it acquires WAN — that is `ARCH-FIT.md`'s
explicit relaxation (`SPIKE-BRIEF.md:49-53`: "the router is the redeemer, and the router has (or will
have, once paid) WAN"), and it makes the router a *liveness dependency* for the customer's refund;
(b) a pre-signed tx handed to a third party at signing time — reintroduces a third party; (c) a
watchtower — same, and needs registration which needs connectivity.

**Verdict (1d): a "chain-blind payer" is real; a "chain-blind construction" is deferred-online with a
named relay.** For TollGate the relay would be the router, which converts "customer needs no chain"
into "**the router must be reachable after expiry or the customer loses**". That is the exact
liveness assumption `ARCH-FIT.md:456-467` calls *fatal for the router's own failure budget*, because
`AGENTS.md:29-31` treats router power-loss/process-kill as the base case. **[Reasoned]**; the
empirical test is a broadcast-window experiment (§falsification).

**Which of (a)-(d) need a test rather than reasoning.** (a), (b), (c), (d) are all **answerable by
reasoning** and I have done so. The two facts that *need measurement* and that I could not measure
from this shell are named in the falsification section: (i) whether the module accepts a **LAN-hosted
mint** (I verified there is no host/scheme guard — see below — but "architecturally legal" ≠
"project-legal", `CODEFEAS.md:391-402`); and (ii) the observed end-to-end `receiveTimeout` worst case
(`CODEFEAS.md:413-416`).

## 1e. Fee / expiry arithmetic — with REAL queried numbers

**Method.** `[M]` Live queries against the DQ05 mainnet node, `bitcoind` `/Satoshi:31.1.0/`,
`-datadir=/mnt`, height **971448**, `getblockchaininfo.latest_time = 1792000846`. Raw output quoted
verbatim in the Appendix. `[V]` Transaction size computed from BIP-341/342 rules by arithmetic
(no tx was built or broadcast — I did **not** have a funded wallet, so this is a size *computation*,
not a broadcast measurement).

**Measured `estimatesmartfee` (BTC/kvB → sat/vB):**

| target | BTC/kvB | sat/vB |
|---|---|---|
| 1 | 0.00001070 | **1.07** |
| 2 | 0.00001070 | **1.07** |
| 6 | 0.00000302 | **0.302** |
| 144 | 0.00000222 | **0.222** |

**Measured mempool state:** 71,747 tx / 34,282,429 vB / 193,086,144 WU; `mempoolminfee` and
`minrelaytxfee` both `0.00000100` BTC/kvB (= **0.1 sat/vB**); `fullrbf: true`. Measured fee
distribution of the live mempool `[M]` (`getrawmempool true`, 72,439 txs at query time): **68,940 tx
below 1 sat/vB**, 1,419 at 1–2, 1,732 at 2–5, 291 at 5–10, 45 at 10–20, 7 at 20–50, 2 at 50–100, 3 at
100–500, 0 above 500 sat/vB. **The chain is quiet; the mempool is a backlog of sub-1-sat/vB txs.**

**Historical fee tails `[M]` (`getblockstats`):**

| height | p10 | p50 | p90 | max | totalfee |
|---|---|---|---|---|---|
| 971448 (latest) | 0 | 0 | 0 sat/vB | 50 | 389,233 sats / 7,297 tx |
| 840000 (halving) | 108 | **200** | 1063 sat/vB | 3,604,819 | 3,762,561,499 sats |
| 818000 | 68 | 92 | 232 sat/vB | 1461 | 127,636,729 sats |
| 810000 | 3 | 4 | 29 sat/vB | 256 | 10,212,490 sats |

**The refund transaction `[V]` (computed).** Canonical Spilman leaf, tapscript-corrected
(§2f) at 114 B, single-leaf control block 33 B, witness stack `[sig, script, control]` per
`25.md:83`; 1-in/1-out, P2TR output, `nVersion=2`:

```
base    = 4 + (1+36+1+4) + (1+8+1+34) + 4 =  94 B
witness = 217 WU (item-count 1 + sig 1+64 + MINIMALIF selector 1+1 + script 1+114 + control 1+33)
weight  = 94*4 + 217 = 593 WU        vbytes = ceil(593/4) = 149 vB
```

**Fee to broadcast the refund at MEASURED rates `[V]`:** 1.07 sat/vB → **159 sats**; 0.302 sat/vB →
**45 sats**; 0.222 sat/vB → **33 sats**.

**Refund fee as a fraction of channel value, at today's measured rates:**

| channel | @0.222 sat/vB (144 blk) | @0.302 sat/vB (6 blk) | @1.07 sat/vB (1-2 blk) |
|---|---|---|---|
| 10,000 sats | 33 sats = **0.33 %** | 45 sats = **0.45 %** | 159 sats = **1.59 %** |
| 100,000 sats | 33 sats = **0.03 %** | 45 sats = **0.04 %** | 159 sats = **0.16 %** |

**Break-even feerates `[V]`** at which a **149**-vB refund consumes the channel:

| channel | 1 % of value | 10 % | 50 % uneconomical | 100 % total loss |
|---|---|---|---|---|
| 10,000 sats | **0.67 sat/vB** | 6.71 | 33.56 | 67.11 sat/vB |
| 100,000 sats | 6.71 sat/vB | 67.11 | 335.57 | 671.14 sat/vB |
| 1,000,000 sats | 67.11 sat/vB | 671.14 | 3,355.7 | 6,711.41 sat/vB |

**When the refund becomes uneconomical — the honest answer.**

1. **Today, never, for any channel; but the *floor* is not zero.** At the measured 0.1 sat/vB
   relay floor and `fullrbf: true`, the refund can be broadcast for **15 sats** (~0.15 % of a 10k
   channel) *and replaced* if the market rises — `fullrbf` allows fee-bumping a stuck refund
   `[M]`. So the *static* pre-signed fee is not the binding constraint in a quiet market; the
   *replacement* is.
2. **A 10,000-sat channel dies at ~0.67 sat/vB** — i.e. **the moment fees are at all normal**. Even
   1 sat/vB (below today's 1–2 sat/vB band, which holds 1,419 txs) eats 1 % of a 10k channel; a mild
   congestion event (818000's p50 = 92 sat/vB) makes a 10k channel's refund cost **13,708 sats =
   137 % of the channel** → **the refund is unbroadcastable and the customer's refund branch is
   worthless.** At the 840000 halving (p50 = **200 sat/vB**), a 10k channel's refund costs **29,800
   sats = 298 % of the channel**. `[V]` from `[M]` rates.
3. **Only a ~1,000,000-sat (0.01 BTC) channel survives a real fee spike**: 671 sat/vB (10 % of value)
   is at 840000's p90 order of magnitude (1063 sat/vB measured at p90 of that block). So the
   construction's economics **require a large channel** — and a large channel is exactly what a
   TollGate *purchase* is not (the task's own framing: 0.0001–0.001 BTC / 10k–100k sats). **There is a
   10–100× mismatch between the channel size the fee market needs and the purchase size TollGate
   sells.**
4. **The expiry arithmetic compounds it, and the units are wrong for LUD-25.** A relative timelock
   must be long enough that the redeemer can be online inside it (`ARCH-FIT.md:456-467`), i.e.
   days-to-weeks; but the value at stake per channel is sats-to-k-sats. A long expiry funded by a
   small amount is a capital-inefficiency: the customer's 10k sats sit locked for the whole window to
   buy a much smaller amount of internet. **And note the BIP-68 unit ceiling for Part 2:**

   `[V]` BIP-68 *time-type* locks have **512 s granularity**, and LUD-25 mandates the time type
   (`25.md:64`: "an enabled relative lock MUST carry BIP-68's time-type flag; block heights and block
   counts MUST be refused"). So an expiry is quantised to 512 s and encoded in `nSequence` as
   `0x400000 | ceil(seconds/512)`: 1 day = 169 units (`0x4000a9`), 2 weeks = **2,363 units
   (`0x40093b`)**, 30 days = 5,063 units. All fit a `u32`. **This is a real, arithmetic conflict with
   the channel's natural block-based CSV — resolved in §2f.**

**Verdict (1e): the pre-signed refund is economical today and uneconomical almost immediately**; the
crossover for a 10k-sat channel is **0.67 sat/vB**, and the historical evidence (`[M]` p50 = 200
sat/vB at 840000) shows the crossover is exceeded by two orders of magnitude in normal congestion.
**A fixed-fee pre-signed refund is not a viable bearer instrument at TollGate purchase sizes.**

---

# PART 2 — LNURLcash (LUD-25) interop

## 2f. Can the leaf be expressed as a `cw1` script-path spend? — **Encoding: yes, with two corrections. Semantics: no (see 2h).**

**The `cw1` byte layout.** Per `25.md:98-104`, verbatim: *"`cw1` payload, integers big-endian:
`u32 locktime || u32 sequence || (u16 len || item)*` over: script, control block, then the witness
items bottom of stack first."*

**I ground-truthed this against the spec's own vector `[V]`, not from memory.** I bech32m-decoded
`25-vectors.json` → `bearer_note.cw1` (`cw1qqqqqq8lllll7qpr4qsxxr…`) and the payload is **114 bytes**,
parsed as:

| field | value | matches |
|---|---|---|
| `u32 locktime` | `0x00000000` | `25.md:48` (bearer cw1: locktime 0) |
| `u32 sequence` | `0xffffffff` | `25.md:48` (sequence `0xffffffff`) |
| `item[0]` len 35 | `a820630dcd…0dd87` | = `bearer_note.leaf` ✓ (the 35-byte `OP_SHA256 <h> OP_EQUAL`) |
| `item[1]` len 33 | `c050929b…3ac0` | = `bearer_note.control_block` ✓ |
| `item[2]` len 32 | `000102…1e1f` | = `bearer_note.preimage` ✓ (the sole witness item, bottom-of-stack-first) |

and the length prefixes **consume the payload exactly** (114 = 8 + 37 + 35 + 34), which is precisely
what `25.md:95` requires (`SERVICE` MUST reject "length prefixes that do not consume the payload
exactly"). The spec says a bearer note's `cw1` is **192 characters**; my decoder + recomputation gives
**exactly 192** `[V]`. **The layout in the task is confirmed correct.**

**Finding 2f-1 (the encoding fits): the Spilman leaf is expressible, with one substitution.**
`25.md:12` explicitly permits "any other tapscript condition (**timelocks, multi-party leaves**)", and
`25.md:27` requires `SERVICE` to accept any spend BIP-341/342 accepts. So the *container* is
indifferent to the leaf. **BUT** — `[M]` — BIP-342 **disables `OP_CHECKMULTISIG`**
(`bip-0342.mediawiki:38`: *"The inefficient `OP_CHECKMULTISIG` and `OP_CHECKMULTISIGVERIFY` opcodes
are disabled"*; `:72`: they "behave in the same way as `OP_RETURN`, by failing and terminating the
script immediately when executed"). So the task's leaf

```
OP_IF <expiry> CSV DROP <payerPub> CHECKSIG OP_ELSE 2 <payerPub> <payeePub> 2 CHECKMULTISIG OP_ENDIF
```

**is invalid tapscript** — it contains `OP_CHECKMULTISIG` (0xae) and would fail immediately. The
BIP-342-legal rewrite is `OP_CHECKSIGADD` (`bip-0342.mediawiki:76`, which gives the exact
translation of `m <pk_1>…<pk_n> n CHECKMULTISIG` → `<pk_1> CHECKSIG <pk_2> CHECKSIGADD … <pk_n>
CHECKSIGADD m NUMEQUAL`, witness `0 <sig_1>…<sig_m>`):

```
OP_IF <expiry> OP_CSV OP_DROP <payerPub> OP_CHECKSIG
OP_ELSE <payerPub> OP_CHECKSIG <payeePub> OP_CHECKSIGADD OP_2 OP_NUMEQUAL
OP_ENDIF
```

That grows the leaf from **113 B to 114 B** `[V]`. Note also `[M]` `bip-0342.mediawiki:73`: tapscript
makes **MINIMALIF consensus**, so the `OP_IF` branch selector must be exactly empty or `0x01` — a
witness item nobody usually thinks about, and it is the *first* element the `IF` consumes.

**The complete byte-level result `[V]`** (payload = `8 + (2+leaf) + (2+control) + Σ(2+item)`):

| spend | witness items (bottom-of-stack-first) | payload | `cw1` chars |
|---|---|---|---|
| refund / `IF` branch | `[sig64, selector=0x01]` | **228 B** | **374** |
| claim / `ELSE` 2-of-2 | `[sigP, sigQ, selector=empty]` | **293 B** | **478** |
| claim, 2-leaf control (65 B) | `[sigP, sigQ, selector=empty]` | **325 B** | **529** |
| *(spec's bearer note, for scale)* | `[preimage32]` | 114 B | **192** |

**What fits and what does not.**

- **FITS:** the script, the control block, the witness items, and the claimed `locktime`/`sequence` —
  the layout is generic, and a channel leaf is an ordinary tapscript. The **refund branch fits in 374
  chars**, under any sane URL limit.
- **FITS WORSE:** the claim branch is **478 chars**, **~2.5× the bearer note's 192** and **~3× a
  `ck1`'s 163**. Against `25.md:190`'s measured URL budget (*"commonly around 2000 characters: about
  20–30 hex bearer notes, 8–12 `ck1`s, **7–10 bearer `cw1`s**"*), a channel note fits **only ~4 per
  merge URL** `[V]`. So a channel is a *costly* note in the merge/rotate algebra — and that algebra
  is LUD-25's entire added value.
- **DOES NOT FIT:** the `cw1` encodes an `expiry` **as an absolute value inside the script**
  (`<expiry>` follows `OP_IF`), plus a **relative** `nSequence` in the payload's `u32 sequence` field.
  The **same payload must carry both**, and they are different kinds of number. That is where the
  semantics break — next.

**Finding 2f-2 (the conflict, resolved): BIP-68 time-type vs block-based CSV.** The task and
`LNURLCASH-REVIEW.md:157-160` name this; here is the resolution.

- LUD-25 **mandates time**: `25.md:64` — *"an enabled relative lock MUST carry BIP-68's time-type
  flag; **block heights and block counts MUST be refused**"*. `nSequence` bit 22 (`0x400000`) is the
  time-type flag; the low 16 bits are **512-second units** `[V]`.
- A channel's CSV is **naturally block-based** — the funding tx's confirmation anchors the counter,
  and every Lightning `to_self_delay` (BOLT-05 `:196-205`, `:259`, `:345`, `[M]`) is a **block count**.
- **These are not reconcilable by re-encoding, only by re-anchoring.** The two are *different clocks*:
  LUD-25's relative lock is anchored to **when `SERVICE` credited the note** (`25.md:63`: *"whose
  `nSequence` enables a relative lock that has not yet elapsed since `SERVICE` credited the note (by
  mint, rotate, split or merge)"*), which is a **service-clock** event; a channel's CSV is anchored to
  **the funding tx's confirmation height**, which is a **chain-clock** event. `LNURLCASH-REVIEW.md:153-156`
  flags exactly this and I confirm it is a real, not cosmetic, gap:
  - If you take LUD-25's time-based lock, you must pick a `locktime`/`sequence` whose **unix time**
    is the deadline, and then **`SERVICE` is the one that checks it** (§2h) — so the CSV is decorative.
  - If you take the channel's block-based CSV, **`SERVICE` MUST refuse it** per `25.md:64`.
  - **There is no encoding in which a single `cw1` carries both a service-relative time lock and a
    chain-anchored block lock.** The conflict resolves only by choosing which clock is authoritative
    — and that choice *is* the custody question (§2h).
- **Secondary constraint `[V]`:** `nLockTime` must be a unix time ≥ 500,000,000 and fit a `u32`
  (`25.md:64`), ceiling **4,294,967,295** = **2106-02-07**. So absolute-time expiries are bounded at
  ~2106. Non-binding, but worth recording.

**`cs1` and the leaf.** `cs1` is a recoverable ECDSA signature over
`"LNURLcash:" || amount_msat || ":" || hex(Q)` (`25.md:209-216`), and `[V]` I confirmed the vector:
`message = LNURLcash:1000:690ac338…a99f` (for the key-path note) and `LNURLcash:1000:d18b6196…0982`
(for the bearer note) — note the **bearer note's `cs1` is over the *bearer* leaf's `Q`, not the fast
note's**. Key point for a channel: **the channel leaf checks a *signature*, so the resulting `Q` is
domain-bound** (`25.md:85`: *"A leaf that checks no signature, such as a bearer note's, is bound to no
`domain`"* — the contrapositive is that a signature-checking leaf **is**). That is *good* for
anti-replay and means the channel variant gets a protection the plain bearer note lacks.

## 2g. `cs1` (offline issuance proof) vs the channel's refund branch — **complementary, different questions**

Be precise about the two questions, because they are the port's own two questions
(`bearer-instrument-port.md:26-28`, quoted in `ARCH-FIT.md:327-330`):

| | `cs1` | channel refund branch |
|---|---|---|
| **question answered** | **ISSUANCE** — "did `SERVICE` credit this `Q` with this amount?" (`25.md:198-216`) | **CONSUMPTION** — "has this value been spent, and which branch won?" |
| **who answers** | `mintPubkey`, **offline**, by signature recovery (`25.md:230-232`) | **Bitcoin consensus**, by which tx confirms |
| **proof type** | signature over `(amount, Q)` — *copied, never consumed* | spend of a UTXO — *consumed, winner-take-all* |
| **replay** | `cs1` is **copyable**; the spec's own warning: *"A certificate proves issuance, **not that the note is still outstanding**: the previous holder still knows the spend"* (`25.md:234-235`) | the pre-image/signature is **single-use**; the first confirmation wins |
| **offline?** | **yes, fully** (`25.md:228-232`, steps 1-3) | **no** — needs broadcast + confirmation (`§1d`) |
| **domain-bound?** | no (`cs1` is over `Q`, and `25.md:85` says a signature-less leaf is unbound) | **yes** — a signature-checking leaf is bound to `domain` (`25.md:85`) |

**So: they overlap in *nothing* and are strictly complementary.** `cs1` gives the TollGate an
**offline existence proof** (which answers Track A's "no mint in the customer path" for the *verify*
step — `LNURLCASH-REVIEW.md:114-135` calls this the hybrid, and it is the genuinely interesting
result). The channel's refund branch gives the **consumption** answer that `cs1` explicitly cannot
(`25.md:234-235`). Mapping onto the port's rules: **existence from the issuance log ↔ `cs1`;
consumption from the authority ↔ the chain** — which is exactly `ARCH-FIT.md:336-337`'s
`existence`/`consumption` split. **The two specs fit the port's two rules like a key in a lock.**

**But that complementarity is the *only* thing that fits, and it is not enough to rescue the
channel:** the port's rule is *"`SPENT` and `UNSPENT` are things the authority **said**. Anything else
is `UNKNOWN`, and `UNKNOWN` fails closed"* (`bearer-instrument-port.md:65-66`, quoted
`ARCH-FIT.md:139-146`). A channel's consumption answer **is** `UNKNOWN` until adjudication — and
`UNKNOWN` fails closed (`ARCH-FIT.md:147-154`). So `cs1` answers existence *and says so*, while the
channel answers consumption *with silence*. Silence is the one thing the port refuses.

**The sharpest way to state 2g:** `cs1` closes the **replay window** (`25.md:234-235`'s "previous
holder still knows the spend") — but it closes it **offline and provisionally**; the channel would
close it **on-chain and finally**. The spike's increment, precisely located, is *"can we upgrade a
provisional, copyable issuance proof into a final, consensus-enforced consumption proof?"* That is a
real question — and §2h says LUD-25 answers it **no**.

## 2h. Is a channel-as-LNURLcash-leaf custodial in the dimension the spike cares about? — **YES.**

**The spec says so in one sentence, and it is decisive.** `25.md:66-67`, verbatim:

> *"A timelock honored by `SERVICE` is `SERVICE` asserting its own clock. It is a custodial policy,
> not a consensus guarantee, and MUST NOT be presented as trustless."*

**Why that sentence governs the whole construction — the mechanism, stated precisely.** LUD-25 does
not evaluate the spend by broadcasting it. It builds exactly one **"canonical spend transaction"**
(`25.md:69-83`): `nVersion=2`; `vin[0]` prevout `(tagged_hash("LNURLcash/mint", domain), 0)` — a
**fictive outpoint**; `vout[0]` value `0`, empty `scriptPubKey`; `nLockTime` and `nSequence` *as
claimed*; spent output `(OP_1 <Q>, 0)`. **`[M]` I verified this against the vector**: the
`key_path_spend.spend_tx` hex `02000000000101d5ac2de3…b2 00000000 00 ffffffff 01 00000000 00000000 00 …`
spends prevout txid `d5ac2de3…d6b2` vout `0`, pays **0 sats** to an **empty** script, and
`25.md:71` says it outright: *"Every note is spent as input 0 of one fixed, **never-broadcast**
transaction."*

Therefore:

1. **`SERVICE` is the enforcement point, not the chain.** Bitcoin never validates a note spend.
   `SERVICE` runs the script interpreter (`libbitcoinkernel`, `25.md:12`) **in its own process,
   against its own view of the time claim**, and then mutates its own DB.
2. **`SERVICE`'s clock is the clock.** `25.md:63`: it rejects a relative lock "that has not yet
   elapsed **since `SERVICE` credited the note**". The anchoring event is a **service DB write**, not
   a block.
3. **So the `csv1` refund branch on a channel leaf is enforced by nobody but `SERVICE`.** The
   customer's "I can reclaim after expiry" is a promise that `SERVICE` (a) is up, (b) is honest, and
   (c) agrees about the time. That is **custody**, in exactly the axis
   (`SPIKE-BRIEF.md:98`: "Custody is a spectrum, not a boolean. Name the trust assumption exactly")
   that the spike is about.
4. **It is even worse than plain Cashu custody in one way and better in another — say both.**
   *Worse*: the value is locked under a script whose only enforcer is the service, so the customer's
   exit depends on the service's *policy*, and `25.md:66-67` explicitly forbids calling that
   trustless. *Better*: the channel leaf is **domain-bound** (`25.md:85`) and **taproot-committed**,
   so the note cannot be re-pointed at another service; and the **refund branch is not something
   `SERVICE` can silently refuse** in the sense that a plain bearer preimage could be
   (`ARCH-FIT.md:525-527` names the channel's *no-reconciliation* problem, which applies here too).

**What it would take to make it consensus-enforced — and is that within LUD-25's model?** You would
have to make the note a **real** on-chain UTXO: fund `Q` (or a leaf-carrying P2TR output) with actual
sats, spend it in a **broadcast** transaction, and let CSV be enforced by *confirmation height*.
That is **exactly the thing LUD-25 does not do**, and three separate spec rules preclude it:

- `25.md:71` — one fixed, **never-broadcast** transaction;
- `25.md:76-79` — the spent output is `(OP_1 <Q>, 0)`: **value 0**; there is no UTXO to fund;
- `25.md:64` — **block heights and block counts MUST be refused**; only the time type is legal.

So: **it is not within LUD-25's model, and the conflict is not a bug to fix — it is LUD-25's design
choice.** LNURLcash deliberately trades consensus enforcement for an offline-verifiable bearer note
that needs no wallet chain state. `LNURLCASH-REVIEW.md:69-91` already frames this correctly, and my
independent read of `25.md` confirms it: **the two systems are trying to buy different things.**
LUD-25 buys *offline verifiability*; a channel buys *consensus-enforced non-double-spend*. You may
have one, by construction, not both — because the offline proof (`cs1`) is over a copyable
signature and the consensus proof is over a non-copyable UTXO condition, and the same `Q` cannot be
spendable both ways (`25.md:20-23` gives exactly two spends, key path and script path, both against
the fictive prevout).

**One genuine nuance worth crediting.** `25.md:12`'s delegation to `libbitcoinkernel` means LUD-25's
*script verification* is the consensus rule — a reimplementation cannot drift. That is the best
decision in the document (`LNURLCASH-REVIEW.md:197-200` agrees) and it means the leaf is *correctly
verified*; it does not mean it is *consensus-enforced*, because verification is applied to a
never-broadcast transaction. `SERVICE` verifies the spend **and then decides**, which is the
definition of custody.

**Verdict (2h): YES, custodial — in precisely the dimension the spike exists to remove.** A channel
expressed as a LUD-25 leaf has a `SERVICE`-asserted clock and a `SERVICE`-mutated ledger; the
`cw1`'s CSV is decorative.

## 2i. Recommendation

**Verdict: (c) a partial fit worth a *bounded* experiment — specifically on the e**ncoding only**,
not on the enforcement.**

- **(a) "the right container"? No.** LUD-25 is the right container for a **programmable bearer note**
  — and a channel is a programmable bearer note's **spend condition**, which is why the encoding
  fits (§2f). But "container" implies it delivers the property that matters, and it does not: its
  enforcement is custodial by explicit design (`25.md:66-67`). Calling LUD-25 the container for a
  consensus-enforced channel is the exact overclaim `25.md:67` forbids ("MUST NOT be presented as
  trustless").
- **(b) "a distraction"? Not entirely — no.** Three things in Part 2 are real and reusable:
  1. **`cs1` gives the TollGate a genuine offline existence check** — a *real* answer to the
     customer-path-verification half of Track A, requiring no chain and no live mint at verify time
     (`25.md:228-232`). That is worth having on its own merits, independent of any channel.
  2. **The `cw1` layout is confirmed generic and the leaf fits** (§2f), so if a channel ever ships,
     its note encoding is a solved, testable problem.
  3. **LUD-25's two footguns are worth adopting regardless** (`25.md:29-30`): `SERVICE` MUST reject
     any leaf with `OP_SUCCESSx` outside pushed data, and MUST NOT use a pubkey that is neither empty
     nor 32 bytes in a signature check. Both are real script-builder assertions, and the second is
     the *upgradable key types* trap.
- **(c) the bounded experiment I recommend — and its scope.** Build **the encoding, not the
  instrument**:
  1. Implement the `cw1` codec against `25-vectors.json` and **assert the bearer note's `cw1` is
     exactly 192 chars and round-trips** — that is a free, high-value conformance test (`[V]` I
     already reproduced 192 exactly).
  2. Encode the **tapscript-corrected** channel leaf (§2f, `OP_CHECKSIGADD`, 114 B) as a `cw1` with
     the canonical spend transaction, and **verify it with `libbitcoinkernel`** as the spec does
     (`25.md:256`). Deliverable: a passing vector, plus a documented, *failing* demonstration that
     `OP_CHECKMULTISIG` in the leaf is rejected. **That failing test is the finding.**
  3. **Do not** build a TollGate integration on it, and **do not** claim consensus enforcement.
  **Explicit non-goal:** making the timelock non-custodial inside LUD-25 (§2h shows it is impossible).
- **The honest one-liner for the PR body:**
  > LNURLcash can *carry* a one-way channel's leaf and can supply a genuinely offline issuance proof
  > (`cs1`), but its timelock is `SERVICE`-asserted by explicit design (`25.md:66-67`) and its
  > canonical spend transaction is never broadcast (`25.md:71`), so a channel-as-LNURLcash-note is
  > custodial in the one dimension the spike cares about. Partial fit: take the encoding and the
  > offline issuance proof; do not take the enforcement.

---

## What would falsify each conclusion

Each line names the **concrete test** that would overturn the section, in the order the sections appear.

| § | Conclusion | Falsifying test |
|---|---|---|
| **1a** | Bootstrapping is nested (needs internet once, at liquidity-acquisition), not circular | Stand up the customer's mint from a **cold, never-online** state using only a hardware wallet and a mobile hotspot for the funding tx; if the mint can serve `GET /v1/keysets` + sign a NUT-03 swap **offline** while its LN channels are unbreached, (a) is confirmed and the recursion is broken. |
| **1a** | The customer's *first* purchase still needs a channel-open confirmation delay | Measure wall-clock from "on-chain funding broadcast" to "router's `mintServesKeysets` returns true" (`tollwallet.go:230`). If it is 0 blocks, the narrowing claim is false. |
| **1b(i)** | An offline mint can still *sign* swaps (credit works, debit fails) | Point a CDK/nutshell mint at a node with its LN backend stopped; POST a NUT-03 swap; if it returns signed outputs, case (i) is confirmed as liveness-only. |
| **1b(ii)** | An offline mint loses the force-close race and the *customer* pays | Open a channel to a node you control, force-close the stale commitment from that node, and keep the "victim" node offline across `to_self_delay`; if the cheating commitment wins, the safety claim holds. |
| **1c** | A LAN channel touches no LN channel in the mint's graph | Adversarial read: enumerate every object a Spilman channel reads/writes; if it touches any LN commitment/HTLC, the graph-separation claim is false. Cheapest empirical form: run the §1b(ii) force-close **with** the channel deployed and show the outcome is unchanged. |
| **1c** | "Liveness → deadline" is a relocation, not a deletion | Build the channel, kill the redeemer across expiry, and show the refund confirms with a reconcile path. If a reconcile path exists after expiry, `ARCH-FIT.md:456-480` is wrong. |
| **1d** | A chain-blind *construction* requires a relay, so the router becomes the customer's liveness dependency | Deploy, then take the router offline from expiry to expiry+1, and see whether the customer's refund is broadcastable by any party the customer can reach. If yes (e.g. via mempool relay from the LAN), the "deferred-online" framing needs revision. |
| **1e** | Refund uneconomical above ~0.67 sat/vB for a 10k channel | Build the actual refund tx (not my size *computation*) with a funded P2TR output, `testmempoolaccept` it, and compare real vsize/fee to my 149 vB / 159 sats. Then re-run at a congestion event. **My 149 vB is COMPUTED, not broadcast-measured — this is the weakest number in the memo.** |
| **1e** | Fee-market mismatch needs a ~1M-sat channel | Obtain historical `estimatesmartfee` traces for 2017/2021/2024 spikes and compute the channel size at which a 149-vB refund stays under 10 % of value. If 10k sats always suffices, the mismatch is overstated. |
| **2f** | The corrected leaf is 114 B and the refund `cw1` is 374 chars | Encode and run through `libbitcoinkernel`; compare to my `[V]` arithmetic. If the sizes differ by >2 chars, my bech32m length model is off. |
| **2f** | `OP_CHECKMULTISIG` in a tapscript leaf is rejected | Submit the as-specified leaf to `libbitcoinkernel` with full flags; if it *passes*, `bip-0342.mediawiki:38,72` does not mean what I read. (**I read the BIP; I did not run the kernel.**) |
| **2f** | Block-based CSV is refused by LUD-25 | Encode a `cw1` with the time-type flag clear (block count) and POST it to `mint.lnurlcash.com` (the spec's own companion, `LNURLCASH-REVIEW.md:8-10`); if it is accepted, `25.md:64` is not implemented as written. |
| **2g** | `cs1` and the refund branch answer disjoint questions | Attempt to spend a note twice with a valid `cs1` and no rotate; if the second spend succeeds, `cs1` *does* answer consumption and `25.md:234-235` understates its power. |
| **2h** | The canonical spend tx is never broadcast, so CSV is custodial | Inspect `libbitcoinkernel`-based verification in `lnurl-mint`/`lnurl-wallet`: if the spend is ever put in a mempool, my reading of `25.md:71` is wrong. **Alternative test that needs no spec reading at all:** hold a timelock-gated note, set your own node's clock back, and try to spend it; if `SERVICE` refuses, its clock — not the chain's — is authoritative. |
| **2i** | LUD-25 is a partial fit, not a container | If a channel-as-LUD-25-note can be made consensus-enforced without broadcast, (a) becomes true and my recommendation flips. Per `25.md:64,71,76-79` I believe this is impossible; the test is to try. |

---

## Appendix A — raw measurements (verbatim, so every number is re-runnable)

**A.1 `ssh dq05 'bitcoin-cli -datadir=/mnt getblockchaininfo'`** (excerpt)
```json
{ "chain": "main", "blocks": 971448, "headers": 971448,
  "bestblockhash": "00000000000000000000a2918ec09ae0f46aa9ac2d2b41f0e2cc6d8c999e074e",
  "difficulty": 132716002350731.3, "time": 1792000846, "mediantime": 1791999339,
  "verificationprogress": 1, "initialblockdownload": false,
  "size_on_disk": 884078643780, "pruned": false }
```

**A.2 `getmempoolinfo`**
```json
{ "loaded": true, "size": 71747, "bytes": 34282429, "usage": 193086144,
  "total_fee": 0.06817374, "maxmempool": 300000000,
  "mempoolminfee": 0.00000100, "minrelaytxfee": 0.00000100,
  "incrementalrelayfee": 0.00000100, "unbroadcastcount": 0,
  "fullrbf": true, "permitbaremultisig": true, "maxdatacarriersize": 100000,
  "limitclustercount": 64, "limitclustersize": 101000, "optimal": true }
```

**A.3 `estimatesmartfee`** — `1` and `2` → `{"feerate":0.00001070,"blocks":2}`; `6` →
`{"feerate":0.00000302,"blocks":6}`; `144` → `{"feerate":0.00000222,"blocks":144}`.

**A.4 `getnetworkinfo`** — `"version": 310100, "subversion": "/Satoshi:31.1.0/"`,
`"connections": 10, "connections_in": 0, "connections_out": 10`.

**A.5 measured mempool feerate histogram** (`getrawmempool true`, 72,439 txs, 34,393,712 vB):
`<1 sat/vB: 68,940 | 1–2: 1,419 | 2–5: 1,732 | 5–10: 291 | 10–20: 45 | 20–50: 7 | 50–100: 2 |
100–500: 3 | >500: 0`.

**A.6 `getblockstats` feerate_percentiles** —
`971448: [0,0,0,0,0] maxfeerate 50 avgfeerate 0 totalfee 389233 txs 7297`;
`840000: [108,134,200,350,1063] maxfeerate 3604819 avgfeerate 3770 totalfee 3762561499 txs 3050`;
`818000: [68,75,92,171,232] maxfeerate 1461 avgfeerate 127 totalfee 127636729 txs 3542`;
`810000: [3,3,4,10,29] maxfeerate 256 avgfeerate 10 totalfee 10212490 txs 1690`.

**A.7 LN/chain infrastructure probe on DQ05** (`ps aux | grep -Ei 'lnd|lightningd|cln|eclair|ldk|cashu|cdk'`
and `ss -lntp`): **no LN node process and no LN port (9735/9736/10009) listening**; `*:8080` only.
**Consequence: §1b(i) and §1b(ii) were NOT measured on a live mint here** — DQ05 runs a chain node
only. The mint-offline tests in the falsification table need a mint host, which this shell does not
provide. Stated so the reader does not mistake my §1b reasoning for a measurement.

**A.8 code greps I ran in `/tmp/spike/tmbg` @ `cf7cad3`** — `grep -rniE 'btcd|bitcoind|electrum|\butxo\b|\btxid\b|OP_CSV|nsequence|txscript|SIGHASH' src/ --include='*.go'`
returns **2 hits, both in `_test.go` comments**, confirming the "no chain code in the module"
claim (`CODEFEAS.md:364-366`) with `btcd` appearing only as `// indirect` in `src/identity/go.mod:15-17`.
`bootstrap.go:3-8` imports are exactly `crypto/sha256, encoding/hex, fmt, sync, time, logrus`
(no mint).
`mintServesKeysets` (`tollwallet.go:230-248`) is a bare `http.Client.Get(mintURL + "/v1/keysets")`
with **no scheme, host, or private-IP validation** — confirming `CODEFEAS.md:394-402` that a
LAN-hosted mint is **architecturally** legal. **"Architecturally legal" is not "project-legal":**
all 7 shipped defaults (`config_manager_config.go:300-360`) are public `https://`, and I found no doc
permitting or forbidding a LAN mint. That remains an **operator decision, not a code reading**
(`CODEFEAS.md:391-402`).
`hasLockedProofs` (`tollwallet.go:521-529`) refuses any proof whose secret is not
`nut10.AnyoneCanSpend` before `Receive` (`tollwallet.go:296-298`), so **a locked/timelocked Cashu
instrument is refused by the router today** — a channel expressed as a *locked Cashu proof* (as
opposed to a taproot `cw1`) would land here first (`CODEFEAS.md:286-291`).

**A.9 `25.md` / `26.md` provenance** — `sha256` `0cff63c2c9f303717b7e04865f0a5b9084c1c4e13475c40437c181e82d5028a6`
(25.md, 256 lines) and `67655944e5e858f573ebf941510d8f8c87a5d80e894e639747bbd7c21cd3c20f`
(26.md, 92 lines), fetched from the `lnurlcash` branch; status `draft` (`25.md:4`). `LNURLCASH-REVIEW.md:163-166`
flags the stability risk; I confirm the header reads `status: draft` and the spec is unfinished.

## Appendix B — honest limits of this memo

1. **No mint and no LN node were run.** §1b, §1c are **reasoned from NUT semantics and BOLT-05's
   quoted revocation handling**, not measured. §1b's two cases need a mint host; DQ05 has none
   (A.7). This is the largest gap and it is the reason the falsification table's §1b rows are
   tests, not confirmations.
2. **No Bitcoin transaction was built or broadcast.** Every size in §1e/§2f is a **computation from
   BIP-341/342 rules**, cross-checked in one place against a published vector (the bearer `cw1`'s
   192 chars `[V]`). The 149-vB refund is the weakest number here.
3. **The mempool snapshot is one instant** (a quiet one: 68,940 of 72,439 txs below 1 sat/vB). The
   economic conclusions rest on the **historical** tail (A.6), not on today's quote — today's quote
   alone would understate the problem by two orders of magnitude.
4. **I did not run `libbitcoinkernel`.** My `OP_CHECKMULTISIG`-is-disabled finding is read from
   `bip-0342.mediawiki:38,72`, which is unusually explicit, but it is a spec read, not an execution.
5. **Track A is not re-litigated here.** Its disproof is `CODEFEAS.md`'s; I only note that Track B is
   a different topology and does not rescue it.
6. **Nothing in Part 2 was tested against a live LNURLcash service.** `mint.lnurlcash.com` exists
   (`LNURLCASH-REVIEW.md:8-10`) and is the obvious test surface; I did not POST to it.
7. **The recommendation deliberately excludes the one experiment that would be most informative**
   (a live consensus-enforced channel leaf) because §2h shows LUD-25 cannot host it. If that reasoning
   is wrong, the recommendation is wrong — and the falsifying test is in the table.
