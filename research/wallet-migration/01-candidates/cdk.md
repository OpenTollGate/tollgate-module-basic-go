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

## Open questions (owner: consultant B, see TASKS.md T3) — ALL ANSWERED, see Consultant B findings

- [x] Exact licence terms + GPL-3.0 compatibility → **Apache-2.0 OR MIT, GPL-3.0-compatible. B4.**
- [x] Does a *wallet* library API exist, or is wallet functionality CLI-only? → **Library API; wrap `cdk-ffi`. B5.**
- [x] Minimum feature set to build only what a wallet needs (dependency graph, build time, resulting binary size) → **NOT SIZED — see B7 for what it pulls in (tokio multi-thread, bundled SQLite, 26-29 MB `.so`); a minimal-feature build is not offered by cdk-go.**
- [x] Cross-compile to `aarch64-unknown-linux-musl` and `mipsel-unknown-linux-musl` (router arches) — does it work, how ugly, what patches → **BLOCKED. cdk-go ships glibc-only, 1 of 5 router arches, and CI itself is `CGO_ENABLED=0`. B6.**
- [x] Runtime footprint: RSS, thread count (does it require a tokio runtime?), startup latency → **Measured: ~9-10 threads, ~17 MB RSS, ~1.9 GB VmSize, tokio multi-thread. B7.**
- [x] Storage: sqlite vs redb — write amplification and flash wear on a router → **NOT MEASURED — cdk-go's binding exposes SQLite only (`WalletStoreSqlite`); redb is upstream but not surfaced. B7.**
- [ ] NUT coverage vs gonuts (diff the two matrices) → **NOT POSSIBLE YET — gonuts matrix not enumerated (T1b open). CDK covers NUT-00…30; see B8.**
- [x] Release cadence, contributor count, breaking-change history — a proxy for "will this churn cost us a fork again?" → **Fast: 73 contributors, minor ~every 12 weeks, nightly tags, ALPHA. B8.**

## Prior art inside the module (verified on this branch's base)

`src/tollwallet/cdk_wallet.go` implements `WalletPort` behind the `cdk_wallet`
build tag and depends on **`github.com/cashubtc/cdk-go/bindings/cdkffi`** — a Go
binding to CDK's FFI surface. That is the integration architecture question
answered in principle: **Go API over cgo into Rust**, not a sidecar process.

Open questions this raises (owner: consultant B, T3):

- [x] Does `github.com/cashubtc/cdk-go` actually exist, and is it maintained?
      → **YES. Real, public, pushed 2026-09-13, unarchived. It is an automated
      publish mirror of the CDK monorepo. Pinned as `v0.17.3` — a published
      version, and there is NO `replace` to a local path (the only `replace` in
      `src/tollwallet/go.mod` is the sibling `src/lightning` module). See B1.**
- [x] Which parts of the adapter are complete vs TODO — and what the tests
      actually exercise (do they run without the cgo library present?)
      → **13/15 methods fully implemented; `SendWithOverpayment` refuses non-zero
      caps; `MeltToLightning` unimplemented. Tests DO run for real (none skipped)
      but cover only decode/construct/persist/mapping — no value-moving path.
      See B3.**
- [x] Does the cgo path cross-compile for the router's musl arches? cgo + musl +
      Rust static library is the highest-risk part of the whole plan.
      → **Confirmed the risk is real and worse than feared: TollGate builds with
      `CGO_ENABLED=0`, cdk-go needs `=1`, ships glibc-only libs for 1 of 5
      arches, no musl and no mips/mipsel artifacts, and `staticlib` is not
      reachable through cdk-go. See B6.**
- [x] Runtime cost of the FFI boundary: threads (tokio inside?), memory, and
      what happens on panic across the FFI boundary.
      → **Measured: ~9-10 threads, ~17 MB RSS, ~1.9 GB VmSize; multi-threaded
      tokio. Rust panic → Go panic → process death (no `recover()` in adapter);
      `panic = "abort"` on the static-musl profile. See B7.**
- [x] Mnemonic-at-rest: the seed is a plain file today — encryption at rest and
      file permissions on the router must be a research item, not an afterthought.
      → **answered, see B3.**

---

## Consultant B findings (2026-09-13)

**Scope.** Everything below was verified on this branch's checkout at
`/home/c03rad0r/worktrees/wallet-research` (branch `research/wallet-migration`),
build host x86_64 Linux, glibc, 4 CPUs, `go version go1.26.0 linux/amd64`,
`CGO_ENABLED=1`. CDK revisions are identified by SHA/tag, not by "latest".
No git command was run.

**Method note.** The candidate revision is pinned *twice*, and both pins are
recorded here:

- Go binding: `github.com/cashubtc/cdk-go v0.17.3`
  (`src/tollwallet/go.mod:34`; sha256 of the module zip per `go.sum`:
  `h1:ncU2UbKCZ8OalMc9RQA2Fbh5rbuDYz4ULbF+aZSE7nw=`)
- CDK itself, as compiled into that binding:
  `bindings/cdkffi/CDK_COMMIT` = `34882e8a90419d15fa2053b067776adccd957a2f`,
  with `cdk-ffi = "=0.17.3"` in the binding crate's `rust/Cargo.toml`.

---

### B1 — Does `github.com/cashubtc/cdk-go` exist, and is it maintained? YES to both — with a caveat that changes the risk picture

Verified by API and by the module cache:

```
$ gh api repos/cashubtc/cdk-go
created_at 2025-10-03T14:03:55Z   pushed_at 2026-09-13T07:50:37Z
stargazers 0   forks 2   open_issues 1   license null
size (KB) 1355206   default_branch main   archived false
```

The repo is real, public, unarchived, and **pushed to the same day as this
research** (2026-09-13). It is under `cashubtc` — the reference Cashu org.

**But it is a generated mirror, not a hand-maintained project.** Its own README
states the mechanism:

> The `go-publish.yml` workflow (in the CDK monorepo) builds native binaries,
> syncs sources to `cdk-go`, and creates a tagged release.

