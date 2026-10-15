# REPORT — tollgate-bearer-instruments ralph loop

Human-readable state. Blockers needing the operator go in HANDOFF below with the
exact action required. Everything else is progress; the raw log is PROGRESS.md.

**Loop started:** 2026-10-15T01:10Z
**Branch:** `pr/one-way-channel-spike` (PR #944, draft) @ `67e2be2`
**Quota gate at start:** ALLOW via fallback-probe; `deliverable=true`; not frozen.

---

## Scope status

| # | Task | Status |
|---|---|---|
| P0.1 | Land the consultant deliverables into the repo | **DONE** (aeeba1f) |
| P0.2a | Fix the errata bug-count inconsistency ("six" → FOUR) | **DONE** — already clean, verified |
| P0.2b | Fix the `step_size` unit erratum (bytes, not ms) | **DONE** — 4 stale spots fixed |
| P1.3 | LNURLcash POC: validate against REAL regtest consensus | **DONE (headline claim MEASURED)** — Core ruled per opcode on real script-path spends: `OP_CHECKMULTISIG(VERIFY) is not available in tapscript` vs CHECKSIGADD executing normally (failing only on a placeholder sig). 6 measured claims total; 3 earlier claims retracted. See KERNEL-CHECK-FINDINGS.md |
| P1.4 | Hedgehog POC: headless harness + verify D5 verdict | pending |
| P1.5 | Router-side taproot-note verifier (instrument-agnostic) | **DONE** (adaccc4) — `src/taprootverify/`, Core-validated, 47 assertions, no new dependency. Core caught a real 3-leaf tree-shape bug. |
| P2.6 | Does any construction give non-custodial AND offline? | **DONE — NO.** Hedgehog is closest (non-custodial ✓ sender-offline ✓ recipient-offline ✓) but RELOCATES the time obligation into a CSV window. See D5-RETEST.md |
| P2.7 | Is an on-chain channel economic at a realistic price? | **DONE — depends on purchase size.** Live config: metric=bytes, 1 sat = 21 MiB. See §10 + granularity.py |
| P2.8 | What should TollGate take from LNURLcash? | **DONE — NOT the two hypothesised items; both REJECTED.** Bounded experiment then watch. See P2-VERDICTS.md + OPTIONS-PAPER.md |
| P3.9 | STC non-interactive question (prepare only) | pending |

---

## HANDOFF — needs the operator (do NOT retry)

| Item | Exact operator action |
|---|---|
| Merge PR #944 | 1 human approval required; the bot cannot self-approve. Flip to ready only on request. |
| Publish the LNURLcash review to the LUD-25 author | Post is written and held at `docs/analysis/LNURLCASH-REVIEW-POST.md`. |
| Decide whether TollGate ships a second instrument | Product decision, not technical. |
| Mainnet spend | Forbidden. Never. |

---

## Key results so far

**OPTIONS-PAPER (T3) verdict on LNURLcash in TollGate.** LNURLcash's benefits
accrue to the **WALLET** side of the protocol; TollGate is the **SERVICE** side.
Consumption state at the moment of grant is the one thing `cs1` structurally
cannot supply — the spec itself says so. Of five claimed gains: one is
real-but-weak-for-a-redeemer, two belong to a counterparty role TollGate is not
in, and two are absent. **Verdict: bounded experiment, then watch — not now.**
It also corrected T1: the chain/taproot dependency is **not** a real cost
(`lnurlcash-go` is pure Go, and `btcec/v2` is already in the shipped binary).
Named as worth doing regardless of the verdict: **X5, kernel agreement**.

**ROUTER-FEASIBILITY (T1) two load-bearing findings:**
(a) the taproot verification primitives are **already compiled into the shipped
`/usr/bin/tollgate-wrt`** (strings: `ErrTaprootSigInvalid`,
`ErrSchnorrHashValue`, `OP_CHECKSIGADD`) via btcd txscript+btcec as *indirect*
deps — so an in-process verifier needs no new dependency and no new cgo;
(b) the `WalletPort` seam is **dead code** — `merchant.go:509` calls
`NewWalletPort` directly, bypassing the only function that could select a backend.

## NEXT ACTION (resume here)

**P1.3 witness fix** — highest value, precisely located. `kernel_check.py` builds
a script-path witness as `[sig, sig, "", script, ctrl]` (MULTISIG) and
`["", sig, sig, script, ctrl]` (CHECKSIGADD). Core rejects with
`Witness program hash mismatch`, so the control block presented does not commit
to the output being spent. Our commit math is PROVEN correct (Core decodes our
key as `witness_v1_taproot`), so the fault is in witness assembly — most likely
the witness item ORDER, or that a 1-leaf tree's control block must carry only
`(parity|0xc0) || NUMS || tapleaf_hash` with the leaf NOT repeated in the witness
stack for the branch actually taken. Fix: derive the spend Core itself would
build (fund to a `tr()` descriptor Core can sign, let Core produce the witness,
compare shapes), then re-submit.

## Notes carried into the loop

- `~/reports/` is **local-only by policy** and never pushed — landing it in the
  repo is the whole point of P0.1.
- The regression bar for any touched assertion: it must still hold. Do not weaken
  a test to make a loop pass.
- Core-validation is the point: the existing rig caught FOUR real bugs that a
  simulator would have passed silently. Validate against consensus, not our code.
- Do not re-verify what a committed artefact already establishes — cite the path
  and move on.
