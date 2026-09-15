# Phase 1 — feasibility spikes: results + go/no-go

**Status:** complete 2026-09-14. Two spikes ran to answer the open buildability
questions for the "best wallet per router" goal. Raw output lives in
`experiments/cdk-footprint/` and `experiments/nucula-port/`.

## Spike C — CDK wallet-only, mipsel + footprint → **GO**

- Built a wallet-only CDK binary (`cdk` `default-features=false, features=["wallet"]`
  + `cdk-sqlite`; **no nostr**).

| Target | Size (stripped) |
|---|---|
| aarch64 musl, release | 7.1 MiB |
| aarch64 musl, `release-smaller` (opt-level=z + LTO) | **4.1 MiB** |
| mipsel musl, release | 9.4 MiB (**builds**) |

- **The mipsel "hard blocker" is gone.** The failure was `cdk-common`'s
  feature-gated test helpers (`AtomicU64`), pulled in because
  `cdk-sql-common`/`cdk-sqlite` depend on `cdk-common` with `features=["test"]`.
  `lightning` and `tokio` compile fine. The fix is **2 imports + 1 dep**
  (`portable-atomic`) — **upstreamable, no fork** (`cdk-common-portable-atomic.patch`).

**Verdict:** CDK is buildable on **both** router arches. Footprint is now a
**budget question (~4–9 MiB), not a wall.**

## Spike N — nucula core cross-compile → **GO on compilation; run pending**

| Target | Objects | Compile | `int64_t` warnings |
|---|---|---|---|
| mipsel_24kc | 20/20 | clean | **0** |
| aarch64_cortex-a53 | 20/20 | clean | **3** (`%lld` vs `int64_t`) |

- nucula's core compiles for both arches with the OpenWrt toolchains.
- The port-map's `int64_t` hazard is **aarch64-only** (LP64 `int64_t == long`);
  on mipsel `int64_t == long long`, so `%lld` is correct → the hazard is *not* a
  mipsel blocker.
- **Not yet done:** target dep-lib builds + link + **run** (qemu-mipsel then a
  physical mipsel router). That is plumbing + hardware, not a portability wall.

**Verdict:** nucula is cross-compilable; the run proof and the port work
(persistence/keystore/HTTP/NUT-07/09 + per-proof storage) remain.

## What Phase 1 changes

1. **CDK is no longer out for mipsel/small routers.** The earlier "CDK can't fit
   16 MB" verdict softens to "depends on the budget": a ~4 MiB (aarch64) wallet
   sidecar is a different proposition from the 19.7 MiB full `cdk-cli`.
2. **Both candidates now build for both arches**; the decision moves from
   *buildability* to **(a) flash budget, (b) security/behavioural parity, and
   (c) ownership**.
3. **The standardisation plan (Phase 2) is now the critical path**: one
   `WalletPort` + sidecar client so either backend can be selected per router
   target, plus T16 test de-coupling.

## Next (Phase 2 + follow-ups)

- Send the `portable-atomic` patch upstream to `cashubtc/cdk` (test-helper
  portability; benefits everyone).
- Finish Spike N's link + run (build mbedtls/TinyCBOR/cJSON/secp256k1 for the
  target; qemu-mipsel; then a physical mipsel router).
- Phase 2: standardise the seam (capability manifest, sidecar `WalletPort`
  client, T16).
- Phase 3: parity suite (T15) — the adoption gate for either candidate.
