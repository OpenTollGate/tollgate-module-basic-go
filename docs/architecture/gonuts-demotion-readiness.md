# Gonuts demotion readiness — decision brief

Status: PROPOSAL (owner decision; no code change rides on the answer before
it). Written 2026-11-01 at the close of the P4 work, on the evidence below.

## The question

`wallet-policy.json` already selects cdk for every target; gonuts remains
the config-overridable fallback (`wallet.backend` absent/`default`) and is
still built, tested and maintained in-tree. Demotion means choosing gonuts's
status from here: (a) maintained fallback indefinitely, (b) unmaintained
legacy — builds and runs, no new features, bug fixes only for stop-ship, or
(c) removal on a dated timeline. This brief does not recommend (c).

## Why the question is answerable now

Every gate the demotion decision was made contingent on now exists and is
green, at both layers:

| Gate | Evidence |
|------|----------|
| Wallet-layer behavioral parity | three-backend parity lane (gonuts in-process / cdk-walletd sidecar / cdk-go in-process), same WalletPort money-cycle assertions, 7 cases each, mutation-checked (the gate-bypassed mutant cannot pass) — tests/cloud-lab/parity/ |
| Crash-boundary fund safety | the kill cases (melt/receive/counter boundaries) with conservation invariants; cdk recovery shapes measured and pinned; micronuts comparison isolates the value of the saga architecture (no-saga loses 100%/40% at the same boundaries) |
| Module-level conformance | MODULE-E2E-MATRIX.md: five legs × both backends, 3× green runs each — payment, replay refusal, outage refusal (correct class per backend), kill-mid-payment value verdict, quote persistence across dependency restart |
| Ambiguity semantics | ErrOutcomeUnknown end-to-end on both paths: unit (RED-first), module e2e LEG D (deterministic swallowed response → do-not-resubmit notice, spent replay refused) |
| Degrade safety under backend=cdk | degraded-mode wallet factory routes through the selection (#1157 fix, RED-first); a dead sidecar = wallet-less degraded, fail closed |
| Packaging on both OpenWrt eras | cdk-walletd .apk installs, boots, round-trips money, survives SIGKILL+reinstall on 25.12; procd supervision + load-or-create seed proven; mipsel 8.13 MB binary fits a 16 MB seat (4.16 MB .apk), qemu-proven minting |
| Rollback path | `wallet.backend: default` in config reselects the in-process wallet with zero other changes — the selection seam, the byte-identical legacy path, and the matrix columns all exist to verify it |

Measured operating facts: sidecar peak RSS 14.1 MB (in-process cdk harness
17.0 MB; gonuts in-process for comparison ~9 MB — smaller but archived);
UDS overhead 0.06 ms/RPC (noise vs mint RTT); aarch64 16.4 MB binary.

## What is NOT done (and what it does/doesn't block)

- On-device proof (labgrid/PRTA: RSS/boot/flash on a real seat, both eras).
  Blocks nothing in this repo's code; it is the deployment sign-off, and the
  QEMU/procd lanes above are the closest software equivalents already run.
- CI on Amperstrand/cdk-walletd (org Actions billing — owner gate).
- A fleet migration path for installed gonuts wallets (journaled token
  transfer) — needed only when gonuts is REMOVED, not for demotion; the
  rehearsal for it is recorded in the campaign archive.

## Risks of demoting (honest list)

- gonuts is the only backend that runs with zero external process; the
  sidecar adds one supervised daemon per router (procd respawn proven, and
  the fail-closed semantics around its outage are now pinned by the e2e).
- The gonuts fork carries ~36 unmergeable-upstream patches; every new
  wallet-level fix there is dead-end work — the maintainability argument
  that started the migration.
- Installed base: existing routers hold gonuts wallets; demotion must not
  change shipped defaults for them without the migration path above. A
  demotion today changes STATUS (engineering attention), not shipped
  behavior — the policy default is already cdk.

## Recommendation

(b): declare gonuts an unmaintained legacy fallback now — freeze features,
stop-ship fixes only, close no doors. Revisit removal only after on-device
proof and the token-transfer migration rehearsal have been exercised on the
installed base. Every artifact cited above is in the repo or its PRs
(#1155 and the campaign handoff for the raw measurements).
