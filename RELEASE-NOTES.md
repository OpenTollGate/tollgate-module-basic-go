# TollGate v0.6.0 (tollgate-wrt)

**Released**: 2026-10-21
**Channel**: stable
**Module commit**: `cf7cad34677e87e775e7f16786b1bbf18c4bc8bc`
**Package**: `tollgate-wrt_0.6.0_<arch>-<target>.{apk,ipk}`

<!-- markdownlint-disable MD013 -->

`v0.6.0` is the rc6 code frozen on 2026-10-14, released with final version
metadata. The release commit adds `VERSION`, the assembled changelog and this
document; it does not change the runtime code that was tested as
`v0.6.0-rc6`.

## What this release is

The 0.6 line is about **funds safety and honest failure**, carried to three
invariants the Go gateway must hold:

1. **One economic payment buys at most one economic session grant** — including
   concurrent submission of the same note.
2. **An ambiguous Cashu outcome never re-exposes deterministic derivation
   outputs** — a processed swap whose response was dropped is UNKNOWN, not
   FAILED, and blinded messages are never re-sent.
3. **A received payment is owed service or a recoverable claim** — a gate-open
   failure after a successful `Receive` becomes a durable, restart-surviving
   entitlement that converges when NoDogSplash accepts.

## Release highlights

- **The admin board is the default face.** The entry pair (`:8080`/`:443`)
  serves the board; LuCI moves to `:8090`/`:8443` and is one button away. The
  board's credential gate fails closed: an empty root hash never opens an admin
  listener, and the package installs the fixed `TollGate` default (provisional,
  forced change at first login) when the operator sets none.
- **Bounded mint registration.** A slow or hanging mint can no longer stall
  boot; registration is bounded, and deferred mints register on first use
  (#940).
- **Private-subnet recheck (#926).** A late-DHCP upstream that collides with
  the derived `br-private` moves the subnet without losing DNS.
- **Target-qualified release assets.** Asset names are
  `tollgate-wrt_<version>_<arch>-<target>.<ext>`, so one CPU arch that serves
  several OpenWrt targets (notably `mips_24kc`) publishes every target without
  collisions. The matrix covers eight `(arch, target)` pairs; see the feed's
  `TARGETS.md` for per-device validation status.
- **CA trust is a declared dependency.** `ca-bundle` is in the package
  metadata on every lane, so a router that can reach the feed can verify TLS
  for mints and Lightning.

## Install

Use the **installer wizard**. It installs both `.apk` (OpenWrt 25.12+) and
`.ipk` (OpenWrt 24.10); on a router already running OpenWrt it skips flashing
and selects the package by package manager.

```sh
bash <(curl -fsSL https://raw.githubusercontent.com/OpenTollGate/tollgate-installer/main/install-and-test.sh) \
  --installer-tag v0.6.0 --tag v0.6.0
```

**Manual install is temporarily unsupported in this release.** The procedure
is withdrawn from the runbook and tracked for restoration at
[OpenTollGate/tollgate-installer#155](https://github.com/OpenTollGate/tollgate-installer/issues/155).
The wizard remains the supported path.

## Known issues

- **Loopback rate limiting vs the startup gate.** The API rate limiter runs
  before the startup gate and has no loopback exemption, so an installer
  polling from `127.0.0.1` can receive `429` while the merchant initializes.
  Installer rc33 classifies that as `pending`; the module-side fix is
  deliberately post-release (#999).
- **Manual install deferred** for the reasons tracked in installer #155: the
  fresh-install default-credential write, the first-pass `entry_ui` fallback,
  the wizard-only `tollgate.lan` alias, and the wizard-only leading-`!` guest
  SSID.
- The `tollgate version` string reports the **module** build, not the feed
  release. Confirm the package with `apk list -I` / `opkg list-installed`.

## Verification

Validated on the rc6 artifacts on the physical pair:

| Device | OpenWrt | Lane | Package |
|---|---|---|---|
| GL-MT3000 | 25.12.5 | `.apk` | `0.6.0_rc6-r1` |
| GL-MT6000 | 24.10.8 | `.ipk` | `0.6.0_rc6-r1` |

`v0.6.0` is the same runtime code with the version and documentation metadata
updated, so the rc6 evidence carries forward.

## Thanks

Everyone who ran the RC line on real hardware, filed the manual-test fallout,
and kept the release honest about what was and was not validated.