Corroborating facts: the repo has **no `.github/workflows` at all**
(`gh api .../contents/.github/workflows` → HTTP 404), **0 stars**, **no licence
file** (see B4), and every commit in its history is a `release:` commit:

```
$ gh api 'repos/cashubtc/cdk-go/commits?per_page=15'
c188814bcb 2026-09-02 release: v0.18.0
ab2d9d75ae 2026-08-26 release: v0.17.6
74cd940af7 2026-07-12 release: v0.17.3     <-- the version this branch pins
8fd572f48b 2026-06-12 release: v0.17.0
```

It is maintained **by automation from the CDK monorepo** — which is arguably
*better* than hand-maintenance (it cannot drift from CDK's FFI), but it means
"is cdk-go maintained?" should be read as "is `cashubtc/cdk` releasing?", which
is answered in B8.

**Pinning — unambiguously a real published version, no `replace`.** This is the
single most important thing to say plainly, because the task brief feared a
local-path `replace`:

```
$ cat src/tollwallet/go.mod
34:  github.com/cashubtc/cdk-go v0.17.3
13:  replace github.com/OpenTollGate/tollgate-module-basic-go/src/lightning => ../lightning
```

The **only** `replace` in that file is for the sibling `src/lightning` module
(line 13) and is unrelated to cdk-go. `src/go.mod:66` also carries
`github.com/cashubtc/cdk-go v0.17.3 // indirect`. The dependency is resolved
from the public module proxy, and the module cache confirms it landed:

```
$ ls ~/go/pkg/mod/github.com/cashubtc/
cdk-go@v0.17.3
```

**So: the adapter is buildable by someone outside this machine** — as long as
their platform is one of the ones cdk-go ships a native library for (see B6,
where that turns out to be the whole problem).

**One live risk:** the pin is **already a full minor version behind**. This
branch pins v0.17.3 (released 2026-07-12); `cdk-go` has since published
v0.17.4, v0.17.5, v0.17.6, v0.18.0-rc.0…3, **v0.18.0 (2026-09-02)**, and 26
nightlies. On a self-declared ALPHA API, a two-month-old pin is not "pinned",
it is "abandoned in place".

---

### B2 — Does the adapter build? Yes on amd64/glibc. That is the *only* combination, and the documented verify command is a false green

**First, a trap worth recording for everyone on this project.** The command in
the research brief produces a misleading pass:

```
$ cd src && GOFLAGS=-buildvcs=false go build -tags cdk_wallet ./...
(no output)  EXIT: 0
```

That exit 0 **does not compile the adapter**. `src/tollwallet/` contains its own
`go.mod`, so it is a **nested module excluded from `src`'s `./...`**:

```
$ cd src && go build ./tollwallet/
main module (github.com/OpenTollGate/tollgate-module-basic-go) does not contain
package github.com/OpenTollGate/tollgate-module-basic-go/tollwallet
```

Anyone who validates the CDK path with the documented command will get a green
tick and compile nothing. The correct commands, run **inside the module**:

```
$ cd src/tollwallet
$ GOFLAGS=-buildvcs=false go build -tags cdk_wallet ./...
EXIT: 0                                   # clean, no output
$ GOFLAGS=-buildvcs=false go vet -tags cdk_wallet ./...
EXIT: 0                                   # clean

$ GOFLAGS=-buildvcs=false go test -tags 'cdk_wallet testenv' -count=1 ./...
ok  github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet  0.064s
```

So, on the build host: **the adapter compiles, vets clean, and its tests run for
real** (see B3 — none are skipped).

**What it requires: a prebuilt shared object, not a static library.** The cgo
link flags are generated per platform
(`bindings/cdkffi/link_linux_amd64.go:5`):

```
// #cgo LDFLAGS: -L${SRCDIR}/native/linux_amd64 -lcdk_ffi -Wl,-rpath,${SRCDIR}/native/linux_amd64 -lm -ldl
```

The library is **shipped inside the Go module zip**, so it is obtainable without
building Rust — cdk-go's README says as much ("Prebuilt native libraries are
included — downstream consumers only need Go"):

```
~go/pkg/mod/github.com/cashubtc/cdk-go@v0.17.3/bindings/cdkffi/native/
  linux_amd64/libcdk_ffi.so    29 MB
  linux_arm64/libcdk_ffi.so    26 MB
  darwin_amd64/libcdk_ffi.dylib 32 MB
  darwin_arm64/libcdk_ffi.dylib 30 MB
  windows_amd64/cdk_ffi.dll    32 MB
```

`crate-type` upstream is `["cdylib","staticlib","rlib"]`, but **the Go binding
crate overrides it to cdylib only** (`rust/Cargo.toml:10-12`,
`crate-type = ["cdylib"]`). There is therefore **no static link path via
cdk-go**; the `.so` must be present at runtime.

**That leads to the deployment landmine nobody has written down.** The built
binary's RPATH points into the *build machine's Go module cache*:

```
$ readelf -d /tmp/tollwallet_cdk.test
 (NEEDED)  Shared library: [libcdk_ffi.so]
 (RUNPATH) Library runpath: [/home/c03rad0r/go/pkg/mod/github.com/cashubtc/cdk-go@v0.17.3/bindings/cdkffi/native/linux_amd64]
$ ldd /tmp/tollwallet_cdk.test
 libcdk_ffi.so => /home/c03rad0r/go/pkg/mod/.../linux_amd64/libcdk_ffi.so
```

On a router, `/home/c03rad0r/go/pkg/mod/...` does not exist. Any binary built
this way **fails to start** unless (a) the `.so` is installed at that literal
path, (b) `LD_LIBRARY_PATH` is set by the init script, or (c) the runpath is
rewritten post-build (`patchelf --set-rpath`). This is an unlisted packaging
workstream.

---

### B3 — Completeness audit of `cdk_wallet.go`

`WalletPort` declares **15 methods** (`src/tollwallet/port.go:183-239`). The
build succeeding proves `*CdkWallet` satisfies the interface set (the return of
`NewWalletPort` is `WalletPort`), so nothing is missing *structurally* — the
gaps are semantic:

| Status | Count | Methods |
|---|---|---|
| Fully implemented | 13 | `DecodeToken`, `Receive`, `GetBalance`, `GetBalanceByMint`, `GetAllMintBalances`, `Send`, `Drain`, `RequestMintQuote`, `GetMintQuoteState`, `MintTokens`, `RequestMeltQuote`, `Melt`, `Shutdown` |
| **Partial** | 1 | `SendWithOverpayment` (`cdk_wallet.go:198-210`) — **refuses** any call with non-zero caps |
| **Missing** | 1 | `MeltToLightning` (`cdk_wallet.go:244-246`) — returns a hard error |

**`SendWithOverpayment` is honestly refused, and the refusal is justified.**
`cdk_ffi.SendOptions` (`cdk_ffi.go:19209-19221`) exposes
`Memo`, `Conditions`, `AmountSplitTarget`, `SendKind`, `IncludeFee`, `UseP2bk`,
max-proofs — and **no overpayment field**. The adapter errors rather than
silently sending unrestricted (`"overpayment limits not supported by the cdk-go
adapter (percent=%d, absolute=%d); refusing unrestricted send"`). Correct
behaviour; the *feature* is nonetheless unimplemented.

**`MeltToLightning` is real work, not a stub to flip on.** The outbound-payment
(reseller) path returns
`"not yet wired — requires LNURL resolution + MeltQuote + Melt flow integration"`.
The building blocks exist in the binding (`Wallet.MeltHumanReadable`,
`MeltBip353Quote`, `MeltLightningAddressQuote`, `MeltHumanReadableQuote` —
`cdk_ffi.go:7062-7234`), so it is wiring, but it is unwritten wiring on the
path that spends money.

**Two more gaps the checklist did not name:**

1. **Trust model is weaker than gonuts.** `mintAccepted` (`cdk_wallet.go:139-149`)
   accepts any untrusted mint as-is when `allowUntrusted` is set; the comment
   concedes *"Swap-to-trusted is not implemented in this adapter — untrusted
   tokens are accepted as-is."* gonuts' swap-to-trusted behaviour is **not**
   replicated.
2. **In-flight quotes do not survive a restart.** `quoteToMint`
   (`cdk_wallet.go:26`, written at `:259` and `:320`) is an **in-memory map
   only**. After any process restart — routine on a router — `GetMintQuoteState`
   (`:275`), `MintTokens` (`:293`) and `Melt` (`:336`) all fail with
   `unknown quote ID`. CDK's SQLite persists the quotes; the adapter's
   mint-attribution map does not. A reboot between paying a bolt11 invoice and
   calling `MintTokens` strands the payment.
3. **A concurrency inconsistency.** `GetBalance` (`:151-167`) snapshots the
   wallet slice, **releases `mu`**, then calls `TotalBalance()` on objects a
   concurrent `Shutdown()` may already have destroyed. `GetBalanceByMint`
   (`:169-183`) carries a comment acknowledging exactly this use-after-free and
   holds `mu` to prevent it. `GetBalance` has the bug its sibling documents.

**Do the tests exercise the adapter? Yes — and they are not skipped.** The test
file's tag is `//go:build cdk_wallet && testenv`
(`cdk_wallet_test.go:1`), and with both tags set, `-v` shows real passes, no
`SKIP`:

```
--- PASS: TestCdkDecodeToken (0.00s)
--- PASS: TestCdkWalletConstruction (0.00s)
--- PASS: TestCdkMnemonicPersistedAcrossRestart (0.00s)
--- PASS: TestMapQuoteState (0.00s)
--- PASS: TestMapCdkError (0.00s)
```

Coverage is **decode / construct / persist / mapping only**. There is **no test
of `Receive`, `Send`, `MintTokens`, `RequestMeltQuote`, `Melt`, or any mint
interaction** — i.e. every method that moves value is untested. There is also
no test asserting the two known refusals above.

**Mnemonic file handling — assessed for a router.** `loadOrCreateMnemonic`
(`cdk_wallet.go:46-67`) writes the seed to `<walletPath>/cdk-wallet-mnemonic.txt`
(dir `0700`, file `0600`), generated by `cdk_ffi.GenerateMnemonic()`; my probe
confirmed it is a 12-word BIP-39 phrase. The adapter itself is admirably candid
(`:18-19`): *"mnemonicFile persists the wallet seed — losing it forfeits every
derived balance."*

Security implications on a router:

- **Plaintext at rest.** The seed is the entire key material and is stored
  unencrypted. Root access, a stolen flash chip, or a backup of the data
  partition yields every proof's spending key. On OpenWrt, `/etc` (the likely
  home) is on flash and readable by any root-capable process.
