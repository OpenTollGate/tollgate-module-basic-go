# One-way payment channels for TollGate — adversarial review and reproducible IaC

**Status:** spike deliverable. 2026-10-14.
**Subject:** *"Can a ONE-WAY payment channel be a non-custodial bearer instrument
letting a customer with NO internet gateway and NO custodian buy internet time?"*
**Companion:** `SPIKE-BRIEF.md` (the framing this memo answers).
**Rig:** `rig/` — every number below comes from a committed script. `rig/out/`
holds the captured execution. `rig/ERRATA.md` lists six things the rig caught.

---

## Headline

| Claim under test | Verdict |
|---|---|
| "A payer with no internet gateway can buy time" | **TRUE, and already true today** — the payer only needs the *local* link to the router, which is the premise. Nothing added. |
| "…with no custodian" | **FALSE on the construction the project has chosen.** The module's own ADR (`session-ticket-decision.md:371`) commits to Cashu-native Spilman (`cdk-spilman`), in which **the mint is the adjudicator** — the same custodian Cashu already had. |
| "…and a blockchain-blind payer" | **FALSE.** A payer can be chain-blind *during* a session; recovering leftover value requires chain access. Pre-signing to avoid that converts the leftover into custodial exposure. |
| "…and does TollGate benefit?" | **NO — Cashu dominates by 3–4 orders of magnitude** at every price the product sells (§A4), and buys back nothing. |

**Bottom line: the spike is a disproof, with a precise and narrow exception.**
The exception is worth recording because it is the *real* finding (§A5).

---

# Part A — adversarial review

## A1. Custody: the exact trust assumption, per variant

A one-way channel is non-custodial **only if the payer holds a pre-signed
transaction that can return its money without the counterparty's cooperation**.
The revocation branch provides that. What it does *not* provide is a payer who
can be absent. I ran both constructions against Bitcoin Core v31.1 consensus;
RED/GREEN in `rig/out/{RED,GREEN}.txt`.

The construction (`rig/demo_channel.py:build_script`), the canonical Spilman script:

```
OP_IF  <CSV> OP_CHECKSEQUENCEVERIFY OP_DROP <payerPub> OP_CHECKSIG     # payer refund
OP_ELSE OP_2 <payerPub> <payeePub> OP_2 OP_CHECKMULTISIG OP_ENDIF     # payments
```

The `ELSE` branch is what does the custody work: **every payment state is a
2-of-2 spend, so a state exists only if the payer signed that exact output
set.** The payee cannot mint a state with a larger payee output. This is
asserted and tested — `GREEN.txt` T3 broadcasts a redeem-100% attempt and Core
**rejects** it (`Signature must be zero for failed CHECK(MULTI)SIG operation`).

### Variant table

| # | Variant | Script's live branch | Adjudicator / custodian | Non-custodial? | Payer offline? | Measured |
|---|---|---|---|---|---|---|
| **V0** | Redeemer signs alone | `<payeePub> OP_CHECKSIG` | **Payee holds a valid 100% spend at all times** | **NO — payee is custodian** | ✅ yes | `RED.txt` T3: Core **ACCEPTS** the 100% theft |
| **V1** | Spilman, payer cosigns per granule | 2-of-2 | Nobody — each state needs the payer's signature | ✅ **YES** | ✅ *during* the session, over the **local link** (no internet needed) | `GREEN.txt` T3 |
| **V2** | Spilman, payer pre-signs a ladder and leaves | 2-of-2 | **Payee, up to the ladder top** | ⚠️ only if `ladder top == amount actually used` | ✅ yes | `GREEN.txt` T9: **300,000 of 500,000 sats extractable (60%)** after 2 granules served |
| **V3** | Cashu-native Spilman (`cdk-spilman`/MONAD) — *the project's chosen direction* | 2-of-2 at the **mint** | **THE MINT** | ❌ no | ✅ yes | by construction; see below |
| **V4** | Duplex channelling with revocation (LN-shaped) | per-commitment keys | Nobody, but **the payer needs a watchtower** | ✅ yes | ❌ needs a watcher | not built |

### Naming the custodian — the answer the brief demanded

- **V0: the payee.** Precisely the party the payer does not trust. This is what
  "payer-offline" costs if you take the shortcut.
