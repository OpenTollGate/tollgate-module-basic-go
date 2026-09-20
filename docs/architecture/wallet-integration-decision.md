# Wallet backend integration: in-process vs sidecar

**Decided:** 2026-09-16, on the measurements recorded in the wallet-migration
archive (<https://github.com/felixfelix-bot/tollgate-wallet-migration-research>).

## Decision

**Per target, not one-size-fits-all:**

| Target tier | Wallet form | Backend |
|---|---|---|
| **Small / 16 MB mipsel** | **in-process** | gonuts (default; no extra artifact) |
| **Large / ≥128 MB aarch64** (MT6000-class) | **sidecar** | CDK daemon (`cdk-walletd`) |

The choice is data (`wallet-policy.json`), not a build fork: the sidecar client
is always compiled in (`src/tollwallet/sidecar.go`, landing with the
wallet-backend pull request #395) and the router points at a daemon (or not) via
`wallet.backend` + `wallet.socket`.

## Why

- An in-process backend costs **0 extra flash**; a sidecar costs its **whole
  binary**: **6.02 MiB (aarch64) / 7.51 MiB (mipsel)**, plus ~6 MiB RSS and one
  supervised process. The installed TollGate footprint (service 11.52 MiB + CLI
  6.82 MiB) is already ~18 MiB, so the daemon is a large *marginal* addition.
  (There is no flash saving from "replacing" gonuts yet — the in-process link is
  not behind a build tag that would drop it.)
- On the small tier that marginal cost does not fit; on the large tier it is
  affordable and buys what this decision is for: **grade-A ownership**,
  **process isolation** (the Go service stays `CGO_ENABLED=0` and links no Cashu
  library), and the parity suite passing unchanged.
- Rejected: **in-process CDK FFI** (`cdk-go`) — glibc-only prebuilt libs and a
  133 MiB source `.a`; it is a grade-B patch-set we would own, and dead on musl.
- Rejected: **CLI wrapper** — spawn-per-call (`cdk-cli`) has cold-start, no
  connection reuse, and shells out on the router; strictly worse than a daemon.

## Supervision (OpenWrt `procd`)

The daemon is a first-class service, not a child of `tollgate-wrt`:

- `procd` instance with **respawn** (`respawn 3600 5 5`) and `stderr` capture to
  syslog; `START=95` so it is up **before** `tollgate-wrt` (`START=99`).
- Socket at `/var/run/tollgate/wallet.sock`, directory `0700`, socket `0600`,
  owned by the service user.
- **Health probe:** the service calls the `info` handshake on startup; if it
  fails, the module falls back per policy (do **not** fail the whole service).
- **Seed source:** pass `--mnemonic` from the device identity seed, never on the
  command line in config (a `procd` env file `0600` or a seed file).
- Sketch: the `cdk-walletd` init script in the wallet-migration archive.

## Socket authentication

A unix socket reachable by any local process can drive the wallet (send/fund).
Two layers, both required before non-experimental use:

1. **Filesystem perms** — `0700` dir + `0600` socket, service-owned.
2. **Peer credentials** — the daemon checks `SO_PEERCRED` (`uid`/`pid`) of the
   connecting process and only serves the expected service user.

Tracked as a follow-up in the pull request body of #395 (the client is
unchanged; the check is daemon-side).

## ALPHA pin policy (CDK)

CDK is a self-declared ALPHA; its cadence is patches every ~2–3 weeks.

- **Pin an exact commit SHA**, not a tag or a branch; record it in the build
  recipe and in the measurement archive.
- **CI builds the pinned rev** for both arches (the recipe is reproducible;
  `-Z build-std` for mipsel).
- **Bump deliberately**: a bump is its own change, gated on re-running the
  parity suite and the behavioural suite against the new rev before it ships.
- **Never auto-update** on-device.

## Failure modes / debuggability

- Daemon crash → `procd` respawns; in-flight sagas are recovered on startup
  (`96018f7`), so a crash does not strand funds.
- Daemon unstartable → the module should **degrade to in-process gonuts** where
  that backend is compiled (small tier), or report a wallet-unavailable state.
- Observability: daemon logs to syslog with a `cdk-walletd:` prefix; the `info`
  handshake exposes version + capability manifest.

## Consequences

- The router build ships the sidecar client for all targets; only images with the
  daemon **package** installed actually use it.
- The daemon becomes a **separately packaged** binary (`cdk-walletd`) with its own
  `procd` init and a policy entry, not part of `tollgate-wrt`.
- The data-migration work assumes the sidecar form on the large tier.

## What would change this

- On-device `df` showing the small tier *can* fit the 7.5 MiB daemon → revisit.
- A build tag that drops the in-process gonuts link → "replace" would then save
  flash and the trade-off shifts.
- CDK leaving ALPHA / a stable release line → relax the pin policy.
