# Research: replacing `gonuts` as TollGate's Cashu wallet

**Status:** in progress · **Branch:** `research/wallet-migration` · **Started:** 2026-09-13

## Why

The router's Cashu wallet is `github.com/OpenTollGate/gonuts-tollgate` — a **fork** of
`elnosh/gonuts` pinned as a Go module dependency. Maintaining a fork of an
unmaintained Cashu library is a long-term liability: upstream fixes and NUT
updates have to be re-based by hand, and the fork is the only thing standing
between the router and security-relevant upstream drift. After the current
release we want a wallet maintained by a Cashu developer upstream.

## Candidates

| Candidate | What it is | Language | Upstream |
|---|---|---|---|
| **nucula** | Cashu ecash wallet **firmware for ESP32-C3** with NFC tap-to-pay (PN7160), OLED (SSD1309), keypad; ESP-IDF v5.x | **C++** | `zeugmaster/nucula` (personal project, 6 stars, no license file) |
| **CDK** | Cashu Development Kit — "collection of rust crates for Cashu **wallets and mints**"; includes `cashu`, `cdk-cli`, `cdk-ffi`, `cdk-sqlite`, `cdk-redb`, `cdk-http-client`, `cdk-mintd`, `cdk-axum`, … | **Rust** | `cashubtc/cdk` (230 stars, actively developed, self-described ALPHA) |

## The finding that shapes everything below

**Neither candidate is a drop-in router wallet, and the reasons differ.**

- The router module is **Go**; the current wallet is a Go library behind a
  `WalletPort` interface (`src/tollwallet/`). CDK is Rust, so using it from the
  router means choosing an integration architecture (`cdk-ffi` + cgo, or a
  sidecar process) rather than swapping a dependency.
- **nucula is not a router component at all.** It is ESP32-C3 firmware — an
  end-user device wallet, not a Linux/OpenWrt service. It cannot "run on the
  router" in its current form, and it carries **no license file**, which means
  no grant of rights by default. Both facts need an explicit decision by the
  maintainer before research effort is spent treating it as a substitution.

So the honest research question is not "which wallet is faster on the router"
but: *what is the smallest, most maintainable way to stop owning a gonuts fork,
given that one candidate is Go-incompatible Rust and the other is not a router
wallet at all?*

## Layout

- `00-context/` — how the module consumes gonuts today, and the coupling surface
- `01-candidates/` — per-candidate capability, licence, portability, footprint
- `02-method/` — measurement protocol and the metric set (incl. overlooked metrics)
- `03-baseline/` — measured baseline for gonuts and for each candidate
- `04-reports/` — the recommendation, with dissenting consultant opinions kept
- `experiments/` — scripts and harnesses; every number in `03-baseline/` must be reproducible from here
- `TASKS.md` — the scheduled research tasks and who owns them

## Reproducibility rule for this branch

Every measurement records: hardware/arch, OS image + version, build commands,
exact revision of each candidate (commit SHA), and the raw output. A number
without those five things is treated as an anecdote, not a finding.