- **The secret lives in two heaps and is never zeroised.** It is a Go `string`
  — immutable, GC-relocated, copied freely — and is then handed to
  `cdk_ffi.NewWallet` (`cdk_wallet.go:392-398`), where uniffi marshals it into a
  Rust string. There is no `memzero` anywhere. With swap enabled it can reach
  storage.
- **Proofs are unencrypted too.** Per-mint SQLite files are named
  `sha256(mintUrl)[:8].sqlite` (`:386-389`) and created by rusqlite with default
  permissions inside the `0700` dir. No SQLCipher.

**Proposed fix (in priority order):** (1) encrypt the seed at rest with a key
that does not sit on the same filesystem — a device-bound key, a TPM/keystore,
or a passphrase-derived key — and document seed backup/restore in the user flow
(the binding does expose `Wallet.Restore()` / `RestoreWithOpts()` for NUT-09,
`cdk_ffi.go:7847` and `:7883`); (2) hold the seed as bytes with explicit
zeroisation rather than a Go string; (3) enable `cdk-sqlite`'s `sqlcipher`
feature (it exists — `crates/cdk-sqlite/Cargo.toml` `sqlcipher =
["rusqlite/bundled-sqlcipher"]` — but cdk-ffi does not currently surface it, so
this is another binding patch); (4) verify that `0600`/`0700` survive OpenWrt
packaging and upgrades, since init/upgrade scripts routinely reset modes.

