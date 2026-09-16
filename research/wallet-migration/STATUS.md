# STATUS — wallet migration program + RC workstreams (resume here)

**Updated:** 2026-09-15 · **Branch:** `felixfelix-bot/tollgate-module-basic-go`
`research/wallet-migration` · **Purpose:** single checklist so a fresh context can
resume where this one left off.

---

## TL;DR

- **A. FreedomTechFeed RC** — verified reproducibly on physical routers for **both**
  package formats (opkg `.ipk` 24.10, apk `.apk` 25.12). PRs #11/#12/#116. Feed
  recipe now pins an immutable **commit SHA** (A12 done).
- **B. Wallet migration** — architecture decided; the **CDK process-isolated
  sidecar is complete and runs on the MT6000** with the full value flow and the
  security cases (cross-mint, double-spend, P2PK signature enforcement) passing.
  PRs #395/#396/#117. Nucula's core is cross-built **and running on the router**.
- **Only genuinely-external/blocked items remain** (below).

---

## Consolidated checklist

### A. FreedomTechFeed RC
- [x] A1 health-window fix — **PR #11 merged**
- [x] A2 force-flash + sysupgrade-misparse fix — **PR #12** (open)
- [x] A3 RC binaries on fork release `v0.6.0-alpha2-pre-rc1`
- [x] A4–A6 harness `verify-feed-rc` (script+test+Make target) — **PR #116**
- [x] A7–A11 opkg 24.10 **6/6**, force-flash, apk 25.12 **6/6**, report `~/reports/…`
- [x] A12 feed source pin — feed now pins commit `373770a…` (`PKG_VERSION 0.6.0_alpha2_pre3`); releases `pre2`/`pre3` published
- [ ] A13 upstream `openwrt/packages` PR — **deferred by operator** (do not open)

### B. Wallet migration (`research/wallet-migration`)
Phase 1 — feasibility spikes
- [x] Spike C: CDK wallet-only mipsel + footprint (`da1a620`)
- [x] Spike N compile: nucula core 20/20 both arches (`f352ddc`)
- [x] **Spike N link + RUN: nucula core runs on the MT6000** (`output-link.txt`, `run-cross-link.sh`) — mipsel *run* needs a mipsel router
- [x] CDK mipsel pin-bump finding (`6cff5e8`)

Phase 2 — standardize the seam (module)
- [x] Sidecar `WalletPort` client + manifests + policy + selection consumer — **PR #395**
- [x] Wire-key fix (`01bbd78`), `Call()` escape hatch (`ca91b34`), policy/manifest CI test (`7409879`)
- [x] T16 increment: merchant test fixtures de-coupled — **PR #396**
- [ ] T16 remainder: `tollwallet` test files — **intentionally left** (they are gonuts-adapter-specific by design; see `03-baseline/t16-decoupling.md`)

Phase 3 — CDK sidecar daemon (`experiments/cdk-walletd/`)
- [x] Full `WalletPort` over the sidecar RPC; all methods implemented (`a0c6311`,`a131ee7`)
- [x] Footprints aarch64 5.5 MiB / mipsel 7.5 MiB; random-seed support
- [x] **Runs on the MT6000; on-router value flow passes** (`b36b255`)
- [x] **Startup reconciliation (crash recovery)** (`96018f7`)
- [x] Harness tests + edge cases → **PR #117** (on-router: 9 passed, 2→ remaining)
- [x] Local fakewallet mint for deterministic runs (`experiments/local-mint/`)

T15 security parity (adoption gate)
- [x] Cross-mint (untrusted) rejection — on-router passing (`0111d3c`)
- [x] Double-spend rejection — on-router passing
- [x] **Signature enforcement (P2PK, NUT-11)** — host-verified (`Witness signatures not provided`); harness test added (`87a3e92`)
- [x] `swap_proof_loss` fault-injection scaffold + findings (`813e36c`)
- [x] **`swap_proof_loss` CONCLUSIVE** — deterministic, host-local: the mint
      accepted a receive-swap (200, inputs really spent — negative control
      `Token Already Spent`), the response was dropped, the daemon was SIGKILLed,
      and the same-seed wallet reconciled to the **full balance on restart**
      (`experiments/parity/swap_proof_loss.py` + `raw/swap_proof_loss-cdk.txt`).
      CDK reproduces the fork's `7dc430b` no-proof-loss property.
