# Adapter effort estimate per candidate (T14)

**Status:** 2026-09-14. Effort to implement the **full `WalletPort`**
(`00-context/walletport-contract.md`) per candidate, using measured facts where
they exist (T5c) and code counts from the branch.

| Candidate | Adapter status today | Work to reach full `WalletPort` | Rough effort |
|---|---|---|---|
| **gonuts** | reference implementation (`gonuts_wallet.go`) | none — it *is* the contract | 0 (but grade-C ownership, T9) |
| **CDK sidecar** | none; `cdk-cli` builds (T5c) | Go RPC client implementing `WalletPort` + sidecar lifecycle + parity/reliability suite | **~3–6 eng-days** for the client + tests |
| **CDK in-process FFI** | `cdk_wallet.go` (443 lines) exists, build-tag `cdk_wallet` | make it real: musl build (✗ today), completeness audit, `recover()` at FFI boundaries, persist `quoteToMint`, encrypt seed | **weeks**; blocked on musl |
| **nucula** | none | NUT-07/09 + persistence/keystore/HTTP rewrite; C++→Go reimplementation of a ~3–4 kLOC protocol core, or a C ABI port | **weeks–months**, gated on nucula#8 |

## The two facts that dominate the estimate (both measured)

1. **cgo/`cdk-go` is dead on musl** (T5c): the prebuilt binding is glibc-only, and
   building `cdk-ffi` from source drops `cdylib` and yields a 133 MiB `.a`. So the
   443-line in-process adapter is **not** the cheap path it looks like — the cheap
   path is the **sidecar**.
2. **The sidecar already builds and runs** (T5c): `cdk-cli` → static musl, 19.7 MiB
   stripped, **~5.6 MB RSS / 5 threads** on the GL-MT6000. The remaining work is a
   thin Go client behind `WalletPort`, not wallet code.

## Why nucula's estimate is dominated by a re-implementation, not a port

From `01-candidates/nucula-port-map.md` (measured): the protocol/wallet core is
~5.5 kLOC that compiles verbatim on Linux, but **all persistence, key material,
HTTP transport, and arg parsing must be rewritten**, NFC/display/keypad/wifi thrown
away, and **NUT-07/09 added**. That is a C++→Go re-implementation project, not a
port — and it cannot start until a licence exists.

## Recommended sequence (effort-ordered)

1. **CDK sidecar client** (~3–6 eng-days) — smallest path to a real, measured
   alternative behind the unchanged `WalletPort`, and grade-A ownership.
2. **T16 test de-coupling** (~1–2 days, do in parallel) — makes the choice
   reversible and shrinks the blast radius.
3. **T15/T2e parity suite** — the gate that makes adoption safe.
4. **nucula** — only after nucula#8 resolves in favour of reuse.