---

### B4 — Licence: **Apache-2.0 OR MIT**, and that IS GPL-3.0 compatible

CDK `LICENSE.md` (root), verbatim:

> Except as otherwise noted in individual files, all files in this repository are
> licensed under the Apache License, Version 2.0 (LICENSE-APACHE …) or the MIT
> license (LICENSE-MIT …), at your option.

Confirmed by the workspace manifest: `license = "Apache-2.0 OR MIT"`, and both
`LICENSE-APACHE` and `LICENSE-MIT` are present at the repo root. GitHub reports
`NOASSERTION` **only because the licence is a dual-licence pointer file** —
`{"license":{"key":"other","spdx_id":"NOASSERTION"}}` — not because terms are
unclear.

**Verdict for TollGate (GPL-3.0, `LICENSE`, 35 KB): compatible.** MIT is
GPL-compatible; Apache-2.0 is *explicitly* GPLv3-compatible (this is the exact
reason Apache-2.0 is not GPLv2-compatible). Taking either branch and
distributing the combined work under GPL-3.0 is fine; the Apache-2.0 branch adds
patent-grant and NOTICE obligations, nothing incompatible.

**`cdk-go` itself has no licence file, and mis-declares one.** Its repo root
contains only `.gitignore, Makefile, README.md, bindings, cdk_test.go, go.mod,
rust, rust-toolchain.toml, scripts, uniffi.toml` — **no `LICENSE`**, and
`gh api` reports `license: null`. It claims MIT in two inconsistent places:
README `## License [MIT](https://github.com/cashubtc/cdk/blob/main/LICENSE)`
(which actually points at the dual-licence file), and `rust/Cargo.toml`
`license = "MIT"` — contradicting upstream's `Apache-2.0 OR MIT`. Verdict: the
code is CDK's, dual-licensed and compatible; the binding repo's own grant is
**implicit and mis-declared**. Worth a one-line upstream issue; not a blocker.

---

### B5 — Wallet API vs CLI: it is a **library API**, and the crate to wrap is `cdk-ffi`

The wallet is a first-class Rust library, not CLI-only:

- `crates/cdk` — "Rust implementation of Cashu protocol"
- `crates/cashu` — core protocol types
- `crates/cdk-sqlite`, `crates/cdk-redb` — storage backends
- `crates/cdk-http-client` — wallet↔mint transport
- `crates/cdk-cli` — **a thin binary over the library**
- `crates/cdk-ffi` — the uniffi-generated FFI surface over the same library

**Named answer: the crate a Go binding must wrap is `cdk-ffi`**, via the
purpose-built `cdk-ffi-go` crate (`bindings/go/rust/Cargo.toml` in the monorepo,
mirrored as `rust/Cargo.toml` in cdk-go), which is `pub use cdk_ffi::*` and
depends on `cdk-ffi = "=0.17.3"` with features `["npubcash","nwc","bip353"]`.
The path is therefore cgo → uniffi C ABI (`cdk_ffi.h`, 5142 lines) →
`libcdk_ffi.so`.

The API is **broader than `WalletPort` needs**, which is good news for the
migration: ~60 methods on `Wallet` alone (`cdk_ffi.go:6090-8267+`), including
`Swap` (NUT-03, `:8195`), `CheckProofsSpent` (NUT-07, `:6291`), `Restore` /
`RestoreWithOpts` (NUT-09, `:7847`/`:7883`), `FinalizePendingMelts` (`:6477`),
`RecoverIncompleteSagas` (`:7743`), `ListTransactions` (`:6910`), plus
`MeltHumanReadable`/`MeltLightningAddressQuote` (`:7109`/`:7188`) that
`MeltToLightning` needs.

**Async is the shape of the API.** Wallet operations are
`#[uniffi::export(async_runtime = "tokio")]`
(`crates/cdk-ffi/src/wallet.rs:43, 870, 930, 1013`); the Go binding contains
**750** `UniffiRustFuture` references and **spawns goroutines** to poll
completion (`go func()` at `cdk_ffi.go:10393, 10402, 10453, …`). So a Go caller
blocks its goroutine, the Rust side needs a tokio runtime, and the boundary is
not free (measured in B7). My probe's `wallet.GetBalance()`-style calls stayed
at **1 goroutine** (synchronous entry points), so there is no goroutine leak in
the simple paths.

---

### B6 — Cross-compilation: **this is where CDK fails, and it fails hard**

#### The blocker, stated first

**TollGate builds every shipped artifact with `CGO_ENABLED: "0"`.**

```
.github/workflows/build-package.yml:181
          CGO_ENABLED: "0"
```

across these arches (`build-package.yml:52-65`, compile map at `:193-197`):

| Router arch | GOARCH | rows in matrix |
|---|---|---|
| `aarch64_cortex-a53` | arm64 | 2 |
| `aarch64_cortex-a72` | arm64 | 2 |
| `arm_cortex-a7` | arm, GOARM=7 | 2 |
| `mipsel_24kc` | **mipsle** | 2 |
| `mips_24kc` | **mips** | 5 |
| `x86_64` | amd64 | 1 |