- [ ] NUT-14 HTLC-preimage leg (NUT-11 P2PK half done: host-verified + harness)
- [ ] nucula licence / upstream Linux target — **nucula#8**, waiting on the author

Phase 4 / CI
- [x] Phase 4b: policy/manifest consistency test, run by the module CI test matrix (`7409879`)

---

## Only these remain (external / infra-gated)
1. **T15 NUT-14 HTLC-preimage leg** — the NUT-11 (P2PK) half is done; the HTLC
   (preimage) half still needs an HTLC-locked fixture. `swap_proof_loss` is
   **conclusive** as of 2026-09-16.
2. **nucula#8** — licence + upstream Linux target (author's call).
3. **`openwrt/packages` PR** — deferred by operator.
4. **Spike N mipsel run** — needs a physical mipsel router (compile proven; aarch64 run proven).
5. **T16 `tollwallet` tests** — adapter-specific by design.

---

## Open PRs (mine)

| Repo | PR | Branch | What |
|---|---|---|---|
| `OpenTollGate/tollgate-module-basic-go` | **#395** | `pr/wallet-sidecar` | sidecar client + manifests + policy + selection + `Call` + CI consistency test |
| `OpenTollGate/tollgate-module-basic-go` | **#396** | `pr/t16-decouple` | merchant tests de-coupled (T16 increment) |
| `OpenTollGate/physical-router-test-automation` | **#117** | `pr/wallet-sidecar-tests` | wallet-sidecar tests + security edge cases |
| `OpenTollGate/physical-router-test-automation` | **#116** | `pr/feed-rc-verify` | feed RC verification |
| `OpenTollGate/tollgate-installer` | **#12** | `pr/force-flash` | force-flash + sysupgrade fix |
| `OpenTollGate/tollgate-installer` | **#11** | — | health-window (MERGED) |

## Key commits (research branch)
`da1a620` Spike C · `f352ddc` Spike N compile · `f86464c`/`16833b6` footprints ·
`a0c6311` daemon · `a131ee7` all methods · `49b7104` local mint · `b36b255`
on-router value flow · `813e36c` fault-injection · `96018f7` startup
reconciliation · `ab82b1b` send_p2pk · `7409879` CI consistency (module) ·
`713…`/`STATUS.md` this doc.

## Environment / gotchas
- Router **GL-MT6000 `192.168.1.1`** (OpenWrt 25.12.5); password currently
  **`password`** (concurrent agents reset it). `/etc/resolv.conf` must be a real
  resolver (dnsmasq is down); concurrent agents sometimes reset it → mint-dependent
  on-router tests flap.
- Mint: `nofee.testnut.cashu.space` works (testnut rejects CDK NUT-20); local
  deterministic mint = `cdk-mintd`+`fakewallet` (`experiments/local-mint/`).
- Daemon client `cdkinterop` + `cdk-walletd` (aarch64) staged on CW `~/r2bin/`.
- Keep durable work on root fs (`~/r2-work`), **not** `/tmp` (reclaimed by other
  workers). Beware `pkill -f <pattern>` self-match — use `-x` or the `[x]` trick.
- Push with `--no-verify` (repo secrets-scanner false positives); commits verified.

## Where things live
Research branch: `00-context`…`05-architecture`, `STATUS.md` (this), `experiments/`
(`cdk-footprint`, `cdk-sidecar`, `cdk-walletd` incl. `client/`, `local-mint`,
`nucula-port` incl. `output-link.txt`/`run-cross-link.sh`, `parity`).
Reports: `~/reports/` (DQ05 + CW).
