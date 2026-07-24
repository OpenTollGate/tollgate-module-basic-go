# Changelog

All notable changes to the TollGate basic module are documented here.
This project loosely follows [Keep a Changelog](https://keepachangelog.com/)
and [Semantic Versioning](https://semver.org/).

> **Note:** Releases prior to `v0.4.0` predate this changelog and were not
> documented. The entries below cover everything merged into `main` since the
> `v0.4.0` tag.

## [Unreleased]

### Fixed

- **NDS fw4/nftables enforcement bridge.** NDS 5.0.2 inserts enforcement
  chains in `ip filter FORWARD` (iptables-nft), but OpenWrt 24.10's fw4
  `inet fw4 forward` chain accepts forwarded traffic at the same priority
  before the ip filter chain runs — leaving NDS's enforcement chain dead
  (0 packets). Authenticated clients had mangle marks set correctly but
  traffic was never actually gated. Added `/etc/nftables.d/20-nds-enforce.nft`
  include file that hooks into `inet fw4 forward` at priority -1 (before
  fw4's accept) and enforces NDS marks. Bumped setup version to v0.5.1.

- **V2 keyset swap crash (critical).** Bump `gonuts-tollgate` from v0.7.4
  to v0.7.6 to pick up the fix for the V2 keyset derivation path bug in
  NUT-13. The old code called `binary.BigEndian.Uint64(keysetBytes)` on
  V2 keyset IDs (33 bytes), silently truncating to the first 8 bytes.
  This produced a wrong deterministic secret derivation path, causing
  every swap against CDK 0.16+ mints with V2-only keysets to fail with
  "outputs have already been signed before." V1 keyset IDs (8 bytes) are
  unaffected — the fix branches on keyset ID length, preserving V1
  behavior and NUT-13 test vectors
  ([#176](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/176),
  [#257](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/257)).

- **Cashu wallet swap-counter race (critical).** Bump `gonuts-tollgate`
  from v0.7.1 to v0.7.4 to pick up the fix for an unrecoverable
  "blinded message already signed" error (NUT-02 code 10002). In v0.7.1
  the keyset counter was incremented only after a successful swap, so
  a transient mint failure (timeout, DNS hiccup, 5xx) left the counter
  stuck — every retry reused the same counter, the mint rejected with
  10002, and the wallet bricked permanently with no self-recovery.
  v0.7.4 increments the counter before the swap call and adds a
  `swapWithRetry` path that regenerates fresh blinded messages on
  retry.

- **Mint URL fuzzy matching in `calculateAllotment()`.** The mint URL
  from Cashu tokens was compared against configured accepted mints
  using exact string equality (`==`), causing payments to fail when
  the URL differed by a trailing slash, uppercase host, or path
  normalization. `calculateAllotment()` now uses the existing
  `tollwallet.MintURLMatches()` function which tolerates these
  differences — the same function already used by the wallet layer
  during `Receive()`
  ([#250](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/250),
  [#251](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/251)).

- **Lightning quote persistence across restarts.** Lightning invoice
  quotes are now persisted to disk (`quotes.json` in the wallet
  directory) so they survive process restarts. Previously all pending
  quotes were stored in-memory only; when `tollgate-wrt` restarted
  (deploy, config change, or crash), users who had already paid saw the
  portal stuck on "Waiting for payment" because the backend returned
  `lightning quote not found`. On startup, persisted quotes are loaded,
  expired/settled ones are pruned, and monitoring goroutines are
  relaunched for unpaid quotes so access is granted if the invoice was
  settled while the process was down
  ([#248](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/248)).

- **Protocol compliance: notice event codes and tips tag.** Map
  implementation-specific notice event codes to spec-defined codes from
  TIP-01 (`session-management-failed`, `gate-open-failed`, and
  `allotment-calculation-failed` → `session-error`;
  `payment-error-token-spent` already matched). Codes with no spec
  equivalent (`payment-error-invalid-token`, `invalid-mac-address`,
  `payment-processing-timeout`, `payment-processing-failed`) are kept
  as-is with precision in the content string. Also remove non-existent
  TIP-03 and TIP-04 from the advertisement `tips` tag — only TIP-01 and
  TIP-02 are defined.

- **Wireless config missing-file guard.** `scanner.GetRadios()` and
  `connector.getRadiosFromConfig()` now return gracefully when
  `/etc/config/wireless` does not exist instead of erroring every scan
  cycle
  ([#196](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/196)).
- **Dead firewall include removed.** The `firewall-tollgate` include file
  was silently rejected by fw4 (nftables); rules now created directly via
  UCI named sections with idempotent guards
  ([#196](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/196)).
- **Makefile references to deleted firewall-tollgate.** PR #196 removed
  `files/etc/config/firewall-tollgate` but two Makefile references (install
  rule + conffiles list) were left behind, breaking all package builds
  ([#235](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/235)).
- **Upstream gateway IP validation.** Loopback, unspecified, and
  link-local addresses are now rejected in the TollGate prober to prevent
  SSRF
  ([#196](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/196)).

### Changed / Internal

- **WalletPort abstraction.** Decouples merchant/lightning code from
  gonuts via a library-agnostic `WalletPort` interface, enabling mock
  injection for tests and a build-tag swap to cdk-go
  (`-tags cdk_wallet`). Includes `GonutsWallet` (default) and
  `CdkWallet` (cdk-go FFI) adapters, NUT-04 spec-compliant
  `MintQuoteState` JSON marshaling (uppercase strings, dual-format
  unmarshal for backward compatibility), and characterization tests
  for token flow and wire format
  ([#285](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/285)).
- **Operator guide.** New `docs/operator-guide.md` covering every `tollgate`
  CLI subcommand (service, wallet, private network, upstream Wi-Fi, config,
  health) with example output, flags, and a troubleshooting section; README
  modules table and documentation list updated to reflect the full CLI
  surface
  ([#188](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/188)).

## [v0.5.0] - 2026-07-03

Everything merged into `main` since `v0.4.0` (tagged 2026-04-06),
including the `v0.5.0-alpha1` through `v0.5.0-alpha3` pre-releases.
Release notes: [RELEASE-NOTES.md](RELEASE-NOTES.md).

### Added

- **Upstream WiFi management.** New manager that detects and connects to
  upstream gateways, with a startup connectivity check, TollGate-aware probing,
  and a cross-radio DHCP nudge to recover stuck links
  ([#109](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/109),
  [#122](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/122)).
- **Mint resilience.** Per-mint health tracking, try-all-mints fallback on
  payment, and automatic recovery of mints that come back online, so a single
  failing mint no longer blocks purchases
  ([#120](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/120)),
  plus aggressive mint health-check retry on startup so a router that boots
  faster than its uplink still finds its mints.
- **SSL/HTTPS management for the captive portal**, all new in this release and
  implemented in Go, with a self-signed certificate mode, hostname setup
  (`TollGate`), and captive-portal domain configuration
  ([#123](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/123)).
- **Lightning checkout and balance view** in the captive portal
  ([#107](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/107)).
- **Schema-driven configuration with a `--json` CLI.** `GetConfigSchema()` and
  dot-path get/set with validation, plus `tollgate --json config
  schema/get/set/save` (and health/wallet) commands to support admin-UI
  integration. Ships with a test workflow, schema contract lint, and a
  build-purity contract test
  ([#147](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/147)).
- **x86_64 / amd64 build target** for virtual-lab testing
  ([#80](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/80) and
  follow-ups).
- **Local OpenWrt SDK source-build helper** for reproducing package builds
  off-CI
  ([#105](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/105),
  [#79](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/79)).
- **Merchant degraded mode.** A zero-dependency `PaymentMerchant` interface
  ([#138](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/138)),
  mint health tracking with a provider and sentinel error plus USM decoupling
  ([#139](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/139)),
  and dynamic upgrade/downgrade between full and degraded operation
  ([#140](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/140)),
  surfaced through a captive-portal degraded-mode UI
  ([#141](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/141)).
- **SSL management rewritten in Go** with wrapper scripts, replacing the earlier
  shell-driven approach
  ([#142](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/142)).
- **V2 keyset ID support** for CDK 0.16.0+ compatibility
  ([#126](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/126)).

### Fixed

- **Transport reliability on OpenWrt:** force TLS 1.2 and set HTTP client
  timeouts so requests no longer hang on constrained routers
  ([#137](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/137)).
- **Security:** generate passwords with `crypto/rand` instead of time-based
  (`math/rand`) entropy
  ([#111](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/111)).
- **First-boot stability:** eliminate the reboot race, speed up `uci-defaults`,
  and unify the AP SSID
  ([#84](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/84)).
- **Install/postinst:** execute UCI defaults and reload services during
  `postinst` so a fresh install comes up correctly
  ([#90](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/90)).
- **Captive portal / HTTPS:** prevent the `uhttpd` crash loop by configuring a
  cert/key for HTTPS, keep NoDogSplash on port 80, and make the cert CN match
  the actual hostname
  ([#123](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/123)).
- **Firewall:** prevent duplicate NoDogSplash firewall rules in
  `users_to_router`
  ([#123](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/123)).
- **Packaging:** wrap the `.ipk` as a gzipped tar instead of an `ar` archive so
  it installs on stock OpenWrt
  ([#100](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/100)).
- **Payment correctness:** case-insensitive mint URL comparison, proper
  spent-token detection, valve re-auth without a stale in-memory cache, and
  trust `X-Forwarded-For` only from localhost, plus IP/MAC input validation and
  a 1 MB request-body cap
  ([#104](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/104)).
- **Merchant payout safety / valve timer race:** guard against `PricePerStep=0`
  division-by-zero, prevent a `uint64` underflow in payout, and stop a stale
  valve timer callback from deleting its replacement
  ([#161](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/161)).
- **Two-router autopay reliability:** retry `ndsctl auth` briefly in the valve
  so a payment's gate-open no longer fails on the first attempt when NoDogSplash
  has not yet registered the reseller client (previously failed with "failed to
  open gate" and recovered only via the token-recovery path ~60–90s later)
  ([#170](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/170)).
- **Wallet/mint registration:** register all accepted mints in the wallet at
  startup, and always open the gate for bytes (data-metered) sessions
  ([#167](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/167)).
- **Config migration:** fix the `config_version` `v0.0.7` → `v0.0.8` migration
  ([#174](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/174))
  and the `upstream_detector` `go.mod`
  ([#172](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/172))
  ([#178](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/178)).
- **BOLT11 / NoDogSplash:** make BOLT11 decode non-fatal and set the NoDogSplash
  gateway port to 2050
  ([#158](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/158)).
- **Captive-portal bypass:** disable IPv6 on the LAN during installation so
  clients cannot route around the portal
  ([#148](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/148),
  [#160](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/160)).
- **Session lifecycle:** evict expired timed sessions and start the scan loop
  ([#106](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/106)).
- **Additional security hardening and correctness guards**
  ([#163](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/163)).

### Changed / Internal

- **Default profit share:** split the 0.21 dev share across three maintainer
  identities (`c08r4d0r`, `amperstrand`, `origami74`, 0.07 each), each with its
  own Lightning address; applies to fresh default configs, existing configs are
  not rewritten
  ([#165](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/165)).
- **Release distribution:** publish redundantly to multiple relays and Blossom
  mirrors
  ([#152](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/152)),
  and list every successful Blossom mirror as a `url` tag on the NIP-94
  release events
  ([#183](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/183)).
- **CI:** split compile from package, add APK output and batched publish, native
  `.ipk` packaging with a flag-based matrix and a compression gate, and run the
  build workflow on pull requests
  ([#97](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/97),
  [#98](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/98),
  [#80](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/80)).
- Moved the `random-lan-ip` UCI default out to `tollgate-os`
  ([#96](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/96)).
- Renamed `c03rad0r` to `c08r4d0r` across the codebase
  ([#92](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/92)).
- Dead-code and docs cleanup sweep
  ([#81](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/81)).
- **CI:** replace artifact actions with Blossom + Nostr coordination
  ([#155](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/155))
  and expand the test matrix to cover standalone-buildable modules
  ([#157](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/157)).
- **CI:** skip the build/publish pipeline for fork PRs, which cannot access the
  publishing secrets
  ([#166](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/166)),
  and build an `x86_64` `.apk` variant
  ([#183](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/183)).
- **Tests:** make the root-module test hermetic via a fresh temp config dir
  (`testenv` build tag), so the suite runs off-router
  ([#169](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/169),
  [#179](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/179)).
- Add `AGENTS.md` with LLM contributor rules and tighten `.gitignore` for
  planning docs
  ([#159](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/159));
  since expanded alongside the new [CONTRIBUTING.md](CONTRIBUTING.md) and
  [PR-REVIEW.md](PR-REVIEW.md).

## [v0.4.0] - 2026-04-06

Router-to-router autopay
([#77](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/77)) and
earlier work. Not documented in this changelog.

[Unreleased]: https://github.com/OpenTollGate/tollgate-module-basic-go/compare/v0.5.0...main
[v0.5.0]: https://github.com/OpenTollGate/tollgate-module-basic-go/compare/v0.4.0...v0.5.0
[v0.4.0]: https://github.com/OpenTollGate/tollgate-module-basic-go/releases/tag/v0.4.0