**cdk-go requires `CGO_ENABLED=1`** (its README lists it as a prerequisite;
measured consequence:

```
$ GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -tags cdk_wallet ./...
imports github.com/cashubtc/cdk-go/bindings/cdkffi: build constraints exclude
all Go files in .../cdk-go@v0.17.3/bindings/cdkffi
```

**Therefore the adapter cannot be built by the current CI for any arch.** This
is not a patch; it is a re-architecture of the cross-compile pipeline (a C
cross-toolchain per arch inside each OpenWrt SDK container, plus a Rust
cross-built cdylib per arch). Note also that `cdk_test.go` in cdk-go *requires*
cgo, so the upstream binding cannot even be smoke-tested in a cgo-free CI.

#### Prebuilt coverage is 1 of 5 arches — and it is the wrong libc

```
bindings/cdkffi/native/          bindings/cdkffi/link_*.go
  linux_amd64/libcdk_ffi.so        link_linux_amd64.go
  linux_arm64/libcdk_ffi.so        link_linux_arm64.go
  darwin_amd64/…dylib              link_darwin_amd64.go
  darwin_arm64/…dylib              link_darwin_arm64.go
  windows_amd64/cdk_ffi.dll        link_windows_amd64.go
```

**No armv7, no mipsle, no mips.** And the Go binding's own publish matrix is
**gnu-only** (`go-publish.yml:213-229`):

```
x86_64-unknown-linux-gnu, aarch64-unknown-linux-gnu,
aarch64-apple-darwin, x86_64-apple-darwin, x86_64-pc-windows-msvc
```

```
go-publish.yml:332-336   TARGET_MAP[x86_64-unknown-linux-gnu]=linux_amd64
                         TARGET_MAP[aarch64-unknown-linux-gnu]=linux_arm64
```

**Not one musl target, ever.** Worse, the Linux libs that *are* shipped are
**glibc**, and OpenWrt ships musl:

```
$ readelf -d .../native/linux_arm64/libcdk_ffi.so
 (NEEDED) libgcc_s.so.1
 (NEEDED) libm.so.6
 (NEEDED) libc.so.6
 (NEEDED) ld-linux-aarch64.so.1        <-- glibc loader, not musl

$ readelf -d .../native/linux_amd64/libcdk_ffi.so
 (NEEDED) ld-linux-x86-64.so.2
$ ldd .../libcdk_ffi.so
 libc.so.6 => /usr/lib/x86_64-linux-gnu/libc.so.6
```

So on a musl router, **the shipped `.so` will not load at all** — even on the
one arch (aarch64) that has an artifact.

#### musl support exists in CDK, but not for the FFI

musl *is* supported upstream — for the mint daemon and CLI only:

- `flake.nix:52-57` defines `muslTarget` for `x86_64-unknown-linux-musl` and
  `aarch64-unknown-linux-musl`, with `isStatic = true` (`:107-119`)
- `static-build-publish.yml` builds `cdk-mintd-static`,
  `cdk-mintd-ldk-static`, `cdk-cli-static` for x86_64 and aarch64
- `DEVELOPMENT.md:233` — "CDK provides fully statically-linked Linux binaries
  built with musl … run on Linux x86_64 and aarch64 systems"

**Nothing builds `cdk-ffi` or the Go binding for musl**, and cdk-go's own
`rust-toolchain.toml` target list is android / iOS / windows /
`*-unknown-linux-gnu` / darwin — **no musl entry**. Code search corroborates:
`musl repo:cashubtc/cdk` returns 2 hits (DEVELOPMENT.md, flake.nix);
`mips repo:cashubtc/cdk` returns **0**. (`grep -i mips cdk-go/rust-toolchain.toml`
→ NO MIPS TARGET.)

#### Static linking is not reachable through cdk-go

The *upstream* crate would allow it —
`crates/cdk-ffi/Cargo.toml`: `crate-type = ["cdylib", "staticlib", "rlib"]` —
but the **Go binding crate overrides to
`crate-type = ["cdylib"]`** (`bindings/go/rust/Cargo.toml`; mirrored at
cdk-go `rust/Cargo.toml:10-12`). A static path therefore requires **forking the
binding crate** to re-add `staticlib`, or writing a bespoke cgo wrapper against
`cdk-ffi` directly.

#### mips/mipsel is a dead end on top of everything else

`mipsel-unknown-linux-musl` / `mips-unknown-linux-musl` are **Rust tier-3**
(no prebuilt `std`; require nightly `-Zbuild-std`). CDK has no target, no
artifact, no CI for either. But `mips_24kc` + `mipsel_24kc` are **7 of the 14
rows** in TollGate's arch/compression matrix (re-counted from the workflow on
2026-09-19 — `experiments/cdk-cross/count-ci-matrix.py`, log
`raw-2026-09-19/24-ci-matrix-rows.txt`; this line said "of the 17" until then,
a recollection no one had checked). A CDK wallet means **either
dropping those devices or maintaining a bespoke Rust toolchain for them**.

Also note `cdk-sqlite` pulls **bundled C SQLite**:
`rusqlite = { version = "0.31", features = ["bundled"] }` — SQLite's C source is
compiled by cc-rs during the Rust build, so every musl cross target additionally
needs a working musl `cc`/`ar`.

#### What is UNVERIFIED, and exactly how to test it

I did **not** produce a musl or mipsel build, and I state no result for one. On
this host both cross attempts failed **before any CDK code was reached**:

```
$ GOOS=linux GOARCH=arm64  CGO_ENABLED=1 go build -tags cdk_wallet ./...
cgo: C compiler "aarch64-linux-gnu-gcc" not found: ... not found in $PATH
$ GOOS=linux GOARCH=mipsle CGO_ENABLED=1 go build -tags cdk_wallet ./...
cgo: C compiler "mipsel-linux-gnu-gcc" not found: ... not found in $PATH
```

so **nothing about musl is verified either way** — this is exactly T5c's job.
The concrete test: (a) in the CDK Nix shell with `pkgsMusl` for
`aarch64-unknown-linux-musl`, patch the binding crate to
`crate-type = ["staticlib","cdylib"]` and
`cargo build --release --target aarch64-unknown-linux-musl`; (b) then
`GOOS=linux GOARCH=arm64 CGO_ENABLED=1 CC=aarch64-openwrt-linux-musl-gcc go build -tags cdk_wallet`
inside the OpenWrt SDK; (c) record patch count, `readelf -d` (must show no
`NEEDED` for a static result) and stripped size. Repeat for mipsle only after
confirming a tier-3 `std` build actually succeeds.

---

### B7 — Runtime cost: **measured**, and it is substantial for a router