- **V1: nobody.** The genuine result. But the payer must be *reachable on the
  local link* at each granule. The brief treats "no internet gateway" as the
  blocker; it is not. The customer is connected to the router's Wi-Fi — a local
  link exists *by definition of the product*. **Co-signing needs interactivity,
  not connectivity to the internet.** These are different requirements and the
  brief conflates them.
- **V2: the payee, up to the ladder top.** Quantified: a 5-state ladder at a
  100,000-sat granule lets a router that served 2 granules take the fifth state
  and extract **300,000 sats (60% of the ladder top)**. No forgery is required —
  every ladder state is validly signed. `GREEN.txt` T9.
- **V3: the mint.** The ADR's own accepted-risks paragraph
  (`session-ticket-decision.md:418-424`) says the operator's balance "sits at a
  third-party mint", the receiver "must claim before expiry or the sender refunds
  the whole channel", and recovery paths (NUT-09 restore) "belong to the wallet
  layer". So the chosen Cashu-Spilman path **reintroduces the mint as the
  adjudicator and adds a hard expiry the raw-Bitcoin variant does not have.** It
  answers the early-exit question — its stated purpose — but it does **not**
  deliver "no custodian". If the goal was custodian-freedom, the project's
  committed direction does not achieve it.

**Finding:** *I could not construct a variant that is simultaneously payer-offline
and custodian-free.* The variant that is custodian-free (V1) requires the payer
at each granule; the variants that are payer-offline (V0, V2, V3) all have a
custodian — the payee, or the payee-up-to-the-top, or the mint.

---

## A2. What the payer must know — the "blockchain-blind" question

"Chain-blind" needs unpacking into three different things, which the brief runs
together. Measured per construction in `GREEN.txt` T7.

| # | Payer activity | Needs internet? | Needs chain state? | Needs interactivity? | Enforced by |
|---|---|---|---|---|---|
| 1 | **Fund** the channel | ✅ yes (an on-chain UTXO) | ✅ yes | — | funding tx |
| 2 | **Co-sign one state** (V1, the non-custodial path) | ❌ **NO** | ❌ **NO** | ✅ yes — the local link | 2-of-2 needs the payer's sig |
| 3 | **Detect an inflated claim** | ✅ yes | ✅ yes | — | nothing; requires watching |
| 4 | **Publish the CSV refund** (V1/V2 escape) | ✅ yes | ✅ **yes** — must know the input has aged | — | OP_CSV |
| 5 | **Know whether the refund will relay** | ✅ yes | ✅ yes — mempool fee rate | — | fixed fee budget |
| 6 | **CPFP-bump a stranded refund** | ✅ yes | ✅ yes | — | needs a funded wallet |
| 7 | **Pre-sign a ladder** (V2) | once, up front | ❌ no | ❌ no | needs *future spend prediction* |

Three corrections to the brief's framing, each of which changes the conclusion:

**(i) Rows 1–2 are the ones that matter, and they show the payer needs no chain
state to *transact*.** A payer holding only a channel state and a signing
device participates fully with zero chain access. The brief's T5 test is met for
the payment path.

**(ii) OP_CSV is a MINIMUM delay, not a deadline — so there is no window the
payer must hit.** My first draft of the rig asserted the opposite; the rig's own
output contradicted the prose (see `rig/ERRATA.md` E5). Once the input ages, the
refund stays spendable **forever**. A payer that reappears after a year can still
refund. This *removes* the reorg/watchtower time-pressure class of risk that a
naive reading of the brief would assign here. It is a genuine strength of the
construction and I record it as such.

**(iii) The real expiring asset is not a block height — it is the fee budget.**
The pre-signed refund is **142 vB** and commits to a **fixed 400-sat fee**. At
that budget it relays at up to 2 sat/vB and is **stranded from 3 sat/vB**
(`GREEN.txt` T6). The payer cannot CPFP it: building a child needs a funded
wallet and mempool visibility, i.e. the internet the payer does not have. The
*payee* can — but the payee is the party the payer does not trust. **The
construction's timeliness requirement is therefore a fee-market race, not a
clock, and this is the sharpest technical objection to the thesis.**

Row 7 is the crux. Pre-signing a ladder removes the need for interactivity (row
2) but replaces it with something worse: the payer must **predict future spend**,
and pays for wrong guesses at the ladder top. Interactivity was never the
problem; *prediction* is.

---

## A3. Attack surfaces, with severity

