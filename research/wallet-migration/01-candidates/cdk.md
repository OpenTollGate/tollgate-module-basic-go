# Candidate: CDK — Cashu Development Kit (`cashubtc/cdk`)

## What it is (verified)

Self-described: *"a collection of rust crates for Cashu **wallets and mints**"*,
and explicitly **ALPHA**: *"the api will change and should be used with
caution"*. Actively developed (last push 2026-09-12, 230 stars).

Crates present in the workspace at time of writing:

```
cashu, cdk, cdk-axum, cdk-bdk, cdk-cli, cdk-cln, cdk-common, cdk-fake-wallet,
cdk-ffi, cdk-http-client, cdk-integration-tests, cdk-ldk-node, cdk-lnd,
cdk-mint-rpc, cdk-mintd, cdk-nostr, cdk-payment-processor, cdk-postgres,
cdk-prometheus, cdk-redb, cdk-signatory, cdk-sql-common, cdk-sqlite, cdk-supabase
```

## Why the crate list matters for us

- `cdk-cli` — a wallet/mint CLI; the most likely "sidecar process" integration path on a router
- `cdk-ffi` — an FFI surface; the path for a **cgo/static-link** integration into the Go module
- `cdk-sqlite` / `cdk-redb` — proof storage backends (flash-friendliness matters on a router)
- `cdk-http-client` — the network layer we would be depending on
- `cashu` — the core types; check whether the wallet API we need is a stable library surface or CLI-only
- Heavy mint-side crates (`cdk-mintd`, `cdk-lnd`, `cdk-cln`, `cdk-bdk`, `cdk-postgres`, `cdk-supabase`) are irrelevant here but inflate the dependency graph if badly feature-gated

## Assessment so far

| Dimension | Finding |
|---|---|
| Runtime target | Cross-platform Rust (lib + binaries). Router use requires cross-compiling to musl for the router's arch. |
| Language mismatch | Router module is **Go**. Integration is either `cdk-ffi` + cgo (static musl linking) or a separate process talking to it. Both are architecture decisions, not a dependency bump. |
| Licence | GitHub reports `NOASSERTION`; `LICENSE.md` exists — **verify the actual terms** (MIT?) and compatibility with TollGate's GPL-3.0. |
| Maturity | ALPHA by the authors' own warning; API churn is a real maintenance risk (ironically the thing we are trying to escape). |
| Governance | Maintained under the `cashubtc` org — i.e. the reference Cashu organisation, which is exactly the "maintained by a proper Cashu developer" criterion. |

## Open questions (owner: consultant B, see TASKS.md T3)

- [ ] Exact licence terms + GPL-3.0 compatibility
- [ ] Does a *wallet* library API exist, or is wallet functionality CLI-only?
- [ ] Minimum feature set to build only what a wallet needs (dependency graph, build time, resulting binary size)
- [ ] Cross-compile to `aarch64-unknown-linux-musl` and `mipsel-unknown-linux-musl` (router arches) — does it work, how ugly, what patches
- [ ] Runtime footprint: RSS, thread count (does it require a tokio runtime?), startup latency
- [ ] Storage: sqlite vs redb — write amplification and flash wear on a router
- [ ] NUT coverage vs gonuts (diff the two matrices)
- [ ] Release cadence, contributor count, breaking-change history — a proxy for "will this churn cost us a fork again?"

## Prior art inside the module (verified on this branch's base)

`src/tollwallet/cdk_wallet.go` implements `WalletPort` behind the `cdk_wallet`
build tag and depends on **`github.com/cashubtc/cdk-go/bindings/cdkffi`** — a Go
binding to CDK's FFI surface. That is the integration architecture question
answered in principle: **Go API over cgo into Rust**, not a sidecar process.

Open questions this raises (owner: consultant B, T3):

- [ ] Does `github.com/cashubtc/cdk-go` actually exist, and is it maintained?
      (`go.mod` in `src/tollwallet/` will tell us the pinned version — if it is a
      local `replace` directive, the dependency may not be published at all.)
- [ ] Which parts of the adapter are complete vs TODO — and what the tests
      actually exercise (do they run without the cgo library present?)
- [ ] Does the cgo path cross-compile for the router's musl arches? cgo + musl +
      Rust static library is the highest-risk part of the whole plan.
- [ ] Runtime cost of the FFI boundary: threads (tokio inside?), memory, and
      what happens on panic across the FFI boundary.
- [ ] Mnemonic-at-rest: the seed is a plain file today — encryption at rest and
      file permissions on the router must be a research item, not an afterthought.
