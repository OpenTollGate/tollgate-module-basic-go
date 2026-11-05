# Wallet Backend on Routers: Migration Decision — Architecture Decision

> **Status: Proposed (2026-11-05).** Nothing in this record is in force yet.
> Acceptance is a maintainer action — the drafting account may not accept
> its own proposal.

## Context

TollGate's wallet layer has three implementations behind the `WalletPort`
seam (`src/tollwallet/port.go`):

- **gonuts-tollgate** — the pure-Go fork we maintain; the default since
  inception; ships on every arch (mips/mipsel/arm/arm64/x86, CGO-free).
- **cdk-go in-process** — the upstream Rust CDK wallet via FFI; behind the
  `cdk_wallet` build tag; requires CGO and per-target native libraries.
- **cdk-walletd sidecar** — the CDK wallet as a standalone Rust daemon,
  consumed over AF_UNIX RPC; behind the sidecar build tag; the gateway
  binary stays CGO-free.

The owner direction is to migrate away from gonuts as the primary, keeping
it as a fallback. This record decides the shipping order and the criteria
for promoting each backend to primary.

## Measured evidence (all qemu-fidelity, live mint, same contract)

| | gonuts (pure Go) | cdk-go in-process (FFI) | cdk-walletd sidecar |
|---|---|---|---|
| WalletPort contract | 5/5 x86, 5/5 mips, 5/5 mipsel | 5/5 x86, 5/5 mips, 5/5 mipsel | minting verified (mipsel); full contract via #1137 lane |
| Corpus decode (7 fixtures) | 7/7 (hex_c lenient-accept is a latent defect) | 7/7 (correctly strict) | not measured |
| Crash isolation | wallet panic = gateway down | wallet panic = gateway down | **wallet crash ≠ gateway crash** |
| Binary constraint | CGO_ENABLED=0 everywhere | CGO_ENABLED=1 + native libs per arch | CGO_ENABLED=0 gateway + separate Rust daemon |
| Memory (router-class 64–128MB) | ~1 process | 1 process (Go GC + Rust allocators) | 2 processes (~+20–40MB Rust RSS) |
| MIPS viability | ships today | proven (nostr-free + portable-atomic nostr, both endiannesses) | proven (mipsel daemon) |
| Upstream alignment | our fork (we own everything) | upstream cdk-go + 3 adapter-side defenses (RuntimeGuard is fixed on their main, pending release; Receive-on-spent and enum panics are open issues) | upstream cdk + vendored nostr patch |
| Upgradability | fork tag bump | native lib swap or version bump | daemon swap, gateway untouched |

## Decision

**D1 — staged adoption with field-data gate.**

1. **Immediate (this release train):** cdk-migration branch carries the
   FFI backend for x86/dev; gonuts remains the shipped default on all
   archs. The parity suite (#1203) is the release gate for the tag.

2. **Next train (after on-target validation):** sidecar on router-class
   hardware. Crash isolation is the deciding property: an unattended
   payment gateway whose wallet daemon can crash without taking the
   captive portal, the admin API, and the firewall with it is strictly
   better than one where a wallet bug is a full outage. The gateway
   binary stays CGO-free (the repo rule).

3. **gonuts remains the fallback** for at least two release trains: any
   sidecar regression falls back by stopping the daemon and rebuilding
   with the default tag. It is not removed until the sidecar has survived
   a full soak cycle on the bench.

**D2 — the on-target run is the last evidence gate.** The qemu matrix is
complete; what it cannot measure is: real NOR flash behavior, real RF/captive
portal interaction, RSS under real load, and long-run stability. The bench
campaign (PRTA + virtual lab) owns this; the evidence lands in #1134.

**D3 — upstream cdk-go v0.18.3 is the minimum.** The parity suite passes
against it without any fork replace. The three fixes we filed upstream
(RuntimeGuard leak-Drop, Receive-on-spent broken buffer, enum zero-value
panics) are worked around in our adapter; each workaround retires when
upstream ships the fix. #1149 tracks the fork-replace retirement.

## Alternatives considered (and why refused)

- **Pure-Go rewrite of the CDK wallet** — months of work to duplicate what
  gonuts already provides; the migration direction is *away* from
  maintaining our own wallet implementation.
- **bbolt → SQLite migration in gonuts** — addresses one symptom (the
  jffs2 mmap wall) while keeping the maintenance burden; the ext4-loop
  image (#1080) already solves that symptom.
- **In-process FFI as the router default** — violates the CGO-free rule
  that keeps the shipped binary reproducible across all targets; the FFI
  poison class (RuntimeGuard, enum panics) takes the whole gateway down.

## Open measured questions (bench-gated)

- Q1: NOR wear with the ext4-loop wallet image (ADR #1079) — soak class.
- Q2: sidecar RSS on a real 64MB router — determines the floor.
- Q3: FFI vs sidecar latency under load — expected irrelevant (mint RTT
  dominates), but measure once to close it.
