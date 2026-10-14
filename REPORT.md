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
| P0.1 | Land the consultant deliverables into the repo | IN PROGRESS |
| P0.2a | Fix the errata bug-count inconsistency ("six" → FOUR) | pending |
| P0.2b | Fix the `step_size` unit erratum (bytes, not ms) | pending |
| P1.3 | LNURLcash POC: validate against REAL regtest consensus | pending |
| P1.4 | Hedgehog POC: headless harness + verify D5 verdict | pending |
| P1.5 | Router-side taproot-note verifier (instrument-agnostic) | pending |
| P2.6 | Does any construction give non-custodial AND offline? | pending |
| P2.7 | Is an on-chain channel economic at a realistic price? | pending |
| P2.8 | What should TollGate take from LNURLcash? | pending |
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

## Notes carried into the loop

- `~/reports/` is **local-only by policy** and never pushed — landing it in the
  repo is the whole point of P0.1.
- The regression bar for any touched assertion: it must still hold. Do not weaken
  a test to make a loop pass.
- Core-validation is the point: the existing rig caught FOUR real bugs that a
  simulator would have passed silently. Validate against consensus, not our code.
- Do not re-verify what a committed artefact already establishes — cite the path
  and move on.
