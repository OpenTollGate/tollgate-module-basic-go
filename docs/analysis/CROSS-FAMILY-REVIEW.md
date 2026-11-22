# Cross-family review — one-way-channel spike (PR #944)

- reviewer_requested: `kimi-k2.7-code` (non-GLM family)
- reviewer_model_served: `deepseek-flash`
- author family: GLM — satisfies the cross-family rule (reviewer family differs)
- artifacts reviewed: `docs/architecture/one-way-channel-spike.md`, `docs/analysis/SPIKE-REPORT.md`, `research/one-way-channel-spike/out/GREEN.txt`
- finish_reason: `stop`
- usage: {"prompt_tokens": 14629, "completion_tokens": 15390, "total_tokens": 30019, "prompt_tokens_details": {"cached_tokens": 0}, "completion_tokens_details": {"reasoning_tokens": 14470}, "prompt_cache_hit_tokens": 0, "prompt_cache_miss_tokens": 14629}

---

VERDICT: CHANGES REQUESTED

1. **blocker** — one-way-channel-spike.md §0/§2 (and SPIKE-REPORT §2) — Calling four hand-built variants "a disproof" is inductive failure-to-find dressed as a universal negative; D1/D2/D5 assert "no construction exists" but only V0–V4 were tried, so "DISPROVED" overreaches the evidence.

2. **blocker** — GREEN.txt / §6 "falsification made executable" — The two `[FAIL]` lines cannot be *executed*: no script can verify the non-existence of a construction, so "made runnable rather than rhetorical" is false; they are hardcoded placeholders needing human editing, and the "disproof" rests on them.

3. **major** — one-way-channel-spike.md §1 vs SPIKE-REPORT §1/§9 — Bug count contradicts itself ("six" cryptography bugs vs "four"), while the errata table lists six rows with E4/E6 unspecified; this is exactly the credibility narrative ("Core caught what a simulator would hide") the spike leans on.

4. **major** — §0/§3 vs §3 table — The target is silently swapped from "no internet gateway" to "payer-offline": V1 is non-custodial with no internet, so "No, on both counts" hides that the real disproof is of the qualifier *bearer* (non-interactive), not of "no internet."

5. **major** — §3 table "Payer offline?" column — The column mixes two definitions (truly absent vs present-on-LAN), so V1's ✅ is not commensurable with V2/V3's ✅ and the four-way comparison is not apples-to-apples.

6. **major** — §3/§2 V2 and SPIKE-REPORT §4 — "Custodial up to the ladder top" is a definitional choice, not a measurement: the top is the payer's own pre-committed cap, so bounded prepayment exposure is conflated with genuine custody; and I could not construct a non-custodial AND fully payer-offline variant either, so this finding narrows the label but does **not** overturn the custody conclusion.

7. **major** — §4 Cashu comparison — The comparison is asymmetric: Cashu carries only a 0.1% melt and ignores the mint's custody, spread, and its own settlement cost (the very custodian the spike cares about), while the channel is charged a full 716-sat setup+close per purchase; the direction (Cashu cheaper) is robust, the "11,218×/746×" magnitudes are not.

8. **minor** — §4 table — The 64-sat ratio is not reproducible from the displayed numbers: 718 / 0.06 = 11,967, not 11,218, so the row is only consistent if the unrounded 0.064 is used but the table shows 0.06.

9. **minor** — §2 "interactivity ≠ connectivity" — The distinction is correct and load-bearing (it shows the LAN link suffices), but it is also the move that lets the spike claim V1 "answers" the question while simultaneously denying it does — so it relocates rather than resolves the question.

10. **minor** — §4/SPIKE-REPORT §4 "relocates the time obligation... removes the reconciler" — Fair as description but one-sided: the reconciler being lost *is* the custodian whose removal is the channel's stated purpose, so counting its loss as pure downside double-counts the trade.

11. **minor** — GREEN.txt T8 — "Core cannot co-sign a channel state with stock tooling" is plausibly a rig-input artifact (witness script not supplied to `signrawtransactionwithkey`) rather than a Core limitation, so the claimed integration cost rests on a possibly-false test.

12. **nit** — §0/§1 "the rig that proves it" / "Every channel assertion is executed against Core" — Overstated: §9 admits V4 was never built, reorgs were never measured, and no channel was wired in, so not every assertion is Core-backed.
