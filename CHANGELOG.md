# Changelog

All notable changes to the TollGate basic module are documented here.
This project loosely follows [Keep a Changelog](https://keepachangelog.com/)
and [Semantic Versioning](https://semver.org/).

> **Note:** Releases prior to `v0.4.0` predate this changelog and were not
> documented. The entries below cover everything merged into `main` since the
> `v0.4.0` tag.

## [Unreleased]

Changes on `main` since `v0.4.0` (tagged 2026-04-06).

### Added

- **Upstream WiFi management.** New manager that detects and connects to
  upstream gateways, with a startup connectivity check, TollGate-aware probing,
  and a cross-radio DHCP nudge to recover stuck links
  ([#109](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/109),
  [#122](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/122)).
- **Mint resilience.** Per-mint health tracking, try-all-mints fallback on
  payment, and automatic recovery of mints that come back online, so a single
  failing mint no longer blocks purchases
  ([#120](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/120)).
- **Lightning capability probing.** Each reachable mint's Lightning backend is
  now probed at startup (and on every proactive health check) by requesting a
  minimal 1-sat mint quote. Only mints whose Lightning node answered are
  advertised with a new `supports_ln` advertisement tag; when a mint's LN
  backend goes down, Lightning is withheld (and reactively degraded on a live
  invoice failure) instead of failing silently at purchase time. See
  [TIP-02](docs/protocol/TIP-02.md).
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
  open gate" and recovered only via the token-recovery path ~60–90s later).
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

- **Release distribution:** publish redundantly to multiple relays and Blossom
  mirrors
  ([#152](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/152)).
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
- Add `AGENTS.md` with LLM contributor rules and tighten `.gitignore` for
  planning docs
  ([#159](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/159)).

## [v0.4.0] - 2026-04-06

Router-to-router autopay
([#77](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/77)) and
earlier work. Not documented in this changelog.

[Unreleased]: https://github.com/OpenTollGate/tollgate-module-basic-go/compare/v0.4.0...main
[v0.4.0]: https://github.com/OpenTollGate/tollgate-module-basic-go/releases/tag/v0.4.0
