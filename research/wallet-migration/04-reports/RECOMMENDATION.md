# Recommendation (draft — awaiting consultant findings)

**Not yet concluded.** This file is deliberately empty of a verdict until:

1. T1b establishes the `WalletPort` acceptance contract,
2. T2/T3 return the candidate assessments with citations, and
3. T4 fixes the metric set so both candidates are judged on the same axes.

## Provisional framing (to be tested, not assumed)

- **nucula** is not a router wallet; it is ESP32-C3 firmware, and it has no
  licence file. Treat "replace gonuts with nucula on the router" as
  **not applicable** unless the maintainer redefines the goal as a device-side
  wallet — in which case the licence question must be settled first.
- **CDK** is the serious candidate for *maintainership*, but it is Rust/ALPHA and
  the router is Go, so the real decision is the integration architecture
  (cgo+`cdk-ffi` vs sidecar) and whether we are trading a fork we maintain for
  an ALPHA API we must track.
- A third option that must be considered and rejected explicitly rather than by
  omission: **staying on gonuts but un-forking** (contribute upstream, or vendor
  a pinned upstream release) if the fork's content is small enough that the
  maintenance burden is lower than an integration project.

## Provisional framing update (after finding the existing CDK adapter)

The comparison is no longer symmetric, and the report must say so plainly:

- **CDK**: an adapter already exists in-tree behind a build tag, using a Go
  binding (`cdk-go`) over CDK's FFI — so the architectural question is largely
  answered, and the remaining risk concentrates in (a) whether `cdk-go` is real
  and maintained, (b) cgo cross-compilation for the router arches, (c) runtime
  cost and FFI failure modes.
- **nucula**: no router integration path exists, it is ESP32-only firmware, and
  it has **no licence file**. It cannot be recommended as a router wallet;
  the only honest recommendation is to decide separately whether TollGate wants
  a *device* wallet product, and to settle licensing before any code is reused.

## Operator constraints added 2026-09-13 (these change the calculus)

### C1 — "Treat nucula as an upstream library and make a Linux service out of it"

Technically this is exactly the port project now scoped (T2b/T2d/T2e): extract the
wallet core, replace ESP-IDF platform services, wrap it in a daemon. Three
conditions decide whether it is *wise*, not merely possible:

1. **Licence.** No licence file has ever existed upstream. A port we cannot
   legally ship is a research artifact. Non-negotiable, and it is the author's
   call, not ours.
2. **Upstream acceptance, or we inherit the fork problem.** This is the point
   that matters most. "Use it as an upstream library" only holds if the Linux
   port lives *upstream* and the author maintains it. If we port it and keep the
   port ourselves, we have recreated `gonuts-tollgate` — a fork we maintain, in
   C++ instead of Go, with a bus factor of one and no licence. That is the exact
   outcome this whole programme exists to escape.
3. **Buildability for musl/OpenWrt** across the router arches — being measured now.

### C2 — Dependency diversification across TollGate implementations

Operator's rationale: other TollGate implementations will use CDK, and
implementations should not all break at the same time when Cashu makes breaking
changes. This is sound and it must be a **scoring axis**, not a footnote.

Consequences to weigh honestly:

- **Diversification is already partly satisfied by *not* moving.** The router's
  current wallet is Go (`gonuts`); CDK is Rust. Keeping the router off CDK is
  itself the diversification. The defect is the *fork*, not the language — so
  "un-fork gonuts" (contribute upstream, or vendor a pinned upstream release)
  scores well on diversification at near-zero integration cost. It must not be
  rejected by omission (T8).
- **Maximum independence has maximum cost.** nucula (C++) gives the most family
  independence — a third language, a third codebase — but requires a port we may
  end up owning (C1.2), on top of an unresolved licence (C1.1).
- **CDK is the family the operator wants *elsewhere*.** Because CDK is self-described
  ALPHA with an API we would have to track, concentrating the router on it is the
  worst diversification outcome even if it is the best maintenance-optics outcome.

### New metric: fork-ownership risk (extends `02-method/overlooked-metrics.md`)

For each option ask: **does adopting this make us the de-facto maintainer of a
downstream copy?** Scoring: (a) upstream maintains it and we consume releases;
(b) upstream maintains it but not our platform, so we carry a platform patch set;
(c) we own a fork outright. `gonuts-tollgate` is (c) and is the thing we are
trying to leave. Any nucula port that does not land upstream is also (c).
