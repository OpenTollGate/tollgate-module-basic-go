<!-- markdownlint-disable MD013 -->

# One-way channels as bearer instruments: a disproof, with the rig that proves it

**Status:** research result, 2026-10-14. Spike deliverable; companion to
[`bearer-instrument-port.md`](bearer-instrument-port.md) and
[`session-ticket-decision.md`](session-ticket-decision.md); supersedes neither.
Full analysis in [`docs/analysis/`](../analysis/); the reproducible rig in
[`research/one-way-channel-spike/`](../../research/one-way-channel-spike/).

## 0. The question and the answer

> Can a **one-way payment channel** be constructed so a payer with **no internet
> gateway** and **no custodian** can buy internet time from a TollGate — and does
> TollGate benefit?

**No — with the scope of the negative stated honestly.** Among the **four**
constructions actually built and run, none is both non-custodial and
payer-offline; and against the module's own advertised price the on-chain
settlement layer is **3–4 orders of magnitude** too expensive. The single
construction that does hold needs interactivity on the *local* link (which exists
by definition of the product) and is dominated by the Cashu path this module
already ships.

**What this is and is not.** This is a *failure to find* a custodian-free,
payer-offline construction over a specific and small search space — not a proof
that none exists. `D5` in [`RISK-AND-IAC.md`](../analysis/RISK-AND-IAC.md) §A5 is
labelled **NOT MET** for exactly that reason, and it is the one condition that
would overturn the conclusion. Do not cite this document as a proof of
impossibility; cite it as an exhaustive search of the constructions worth
building, plus a cost argument that does not depend on the search being
exhaustive.

The two `[FAIL]` lines in `GREEN.txt` (§6) are **assertions, not a proof
procedure**: they encode the claim in executable form so a contributor who
believes otherwise has a target to attack, but making them pass requires editing
the rig, which is a *manual* step. They are a signpost, not an oracle.

## 1. How to reproduce

```bash
bash research/one-way-channel-spike/rig/run-rig.sh    # writes rig/out/
```

| Step | Network need |
|---|---|
| `sim_economics.py` | **none** — pure arithmetic |
| `demo_channel.py` | **none** — a local `regtest` `bitcoind` on `127.0.0.1:18443` |
| `run-rig.sh` step 1 | once, only if `bitcoind` is absent (checksum-verified download); set `BITCOIND=` to skip |

**Every channel assertion is executed against Bitcoin Core v31.1 consensus.**
A transaction the rig signs is accepted only if the sighash and DER encoding are
byte-correct. That cross-check is the reason to use Core rather than a simulator —
and it was load-bearing: **four** bugs in the rig's own cryptography were caught
by Core *rejecting*, not by self-consistency, and are recorded in
[`ERRATA.md`](../../research/one-way-channel-spike/ERRATA.md) (E1–E4; the file's
E5 and E6 are separately labelled *not* bugs — a wrong prose claim and a finding).
A simulator would have passed all four silently and this document would contain
four false claims.

## 2. The construction, and where custody actually lives

```
OP_IF  <CSV> OP_CHECKSEQUENCEVERIFY OP_DROP <payerPub> OP_CHECKSIG     # payer refund
OP_ELSE OP_2 <payerPub> <payeePub> OP_2 OP_CHECKMULTISIG OP_ENDIF      # payments
```

The `ELSE` branch does the custody work: **every payment state is a 2-of-2
spend**, so a state exists only if the payer signed that exact output set. The
payee cannot mint a state with a larger payee output — asserted, tested, and
confirmed by Core rejecting a redeem-100% attempt.

**The crucial distinction this spike establishes: interactivity ≠ connectivity.**
Co-signing a state needs the payer reachable on the **local** link; it needs
**no internet** and **no chain state**. The customer is attached to the router's
Wi-Fi — a local link exists by definition of the product. So "no internet
gateway" was never the blocker it was assumed to be, and the question as posed
conflates a payment-protocol property (interactivity) with a transport-layer one
(connectivity).

## 3. The four constructions, and the custodian in each