| # | Attack / failure | Severity | Quantified? | Detail |
|---|---|---|---|---|
| 1 | **Malicious router inflates its claim from a pre-signed ladder** | **HIGH** | ✅ **300,000 sats = 60% of ladder top** for 2 granules served (`GREEN.txt` T9) | No forgery needed: every ladder state is validly signed and states are mutually exclusive (T4). The router simply submits the largest. Unfixable without per-granule interactivity. |
| 2 | **Malicious router takes 100% in the simplified construction** | **CRITICAL** | ✅ 100% of channel (`RED.txt` T3, Core ACCEPTS) | The "payer-offline" shortcut. This is the failure the brief's "chain-blindness is the crux" warning is pointing at, and it is not hypothetical — it is a two-line script change. |
| 3 | **Fee spike strands the pre-signed refund** | **HIGH** | ✅ stranded from **3 sat/vB**; CPFP at 100 sat/vB needs a **24,800-sat** child (`GREEN.txt` T6) | The payer cannot self-rescue. At 50 sat/vB a 1,000-sat purchase pays **746 sats** in chain cost — 71.6% overhead (`SIM-OUTPUT.txt` T1/T3). |
| 4 | **Router goes dark before claiming** | LOW | — | The payer's CSV refund covers it, with **no time pressure** (CSV is a floor). Harm is capital lockup, not loss. Mitigated by construction. |
| 5 | **Router goes dark *after* the payer prefunded, and the payer has no chain access** | MEDIUM-HIGH | — | Leftover is stuck in a 2-of-2 until the payer gets online once. Recoverable at leisure, but the *fee budget* of the pre-signed refund decays meanwhile. Not a loss, a stranded asset. |
| 6 | **Chain reorg during the timelock** | MEDIUM | ❌ **NOT MEASURED** | The rig does not reorg on demand (`regtest` mine-on-command is not a reorg). Two specific hazards, argued from construction: a reorg can flip *which* mutually-exclusive state won (the T4 race); and a CSV-age-triggered broadcast can be un-confirmed. Mainnet LN operates on a 3–6-confirmation convention for this reason; **I make no measurement here and flag it as an open item.** |
| 7 | **The payer cannot verify the state it co-signs** | MEDIUM | ✅ measured (`GREEN.txt` T8) | Stock `bitcoind` **refuses** to co-sign this script (`Unable to sign input, invalid stack size`). `bitcoind` cannot be the payer's channel wallet; the payer needs custom signing software. The Cashu path has no analogue — a Cashu token is verifiable by any wallet. |
| 8 | **Mempool policy / rebroadcast** | MEDIUM | — | A low-fee tx can be dropped and needs rebroadcast. A payer with no internet cannot rebroadcast; only the router can. Another asymmetry in the router's favour. |
| 9 | **Funding-tx privacy linkage** | LOW-MED | — | The funding UTXO links the payer's coins to a router session. Not TollGate-specific; recorded for completeness. |

**A note in the construction's favour (avoiding a strawman):** a *pure one-way*
channel needs **no revocation and therefore no watchtower**. There is nothing to
"breach": the payee is the only party who ever submits a state, and it is
self-interested in submitting a high one. The watchtower requirement that makes
LN operationally heavy **does not apply here.** Direction-1 duplex channels —
which the project's ADR chooses — do have that requirement. This is the strongest
technical argument for the one-way shape and it should be stated.

---

## A4. Cashu vs one-way channel — the dominance verdict

The honest question is not "is the channel technically possible" but "given
TollGate already accepts Cashu tokens *handed to the router*, what does the
channel buy that Cashu does not already give?"

**Grounding (module @ cf7cad3):** Cashu already delivers the bearer property —
`src/tollwallet/port.go` models receive as *redeem-and-reissue* of a bearer
instrument, and `src/tollwallet/cdk_wallet.go:121` `Receive` only needs the
**router's** mint; the customer performs no mint interaction
(`RECEIVE` in `port.go:187-256`). The customer hands over a token; the router
does the round-trip.

**Sourced price:** `step_size = 22020096 ms` and `price_per_step = 1 sat`
(`src/tollgate_protocol/validator_test.go`, `src/config_manager/config_manager_config.go:305`,
`README.md:351`). Therefore:

> **1 satoshi buys 6.12 hours of internet.** 64 sats — the module's own
> `MinBalance` — is **16.3 days**.

