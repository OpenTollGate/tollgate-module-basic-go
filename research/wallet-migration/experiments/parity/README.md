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

## Implementation status (2026-09-14)

Implemented and passing on the physical MT6000
(`physical-router-test-automation` PR #117, `tests/scenarios/test_wallet_sidecar.py`):

- **Cross-mint (untrusted) token rejection** — `test_reject_token_from_other_mint`:
  a token minted/sent on the trusted mint is fed to a second daemon configured
  for a different mint; it must **not** be credited (receive fails, balance 0).
- **Double-spend rejection** — `test_double_spend_rejected`: receiving the same
  token twice fails the second time.
- **Value conservation** — `test_send_receive_roundtrip`.

Status of the remaining two cases:

- **`htlc_signature_enforcement`** (fork `296c7bf`) — **PARTIAL**. The NUT-11
  (P2PK) half is implemented and passing: a P2PK-locked token presented without
  the required witness is rejected (`Witness signatures not provided`,
  host-verified), and a harness test exists (`physical-router-test-automation`
  #117, `87a3e92`). The NUT-14 (HTLC, preimage) half is still specified.
- **`swap_proof_loss`** (fork `7dc430b`) — **DONE, conclusive**. See below.

### `swap_proof_loss` — conclusive (2026-09-16)

`fault_injection.py` (timer-based) was insufficient: the local fakewallet mint
settles a swap in single-digit milliseconds, so a timed SIGKILL almost never
lands inside the one window that matters (mint accepted, wallet not yet told).

`swap_proof_loss.py` widens that window **deterministically at the network
layer** instead of racing it. The local `cdk-mintd` serves plain HTTP, so
`drop_swap_proxy.py` needs no TLS interception:

1. `cdk-mintd` (fakewallet) behind the proxy. Wallet **A** mints 100 and sends a
   100-sat token.
2. Wallet **B** `receive`s it — a receive always performs an online NUT-03
   `swap`, so it is a deterministic swap trigger.
3. Arm the proxy; B's `POST /v1/swap` is **delivered to the mint** (inputs really
   spent, outputs really issued — the proxy logs `DROPPED 200 … (5635B)`) and the
   **response is discarded**; B is SIGKILLed in that window.
4. **Negative control:** a *fresh-seed* wallet offered the same token fails with
   `Token Already Spent`, balance 0 — proving the inputs really were spent and
   the fault injection is not vacuous.
5. B restarts on the same wallet DB + seed. `recover_incomplete_sagas` runs and
   the balance reconciles to **100 — no value lost**.

Result (raw: `raw/swap_proof_loss-cdk.txt`): **CDK reproduces the gonuts fork's
`7dc430b` no-proof-loss property.**

Run it with the built binaries (see `raw/swap_proof_loss-cdk.txt` for the exact
environment and SHAs):

```
python3 experiments/parity/swap_proof_loss.py \
    --workdir /home/c03rad0r/r2-work/parity-run
```

Earlier daemon-side robustness items this work surfaced, now fixed: a fixed
mnemonic collides against the mint on a fresh DB (`10002 outputs already signed`)
— solved by the per-run random seed; and crash recovery relied on saga /
pending-proof reconciliation, which the daemon now explicitly invokes on startup
(`96018f7`).

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

## Where this stands now

- `cross-mint` / `double-spend` / value conservation — passing on the physical
  MT6000 (`physical-router-test-automation` #117).
- `swap_proof_loss` — **conclusive** host-local (this directory).
- `htlc_signature_enforcement` — NUT-11 half passing; NUT-14 half still open.

The remaining T15 work is the NUT-14 HTLC-preimage leg, plus the same
`swap_proof_loss` run repeated **on the router** against the local mint once the
on-device mint path exists (currently the router has no upstream and cannot reach
a host mint — see `../local-mint/README.md`).
