# Fork-ownership grade per option (T9)

**Status:** 2026-09-14. Grade: **(A)** we consume upstream releases · **(B)** upstream
maintains it but not our platform, so we carry a patch set · **(C)** we own a fork.
This measures **cost**, not preference (operator demoted diversification to an
observation; ownership cost survives).

## The measurement inputs

- our commits to the dependency (how much fork delta we carry),
- unadopted upstream releases (drift),
- whether upstream CI covers musl / our arches,
- (for a port) whether a Linux target lives upstream at all.

## gonuts-tollgate — **grade C (the thing we are trying to leave)**

- Upstream `elnosh/gonuts` is **dead**: no commits since 2025-09-13; the
  steward's own PR #149 blocked ~12 months; steward issues unanswered.
- Our fork carries **load-bearing** content upstream can never absorb:
  `296c7bf` HTLC signature-enforcement fix, `7dc430b` swap proof-loss fix, plus
  V2 / short-keyset-ID support and reseller overpayment handling.
- Drift: unbounded, because there is no counterparty to upstream to.
- Blast radius: **327 branches carry a `replace`**, **50 already point at our own
  fork-of-the-fork** — renaming/re-pointing is not a small edit.

**Verdict:** C by definition; the maintenance is ~1–2 eng-days/month forever, and
it is the fork that the whole programme exists to escape.

## CDK — **grade A/B (sidecar) · B (in-process FFI)**

- Upstream `cashubtc/cdk` is **active**: minor roughly every ~12 weeks, patches
  every ~2–3 weeks; `v0.18.0` shipped 2026-09-02; nightly builds published. On a
  self-declared ALPHA, that cadence is the risk we take on by adopting.
- **In-process `cdk-go` FFI → grade B.** `cdk-go` publishes prebuilt native libs
  for glibc only (T5c); to use it on musl we would carry a platform patch/build
  fork of the binding's native layout — a patch set we own.
- **Process sidecar `cdk-cli` → grade A** (with a build recipe). We consume an
  upstream crate and build it for musl ourselves. The only local delta is the
  build recipe (`-C panic=abort` + self-contained musl libs, from T5c) — not a
  source fork.
- Platform coverage: upstream CI does **not** build musl/mipsel for us, and
  **mipsel is blocked** for the full `cdk-cli` (T5c: no 64-bit atomics).

**Verdict:** A via sidecar for aarch64; the mipsel gap and the ALPHA churn are the
costs. Never grade C as long as we don't fork the source.

## nucula — **grade C unless upstream accepts the Linux target**

- No licence file has ever existed (nucula#8 filed 2026-09-14). No rights ⇒ no
  reuse.
- A downstream Linux port we maintain would be **grade C in C++** — literally the
  gonuts problem again, different language, bus factor one.
- Only if the author accepts and **maintains** an upstream Linux target (option
  (a) in nucula#8) does nucula become grade A/B.

**Verdict:** C today; gated entirely on nucula#8.

## Summary

| Option | Grade | Why |
|---|---|---|
| stay on gonuts (forked) | **C** | we own a fork of a dead upstream, with security-critical deltas |
| un-fork gonuts | *impossible* | path (i) has no counterparty (upstream dead); path (ii) reverts the security fixes + V2 support |
| CDK via sidecar | **A** (aarch64) | consume upstream crate; build recipe only |
| CDK in-process FFI | **B** | we'd own a musl native-layout patch set |
| nucula | **C** | unlicensed; a port we own unless upstream adopts it |