| Variant | Live branch | Custodian | Non-custodial? | Payer offline? |
|---|---|---|---|---|
| **V0** redeemer signs alone | `<payeePub> CHECKSIG` | **the payee** | ❌ | ✅ |
| **V1** per-granule cosign | 2-of-2 | **nobody** | ✅ | ✅ *during a session, over the local link* |
| **V2** pre-signed ladder | 2-of-2 | **payee, up to ladder top** | ⚠️ | ✅ |
| **V3** Cashu-native Spilman (`cdk-spilman`) | 2-of-2 **at the mint** | **the mint** | ❌ | ✅ |
| **V4** duplex + revocation | per-commitment keys | nobody, **needs a watchtower** | ✅ | ❌ |

Two quantified findings, both measured:

- **V0 is one line away from V1 and admits a total loss.** The "payer-offline
  simplification" removes the payer's co-signature; Core then **accepts** a spend
  paying the payee 100%. This is not hypothetical — it is a two-line script
  change, and it is the trap the "chain-blindness is the crux" intuition is
  reaching for.
- **V2 is a 60% theft, with no forgery.** A 5-state ladder at a 100,000-sat
  granule lets a router that served **2** granules submit the **fifth** state and
  extract **300,000 sats**. Every state is validly signed and the states are
  mutually exclusive; the router simply submits the largest. Unfixable without
  per-granule interactivity, i.e. V1.

**V3 is worth naming explicitly because it is this project's committed
direction.** `session-ticket-decision.md` adopts Cashu-native Spilman for
granularity. It does not deliver custodian-freedom: the mint adjudicates, and the
decision doc's own accepted-risks paragraph records that the balance "sits at a
third-party mint" and that the receiver "must claim before expiry or the sender
refunds the whole channel". That is a deliberate trade — but the custodian should
not be claimed away later.

## 4. Why it does not pay: the price is 1 sat per 6.12 hours

The module's own constants (`src/tollgate_protocol/validator_test.go`,
`src/config_manager/config_manager_config.go`, `README.md`):
`step_size = 22020096 ms`, `price_per_step = 1 sat`.

> **1 satoshi buys 6.12 hours of internet.** The module's own `MinBalance`
> of 64 sats is **16.3 days**.

A channel's on-chain cost is **fixed per channel**; the purchase it settles is
not. At 716 sats of setup+close (2 sat/vB):

| Purchase | = internet | Overhead |
|---|---|---|
| 1 sat | 6.1 h | **71,600%** |
| 64 sat | 16.3 d | **1,118.8%** |
| 1,000 sat | 254.9 d | 71.6% |
| 100,000 sat | 25,486 d | 0.7% |

To bring on-chain overhead under the 0.1% a mint/Lightning fee costs, a customer
must prepay for **centuries** of internet (`716,000 sats = ~500 years`). Cashu is
cheaper at **every** size this product sells — 11,218× at 64 sats, 746× at
1,000 sats. The one-way *Bitcoin* channel is a solution to a problem this product
does not have, at a price this product cannot pay.

Three assumptions drive those tables, all declared in the sim: a 716-sat
setup+close at 2 sat/vB, a 0.1% Cashu melt, and a BTC/USD rate of 100,000 (which
affects only the fiat column, so no conclusion depends on it). The comparison is
**asymmetric by construction and should be read as a lower bound on Cashu's
advantage**: it charges the channel its full on-chain setup+close on every
purchase, and charges Cashu only its melt fee — not the mint's own settlement
cost, spread, or custody. The *direction* is robust; the magnitudes are an upper
bound on the channel's cost, not a like-for-like comparison.

## 5. What is true, and the increment actually worth doing

**V1 is a real product, and it is not the question that was asked:** non-custodial,
no internet required, works over the local link, payer cosigns each granule.

The increment worth pursuing is **deferral**, not a new instrument:

> the router accepts a customer's value **before** it can reach a mint, and
> settles later — removing the 30-second `Receive` block and the
> `payment-outcome-unknown` class from the customer-facing leg.

