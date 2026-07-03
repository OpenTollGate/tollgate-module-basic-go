# TollGate v0.5.0 (tollgate-wrt)

**Released**: 2026-07-03

<!-- markdownlint-disable MD013 -->

v0.5.0 is the resilience-and-hardening release. The headline theme is
that a TollGate now degrades gracefully instead of falling over: a
failing Cashu mint no longer blocks purchases, the merchant drops into
a degraded mode (and recovers) when no mint is reachable, and the
reseller path — one TollGate buying from another over Wi-Fi — got a
dedicated upstream Wi-Fi manager plus a series of two-router autopay
reliability fixes. Around that core, the captive portal gains HTTPS,
Lightning checkout, and a balance view; configuration becomes
schema-driven with a JSON CLI for admin-UI integration; packaging adds
`.apk` output for OpenWrt 25.x and an x86_64 target; and a security
pass hardened password generation, proxy-header trust, and the
captive-portal firewall posture.

## At a glance

- Per-mint health tracking, try-all-mints fallback on payment, and
  automatic recovery of mints that come back online.
- Merchant degraded mode: the router keeps serving and recovers
  dynamically when mints become reachable again, with a matching
  captive-portal UI.
- New upstream Wi-Fi manager: startup connectivity check,
  TollGate-aware probing, and a cross-radio DHCP nudge to recover
  stuck links.
- Captive portal: HTTPS with a self-signed certificate mode, Lightning
  checkout, and a balance view.
- Schema-driven configuration with `tollgate --json config
  schema/get/set/save` for admin-UI integration; config schema moves
  to `v0.0.8` with in-place migration.
- Packaging: `.apk` output for OpenWrt 25.x alongside `.ipk`, an
  x86_64/amd64 target for virtual-lab testing, and a local OpenWrt SDK
  build helper.
- Releases are published redundantly: NIP-94 events on multiple Nostr
  relays, with every successful Blossom mirror listed as a download
  URL.
- Security: `crypto/rand` password generation, `X-Forwarded-For`
  trusted only from localhost, IP/MAC input validation, a request-body
  cap, and an IPv6 captive-portal bypass closed.

## What's new

### Mint resilience and merchant degraded mode