Probe (separate module, `go get github.com/cashubtc/cdk-go@v0.17.3`, reading
`/proc/self/status`; host: x86_64 Linux glibc, **4 CPUs**, Go 1.26.0):

| Point | Threads | VmRSS | VmSize |
|---|---|---|---|
| startup (lib loaded, no call) | **5** | 8.4 MB | 1.49 GB |
| after `GenerateMnemonic` (12 words) | 5 | 9.2 MB | 1.49 GB |
| after `NewWallet` (testnut + sqlite) | **9** | 15.8 MB | 1.78 GB |
| after `TotalBalance` | **10** | 16.8 MB | **1.86 GB** |
| after `Destroy` | 6 | 17.3 MB | 1.86 GB |

- **~9-10 threads and ~17 MB RSS for a single wallet**, +4 threads just for
  loading the `.so`. VmSize reached **~1.9 GB** and grew ~67 MB per call.
- `Destroy()` did **not** reclaim threads or RSS (10 → 6): the runtime is
  process-lifetime.
- Goroutines stayed at 1 on these synchronous paths — no goroutine leak.

Where it comes from, from the manifests: `cdk-ffi` depends on
`tokio = { features = ["sync","rt","rt-multi-thread"] }` plus `uniffi` with
`tokio`, and `crates/cdk-ffi/src/runtime.rs`'s `RuntimeGuard` **lazily creates a
multi-threaded `Runtime::new()`** when no runtime is present on the thread
(and uses `block_in_place` when one is) — hence a tokio worker pool with
`worker_threads ≈ CPU count`. Storage is **SQLite via rusqlite bundled**, one
file per mint.

VmSize is mostly reserved address space rather than resident pages, so it should
not be read as "1.9 GB of RAM". But it is a real measurement, it needs to be
re-taken on-device, and it does not appear anywhere in the plan. **A 26-29 MB
shared object + ~9-10 threads + ~17 MB RSS is a large ask for a device whose
entire binary is currently built `CGO_ENABLED=0` to stay small and portable.**

#### Panic across the FFI boundary: the process can die, and the adapter cannot catch it

uniffi converts a Rust panic into a status carrying the message, and the
generated Go code turns that into a **Go panic, not an error**:

```
cdk_ffi.go:170-180   checkCallStatus, case 2:
                       panic(fmt.Errorf("%s", FfiConverterStringINSTANCE.Lift(...)))
cdk_ffi.go:185-205   checkCallStatusUnknown, case 2:  panic(...)
cdk_ffi.go:207-213   rustCall:  if err != nil { panic(err) }
```

Case 2 fires **regardless of whether the function declares an error**, so even
`NewWallet` — which uses `rustCallWithError` (`cdk_ffi.go:6077-6087`) — can
surface a panic rather than an error. `Destroy()`/`Close()` is worse: it routes
through `freeRustArcPtr` → `rustCall` (`cdk_ffi.go:3541`) → **panic on any
error**, and the adapter calls it from `defer token.Close()`
(`cdk_wallet.go:208`) and `Shutdown()` (`:368`).

**The adapter has no `recover()` anywhere** — verified:
`grep -n "recover()" cdk_wallet.go` → *NO recover() ANYWHERE*. Consequences:

- A Rust panic becomes a Go panic that unwinds out of `tollwallet`. It is
  recovered by `net/http` **only** if the call is on an HTTP-handler goroutine;
  from the lightning quote-monitor goroutine or any other non-handler goroutine
  it **takes the whole router process down**.
- If the static-musl path is used, `DEVELOPMENT.md:279` says the
  `release-static` profile sets **`panic = "abort"`** — the unwind is gone and
  any Rust panic **aborts the process outright, unrecoverable from Go**.
- Because `Close()` can panic, even *cleanup* paths are a crash vector.

Minimum remediation if in-process is ever chosen: `recover()` at every FFI
entry point, a supervisor/watchdog around the wallet, and a deliberate decision
about `panic = "abort"` versus unwinding builds.

---

### B8 — Churn / governance: live and well-funded, which is exactly the churn risk

**Maintainership is real.** `cashubtc` is the reference Cashu organisation;
230 stars, 139 forks, 9 watchers, 111 open issues, not archived, last push
2026-09-12. **73 contributors**, with a clear lead:

```
$ gh api 'repos/cashubtc/cdk/contributors?per_page=12'
thesimplekid 2021   crodas 158   asmogo 64   github-actions[bot] 42
ok300 30            a1denvalu3 30   prusnak 24   Forte11Cuba 19
vnprc 18            gudnuf 17    davidcaseria 13   cdk-bot 10
```

**Release cadence is fast.** CDK: `v0.17.0` 2026-06-12 → `v0.17.1…v0.17.6` →
`v0.18.0` 2026-09-02 — a **minor roughly every ~12 weeks, a patch every ~2-3
weeks**, each with 1-4 RCs. cdk-go additionally publishes a **nightly**
(`v0.18.0-nightly.20260913.gff5a3f0`, 2026-09-13T07:50:35Z) on top of RC and
stable tags.

**Breaking changes are structural, not hypothetical.** The project self-declares
ALPHA — *"the api will change and should be used with caution"* (README), plus a
warning banner: *"This project is in early development, it does however work
with real sats! Always use amounts you don't mind losing."* It is pre-1.0, so
**every 0.x minor may break**. The router's pin (`v0.17.3`, 2026-07-12) is
already one minor behind `v0.18.0`, and per B6 it cannot be validated on any
router arch anyway.

**Name the irony in the report.** The goal was to stop owning a fork of an
unmaintained library. CDK replaces that with: tracking an ALPHA API that ships
nightlies; owning a cgo + musl + Rust cross-compile pipeline for 5 router arches
of which upstream supports 1 (glibc) Linux arch; and very likely **forking
cdk-ffi's Go binding crate** to regain `staticlib` and to patch around gaps
(overpayment options, SQLCipher, quote persistence). That is **more**
maintenance, not less — but it is maintenance of *integration glue over a live
upstream*, versus maintenance of *a dead upstream's protocol code*. Those are
not equivalent risks, and the synthesis should say so in exactly those terms.