The grant half of that **already exists**: `AddAllotment` is pure memory
(`merchant.go`) and `valve.OpenGate*` is local (`lightning.go:753`, `:761`). What
is missing is *verify-and-defer* — (i) verify the instrument with no mint
reachable, and (iii) defer settlement. Those two are the whole new capability.
This is a **Cashu-level** change at the layer the money already lives on, and it
is genuinely non-duplicative with #239.

### 5.1 A correction to the record on #239

This spike set out believing #239's zero-float bootstrap "partially answered" cold
start. **It does not.**

- `ForwardFirstProof` **has no production caller** — the only non-test references
  are its definition, a manager wrapper, and an interface declaration that
  nothing implements or consumes.
- `bootstrap.go` performs **no mint interaction at all** — its imports are only
  `crypto/sha256`, `encoding/hex`, `fmt`, `sync`, `time`, `logrus`; "mint" appears
  only in comments.
- The release notes call it *"lab-tested features awaiting router acceptance —
  treat both as experimental"*, and the merchant side that would call it
  (phase 2b) is unwritten and admitted so in the file itself.
- The merchant **cannot reach** the machinery: `MerchantInterface` exposes no
  forward-a-proof method, and the two packages are wired in the opposite
  direction.

Its cold start is an existence proof that a **reseller** can be funded by an
**upstream with mint access** — a different claim from "a customer can buy
without a mint in the loop".

### 5.2 Do not build this on the current crash-recovery lane

#719 records that `main` does not recover value when the process is SIGKILLed
between "mint signed the swap" and "wallet persisted". Channel state is *more*
durable state than the router has ever held. Building a channel on a tree whose
crash-recovery lane is red would multiply an existing defect.

## 6. Falsification conditions (executable)

`GREEN.txt` carries two deliberate `[FAIL]` lines:

- *"named a construction that is BOTH payer-chain-blind AND non-custodial"*
- *"the ladder makes the construction non-custodial AND payer-offline"*

**If a contributor makes either pass, this document is disproved and must be
revised.** Be precise about what that means: these are **hardcoded assertions**
that fail on purpose. No script can decide whether some *other* construction
exists, so this is a **signposted falsification target a human must implement**,
not an automatically checked property. It is honest to call it a target; it is
not honest to call it a proof procedure.

The five typed disproof conditions (D1–D5) are enumerated in
[`RISK-AND-IAC.md`](../analysis/RISK-AND-IAC.md) §A5. **D5** — "a construction
exists that is both non-custodial and chain-blind" — is **NOT MET** and remains
the only route by which the thesis survives.

## 7. Related work, and two asymmetries worth carrying forward

- **Prior art.** The construction is Spilman (2013), written up by Hearn &
  Spilman. The Bitcoin-wiki description defeats the payer's double-spend via
  nSequence-based mempool replacement — a mechanism the **same page** warns "was
  disabled in 2010, and more recently the code has been removed completely". Do
  not build on it; §3's V0/V2 results are the modern restatement of that hazard.
- **The closest prior art is a semester thesis, not a master's.** Christian
  Decker supervised **SA-2013-49, Sandra Siby, Dec 2013**, *"Paying your Internet,
  One Byte at a Time"* — a working one-way-channel PoC for **selling WiFi
  Internet access**. Burchert's work under Decker is duplex/tree funding, not
  one-way. See [`PRIOR-ART.md`](../analysis/PRIOR-ART.md) §5b.
- **The expiring asset is the fee budget, not a clock.** `OP_CSV` is a
  **minimum** delay, not a deadline — once the input ages, the refund stays
  spendable forever, which *removes* the reorg/watchtower time-pressure class.
  What does expire is the fixed-fee commitment: a pre-signed refund relays at up
  to 2 sat/vB and is **stranded from 3 sat/vB**, and the payer (who has no
  internet) cannot CPFP it, while the payee can. **The asymmetries —
  fee-bumping, rebroadcast, and claim — all favour the router.**
