# Should TollGate accept anything other than Cashu? — programme result

**Consolidated summary.** This PR is a research branch: it answers the question
above from committed, reproducible evidence rather than prose, and it ships the
one enabling artefact that pays off regardless of the answer. Everything below is
either a **MEASURED** result (a command was run, output quoted) or is explicitly
labelled **REASONED**.

**Read this file and you do not need to read the rest of the branch.**

---

## The answer, in one paragraph

**No — not today, and the reason is structural rather than incidental.** Every
candidate alternative instrument reduces to a taproot commitment plus a script
path, and the programme examined the two live candidates (LNURLcash bearer notes,
and on-chain one-way channels including Hedgehog) against the module's actual
requirements. Both fail on the same axis: TollGate is a **SERVICE** that must know
**consumption state at the moment of grant**, and a bearer instrument carried by
the *customer* cannot supply that without either a durable acceptor-side nullifier
journal (new money state on a module whose crash-recovery lane is currently red,
#719) or an external authority. An on-chain channel could supply it, but even the
best construction available (Hedgehog) achieves non-custodial + sender-offline +
recipient-offline only by **relocating** the time obligation into a timelock window
that somebody must watch. The programme therefore recommends **bounded experiment
then watch**, and ships the refactor that makes the eventual answer cheap.

---

## 1. The disproof: a one-way channel cannot be a non-custodial bearer instrument here

Four channel constructions were **built and executed against Bitcoin Core v31.1
consensus**, not against a simulator. No variant is both non-custodial and
payer-offline. The custodian is always the payee (Core *accepts* a 100% theft,
`RED.txt` T3), the payee-up-to-the-ladder-top (60% of a 5-state ladder extractable
after 2 granules served, `GREEN.txt` T9), or the mint.

The rig caught **four real bugs** that a self-consistent simulator would have
passed silently — every one of them surfaced only because the assertion was
checked against Core rather than against our own code. That cross-check is the
methodological point of the whole branch, and it paid off twice more below.

→ `docs/analysis/SPIKE-REPORT.md`, `docs/architecture/one-way-channel-spike.md`,
`research/one-way-channel-spike/` (`ERRATA.md` lists all four).

## 2. D5 — the one open condition — is **NOT MET**

The spike left one condition unfilled: does *any* construction give a payer who
is both non-custodial **and** offline? Hedgehog was tested, because it is the
strongest candidate (its README claims exactly this property).

**Verdict: NOT MET — but it is the closest thing found.**

| | non-custodial | sender offline | recipient offline | verdict |
|---|---|---|---|---|
| V0 naive / V1 Spilman / V2 ladder / V4 LN-penalty | mixed | mixed | mixed | each fails at least one |
| **Hedgehog** | **✓** | **✓** | **✓** | fails on the **time obligation** |

Hedgehog is the first construction in this programme to achieve all three at once.
It pays with a **CSV timelock window**: reclamation is unilateral only after a
block delay, so safety requires *some party* to observe the chain inside the
window — a watchtower-equivalent duty. It **relocates the reconciler to the
counterparty** rather than eliminating it, which is the same "relocates, does not
delete" pattern the original spike predicted, now with the refinement that what
moves is *who* must be live.

**Exact trust assumption:** (1) the recipient eventually returns, and (2) some
party watches the chain inside the CSV window. Whether (1) is acceptable is a
product question, not a protocol property.

Strongest supporting evidence: `xcheck.py` — **10/10 independent cross-checks**,
none of them our own code, including **byte-for-byte agreement with the *shipped*
`tapscript.js`** for the real 3-leaf Hedgehog tree. That proves we model the tree
the deployed site actually builds.

**Honest limit:** no end-to-end Core *accept* of the Hedgehog spend — Core
executed the tapscript and rejected on the signature, so our signature agrees only
with our own verifier. The D5 conclusion does not depend on that leg; it rests on
the timelock structure, directly observable in the tree.

→ `docs/analysis/D5-RETEST.md`, `research/hedgehog-poc/`.

## 3. The price erratum — and why it changes the economics

Read **live from a running router** (`tollgate-A04E`, `tollgate-wrt
v0.6.0-rc4-gcf7cad3`):

```json
{ "metric": "bytes", "step_size": 22020096, "price_per_step": 1 }
```

The shipped metric is **bytes**, so **1 satoshi buys 21 MiB** — *not* the
"6.12 hours" an earlier pass claimed by reading `step_size` as milliseconds.
`22020096 = 21 × 2²⁰` exactly, which is itself evidence the bytes reading is
intended.

This matters because the fixed on-chain settlement cost (~716 sat) is a
sat-to-sat ratio that does **not** improve with granularity, while the *size of a
purchase* scales with price. So the economics of a channel are a
**purchase-size property, not a protocol property** — the two instruments are best
read as two ends of one dial rather than competitors.

→ `docs/architecture/one-way-channel-spike.md` §10,
`research/one-way-channel-spike/granularity.py` (every number reproducible).

## 4. LNURLcash: the benefits accrue to the other side of the protocol

**Verdict: bounded experiment then watch — not now.**

The structural finding:

> **LNURLcash's benefits accrue to the WALLET side of the protocol. TollGate is
> the SERVICE side.** TollGate's requirement — consumption state at the moment of
> grant — is the one thing `cs1` structurally cannot supply.

Two specific hypotheses were tested and **both are rejected by measurement**:

- **`cs1` as an offline "is it spendable" pre-check — rejected as a grant basis.**
  It proves *issuance*, not *outstandingness*: recoverable ECDSA over
  `"LNURLcash:" || amount || ":" || Q`, verified offline against `mintPubkey`. It
  carries **no nonce, no timestamp, no recipient, no counter** (measured).
  Accepting on it means accepting a **replayable** instrument, and the spec does
  not bound the replay window.
- **LUD-03 withdraw link as a receipt format — rejected as unnecessary.** The
  module already has a strictly better receipt: #502's receive-intent journal,
  written *before* the money moves, with a temp-file → fsync → rename →
  fsync-parent-directory commit sequence and `granted`/`owed`/`abandoned`/`pending`
  states, already wired into the owed-grant path. More information, shipping today.

**Watch triggers that would flip this to "adopt":** a normative locked-note or
one-time-`cs1` variant in LUD-25; TollGate acquiring a chain view; the #719 lane
going green *and* a durable nullifier store shipping anyway; or the journal-cost
experiment (X4) coming back cheap.

**Highest-leverage action available, and it costs nothing:** the review of
LUD-25/LUD-26 is drafted and held at
`docs/analysis/LNURLCASH-REVIEW-POST.md`.

→ `docs/analysis/OPTIONS-PAPER.md`, `docs/analysis/P2-VERDICTS.md`,
`research/lnurlcash-tollgate/`, `docs/analysis/LNURLCASH-REVIEW.md`.

## 5. What actually ships — and it pays off either way

`src/taprootverify/` — a bounded, **instrument-agnostic** taproot commitment
verifier.

Every candidate instrument is ultimately *a taproot output key plus a script
path*, and the router had no way to reason about one. This package verifies that
an internal key, leaf script and control block reproduce an output key, and
derives output keys from a script tree. It does **not** accept payments, keep a
ledger, touch the wallet, or make a grant decision — verifying a commitment is
necessary but **not sufficient** to accept value, and `AGENTS.md` forbids granting
on a local check alone.

Why it is worth merging regardless of the answer above:

- **It is the enabling refactor the CDK migration also wants.** Nothing in it
  knows what Cashu, LNURLcash or a channel leaf is — it knows taproot.
- **No new dependency and no new cgo.** `btcd` and `btcec/v2` were already
  `// indirect` requires via the gonuts stack, and `txscript` already contains the
  full taproot machinery. `go mod tidy` promoted three requires to direct: **4
  insertions, 5 deletions, no module added.** Pure Go, so `CGO_ENABLED=0`
  multi-arch builds are unaffected.

**Core caught a real defect here too.** Every expected output key in the test
suite was produced by **Bitcoin Core v31.1.0**, not by this package. The 1- and
2-leaf cases agreed immediately; the **3-leaf case disagreed** — a flat
`MerkleRoot(scripts ...[])` silently assumed a balanced tree and computed
`br(br(hA,hB),br(hC,hC))` where Core's nested descriptor
`tr(K,{pk(A),{pk(B),pk(C)}})` requires `br(hA,br(hB,hC))`. Same leaves, different
shape, different key, valid-looking either way. The defect was the **API**: a flat
list cannot express shape. Fixed with an explicit nested-tree API
(`TreeRoot`/`BranchOf`/`Leaf`) plus a regression test that computes the correct
*and* the incorrect root and fails if they ever converge.

→ `src/taprootverify/`, `research/router-taproot-verify/` (RED + GREEN captured),
`changelog.d/944-taproot-verifier.internal.md`.

## 6. What is NOT done — stated plainly

| Item | Status | Why |
|---|---|---|
| Merging this PR | **operator** | 1 human approval required; the bot cannot self-approve |
| Publish the LNURLcash review to the LUD-25 author | **operator** | drafted and held deliberately |
| Cross-compile `taprootverify` for the router's aarch64 target | **not done** | pure Go, so expected unaffected — but "expected" is not "verified" |
| Wire the verifier into a spend path | **not done** | the `WalletPort` seam is **dead code** (`src/merchant/merchant.go` calls `NewWalletPort` directly); reviving it is a larger change with its own fund-safety analysis |
| Hedgehog override race | **REASONED only** | needs a reorg or time-warp; not modelled |
| Hedgehog end-to-end Core accept | **not achieved** | see §2's honest limit |
| Decide whether TollGate ships a second instrument | **operator** | product decision, not technical |
| Mainnet | **untouched** | all work was on regtest |

## 7. Method note — why the errata are the most valuable output

This branch records **seven retracted or corrected claims** across its life,
including three in the immediately preceding pass alone: a claim that Core had
confirmed our commitment math (it had not — an invalid `pk()` form with an
unchecked return code), an implication that a witness failure might be a BIP-342
semantic rejection (it was our own 65-byte control block on a single-leaf tree,
which needs 33), and a claim that the BIP-342 answer would stay unreachable for
want of a host (the rig's own local regtest node sufficed all along).

Every one was caught by the same discipline: **ask Bitcoin Core, not our own
code.** That is the single transferable lesson here, and it is why the four
original rig bugs, the tree-shape bug, and the BIP-342 ruling itself were all
obtainable at all.
