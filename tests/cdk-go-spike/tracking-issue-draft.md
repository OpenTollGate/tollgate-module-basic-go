# cdk-go evaluation: can it replace gonuts-tollgate?

## TL;DR

cdk-go ([cashubtc/cdk-go](https://github.com/cashubtc/cdk-go)) was evaluated as a replacement for the gonuts-tollgate Cashu wallet library. It is NOT a drop-in replacement. cdk-go ships as FFI bindings over prebuilt native libraries (`.so`/`.dylib`/`.dll`) via `uniffi-bindgen-go`, and its platform support covers only linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, and windows/amd64. TollGate builds for six OpenWrt architectures, three of which (arm_cortex-a7, mipsel_24kc, mips_24kc) are not covered by cdk-go. Since these MIPS routers are real, deployed hardware targets that cannot be dropped, cdk-go cannot replace gonuts across the full build matrix. A feature branch `research/cdk-go-evaluation` with a POC spike has been created at https://github.com/OpenTollGate/tollgate-module-basic-go/tree/research/cdk-go-evaluation. Recommendation: continue the gonuts-tollgate fork-bump cycle short-term; pursue either (a) a per-architecture wallet split using cdk-go for amd64/arm64 and gonuts for MIPS, (b) a full tollgate-rs migration as outlined in #176, or (c) a native Go NUT reimplementation.

## Background -- gonuts was never the long-term plan

The maintainer TODO at `src/tollwallet/tollwallet.go:49` states it plainly:

> The issue arises because of our hacky fork of gonuts for the offline functionality we need. Long term fix is switching to CDK.

TollGate does not use the upstream gonuts directly. It runs through a fork chain:

1. `elnosh/gonuts` (upstream)
2. `Origami74/gonuts-tollgate` (first fork with offline patches)
3. `Amperstrand/gonuts-tollgate` (maintenance handoff)
4. `OpenTollGate/gonuts-tollgate` (current canonical fork, v0.7.4)

The `src/go.mod` `replace` directive at line 32 pins this: `github.com/Origami74/gonuts-tollgate => github.com/OpenTollGate/gonuts-tollgate v0.7.4`.

**Upstream gonuts is effectively dead.** The `elnosh/gonuts` repo ([github.com/elnosh/gonuts](https://github.com/elnosh/gonuts)) shows 6 commits in the last 12 months versus 228 in the prior 12 months. Its last release was v0.4.2 on August 16, 2025. The Cashu ecosystem has moved on to the Rust-based CDK ([cashubtc/cdk](https://github.com/cashubtc/cdk)) with FFI bindings for Kotlin, Swift, and now Go.

**gonuts is not listed on the official Cashu libraries page.** The [docs.cashu.space/libraries](https://docs.cashu.space/libraries) page lists libraries for Python, TypeScript, Rust, Kotlin, Swift, and Java. There is no Go entry. gonuts is an unofficial, third-party implementation outside the cashubtc ecosystem.

**Security disclaimer.** The `elnosh/gonuts` README ([github.com/elnosh/gonuts](https://github.com/elnosh/gonuts)) carries this notice:

> The author is NOT a cryptographer and this work has not been reviewed. This means that there is very likely a fatal flaw somewhere.

## Trouble we've had with gonuts (concrete, cited)

| Reference | Problem | Resolution |
|-----------|----------|------------|
| [#260](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/260) (OPEN) | gonuts-tollgate returns a generic error when a mint responds with HTTP 429 (rate limit). The UX shows "Payment failed" instead of "Mint busy," and there is no retry with backoff. This affects coinos.io in production. | Unresolved. gonuts has no concept of rate-limit handling. |
| [#257](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/257) (CLOSED) | gonuts-tollgate v0.5.0 produced "outputs have already been signed before" errors. Root cause: V2 keyset swap serialization was broken in the fork. | Fixed in gonuts-tollgate v0.7.1. |
| [#156](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/156) (CLOSED) | PR #126 merge accidentally dropped `bolt11` `FakeWallet` tolerance, a gonuts v0.7.0 regression. Lightning payment simulation broke with testnut. | Fixed by restoring the tolerance check in a follow-up commit. |
| [#134](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/134) (CLOSED) | gonuts-tollgate's transitive test dependencies (testcontainers, docker, etcd, embedded-postgres, btc-docker-test) leaked into tollgate's `go.sum`, adding roughly 135 lines of unrelated churn. | Resolved by pruning indirect deps and adjusting go.mod. |
| [PR #266](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/266) (MERGED) | An unrecoverable swap-counter race in gonuts-tollgate v0.7.1. The counter was incremented only after a successful swap, so any transient mint failure (timeout, DNS resolution failure, 5xx) left the counter stuck. Retries reused the same counter value, the mint rejected with NUT-02 error code 10002, and the wallet was bricked permanently with no self-recovery path. | Bumped to gonuts-tollgate v0.7.4, which increments the counter before the swap attempt. |
| [PR #253](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/253) (CLOSED) | DLEQ proof handling was broken for certain keyset configurations. | Bumped to gonuts-tollgate v0.7.3 for the DLEQ keyset fix. |
| [#176](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/176) (CLOSED) | Comprehensive ecosystem analysis of V2 keyset support, Cashu library landscape, and migration paths. Explicitly rejected a CGo/FFI wrapper around the Rust CDK for embedded MIPS targets. Recommended migrating to tollgate-rs (a Rust-based TollGate) as the long-term solution. | Closed in favor of the tollgate-rs migration path. |

## cdk-go evaluation

**What cdk-go actually is.** Verified from the repo at [cashubtc/cdk-go](https://github.com/cashubtc/cdk-go):

- **Form factor:** FFI bindings generated via [uniffi-bindgen-go](https://github.com/NordSecurity/uniffi-bindgen-go), wrapping the Rust CDK ([cashubtc/cdk](https://github.com/cashubtc/cdk)). This is NOT a native Go reimplementation of Cashu NUTs.
- **Requirement:** `CGO_ENABLED=1` (the default on most systems, but NOT default for Go cross-compilation).
- **Distribution:** Prebuilt native libraries bundled in the repo (`libcdk_ffi.so`, `libcdk_ffi.dylib`, `cdk_ffi.dll`), totalling approximately 147 MB.
- **API surface:** Wallet only. The mint is NOT exposed via the FFI bindings.

**Supported platforms** (from [cdk-go README](https://github.com/cashubtc/cdk-go#supported-platforms)):

| OS | Arch | Library |
|----|------|---------|
| Linux | amd64 | `libcdk_ffi.so` |
| Linux | arm64 | `libcdk_ffi.so` |
| macOS | arm64 | `libcdk_ffi.dylib` |
| macOS | amd64 | `libcdk_ffi.dylib` |
| Windows | amd64 | `cdk_ffi.dll` |

**NUT coverage:** NUT-00 through NUT-30, with the exception of NUT-16 (Animated QR). This coverage is inherited directly from the upstream Rust CDK.

**Release and maturity signals:**

| Metric | Value |
|--------|-------|
| Latest release | v0.17.3 (July 12, 2026) |
| Total releases | 11 |
| Stars | 0 |
| Watchers | 0 |
| Forks | 2 |
| Open issues | 0 |
| Total commits | 51 |
| Last commit | July 12, 2026 |
| Upstream Rust CDK status | Self-describes as alpha |

Source: [cashubtc/cdk-go](https://github.com/cashubtc/cdk-go)

## The MIPS/armv7 blocker (the primary finding)

TollGate's CI build matrix (`.github/workflows/build-package.yml`) targets six OpenWrt architectures:

| Architecture | Compile key | SDK | cdk-go support? |
|---|---|---|---|
| aarch64_cortex-a53 | arm64 | mediatek-filogic | Yes |
| aarch64_cortex-a72 | arm64 | bcm27xx-bcm2711 | Yes |
| arm_cortex-a7 | armv7 | bcm27xx-bcm2709 | **No** |
| mipsel_24kc | mipsle-softfloat | ramips-mt7621 | **No** |
| mips_24kc | mips-softfloat | ath79-generic | **No** |
| x86_64 | amd64 | x86-64 | Yes |

cdk-go covers 3 of 6 TollGate architectures (50%). The three unsupported architectures are:

- **arm_cortex-a7** (armv7, 32-bit): Raspberry Pi 4 running in 32-bit mode, and various BCM2709-based boards.
- **mipsel_24kc** (little-endian MIPS): ramips/MT7621-based routers (e.g., Xiaomi Mi Router 4A, Netgear EX6150).
- **mips_24kc** (big-endian MIPS): ath79/Atheros-based routers (e.g., Ubiquiti Nanostation, TP-Link C7, GL.iNet devices).

These are not hypothetical targets. They represent real, deployed hardware running TollGate today. Dropping them is not an option.

**This is the same blocker that #176 identified and rejected.** From #176:

> CGo/FFI wrapper around CDK -- Complex cross-compilation for MIPS/OpenWrt targets. Fragile build chain. Not recommended for embedded targets.

cdk-go is exactly the pattern #176 warned against: a CGo/FFI wrapper around the Rust CDK with no MIPS cross-compilation support. Nothing has changed on this front.

**Conclusion:** cdk-go is NOT a drop-in gonuts replacement for this project.

## POC findings

A POC spike was set up at `tests/cdk-go-spike/` as a separate Go module to keep cdk-go dependencies isolated from `src/go.mod`. Branch: `research/cdk-go-evaluation`.

**Spike setup:**

- Separate Go module (`tests/cdk-go-spike/go.mod`) with `github.com/cashubtc/cdk-go v0.17.3` as a direct dependency.
- Build constraints: `//go:build cgo && linux && (amd64 || arm64)` to prevent compilation on unsupported architectures.
- Three commits: scaffold (`ba16d6b`), token tests (`e1e2072`), wallet integration test (`0441bd9`), go.mod/go.sum fix (`27c4b8d`).

### What works (positive findings)

- **Token decode/encode roundtrip** (`TestCdkGoTokenRoundtrip`): `cdk_ffi.TokenDecode(string)` -> `(*Token).Encode() string` roundtrips correctly. Value extraction, mint URL extraction, and `ProofsSimple()` all return expected values. Tests passed on the first run with zero glue code, demonstrating that cdk-go's API surface is ergonomic for the operations TollGate's `tollwallet` wrapper needs.
- **Malformed input handling** (`TestCdkGoMalformedToken`): empty string, garbage, and missing-prefix inputs all return non-nil errors without panicking. Error handling on the token path is clean.
- **Wallet construction** (`TestCdkGoTestmintWallet` local subtests): `GenerateMnemonic()`, `NewWallet(mintUrl, CurrencyUnitSat{}, mnemonic, WalletStoreSqlite{...}, WalletConfig{...})` all work. FFI native library (`libcdk_ffi.so`, 147 MB for all platforms combined) loads cleanly on linux/amd64.
- **API matches cdk-go's own test patterns**: `PaymentMethodBolt11{}`, `Amount{Value: 1}`, `SplitTargetNone{}`, `&amount` pointer in `MintQuote` -- all consistent with cdk-go's canonical `cdk_test.go`.

### What doesn't work / friction found (negative findings)

- **cdk-go FFI does NOT propagate network errors as Go errors.** When the testmint (`testnut.cashudevkit.org`) was unreachable from the test environment (TCP 443 timeout), the FFI call returned an empty Rust buffer and the Go binding **panicked** with `panic: EOF` in `readInt8` via `FfiConverterOptionalMintInfo.Read` instead of returning a Go `error`. Consumers MUST do their own pre-flight reachability checks (e.g., `net.DialTimeout`) or accept that network failures surface as unrecoverable CGO panics. This is a real ergonomic gap relevant to TollGate's degraded-mode / offline-reload paths (`src/merchant/merchant_degraded.go`).
- **Manual CGO memory management is a footgun.** `Wallet.Destroy()` and any returned `*Token` MUST be explicitly destroyed or memory leaks. Registering `t.Cleanup(wallet.Destroy)` inside a Go subtest destroys the wallet when the subtest ends -- not when the parent test ends -- which caused `*Wallet object has already been destroyed` panics. Cleanup must be registered at the top-level test scope. This is an ongoing maintenance burden versus gonuts's GC-managed objects.
- **Testmint unreachability blocked the full end-to-end wallet flow.** `testnut.cashudevkit.org` was unreachable from the sandbox where the spike ran (TCP 443 timeout), so the `request_mint_quote` -> `wait_for_quote_paid` -> `mint_tokens` -> `total_balance_reflects_mint` subtests could not be exercised against a live mint. General HTTPS works fine from the same host (`https://example.com` -> 200, `https://cashu.space` -> 200), so the block is specific to that mint. The local subtests (mnemonic, wallet construction) PASS, proving the FFI loads and the local code path works. Running the network-gated subtests from a host with mint reachability remains as follow-up.

### Transcript artifact

The manual run transcript is captured at `tests/cdk-go-spike/` (run `CDK_SPIKE_NETWORK=1 make test-network` from that directory to reproduce). The test cleanly skips when `CDK_SPIKE_NETWORK` is unset so CI is green by default.

## Options compared

| | A: Maintain gonuts fork indefinitely | B: cdk-go per-arch wallet split | C: tollgate-rs migration (per #176) | D: Native Go NUT reimplementation |
|---|---|---|---|---|
| **Initial effort** | Low (status quo) | Medium (abstraction layer, dual wallet impls, build matrix changes) | High (Rust rewrite of TollGate wallet/merchant modules, hardware testing) | Very high ( reimplement NUT-00 through NUT-30 in Go from scratch) |
| **Ongoing cost** | High (each gonuts bump risks regressions, fork drift from upstream) | High (dual wallet codepaths, doubled test surface, per-arch conditional compilation) | Low (CDK is the upstream Cashu reference implementation, actively maintained) | Very high (keep pace with evolving NUT specs, no upstream to follow) |
| **V2/V4 support** | No (gonuts lacks V2 keyset work, upstream is inactive) | Yes (inherited from Rust CDK) | Yes (CDK native) | Yes, but only if implemented and maintained |
| **MIPS support** | Yes (pure Go, cross-compiles anywhere) | No (cdk-go does not ship MIPS binaries) | Yes (Rust cross-compiles to MIPS, no FFI needed) | Yes (pure Go, same as gonuts) |
| **Ecosystem alignment** | Against it (gonuts is not on docs.cashu.space/libraries) | Partial (cdk-go uses CDK, but Go is a second-class citizen in the Cashu ecosystem) | Fully aligned (CDK is the reference implementation, Rust is the primary language) | Against it (duplicates work already done in CDK, not upstream-recognized) |
| **Risk** | Fork bit-rot, unrecoverable wallet states, crypto audit gaps | Build matrix complexity, cdk-go itself is alpha with 0 stars, FFI stability | Physical router testing required for MIPS targets, Rust learning curve for Go contributors, #176 M4 milestone still open | Massive scope, high probability of incomplete/incorrect crypto, no community to share maintenance burden |

**Summary:** Option A is what we are doing now and it works, but every gonuts bump (PR #253, PR #266, #257) has carried risk of wallet-bricking regressions. Option B is technically possible but halves the architecture coverage and doubles the test surface for an alpha-quality Go binding with zero community adoption. Option C is the path #176 recommended and remains the strongest long-term bet, but requires upfront Rust investment and physical hardware testing. Option D is the most effort for the least strategic return.

## Recommendation

1. **cdk-go as-is cannot replace gonuts across TollGate's full architecture matrix.** The MIPS/armv7 gap is a hard blocker, not a future possibility. cdk-go does not ship native libraries for these targets, and cross-compiling the Rust CDK for MIPS soft-float OpenWrt would require rebuilding the entire FFI chain outside the prebuilt distribution model.

2. **Short-term: continue the gonuts-tollgate fork-bump cycle.** This is the current state of affairs. Each bump should come with a characterization test suite to catch the class of regressions seen in #156 and PR #266 before they reach production.

3. **Medium-term: the POC spike on `research/cdk-go-evaluation` establishes evidence for whether a per-architecture wallet split is worth the complexity.** If the spike shows clean FFI integration and the abstraction layer is tractable, option B becomes a viable interim path for amd64/arm64 deployments while MIPS continues on gonuts. If the spike shows friction (build pain, test surface explosion, FFI edge cases), option B should be ruled out.

4. **Long-term: tollgate-rs (per #176) remains the recommended path for MIPS-class devices.** The Rust CDK is the reference Cashu implementation. It cross-compiles to MIPS without FFI gymnastics. The cost is a rewrite, but the payoff is permanent alignment with the Cashu ecosystem. cdk-go could serve amd64/arm64 in a hybrid model if option B's POC is positive, but it should not be the primary migration target.

5. **Decision needed from maintainers:** pursue (B) per-architecture hybrid, (C) full tollgate-rs migration, or (D) native Go reimplementation? This tracking issue documents the tradeoffs; a maintainer decision should close it with the chosen path and link to the implementation plan.

## Related

- Branch: [`research/cdk-go-evaluation`](https://github.com/OpenTollGate/tollgate-module-basic-go/tree/research/cdk-go-evaluation) (PR to follow)
- Prior ecosystem analysis: #176 (V2 keyset support: current state, ecosystem analysis, and path forward)
- gonuts fork bumps: #134, #156, [PR #253](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/253), [PR #266](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/266)
- Recent production pain: #257, #260
- cdk-go repository: https://github.com/cashubtc/cdk-go
- gonuts upstream: https://github.com/elnosh/gonuts
- gonuts-tollgate fork: https://github.com/OpenTollGate/gonuts-tollgate
- Cashu libraries (official): https://docs.cashu.space/libraries
- Rust CDK (upstream): https://github.com/cashubtc/cdk
