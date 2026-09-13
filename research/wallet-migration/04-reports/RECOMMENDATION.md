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
