# Parity harness (T15 + T2e)

**Status:** design + skeleton 2026-09-14. These are **acceptance tests**: the gonuts
fork carries two funds-safety fixes that upstream will never absorb
(`03-baseline/unfork-gonuts.md`), so any replacement must reproduce them before
the old wallet is removed. A wallet swap that silently reverts a
signature-enforcement fix is a **security regression**, not a migration.

## What must be reproduced (the two fork fixes)

| Case | Fork commit | Property to prove |
|---|---|---|
| **HTLC signature enforcement** | `296c7bf` | the wallet rejects a melt/flow that bypasses HTLC signature checks |
| **Swap proof-loss** | `7dc430b` | an interrupted swap preserves proofs / value (no loss) |

## Case set (run against *every* adapter)

Each case gets a **failing test against the pre-fix behaviour** and a passing
assertion against the candidate. Cases are driven through `WalletPort` only.

### `htlc_signature_enforcement`
1. Stand up a local mint fixture (below) that serves a melt quote whose HTLC
   conditions the pre-fix wallet accepts without enforcing.
2. Attempt the flow through the candidate.
3. **Assert:** the candidate **rejects** it (error, no funds moved).

### `swap_proof_loss`
1. Request a swap (send/receive path) against the local mint.
2. Fault-inject a kill between proof deletion and re-issue (or drop the mint
   response after acceptance).
3. Restart the candidate and reconcile (NUT-07/NUT-09).
4. **Assert:** no proofs/value lost; balance reconciled with the mint.

### `behavioural_parity` (T2e)
Run the same mint-quote → mint → swap → melt sequence with fixed inputs against
gonuts and the candidate; **assert identical results** (amounts, states, tokens
modulo blinding).

## Reproducibility

Per the branch rule, every run records: arch, OS image+version, build commands,
the candidate's commit SHA, and raw output.

- **Mint fixture:** reuse the harness mint used elsewhere in this programme
  (`mint-health/` / the `TEST_MINT_URL` fixture). Pin the mint's version and the
  keyset ids used, so vectors are stable.
- **Runner (skeleton):** `run.sh` — parameterised by `ADAPTER` (gonuts|cdk|nucula)
  and `MINT_URL`; emits `raw/<case>-<adapter>.txt` + `env.txt`.
- **Where it plugs in:** the same table-driven suite defined in
  `03-baseline/interchangeability.md` §"single conformance suite".

## Why this is the decisive gate

- The **interface** is small and stable (T1b); all three candidates *can* be
  wrapped in principle.
- The risk is entirely in **behavioural + security parity**. These cases are what
  turn "CDK is the serious candidate" (T3/T5c) into "safe to adopt".
- Until they pass, the honest recommendation is to keep gonuts (forked) as the
  router wallet, ship CDK as a **shadow sidecar** for measurement, and resolve the
  nucula licence question (nucula#8).

## Not yet done

The cases above are specified but **not implemented** — they need the local mint
fixture wired to the adapter harness. This is the next executable step for T15;
T5c already produced the candidate build/measure apparatus
(`../cdk-cross/`, `../cdk-sidecar/`).