---

### Bottom line (Consultant B)

**CDK is the right upstream to target and the wrong thing to ship this way.**

Favourable, all verified: the licence is **Apache-2.0 OR MIT**, hence
**GPL-3.0-compatible**; the wallet is a genuine Rust **library** API, and
`cdk-go` already projects it to Go; NUT coverage is broad (NUT-00…30, incl.
07/09/11/12/13/14/17/20 used by this module); governance is the reference Cashu
org with 73 contributors; and — the strongest single datum — **the prior-art
adapter compiles, vets clean and passes its tests today** (`go build`/`go vet`
exit 0, `go test -tags 'cdk_wallet testenv'` → `ok … 0.064s`, no skips).

Disqualifying for *this* integration architecture:

1. **`CGO_ENABLED=0` in CI** (`build-package.yml:181`) vs cdk-go's hard cgo
   requirement → cannot build for any of the 5 arches as things stand.
2. **1 of 5 arches covered, and the wrong libc** — prebuilt libs are
   `linux_amd64`/`linux_arm64` **glibc** (`ld-linux-aarch64.so.1`); router is
   musl; **no armv7, no mipsle, no mips**.
3. **Static linking unreachable** (`crate-type = ["cdylib"]` in the binding
   crate) and **mips/mipsel are Rust tier-3** with zero CDK support.
4. **Binary RPATH points into the build machine's module cache** — unlisted
   packaging work.
5. **Runtime cost**: ~9-10 threads, ~17 MB RSS, ~1.9 GB VmSize per wallet, plus
   a 26-29 MB `.so`, on a service currently built cgo-free.
6. **Panics become Go panics with no `recover()` in the adapter** — process
   death from non-handler goroutines, and `panic = "abort"` if the static
   profile is used.
7. **Semantic gaps**: `MeltToLightning` missing, overpayment unenforced by
   upstream, swap-to-trusted absent, in-flight quotes lost on restart, value-
   moving paths untested, seed in plaintext.

**Recommendation:** reject the **cgo/cdk-go adapter** as the shipping mechanism.
Keep CDK as the *target* via a **process-isolated integration** — a sidecar
(`cdk-cli`-style helper or a small Rust wallet daemon) — which sidesteps cgo, the
musl/mipsel cross-compile wall and FFI panics simultaneously, and is the only
architecture that preserves `CGO_ENABLED=0` builds and all five arches. If
in-process is mandated, the minimum viable set is: fork the Go binding crate to
build `staticlib` for musl; add mips/mipsel targets or drop those 7 matrix rows;
add `recover()` at every FFI boundary plus a watchdog; encrypt the seed at rest;
persist `quoteToMint`; and pin behind an ALPHA that publishes nightlies.

**Out of scope / not answered here:** the NUT-coverage *diff against gonuts* is
not possible yet, because the gonuts side of that matrix is still TODO
(T1b, `00-context/walletport-contract.md` does not exist on this branch).
Everything in B6 about musl and mipsel is **UNVERIFIED** — see the test recipe
there; T5c owns producing it.

---

# T5c — measured cross-compile results (2026-09-14)

B6 is now **VERIFIED** on real toolchains. Full raw output and reproducible
scripts: `experiments/cdk-cross/` and `experiments/cdk-sidecar/`.

**Environment:** OpenWrt SDK images `openwrt/sdk:{mediatek-filogic,ramips-mt7621}-v25.12.5`
(OpenWrt GCC 14.3.0, musl); host rustc 1.94.0 (aarch64-musl std) + 1.99.0-nightly
(`-Z build-std`); cdk `v0.17.3` (`482e4df8`); `cdk-go v0.17.3`.

### 1. cgo + prebuilt `cdk-go` on aarch64 musl → FAIL (hard)

`cdk-go@v0.17.3` ships **glibc-only** `.so` files (`native/linux_arm64/libcdk_ffi.so`
has `NEEDED: libc.so.6, ld-linux-aarch64.so.1`; there is **no musl `.a`** and **no
mipsel dir**). The final link of the service (`-tags cdk_wallet`) against the
OpenWrt aarch64-musl toolchain fails with a cascade of
`undefined reference to '<sym>@GLIBC_2.x'` (access, pow, fstat64, dlerror,
epoll_ctl, …). **The in-process adapter is not buildable on OpenWrt musl as
published.** Caveat: building the *library* package looks green (Go does no final
link for a lib); only the main build exposes it.

### 2. cgo with `cdk-ffi` built from source → still not shippable

`cargo build -p cdk-ffi --target aarch64-unknown-linux-musl` **succeeds** but
`cdylib` is **dropped for musl** (`warning: dropping unsupported crate type
'cdylib'`), i.e. no `.so` is produced, which is what the Go binding expects. It
yields only `libcdk_ffi.a` = **133 MiB** unstripped (plus a 31 MiB rlib). Linking
that into the Go binary is the antithesis of the footprint goal.

### 3. Sidecar `cdk-cli` → SUCCESS on aarch64

`cargo build -p cdk-cli --target aarch64-unknown-linux-musl --no-default-features`
with the OpenWrt gcc as CC/linker links cleanly (fix: `RUSTFLAGS=-C panic=abort`
+ Rust **self-contained** musl libs; `link-self-contained=no` dies on `-lunwind`).
Result: **statically linked**, **19.7 MiB stripped** (25.2 MiB unstripped),
`readelf -d` NEEDED = none. All C deps cross-compiled: ring, secp256k1, bundled
sqlite, zstd. This is the off-the-shelf upper bound (a wallet-only sidecar would
be smaller). **Runtime RSS/threads NOT measured yet** — no arm64 user-space
emulation on the build host; deferred to the physical aarch64 router.

### 4. Sidecar `cdk-cli` on mipsel → FAIL (hard)

