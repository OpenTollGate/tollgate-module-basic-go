# Recommendation (T7) — replacing `gonuts` as TollGate's Cashu wallet

**Status:** concluded 2026-09-14 on the evidence in this branch. Opens with the
objective scorecard (T13), then the recommendation (T7), then the explicit
stay-on-gonuts decision (T8) and preserved dissent.

---

## Objective scorecard (T13)

Values are measured where a measurement exists; "not measured" is stated as such
rather than assumed. Weights are **not** applied — the operator asked for an
objective comparison, and the trade-offs are written where each option wins.

| Axis | gonuts (current, forked) | CDK — sidecar (`cdk-cli`) | nucula |
|---|---|---|---|
| **Fit for OpenWrt** | ✅ aarch64 + mipsel, static, in-process | ✅ aarch64 static musl (T5c); **✗ mipsel blocked** (no `AtomicU64`) | ✗ not a router component (ESP32 firmware) |
| **Footprint** | service: 11.5 MiB bin, ~26 MiB RSS, 8 thr | sidecar: 19.7 MiB bin, **~5.6 MiB RSS, 5 thr** (unconfigured) | n/a |
| **Coverage / contract** | complete (the reference) | adapter exists but **unverified** (T5a); sidecar client not written | **missing NUT-07/09** + persistence rewrite |
| **Interchangeability** | n/a (incumbent) | good — RPC client behind unchanged `WalletPort`; grade-A ownership | poor — needs C++→Go re-implementation |
| **Reliability** | in production; holds the two security fixes | **not measured** — parity suite (T15) not yet implemented | not measured; disqualified earlier |
| **Ownership cost (T9)** | **C** (we own the fork) | **A** sidecar / **B** FFI | **C** unless upstream adopts the port |
| **Licence** | GPL-3.0 (fork) | MIT/Apache-2.0-compatible | **none** (nucula#8 filed) |

Diversification is an **observation**: keeping the router on a Go wallet already
diversifies it from CDK (Rust). The defect is the *fork*, not the language.

---

## Recommendation (T7)

**Do not switch the production wallet yet. Do this instead:**

1. **Keep gonuts-tollgate (the fork) as the router wallet for now** — it is the
   only option that is complete, mipsel-capable, and holds the two funds-safety
   fixes. It is grade C, but switching today would be a regression risk, not a
   gain.
2. **Adopt CDK as a shadow, process-isolated sidecar** (the T5c-measured path).
   Write the thin Go `WalletPort` client (~3–6 eng-days, T14) and run it
   **alongside** gonuts behind the shared conformance suite. This yields a real,
   measured alternative without touching production.
3. **Implement the parity suite (T15/T2e) before any switch.** The fork's HTLC
   signature-enforcement (`296c7bf`) and swap proof-loss (`7dc430b`) fixes are
   acceptance criteria; no candidate is adoptable until it passes failing-tests-
   per-case. This is the single highest-risk gate.
4. **Resolve the nucula licence question (nucula#8) before spending further
   nucula effort.** Even then, nucula is a device wallet product, not a router
   wallet; treating it as a router substitution was the original misconception,
   and the port is an upstream-or-nothing decision.
5. **Do T16 (test de-coupling) regardless** (~1–2 days) — it turns the three
   un-fork paths into a reversible choice and shrinks the 327-branch blast radius.

**Bottom line:** CDK via sidecar is the intended destination (grade-A ownership,
active upstream, measured to build and run on the primary arch); the path there is
parity + optionality, not a big-bang swap. nucula is out as a router wallet.

---

## T8 — explicit stay-on-gonuts decision (adopted, with conditions)

We explicitly **adopt** "stay on gonuts, forked" **as a deliberate, time-boxed
position**, not by omission:

- **Why it wins today:** complete contract coverage; mipsel support; it already
  contains the security fixes; zero integration risk.
- **Why it loses long-term:** grade-C ownership of a dead upstream, ~1–2
  eng-days/month forever, with security drift risk.
- **Exit condition:** switch to the CDK sidecar once (a) the sidecar client
  exists, (b) the parity suite passes, and (c) a mipsel story exists (a
  Nostr-free wallet-only sidecar, or the decision that mipsel routers are
  out-of-scope). Until then, gonuts stays.

**Rejected alternative — "un-fork gonuts":** path (i) is impossible (upstream is
dead — no counterparty), and path (ii) (drop to upstream root v0.4.2) costs 4–8
weeks and reverts the signature fix, the proof-loss fix, all V2/short-keyset
support and reseller overpayment (`03-baseline/unfork-gonuts.md`).

---

## Dissent preserved

- **Consultant B (CDK):** prefer the sidecar over FFI, but flags the ALPHA API
  churn — a two-month-old pin is not "pinned" on a self-declared ALPHA, and the
  router pin (`v0.17.3`) is already a minor behind `v0.18.0`.
- **Consultant A (nucula):** the protocol core is genuinely reusable *as ideas*;
  the recommendation must keep reading it as a reference even though it is out as
  a substitution.
- **Consultant C (method):** every number must stay reproducible; this branch
  keeps `experiments/` as the source of truth, and "not measured" is a legitimate
  scorecard value.

## What would change this recommendation

- Parity suite passing for CDK **and** a mipsel story → switch to CDK sidecar.
- nucula#8 answered with a licence **and** upstream acceptance of a Linux target
  → re-open nucula as a grade-A/B option.
- Upstream `elnosh/gonuts` reviving (a counterparty appearing) → re-open un-fork.
