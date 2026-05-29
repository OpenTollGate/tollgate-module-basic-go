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

## [v0.4.0] - 2026-04-06

Router-to-router autopay
([#77](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/77)) and
earlier work. Not documented in this changelog.

[Unreleased]: https://github.com/OpenTollGate/tollgate-module-basic-go/compare/v0.4.0...main
[v0.4.0]: https://github.com/OpenTollGate/tollgate-module-basic-go/releases/tag/v0.4.0