v0.5.0 assumes mints fail, because they do. The wallet now tracks
health per mint, payment tries all accepted mints instead of giving up
on the first failure, and mints that come back online are recovered
automatically ([#120](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/120)).
All accepted mints are registered in the wallet at startup
([#167](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/167)),
and the startup health check retries aggressively so a router that
boots faster than its uplink still finds its mints.

When *no* mint is reachable, the merchant no longer wedges: a
zero-dependency `PaymentMerchant` interface
([#138](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/138)),
mint-health provider plumbing
([#139](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/139)),
and dynamic upgrade/downgrade between full and degraded operation
([#140](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/140))
let the router advertise its state honestly, with a degraded-mode
captive-portal UI so customers see what is happening
([#141](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/141)).

V2 keyset IDs are now supported, keeping payments working against
mints running CDK 0.16.0+
([#126](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/126)).

### Reseller mode: upstream Wi-Fi management and autopay reliability

The buying side — a TollGate purchasing access from an upstream
TollGate and reselling it — got its own manager for the Wi-Fi link
([#109](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/109),
[#122](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/122)):
a startup connectivity check, TollGate-aware probing of candidate
gateways, and a cross-radio DHCP nudge that recovers links stuck after
a handoff.

Two-router autopay got a matching reliability pass. The valve now
retries `ndsctl auth` briefly so a payment's gate-open no longer fails
when NoDogSplash hasn't yet registered the reseller client
([#170](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/170))
— previously this surfaced as "failed to open gate" and only recovered
via token recovery a minute later. A stale valve timer callback can no
longer delete its replacement, and payout math is guarded against
division-by-zero and `uint64` underflow
([#161](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/161)).
Data-metered (bytes) sessions always open the gate on purchase
([#167](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/167)).

### Captive portal: HTTPS, Lightning checkout, balance view

SSL/HTTPS management for the captive portal is new in this release and
implemented in Go with wrapper scripts
([#123](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/123),
[#142](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/142)):
a self-signed certificate mode, hostname setup (the router presents as
`TollGate`), and captive-portal domain configuration. Along the way
this fixed a `uhttpd` crash loop, keeps NoDogSplash on port 80, and
makes the certificate CN match the actual hostname.

The portal itself gains a Lightning checkout flow and a balance view
([#107](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/107)).

### Schema-driven configuration and the JSON CLI

Configuration is now schema-driven: `GetConfigSchema()` describes
every field, and dot-path get/set with validation backs a new
`tollgate --json config schema/get/set/save` command family (plus
health and wallet queries) built for admin-UI integration
([#147](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/147)).
The schema ships with a contract lint and a build-purity contract test
in CI, so the schema, the Go types, and any JS consumer cannot drift
apart silently.

### Packaging and distribution

- **OpenWrt 25.x `.apk`.** The CI matrix now produces `.apk` packages
  alongside `.ipk`
  ([#97](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/97),
  [#183](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/183)).
  Install with `apk add --allow-untrusted`; the `.ipk` continues to
  cover OpenWrt 24.10 and earlier via `opkg`.
- **x86_64 / amd64 target** for virtual-lab testing
  ([#80](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/80)).
- **Local OpenWrt SDK build helper**
  ([scripts/build-sdk-package.sh](scripts/build-sdk-package.sh)) for
  reproducing package builds off-CI
  ([#105](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/105),
  [#79](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/79)).
- **Redundant release distribution.** Packages are uploaded to
  multiple Blossom servers and announced as NIP-94 events on multiple
  Nostr relays
  ([#152](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/152),
  [#155](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/155)),
  with every successful mirror included as a `url` tag
  ([#183](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/183)),
  so one dead mirror doesn't strand a release.

## Behavior changes worth flagging

These affect operators on upgrade.

- **Config schema is now `v0.0.8`.** Existing `v0.0.7` configs are
  migrated in place on first start: the version is bumped and missing
  upstream-Wi-Fi defaults are filled in; your explicit settings are
  preserved
  ([#178](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/178)).
- **Default profit share now splits the dev cut individually.** The
  0.21 `developer` share in *fresh* default configs is replaced by
  three maintainer identities at 0.07 each (`c08r4d0r`,
  `amperstrand`, `origami74`), each with its own Lightning address;
  the 0.79 `owner` share is unchanged
  ([#165](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/165)).
  Existing configs keep whatever `profit_share` they already have.
- **IPv6 is disabled on the LAN at installation.** Clients could
  previously route around the captive portal over IPv6; NoDogSplash
  only gates IPv4
  ([#148](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/148),
  [#160](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/160)).
- **NoDogSplash gateway port is now 2050**, and a malformed BOLT11
  invoice no longer aborts startup
  ([#158](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/158)).
- **The router presents as `TollGate`**: hostname and captive-portal
  domain are configured at setup, and the TLS certificate CN matches
  ([#123](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/123)).

## Notable bug fixes

The [CHANGELOG](CHANGELOG.md) has the exhaustive list. The
operator-relevant subset:

- **Payment correctness bundle**: case-insensitive mint URL
  comparison, proper spent-token detection, and valve re-auth without
  a stale in-memory cache
  ([#104](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/104)).
- **Transport reliability on OpenWrt**: TLS 1.2 is forced and HTTP
  clients get timeouts, so requests no longer hang indefinitely on
  constrained routers
  ([#137](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/137)).
- **First-boot stability**: the reboot race is gone, `uci-defaults`
  run faster, the AP SSID is unified
  ([#84](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/84)),
  and `postinst` executes UCI defaults and reloads services so a fresh
  install comes up correctly
  ([#90](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/90)).
- **`.ipk` installs on stock OpenWrt**: the package is wrapped as a
  gzipped tar instead of an `ar` archive
  ([#100](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/100)).
- **Expired timed sessions are evicted** and the scan loop actually
  starts
  ([#106](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/106)).
- **Duplicate NoDogSplash firewall rules** in `users_to_router` are
  prevented
  ([#123](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/123)).

### Security

- Passwords are generated with `crypto/rand` instead of time-seeded
  `math/rand`
  ([#111](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/111)).
- `X-Forwarded-For` is trusted only from localhost, client-supplied
  IP/MAC inputs are validated, and request bodies are capped at 1 MB
  ([#104](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/104)).
- The IPv6 captive-portal bypass is closed (see behavior changes).
- An additional hardening and correctness-guard sweep landed late in
  the cycle
  ([#163](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/163)).

## Upgrade notes

Operator-actionable items moving from v0.4.0 to v0.5.0:

- **Config migrates automatically.** On first start the config is
  migrated `v0.0.7` → `v0.0.8` in place, preserving your settings. If
  you upgraded through the v0.5.0 alphas and the config version looks
  stuck, the migration fix in
  [#178](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/178)
  resolves it — just restart the service after upgrading.
- **Review your `profit_share`** if you started from a default config
  and expected the old single `developer` entry; fresh defaults now
  split the dev cut across three maintainer identities. Existing
  configs are not rewritten.
- **Pick the right package format**: `.apk` for OpenWrt 25.x,
  `.ipk` for OpenWrt 24.10 and earlier.
- **Expect the portal on HTTPS** with a self-signed certificate and
  the `TollGate` hostname; NoDogSplash stays on port 80, gateway port
  2050.
- **IPv6 on the LAN is disabled at install time.** If you re-enable it
  manually, you are reopening the captive-portal bypass.

## Getting v0.5.0

- **Pre-built packages and firmware**:
  [releases.tollgate.me](https://releases.tollgate.me) — the release
  manager for firmware images and package builds.
- **Nostr**: releases are announced as NIP-94 file-metadata events on
  the project relays, with multiple Blossom mirror URLs per artifact.
- **OpenWrt 25.x**: `apk add --allow-untrusted tollgate-wrt-<version>.apk`
- **OpenWrt 24.10 and earlier**: `opkg install tollgate-wrt_<version>_<arch>.ipk`
- **From source**: [scripts/build-sdk-package.sh](scripts/build-sdk-package.sh)
  cross-compiles the binaries (Go version per [src/go.mod](src/go.mod))
  and stages the canonical [packaging/](packaging/) recipe into the
  OpenWrt SDK, producing either format.

The full per-PR changelog lives in [CHANGELOG.md](CHANGELOG.md).
Issues and discussion at
[github.com/OpenTollGate/tollgate-module-basic-go](https://github.com/OpenTollGate/tollgate-module-basic-go).

## Contributors

Thanks to everyone who contributed code, packaging work, bug reports,
or reviews to this release:
[@c03rad0r](https://github.com/c03rad0r),
[@Amperstrand](https://github.com/Amperstrand),
[@Origami74](https://github.com/Origami74), and Alex Xie.