- **LNURLcash (LUD-25/26) can *carry* the leaf but cannot *enforce* it.** Every
  note is a BIP-341 taproot output key spent by arbitrary tapscript — so a
  one-way channel is one *leaf* inside it, not a rival instrument. But three
  things block the composition as an answer:
  1. **`BIP-342` disables `OP_CHECKMULTISIG`,** so the leaf in §2 **cannot** be
     encoded as a tapscript leaf as written; it needs the `OP_CHECKSIGADD` form
     (corrected leaf: 114 B, refund `cw1` 374 chars — arithmetic verified against
     the spec's own `25-vectors.json`, which confirms the bearer-note `cw1` is
     exactly 192 chars).
  2. **LUD-25's timelock is custodial by explicit design** — *"a custodial policy,
     not a consensus guarantee, and MUST NOT be presented as trustless"* — so a
     channel-as-LNURLcash-leaf is custodial in exactly the dimension this spike
     is about.
  3. **The spec's canonical spend transaction is never broadcast**, so it cannot
     supply consensus enforcement.
  **What *is* worth taking:** `cs1` is a genuine **offline issuance proof** (no
  network, no chain, verified against `mintPubkey`) — a real answer to the
  customer-path-verification half, valuable independently of any channel — plus
  the two script-builder footguns the spec already states (reject `OP_SUCCESSx`
  outside pushed data; never use a key that is neither empty nor 32 bytes in a
  signature check). See
  [`LNURLCASH-REVIEW.md`](../analysis/LNURLCASH-REVIEW.md) and
  [`CUSTOMER-MINT-AND-INTEROP.md`](../analysis/CUSTOMER-MINT-AND-INTEROP.md) §2f-§2i.

## 8. Recommendations

1. **Do not build an on-chain one-way channel for this module.** Not because it
   is impossible, but because no variant is non-custodial *and* payer-offline,
   and settlement is 3–4 orders of magnitude too expensive at this price point.
2. **Keep the Cashu-native Spilman direction; stop implying custodian-freedom.**
   It solves granularity, which was the actual motivation, at the layer the money
   is already on.
3. **If cold start is the goal, build the verify-and-defer seam.** Grant half
   already exists; verify-without-mint + defer-settlement is the new capability.
4. **Correct the #239 record** (§5.1) so cold start is not assumed covered.
5. **Sequence behind the crash-recovery lane** (§5.2), not around it.
6. **Engage LNURLcash rather than competing with it** — it is the container, and
   its `cs1`/rotate model already maps onto the existence/consumption split this
   repo's bearer port documents.
7. **If a LAN mint is deployed, call it operator-custodial.** Mint URLs get no
   host or scheme validation, so a LAN mint is legal — but it answers "no
   internet" only by making the *operator* the custodian, not by removing one.
8. **On the customer-held-mint topology (Track B): delete the mint, don't wrap
   it.** The construction is coherent only as *"the customer runs no mint and
   funds the channel directly"* — because a mint is itself a custodian, and a
   channel exists to replace one. Deploying both keeps the custodian *and* pays
   the deadline cost. Do not pursue "customer's mint + a channel".
9. **If LNURLcash work is picked up, scope it to the encoding only.** The `cw1`
   codec plus the `OP_CHECKSIGADD`-corrected leaf is a bounded, testable
   experiment; **do not** claim consensus enforcement, and do not integrate.
   Take `cs1` as the offline issuance proof on its own merits.

## 9. What this work did not establish

- **No channel was wired into the module.** The interface delta in
  [`ARCH-FIT.md`](../analysis/ARCH-FIT.md) is **type-checked** (`go vet`,
  `go build`, `gofmt` clean) — that proves *well-typedness*, not *behaviour*.
- **Reorg behaviour is not measured.** `regtest` mine-on-command is not a reorg.
  Two hazards are argued from construction and left open: a reorg can flip which
  mutually-exclusive state won, and a CSV-age-triggered broadcast can be
  un-confirmed.
- **No Lightning implementation was built or run.** V4 is argued from literature.
- **The economics are a deterministic simulator**, not a fee-market model. It
  establishes the *scale* of the mismatch (3–4 orders of magnitude), which no
  parameter tuning closes; it does not predict fee variance.
- **The module has no client implementation in-tree**, so what a real customer
  client could do with a channel instrument is not established from code.