That is the unit the channel's on-chain cost must be amortised against, and it
is three orders of magnitude too small.

| Dimension | Cashu | One-way channel | Winner |
|---|---|---|---|
| Bearer instrument | ✅ a token | ✅ a signed state | tie |
| Payer can hand over value while fully offline | ✅ **hand a token over** | ✅ (V2) / needs local link (V1) | tie |
| Payer needs chain state | ❌ **none, ever** | ✅ to refund/verify | **Cashu** |
| Cost per purchase | **~0** (0 bps mint swaps; 0.1% melt) | **746 sats at 1,000 sats** → 746× | **Cashu** |
| Cost at 64 sats (the module's own minimum) | 0.064 sats | 718 sats → **11,218×** | **Cashu** |
| Amortise chain cost to 0.1% | n/a | needs a **716,000-sat** channel = **500 years** of internet | **Cashu** |
| Capital lockup before service | none | whole prefund | **Cashu** |
| Customer's exposure per purchase | **exactly the amount bought** | amount + full settlement cost (2×–717×) | **Cashu** |
| Timelock / fee-timeliness risk | none | stranded from 3 sat/vB; cannot self-CPFP | **Cashu** |
| Integration surface | existing `WalletPort`, 5 shipped mints | new script, custom signer, watcher/journal | **Cashu** |
| Custodian | the mint | the mint (V3) or nobody (V1) | tie / Cashu |
| Removes the mint round-trip from the re-… | n/a | only from the **router's** path | see below |
| **Direction** | | | **CASHU DOMINATES** |

### What the channel adds, precisely

The brief's own framing (`SPIKE-BRIEF.md` §2) is that the increment is *"the mint's
oracle disappears from the customer's path."* But `port.go`/`cdk_wallet.go` show
**the mint round-trip is already the router's, not the customer's.** The customer
hands over a bearer token. So the channel's entire benefit is: *the router no
longer performs a mint round-trip that costs ~0 sats and ~1 RTT.* You pay
hundreds of sats and permanent capital lockup to save the router one HTTP call.

### What the channel costs

On-chain fund+claim = **716 sats at 2 sat/vB** (155 vB + 203 vB, both measured in
the rig). Overhead vs purchase (`SIM-OUTPUT.txt` T1): **71,600% at a 1-sat
purchase, 71.6% at 1,000 sats, 0.7% at 100,000 sats.** Plus capital lockup, plus
the abandon/dark-router expected loss, plus the V2 ladder-top exposure, plus the
fee race. At a 50 sat/vB spike the setup alone buys **4,562 days** of internet —
a customer facing that choice buys bandwidth, every time, which is correct for
the product and fatal for the channel.

### Falsifiability of this verdict

My verdict rests on `price_per_step = 1`, which is only a *default*. So
`SIM-OUTPUT.txt` T7 re-runs the overhead at 10×, 100× and 1000× that price:

| price sat/step | hours per sat | 1000 h of internet costs | overhead |
|---|---|---|---|
| 1 (the default) | 6.117 | 163 sat | **439%** |
| 10 | 0.612 | 1,634 sat | 43.8% |
| 100 | 0.061 | 16,348 sat | 4.4% |
| **1000** | 0.006 | 163,487 sat | **0.44%** |

**If TollGate's real price were ~1,000 sat/step, the cost argument weakens
materially.** If that is so, say so and re-run the sim with the true constant —
it is one line in `sim_economics.py`. The **custody** argument (V0/V2/V3 always
have a custodian) does not weaken at any price.

> **RESOLVED 2026-10-14 — and the answer is partly yes.** Verified against the
> live router `tollgate-A04E` (`tollgate-wrt v0.6.0-rc4-gcf7cad3`): the shipped
> config is `metric = "bytes"`, `step_size = 22020096` (= 21 MiB exactly),
> `price_per_step = 1 sat`. So this document's `step_size` was read as
> **milliseconds** in error — the sim's "6.12 hours per sat" was an artefact.
>
> The overhead percentages above are a sat-to-sat ratio and stand. What changes
> is the **size of a realistic purchase**: a heavy user (300 GB/month) pays
> ~13,624 sat, so a fixed 716-sat channel reaches **5% overhead in about one
> month** and 1% in ~5 months. The corrected gap is **1–3 orders of magnitude at
> small purchases and comparable at large ones** — weaker than published.
>
> **The verdict on custody does not move:** V0/V2/V3 carry a custodian at every
> price. See §10 of the design record,
> [`granularity.py`](../../research/one-way-channel-spike/granularity.py), and
> `out/GRANULARITY.txt`.

**VERDICT: Cashu dominates.** It is a bearer instrument, needs no chain, lets the
payer hand value over while fully offline, is free per purchase, caps the
*customer's* exposure at the amount bought, and is already implemented. The
channel adds on-chain cost, capital lockup, a fee-timeliness race, a custom
signing requirement, and a *larger* customer exposure — and buys back only the
removal of a mint round-trip the customer was never performing. **Do not build
this. Do not merge it upstream as a payment rail.**

---

## A5. What would disprove the thesis — and my verdict

### Disproof conditions (stated so they can be checked)

The thesis *"a one-way channel lets a chain-blind, custodian-free payer buy
internet time"* is **disproved** if any of:

1. **D1** Any variant with no custodian requires the payer to be reachable per
   granule. → **MET** (`GREEN.txt` T3 requires the payer's cosignature; `RED.txt`
   T3 shows the custodian-free alternative hands the router the whole channel).
2. **D2** Any variant that is payer-offline gives a custodian extractable power.
   → **MET, quantified**: the ladder top, 60% of a 5-state ladder (`GREEN.txt` T9).
3. **D3** The payer needs chain access for some step the thesis claims is
   chain-free. → **MET for recovery** (`GREEN.txt` T5: the refund needs the input
   aged; T6: relaying needs a competitive fee). **NOT met for transacting.**
4. **D4** Cashu already provides the property at lower cost. → **MET by
   3–4 orders of magnitude** (`SIM-OUTPUT.txt` T1/T3).
5. **D5** A construction exists that is both non-custodial and chain-blind.
   → **NOT met — I could not construct one, and the rig asserts its non-existence
   as an executable check.** If a contributor makes `GREEN.txt`'s
   *"named a construction that is BOTH payer-chain-blind AND non-custodial"*
   check PASS, this memo is disproved and must be revised. That is the
   falsification condition, made runnable.

### Verdict

**The strong thesis is DISPROVED. The narrow thesis is TRUE BUT WORTHLESS.**

- **Disproved:** "a payer with no internet gateway and **no custodian**". No such
  construction exists among the four I built and ran (§A1). The custodian is the
  payee (V0), the payee-up-to-the-ladder-top (V2), or the mint (V3, the project's
  own committed direction).
- **True but worthless:** "a payer with no internet gateway needs no chain state
  **to transact**." V1 delivers this, because co-signing needs the *local* link,
  not internet — and the local link exists by construction. But (a) Cashu already
  gives an offline payer more, for less, and (b) it is dominated by 3–4 orders of
  magnitude.
- **The correct positive result is V1, and it is not the brief's question.**
  "Non-custodial, no internet, works over the local link, but the payer must
  cosign each granule" is a defensible product. It is also exactly what the
  module's **existing** #239 bootstrap path already approximates for cold start,
  and it needs no chain at all if the instrument is Cashu rather than a Bitcoin
  output. That is the direction worth a PR — not an on-chain channel.

### The honest, unflattering finding

The project has **already committed** (`session-ticket-decision.md:371`) to
Cashu-native Spilman. This spike's question — "can a one-way channel be
custodian-free?" — is answered **no**, including for the chosen direction, because
the mint adjudicates. If the *real* motivation is granularity (the ADR's stated
reason: "small, frequent, granular payments stop costing an invoice and a mint
round-trip each"), then granularity is a **Cashu-level** problem with a
**Cashu-level** solution, and an on-chain channel is the wrong layer for it. The
one-way *Bitcoin* channel is a solution to a problem this product does not have,
at a price this product cannot pay.

---

# Part B — reproducible IaC design

## B1. What infrastructure exists on this host (actual output)

Run of `rig/check-env.sh`, committed verbatim at `rig/out/ENV.txt`:

```
host        : <host>
distro      : Ubuntu 24.04.3 LTS
cpus        : 8
mem         : 7683 MiB total, 4584 MiB available
disk(/)     : 197G total, 69G avail, 64% used
sudo        : NOPASSWD available

== binaries relevant to this spike ==
  [ABSENT]  bitcoind  /  bitcoin-cli  /  lightningd  /  lnd  /  lncli
  [ABSENT]  cln  /  lightning-cli  /  elementsd  /  docker  /  podman
  [PRESENT] python3   /usr/bin/python3   (3.12.3)
  [PRESENT] go  /tmp/goroot/bin/go       (1.22.5)
  [PRESENT] rustc / gcc / jq / gh

== is a bitcoin/LN daemon already listening anywhere? ==
  (no bitcoin/Lightning listener found on this host)

== network reachability ==
  https://bitcoincore.org/bin/   200
  https://github.com             200
  https://pypi.org               200
```

**Answers to the questions the brief asked, as measured:**

| Question | Answer |
|---|---|
| Is a `bitcoind` running on this host? | **NO.** `ss -ltnp \| grep 8332` → no match. |
| Is a bitcoind available *on this fleet*? | **YES, on `dq05`.** `listening 127.0.0.1:8332`, `Bitcoin Core daemon version v31.1.0`, pid 2151, running 85,416 s (≈23.7 h) with `-datadir=/mnt -conf=/mnt/bitcoin.conf`. `du -sh ~/.bitcoin` = 12K; `/mnt` holds the data. **But its RPC credentials are absent from `dq05`'s user context** (`Could not locate RPC credentials… Configuration file: (<dq05-user>/.bitcoin/bitcoin.conf)`), and its 8332 port is bound to **localhost only** — so it is **not** usable as a remote rig backend without further work. Report it, do not depend on it. |
| Is an LN implementation (CLN/LND) installed? | **NO, anywhere found.** `lnd`, `lncli`, `lightningd`, `cln`, `lightning-cli` all absent on this host and on `dq05`/`t440`. No 9735/9736 listener. |
| Container runtime? | **NO** `docker`, **NO** `podman`. |
| Go toolchain? | **Three-way finding** (`rig/out/GO-TOOLCHAIN.txt`): Ubuntu's apt `golang-go` is **1.22.5**, but `src/go.mod` requires **`go 1.26.0`** → `go build` fails with *"go.mod requires go >= 1.26.0"*. A **fetched Go 1.27.2 does build `src/`** (rc=0) and runs `research/bearer-port-demo` green. So the Go research surface **is** reachable here, at the cost of one ~70 MB network fetch. |
| QEMU / OpenWrt image? | **Absent** — so a real router aarch64 image on the host is out of scope for this rig. |

**Consequence for the rig design:** the brief offered "bitcoind regtest *or* an LN
implementation". **Neither is present.** That forced the architecture choice in
B2, and it is why the deployed rig **downloads its own pinned node** rather than
assuming one.

## B2. Rig topology

```
rig/
  run-rig.sh          ← THE one command. Writes everything to rig/out/.
  check-env.sh        ← B1 evidence, from the live host
  check-go.sh         ← the Go three-way finding
  demo_channel.py     ← channel construction, --variant spilman|naive. All
                        assertions executed against Bitcoin Core consensus.
  sim_economics.py    ← A4 costing. Deterministic arithmetic. NO chain.
  btclib.py           ← secp256k1 + RFC6979 + BIP143 P2WSH + BIP68/BIP112.
                        ZERO third-party imports.
  out/                ← committed artefacts (never hand-edited)
  ERRATA.md           ← six things the rig caught
  README.md
```

**Design choices, and their justification:**

1. **The crypto is bare-stdlib Python, not a library.** The claim under test is
   about a payer with no internet. A rig that must reach PyPI to check a
   no-internet claim is not evidence. `btclib.py` therefore implements
   secp256k1 + RFC 6979 + BIP143 by hand — and is *cross-checked against Core*
   rather than trusted (`GREEN.txt` T8; every spend Core accepts is a signature
   and sighash Core verified).
2. **P2WSH only.** SegWit is structurally required: a legacy input's txid is
   malleable, and a pre-signed state chain keyed on a malleable parent txid is
   broken. Pinning to v0 SegWit also removes `ripemd160` from the critical path
   (OpenSSL 3 can drop it via providers).
3. **Pinned, checksum-verified node.** `run-rig.sh` fetches Bitcoin Core **31.1**
   and verifies `sha256 = b80d9c3e04da78fb6f0569685673418cf686fadba9042d926d13fb87ff503f9e`
   against the published `SHA256SUMS` before installing. It **refuses to
   install on mismatch.** A rig that fetches binaries without pinning is a supply
   chain, not an IaC artefact.
4. **A simulator for economics, a real chain for crypto — and the split is
   declared.** This is the honest answer to the brief's "prefer a deterministic
   simulator if the live path is not reliably available". The live path (regtest)
   *is* reliably available, so it is used for everything cryptographic. Only the
   *cost model* is simulated, because a fee market cannot be reproduced on
   regtest. `sim_economics.py` prints its own LIMITS section and **cannot** be
   cited for any validity or custody claim — those all come from Core.
5. **Everything lands in `out/`** and `out/` is committed. Nothing in the memo is
   retyped from a terminal.
6. **Falsification is executable.** The two `[FAIL]` lines in `out/GREEN.txt` are
   assertions the memo *wants* to hold. A future contributor who makes them pass
   has disproved the memo, and the rig says so.

**Topology when run:**

```
run-rig.sh
 ├─ step 0: check-env.sh  → out/ENV.txt            (no network)
 ├─ step 1: ensure bitcoind (network ONLY if absent; BITCOIND= to skip)
 ├─ step 2: sim_economics.py → out/SIM-OUTPUT.txt  (no network, no chain)
 ├─ step 3: start LOCAL regtest on 127.0.0.1:18443, mine 101
 ├─ step 4a: demo_channel.py --variant naive   → out/RED.txt
 └─ step 4b: demo_channel.py --variant spilman → out/GREEN.txt
```

## B3. Exact commands and expected output

### The command

```bash
cd <repo>/research/one-way-channel-rig        # rig/ dropped in here in the PR
bash rig/run-rig.sh
```

### Declared network needs

| step | network | notes |
|---|---|---|
| 0 | **none** | host inventory |
| 1 | **once**, only if `bitcoind` absent | `BITCOIND=/path/to/bitcoind` skips it entirely |
| 2 | **none** | arithmetic only |
| 3 | **none** | localhost `127.0.0.1:18443` only; `-rpcbind=127.0.0.1` |
| 4 | **none** | localhost only |

Once a toolchain is present, **the entire rig runs with the network down** — and
that is itself a checkable property of the artefact.

### RED — expected (the finding)

`$ python3 rig/demo_channel.py --variant naive` → `out/RED.txt`, **11/14 checks
pass; 3 FAIL, and all three failures are the finding:**

```
  [FAIL] redeemer CANNOT take more than the signed state (non-custodial)
          custodial: redeemer holds a valid unilateral 100% spend
  [FAIL] named a construction that is BOTH payer-chain-blind AND non-custodial
          none exists: chain-blindness and non-custody are in direct tension
  [FAIL] the ladder makes the construction non-custodial AND payer-offline
          it is payer-offline, and it is custodial up to the ladder top.
```

The load-bearing line, from the real run:

```
    redeemer takes 100% of channel: ACCEPTED
```

That is Bitcoin Core **accepting** the theft. It is the RED case for A1.

### GREEN — expected

`$ python3 rig/demo_channel.py --variant spilman` → `out/GREEN.txt`, **15/17 pass;
2 FAIL, both deliberate** (the two disproof assertions above). Key expected lines:

```
    state(owed=100000) broadcast: ACCEPTED
    vsize 203 vB, fee 400 sats (2.0 sat/vB, 0.40% of the 100000 sat granule)

    redeemer takes 100% of channel: REJECTED Signature must be zero for failed CHECK
  [PASS] redeemer CANNOT take more than the signed state (non-custodial)

    refund broadcast at height 143: REJECTED non-BIP68-final
    mined 6 blocks -> height 149; refund again: ACCEPTED
  [PASS] refund rejected while the CSV delay is immature
  [PASS] refund accepted once the delay ages

             1 s/vB |     142 sat | OK
             2 s/vB |     284 sat | OK
             3 s/vB |     426 sat | STRANDED (unconfirmed, possibly forever)

    MALICIOUS router, having served only 2 granules, broadcasts the TOP state
      owed=500,000: ACCEPTED
    => overcharge realised: 300,000 sats (60% of the ladder top)

  [FAIL] the ladder makes the construction non-custodial AND payer-offline
```

`RED.txt` and `GREEN.txt` are the RED/GREEN pair in the
`research/bearer-port-demo/` convention (`RED.txt`, `GREEN.txt`,
`DEMO-OUTPUT.txt`); the third artefact there is `out/SIM-OUTPUT.txt`.

### Simulator — expected

`$ python3 rig/sim_economics.py` → `out/SIM-OUTPUT.txt`, exit 0. Reproduces the
A4 tables. Selected expected values: **716 sats** setup+close at 2 sat/vB;
**71,600%** overhead at a 1-sat purchase; **11,218×** Cashu-vs-channel cost ratio
at 64 sats; **716,000 sats / 500 years** to amortise to 0.1%.

### Determinism

Regtest txids differ per run (fresh funding). **Every quantitative claim is a
ratio, a vsize, or a count, and all are run-invariant.** No memo number depends
on a txid.

## B4. Which claims this rig can and cannot support

**CAN support (executed against real Bitcoin Core consensus):**

- ✅ The witness script is consensus-valid (Core accepts a state spend).
- ✅ A payee *cannot* take more than the signed state (Core rejects it) → non-custody of V1.
- ✅ A payee *can* take 100% when the script omits the payer's cosignature → V0 is custodial.
- ✅ Only one state can settle; states are mutually exclusive (`bad-txns-inputs-missingorspent`).
- ✅ The CSV refund is rejected before aging and accepted after → the payer's escape is real.
- ✅ The overcharge exposure of a pre-signed ladder, in sats and as a % of the top.
- ✅ vsize/fee of every transaction (142 vB refund, 203 vB claim, 165 vB naive).
- ✅ The fee level at which the pre-signed refund becomes unrelayable.
- ✅ Our txid/size/vsize agree with Core's decoder (serialization correctness).
- ✅ What the host has, and that `apt`'s Go cannot build the module.

**CANNOT support (and no claim is made):**

- ❌ **Reorg behaviour.** Regtest mines on command; it does not reorg on demand.
  A3-6 is **UNQUANTIFIED** and argued from construction.
- ❌ **Watchtower requirements / breach races.** Not built. (I argue no watchtower
  is needed for the *pure one-way* case; that argument is from construction, not
  measurement.)
- ❌ **Real fee markets, mint fee schedules, BTC price, user behaviour.** The sim's
  `ASSUMPTIONS` are explicit guesses. The *direction* of A4's verdict is set by a
  3–4 order-of-magnitude ratio, not by any single assumption — but the magnitude is.
- ❌ **Any claim about the `cdk-spilman`/MONAD implementation.** Not installed, not
  run. V3's custody analysis is from the module's own ADR text.
- ❌ **OpenWrt/aarch64 behaviour.** No QEMU, no router image. The `cdk` sidecar's
  RAM/flash fitting on target hardware remains unverified — as the ADR itself
  already flags.
- ❌ **Live mainnet/testnet fee behaviour.** Regtest fees are the relay floor.

**A note on rig quality, in the memo's own interest:** the rig was wrong six
times before it was right (`rig/ERRATA.md`) — a malformed DER SEQUENCE length, a
BIP68 type-flag that silently turned 6 blocks into 51 minutes, a checkmultisig
witness-stack order error, non-minimal pushes, a prose claim about CSV being a
deadline, and a Core signer that refuses this script. **Four of those would have
passed silently under a simulator.** That is the argument for using the real
chain for the cryptographic legs, and it is why I am willing to report the
numbers above as measurements.

---

## Recommended disposition

1. **Do not build the on-chain one-way channel.** A4 is a dominance result, not a
   preference.
2. **Record the two executable disproof assertions** so a future contributor
   cannot quietly re-assert chain-blind non-custody.
3. **Correct the brief's framing** for the next spike: "no internet gateway" is
   not the requirement — the **local link** to the router is available by
   construction, and co-signing needs that, not connectivity. The genuine hard
   requirements are *no custodian* and *no chain access for recovery*, and the
   first is what fails.
4. **Take the ADR's granularity motivation to the Cashu layer**, where it belongs
   — `#685`/`#866` — rather than to an on-chain channel that costs 3–4 orders of
   magnitude more.
5. **One upstream PR maximum**, as the brief requires: the rig, the captured
   RED/GREEN/sim output, the ERRATA, and this memo — framed as a *negative*
   result with the V1 exception stated. A spike that returns "no, and here is the
   construction that fails" is explicitly a valid outcome, and the falsification
   conditions are supplied.