`mipsel-unknown-linux-musl` is tier-3; with nightly `-Z build-std=std,panic_abort`
compilation dies in `nostr-relay-pool`: `no AtomicU64 in sync::atomic` —
`mipsel_24kc` has **no 64-bit atomics**, and `std` omits `AtomicU64`. The full
`cdk-cli` (which pulls `nostr-sdk`) is therefore **aarch64-only** today. mipsel
would need a Nostr-free wallet-only sidecar, or `portable-atomic` shimming
throughout — real work, not a flag.

**Net effect on the recommendation:** unchanged and now evidence-backed — reject
cgo/`cdk-go`; ship CDK as a **process-isolated sidecar**. New hard fact for the
scorecard (T13): **the current `cdk-cli` sidecar covers aarch64 but not mipsel**
(the aarch64 sidecar is measured and clean).

---

# T5c RE-VERIFICATION (2026-09-19) — the "impossible" verdict was two patches too strong

Re-run of the T5c claims on the **SDK release pinned in
`packaging/build-inputs.json` (25.12.0 tarballs**, not the `v25.12.5` images the
2026-09-14 run used), with the deployment link added. Raw output, scripts and env
facts: `experiments/cdk-cross/` (`FINDINGS-2026-09-19.md`,
`env-2026-09-19.txt`, `raw/`).

**Two claims in §1/§2 of the T5c block above do not survive re-measurement.**

1. *"musl has no `cdylib`, so the binding's `.so` contract cannot be met from
   source"* — **wrong**. The `cdylib` drop is musl's `crt-static` **default**, not
   a target limitation:

   ```
   $ cargo build --release --target aarch64-unknown-linux-musl          # default
   warning: dropping unsupported crate type `cdylib` for target `aarch64-unknown-linux-musl`
   → libcdk_ffi.a only, no .so
   $ RUSTFLAGS="-C target-feature=-crt-static" cargo build …            # one flag
   → libcdk_ffi.so, NEEDED libgcc_s.so.1 + libc.so (musl)
   ```

   Applied to the real crate: `cargo build -p cdk-ffi --release --target
   aarch64-unknown-linux-musl` **succeeds** (23m51s, no errors, rust 1.96.0 as
   pinned by cdk's own `rust-toolchain.toml`) and yields `libcdk_ffi.so`
   35,021,264 B unstripped / **26,452,880 B stripped** — 0.2% off the shipped
   glibc `linux_arm64` `.so` (26,518,672 B), so it is the same animal.
2. *"only the main build exposes the failure"* — right in substance, and now
   measured on the artifact that ships. `src/main.go` → `src/merchant` →
   `src/tollwallet` (`src/go.mod` has the `replace`), so:

   | build (`-tags cdk_wallet`) | aarch64 musl | mipsle softfloat musl |
   |---|---|---|
   | `go build ./...` in `src/tollwallet` | exit 0 (no link — false green) | exit 0 (false green) |
   | **service link** (`go build .` in `src/`) | **exit 1**, 138 undefined `@GLIBC_2.17…2.34` | **exit 1**, 691 undefined `ffi_cdk_ffi_*` |
   | service link **without** the tag | exit 0, 16,770,936 B | exit 0, 18,073,276 B |

   The mipsle failure has a different cause worth naming: `cdk-go` has **no
   `link_linux_mipsle.go` stanza at all**, so no library is ever passed to the
   linker — every FFI symbol is unresolved (zero `@GLIBC` refs, versus 138 for
   aarch64).

**And the from-source path now links end to end on aarch64 — with two patches.**
Replacing the glibc object in a throwaway copy of the module with the musl build
(P1 `-crt-static`; P2 the musl `.so` at `native/linux_arm64/libcdk_ffi.so`, where
`link_linux_arm64.go` looks) gives `exit=0` and an 18,033,248 B aarch64-musl
binary — `NEEDED libcdk_ffi.so`, `interpreter /lib/ld-musl-aarch64.so.1`, and an
**RPATH pointing into the build machine's module cache** (the §B2 landmine,
confirmed for musl too → patch P3), plus a **26.5 MB runtime `.so`** on a
64–128 MB router. Rust `std` cannot be excluded: it is statically inside that
`.so` and CDK needs it (tokio multi-thread, bundled SQLite, ring, secp256k1).

**mipsel:** `rustup target add mipsel-unknown-linux-musl` → *exit 1, "no prebuilt
artifacts … low-tier target"* (Rust tier 3 ⇒ nightly `-Z build-std` is
mandatory), yet a minimal MIPS `.so` **does** build that way (66,832 B) — so the
arch and the toolchain are not the wall. The wall is a **dependency**, and it is
now measured on the real graph (254 crates into `cargo +nightly check -Z
build-std=std,panic_abort --target mipsel-unknown-linux-musl -p cdk-ffi`):

```
error[E0432]: unresolved import `std::sync::atomic::AtomicU64`
 --> …/nostr-relay-pool-0.44.1/src/relay/flags.rs:8:25
error: could not compile `nostr-relay-pool` (lib) due to 3 previous errors   EXIT=101
```

`nostr-relay-pool` (via `nostr-sdk`) is pulled in by CDK's **default** features
(`npubcash`, `nwc`), and `mipsel_24kc` has no 64-bit atomics — so this is **not**
a `cdk-cli`-only problem: the in-process path inherits it. Fixing it is an
upstream dependency change (Nostr-free feature set or `portable-atomic`), not a
flag. **arm_cortex-a7 (armv7) was not measured at all** and has no `cdk-go`
artifact either.

**Revised verdicts.** aarch64: *cross-compiles with 2 patches* (plus P3 packaging
and P4, the `CGO_ENABLED=0` → per-arch-cgo CI change at
`build-package.yml:181`). mips/mipsel and armv7: not buildable as published, open
work. **The sidecar recommendation is unchanged** — but the honest cost of the
in-process alternative is now stated: two patches, a 162.7 MiB `.a` / 26.5 MB
`.so` supply chain, an RPATH/packaging workstream, and a CI rewrite — not
"impossible on musl".
