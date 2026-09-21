# Wallet-backend measurement protocol

<!-- markdownlint-disable MD013 — long table lines are data, not prose -->

> **Status:** the procedure described here is a proposal, not a measurement.
> Every threshold below is a **PROPOSED** acceptance criterion with its basis
> stated. The maintainer may tune a threshold; the *procedure* is what must not
> drift, because a metric is only comparable to an earlier run if it was taken
> the same way.
>
> It is written so that a third party holding the module and the measurement
> harness can rebuild every number, and so that a candidate that fails a
> **blocker threshold** is disqualified by measurement rather than by opinion.
>
> The raw measurements, the harnesses and the per-candidate studies this
> protocol was developed against live in the wallet-migration archive:
> <https://github.com/felixfelix-bot/tollgate-wallet-migration-research>.

---

## 0. How to use this document

1. For every claim in a wallet-backend recommendation there must be a metric
   ID from §3 measured by the procedure in §4, with the harness used and the
   raw output recorded alongside it.
2. A metric with status **UNMEASURED** is written as `UNMEASURED` — never as a
   guess. Estimates are written `ESTIMATE` with the basis.
3. §5 (fault injection) is **mandatory and blocking**. A candidate that has not
   been put through INJ-1…INJ-8 cannot be recommended, however good its
   footprint numbers look.
4. §7 keeps the integration-architecture comparison deliberately verdict-free:
   it lists trade-offs and the measurements that would settle them.

**Anti-rule (what this protocol forbids):** quoting README claims, crate counts,
star counts, or GitHub metadata as a metric. Those may inform §7 questions but
are not measurements.

---

## 1. Reproducibility contract

Every measurement records **six** facts (the earlier five plus provenance), in a
committed `env.txt` next to the raw output:

| # | Fact | Exact source |
|---|---|---|
| 1 | Hardware / arch | `uname -a`; `cat /proc/cpuinfo` (model, CPU count, MHz); `cat /proc/device-tree/model` if present; for a router also the exact device name and the `board_name` from `/tmp/sysinfo/board_name` |
| 2 | OS image + version | `cat /etc/openwrt_release` and `cat /etc/openwrt_version` (on the build host: `uname -a`, distro `/etc/os-release`) |
| 3 | Build commands | the literal `run.sh` line(s) run, including env vars (`CGO_ENABLED`, `GOARCH`, `GOARM`, `GOMIPS`, `RUSTFLAGS`, `CC`, `TARGET_CC`) |
| 4 | Candidate revision | commit SHA of gonuts / CDK / nucula and the SHA of the adapter file under test; `sha256sum` of every produced binary |
| 5 | Raw output | unedited stdout/stderr + the machine-readable form (`.json`/`.csv`), committed |
| 6 | Provenance | who ran it, when (UTC), on which host (router serial / QEMU invocation / build host name), and whether it is **on-device** or **emulated** |

Rules:
- A number without all six is an **anecdote**.
- Anything run under QEMU must be labelled `emulated` in the artifact filename
  and in the report. Emulated numbers may never be reported as router numbers.
- Build-host (x86_64) numbers may only be used for build cost, dependency graph,
  binary size and reproducibility — never for RSS, threads or latency.
- Measurement scripts must print their own `env.txt`, take no absolute home
  paths, and be re-runnable without secrets.

---

## 2. Environment matrix and router constraints

### 2.1 Environments

| ID | Environment | Used for | Label |
|---|---|---|---|
| ENV-ROUTER-A64 | Target router, OpenWrt 25.12.x, `apk`, `aarch64_cortex-a53` (media SDK target, cf. `mediatek-filogic-25.12.0-rc4` in `scripts/build-sdk-package.sh`) | all runtime metrics, all fault injection | on-device |
| ENV-ROUTER-MIPSEL | Second router or the mipsel build target, `mipsel`/`mipsle` softfloat | cross-arch parity for size, threads, and at least INJ-5/INJ-7 | on-device |
| ENV-QEMU-<arch> | Same OpenWrt image, `qemu-system-{aarch64,mipsel}` | iteration; full fault-injection rehearsal | emulated |
| ENV-HOST-X86 | x86_64 Linux build host | build cost, dependency graph, reproducibility, binary size only | build-only |

### 2.2 Router constraints that shape every threshold

| Constraint | Value | How recorded | Consequence for measurement |
|---|---|---|---|
| OS + package manager | OpenWrt 25.12.x, `apk` (`PACKAGE_FORMAT=apk` in `scripts/build-sdk-package.sh`) | `/etc/openwrt_release` | install/upgrade path tests must use `apk`, not only `.ipk` |
| libc | **musl** | `readelf -d` / `ldd` on the produced binary | a glibc-only or `dlopen`-dependent dep is a hard blocker |
| RAM class | **64–128 MB** (ASSUMPTION; basis: the class the operator states for this product, and the fact that the router also runs hostapd/uhttpd/nftables/uci) | `free -m` captured in `env.txt` | RSS/PSS thresholds in §4 are expressed as a fraction of *this measured* total, not as absolutes |
| Arch | `aarch64` **and** `mipsel` | `uname -m` | mipsel softfloat means **no FPU assumptions**; any candidate that needs hardware FP or emits FP instructions for crypto/amount maths must be re-checked |
| CPU count | record, do not assume | `grep -c ^processor /proc/cpuinfo` | the shared-mutex contention metrics (C2/C3) only mean something alongside the core count. ASSUMPTION: at least one supported target is single-core-equivalent for the wallet's critical path; must be confirmed per device |
| Flash | typically 16–32 MB NOR/NAND with JFFS2/UBIFS overlay; erase-block-limited writes (ASSUMPTION: ~10⁵ erase cycles/block, ~64–128 KiB blocks — **verify against the actual device's flash datasheet before quoting a wear budget**) | `cat /proc/mtd`, `flash_erase --help` availability, `df -h /overlay` | makes S2 (bytes written per payment) a first-class metric, not a footnote |
| Upgrade | `sysupgrade` with `packaging/files/lib/upgrade/keep.d/tollgate` preserving `/etc/config/network`, `/etc/config/wireless`, `/etc/tollgate` | read that file | the wallet's data must live somewhere that keep.d covers, or migration/rollback is broken by design |
| Service model | `procd` init (`/etc/init.d/tollgate-wrt`), hotplug `95-tollgate-restart` | read the init script | restart-under-load and hotplug-during-payment are real scenarios; a sidecar needs its own procd supervision |
| Startup ordering | router boots before NTP and before WAN is up | `logread`, and `tollwallet.go`'s own TODO about the wallet DB not unlocking without a network connection | clock-skew injection (INJ-4) and unreachable-mint injection (INJ-1) are *normal* boot conditions, not exotic ones |

---

## 3. Metric catalogue (index)

Statuses: `TODO` (not measured), `DONE`, `N/A`. "Blocker?" = failing this
threshold disqualifies the candidate.

| ID | Metric | Env | Blocker? |
|---|---|---|---|
| F1 | Stripped binary size, per arch | HOST-X86 + cross | soft |
| F2 | Unstripped size + symbol/section breakdown | HOST-X86 | no |
| F3 | Packaged `.apk` size contribution | HOST-X86 | soft |
| F4 | Dynamic dependencies / libc linkage | ROUTER | **yes** |
| F5 | RSS at idle and at peak | ROUTER | **yes** |
| F6 | PSS (smaps_rollup) idle/peak | ROUTER | yes |
| F7 | Thread count idle / during ops | ROUTER | **yes** |
| F8 | Open FDs and sockets held | ROUTER | yes |
| F9 | Binary size growth per release (trend) | HOST-X86 | soft |
| F10 | Build time and CI minutes per arch | HOST-X86 | soft |
| S1 | Store format + bytes per stored token/quote | ROUTER | no |
| S2 | **Bytes written to flash per payment** | ROUTER | **yes** |
| S3 | Store growth per 1 000 payments; fsync/commit frequency | ROUTER | soft |
| S4 | Crash consistency of the store (see INJ-5/6/7) | ROUTER | **yes** |
| S5 | Erase-cycle wear budget projection (formula) | derived | **yes** |
| P1 | Startup time to wallet-ready | ROUTER | **yes** |
| P2 | mint-quote → mint completion latency p50/p95 | ROUTER | soft |
| P3 | melt/receive latency p50/p95 | ROUTER | soft |
| P4 | CPU seconds per payment | ROUTER | soft |
| P5 | Thermal / load behaviour under sustained payments | ROUTER | soft |
| P6 | Latency added by the integration seam (FFI vs socket) | ROUTER | soft |
| C1 | Two operations in flight: serialise, deadlock, or corrupt? | ROUTER | **yes** |
| C2 | Lock contention on the target CPU (lock held across I/O) | ROUTER + code read | **yes** |
| C3 | Behaviour when a quote-monitor goroutine and a user action collide | ROUTER | yes |
| E1 | Direct + transitive dependency count and each licence | HOST-X86 | **yes** |
| E2 | Language/runtime licence compatibility with GPL-3.0 | n/a | **yes** |
| E3 | Build reproducibility of the candidate itself | HOST-X86 | soft |
| E4 | Cross-compile effort for musl arches (patch count) | HOST-X86 | **yes** |
| E5 | CI integration cost (does it fit the existing matrix?) | HOST-X86 | **yes** |
| E6 | API churn / breaking changes over the last N releases | n/a | yes |
| E7 | Governance: maintainers, merge rights, release cadence | n/a | soft |
| E8 | Test story: fake-mint/regtest harness runnable in our CI | HOST-X86 | soft |
| E9 | Debuggability on device (logs, symbols, attach, repro) | ROUTER | soft |
| E10 | NUT coverage delta vs gonuts that changes user-visible behaviour | n/a | **yes** |
| E11 | Secrets at rest: seed/proof store encryption, permissions, backup | ROUTER | **yes** |
| E12 | Panic/abort behaviour at a language boundary (FFI) | ROUTER | **yes** |
| M1 | Migration of existing users' stored proofs and keys | ROUTER | **yes** |
| M2 | Rollback: new data read by the previous binary | ROUTER | **yes** |
| M3 | `sysupgrade`/`apk upgrade` interruption and data survival | ROUTER | **yes** |
| R1–R8 | Fault-injection suite (§5) | ROUTER/QEMU | **yes** |

---

## 4. Per-metric methodology

Each card: **procedure** (the exact thing to run), **blocker threshold**
(PROPOSED, with basis), **third-party reproduction** (what makes it re-runnable).

### Footprint

#### F1 — Stripped binary size, per arch
- **Procedure:** build the full router binary the way CI does
  (`GOOS=linux GOARCH=<arch> GOARM=… GOMIPS=… CGO_ENABLED=<as required> go build -trimpath -ldflags="-s -w" -o tollgate-wrt main.go`), then `ls -l`, `size tollgate-wrt`, and for each arch record the delta against the gonuts build of the same commit. Also record the standalone wallet process/daemon size if the architecture is a sidecar. **Check `tests/size/measure.sh` first** — an in-tree size-measurement script exists and should be extended rather than duplicated.
- **Blocker threshold (PROPOSED):** stripped delta vs the gonuts baseline > **+8 MB** per arch, or total binary exceeding the largest image that still fits the device's flash budget with the rest of the package installed. Basis: 16–32 MB flash class; the exact ceiling must come from the measured `df` on the target, not from this number.
- **Third-party reproduction:** the `run.sh` plus `env.txt` plus `sha256sum` of the output; the delta is expressed against a named gonuts SHA, so anyone can rebuild both sides.

#### F2 — Unstripped size + section/symbol breakdown
- **Procedure:** build without `-s -w`; `size -A`, `readelf -S`; for Rust, `cargo-bloat`/`llvm-size` on the staticlib; look for which crates dominate.
- **Blocker threshold:** none by itself. Signal only: if unstripped-stripped is enormous, symbol stripping/symbol files are load-bearing for on-device debugging (see E9).
- **Third-party reproduction:** same build script without the ldflags.

#### F3 — Packaged `.apk` contribution
- **Procedure:** build the `.apk` via `scripts/build-sdk-package.sh` (or `packaging/local-build-ipk.sh` for iteration), then compare package size and uncompressed installed size with/without the wallet change.
- **Blocker threshold (PROPOSED):** any increase that pushes the package past the device's remaining flash × 1.3 headroom (upgrade needs space to write the new package before removing the old).
- **Third-party reproduction:** SDK tag recorded in `env.txt`; script does not use home paths.

#### F4 — Dynamic dependencies / libc linkage
- **Procedure:** on the produced binary: `readelf -d tollgate-wrt | grep NEEDED`, `file tollgate-wrt`, and on-device `ldd` (musl). For a static Rust lib: confirm `ar t`/`nm` and that no `dlopen`-style runtime loading is required. Also confirm the binary is either fully static or needs only musl.
- **Blocker threshold (PROPOSED):** **any** NEEDED entry that is not `libc.so` (musl) / `ld-musl-*.so.1`, or any requirement for a glibc-only or `libgcc_s`-only-at-runtime dependency, or any runtime `dlopen` of a Rust artifact. Basis: OpenWrt does not ship glibc; there is no package-manager escape hatch for a missing soname on an embedded device.
- **Third-party reproduction:** `readelf -d` output committed verbatim.

#### F5 — RSS at idle and at peak
- **Procedure:** on the router, sample `/proc/$(pidof tollgate-wrt)/status` (`VmRSS`, `VmHWM`) every second from service start, and separately under a scripted burst: (a) idle 5 min after wallet-ready, (b) during mint quote, (c) during mint, (d) during swap/receive, (e) during melt. Record `VmHWM` (peak) as the headline. For a sidecar, sum both processes and state the split.
- **Blocker threshold (PROPOSED):** peak `VmHWM` > **32 MB** for the wallet-bearing process, or > **25 % of measured total RAM** (whichever is smaller) on a 64 MB device. Basis: the router must still run hostapd/uhttpd/nftables; a wallet that monopolises RAM causes OOM-kills of the *network*, i.e. the product's primary function. Confirm against measured `free -m`.
- **Third-party reproduction:** a `sample-rss.sh` that prints a timestamp/RSS CSV; the same script drives the fixed operation script; both committed.

#### F6 — PSS (proportional set size)
- **Procedure:** on-device `grep -E 'Pss|Private' /proc/PID/smaps_rollup` idle and at peak; compare with RSS. A large gap between RSS and PSS means shared pages (e.g. a shared tokio/sqlite-mapped file) and makes RSS look worse than reality.
- **Blocker threshold:** none independent of F5; used to *correct* F5 and to justify any claim that a candidate is lighter than it looks.
- **Third-party reproduction:** same script, `smaps_rollup` committed.

#### F7 — Thread count idle / during operations
- **Procedure:** `cat /proc/PID/status | grep Threads` and `ls /proc/PID/task | wc -l` at idle, and every 0.5 s during each operation. Explicitly record whether an async runtime (e.g. tokio worker threads) is spun up and how many workers it chooses on a 1–4 core device. Compare against the Go runtime's own threads.
- **Blocker threshold (PROPOSED):** > **32** threads at idle, or thread count that scales with CPU count in a way that multiplies memory per mint. Basis: 64–128 MB RAM and a router whose CPU is also doing forwarding; each thread costs stack + scheduler overhead. The absolute number should be sanity-checked against the measured core count.
- **Third-party reproduction:** the same sampler records threads alongside RSS, so both are time-aligned.

#### F8 — Open FDs and sockets held
- **Procedure:** `ls /proc/PID/fd | wc -l` idle and at peak; `ls -l /proc/PID/fd` to classify (sqlite files, socket to mint, listening sockets); check `ulimit -n` and whether the process bumps it.
- **Blocker threshold (PROPOSED):** idle FD count > **24**, or any leaked FD across a 1 000-operation loop (count must return to baseline). Basis: descriptor leaks are the classic embedded failure and are trivially detectable here.
- **Third-party reproduction:** FD sampler + the 1 000-operation loop script.

#### F9 — Binary size growth per release (trend)
- **Procedure:** for the last N (say 6) releases of the candidate, build the minimal wallet feature set at each tag and record size; fit a simple per-release delta.
- **Blocker threshold (PROPOSED):** a monotone growth that would exhaust the flash headroom measured in F3 within the device's expected service life (state the assumed life explicitly).
- **Third-party reproduction:** the tag list and build script committed; sizes in a CSV.

#### F10 — Build time and CI minutes per arch
- **Procedure:** on CI-equivalent hardware, time a cold build (no caches) and a warm build per arch; record added CI minutes against the current matrix (`arm64`, `armv7`, `mipsle-softfloat`, `mips-softfloat`, `amd64` in `.github/workflows/build-package.yml`).
- **Blocker threshold (PROPOSED):** cold cross-build > 20 min/arch, or a requirement for a cross toolchain image that CI cannot currently produce. Basis: CI cost is the recurring tax of the chosen architecture and is one of the reasons forks get abandoned.
- **Third-party reproduction:** `time` output, runner image, cache state.

### Storage

#### S1 — Store format, bytes per stored token/quote
- **Procedure:** snapshot the store file(s) before/after a single receive of a known token; `stat -c %s`; for sqlite, `sqlite3 <db> "select count(*) from …"` and `.schema`; record per-mint files (the in-tree adapter uses one `<sha256(mintURL)[:8]>.sqlite` per mint plus a plain-text mnemonic file) and any global `quotes.json`.
- **Blocker threshold:** none absolute; used to compute S2/S5 and to detect per-mint fan-out that multiplies baseline size.
- **Third-party reproduction:** store snapshots (or their checksums and sizes) committed with the operation log.

#### S2 — Bytes written to flash per payment  ← first-class metric
- **Procedure:** read the device's write counters before and after a scripted payment cycle: `cat /proc/diskstats` (field 7 = sectors written × 512) for the overlay's block device, and/or `/sys/block/*/stat`; on UBIFS also record `/sys/class/ubi/ubi*/…` counters. Take the delta across N ≥ 20 identical payment cycles, divide by N. Repeat with the store on a bounded `tmpfs` mount to separate *logical* bytes written by the wallet from *kernel-level* writeback amplification, and state both.
- **Blocker threshold (PROPOSED):** > **256 KiB written per payment** (logical, before amplification), or a payment that triggers more than one full-file rewrite of a store larger than 64 KiB. Basis: flash erase-block granularity means a single small append can cost a whole block erase; a wallet that rewrites its whole DB per payment converts a router into a consumable. Must be reconciled with the measured flash geometry the operator supplies.
- **Third-party reproduction:** the write-counter delta script plus the payment script, both committed, plus the `df`/`/proc/mtd` snapshot proving which filesystem the store lives on.

#### S3 — Store growth per 1 000 payments; fsync/commit frequency
- **Procedure:** run 1 000 scripted operations; record store size at 0/10/100/1 000 and the count of fsync/commit calls (strace-free on a router: infer from write counters and, where possible, an instrumented build; do not require ptrace on the device unless available). Check for unbounded growth of spent-proof history and for compaction/vacuum behaviour.
- **Blocker threshold (PROPOSED):** any unbounded linear growth with no compaction path, or a store that cannot be vacuumed without a full rewrite on device.
- **Third-party reproduction:** the loop script; the size CSV.

#### S4 — Crash consistency of the store
- **Procedure:** executed as part of INJ-5/INJ-6/INJ-7 (§5). Store-level check: `PRAGMA integrity_check` for sqlite (or the redb equivalent) after each injection, plus an independent reconciliation against the mint (NUT-07 checkstate for every proof the wallet believes it holds).
- **Blocker threshold:** **any** state where the wallet and the mint disagree about whether a proof is spent, with no automatic or scripted recovery path.
- **Third-party reproduction:** the fault-injection scripts plus the independent oracle script.

#### S5 — Erase-cycle wear budget projection
- **Procedure:** compute, from S2 and the measured store size:
  `total_writes_budget = erase_cycles × usable_flash_bytes`;
  `payments_budget = total_writes_budget / bytes_per_payment`;
  then express as payments/day at the expected traffic and as years.
- **Blocker threshold (PROPOSED):** projection below the device's expected service life at the planned payment rate. Print the arithmetic, the flash geometry used, and mark the geometry source (datasheet vs estimate).
- **Third-party reproduction:** the formula, inputs, and units are in the artifact; anyone can substitute a different flash geometry.

### Performance

#### P1 — Startup time to wallet-ready
- **Procedure:** instrument the service to log a single `wallet-ready` line (or time the CLI's first successful wallet call) and measure from `procd` start to that line, 10 runs, on-device, with (a) WAN up, (b) **WAN down**, (c) mint unreachable. Report p50/p95 and whether readiness is gated on network reachability.
- **Blocker threshold (PROPOSED):** wallet-ready > **10 s** with WAN up, or **any** case where the wallet is not ready with WAN down (except where the operation itself needs the mint). Basis: the existing code carries a TODO stating the wallet DB is not unlocked when the device boots without a network connection — that is precisely the failure this metric exists to catch.
- **Third-party reproduction:** the init script timing harness, committed; the boot log committed.

#### P2 — mint-quote → mint completion latency
- **Procedure:** against a real mint (record its URL and version) and against the fake mint, run ≥ 20 quote→pay→mint cycles; timestamp each phase with a monotonic clock inside a small Go harness (not `date` — busybox `%N` is not portable); report p50/p95/p99 per phase and total.
- **Blocker threshold (PROPOSED):** p95 total worse than **2×** the gonuts baseline on the same device and mint. Basis: this path is user-visible (waiting for access after paying); a 2× regression is the point where support tickets start.
- **Third-party reproduction:** harness + mint URL + mint version + baseline run in the same artifact.

#### P3 — melt/receive latency
- Same as P2 for `Receive` (swap) and `MeltToLightning`. Note: the in-tree CDK adapter currently returns "not yet wired" for `MeltToLightning`, so this metric is `N/A — unimplemented` until the adapter work closes; record it as such rather than as a pass.

#### P4 — CPU seconds per payment
- **Procedure:** delta of `utime+stime` (fields 14/15) in `/proc/PID/stat` across N payments; divide. Record on-device only.
- **Blocker threshold (PROPOSED):** > 1.0 CPU-second per payment on the target CPU, or any operation that visibly competes with packet forwarding (measure forwarding throughput during a payment burst as a sanity check).
- **Third-party reproduction:** the same script as S2; both deltas come from one run.

#### P5 — Thermal / load behaviour under sustained payments
- **Procedure:** 200 consecutive payments; sample `/sys/class/thermal/thermal_zone*/temp`, `/proc/loadavg`, and per-core `/proc/stat` idle delta every 5 s; also confirm forwarding/throughput is unaffected.
- **Blocker threshold (PROPOSED):** any thermal zone above the device's throttle point, load average sustained > core count, or measurable forwarding loss. Basis: a router's wallet must never be the reason the router stops routing.
- **Third-party reproduction:** sampler script + operation loop.

#### P6 — Latency added by the integration seam
- **Procedure:** measure the same logical operation through each architecture in §7: direct Go call (gonuts), cgo/FFI call, and a local-socket call to a sidecar; report the per-call overhead distribution. Use a no-op or cached operation to isolate the seam.
- **Blocker threshold (PROPOSED):** per-call overhead > 5 ms p95 for a non-network operation, or any overhead that scales superlinearly with the number of FDs/wallets.
- **Third-party reproduction:** the micro-benchmark harness, one per architecture, committed with the measurements.

### Concurrency

#### C1 — Two operations in flight: serialise, deadlock, or corrupt?
- **Procedure:** drive two concurrent operations from separate goroutines/processes against the same wallet (e.g. a receive and a mint completion, and two receives of different tokens) for 100 iterations; assert the final balance equals the sum of credits; assert no error is silently swallowed; assert no deadlock within a bounded timeout (Go: `go test -race -timeout 60s`; on-device: run the concurrency test binary).
- **Blocker threshold:** **any** deadlock, panic, race detector hit, lost credit, or double-credit. Hard blocker.
- **Third-party reproduction:** the concurrency test binary + its output; the race build command recorded.

#### C2 — Lock contention on the target CPU
- **Procedure:** code read **plus** measurement. Do the read first: for the in-tree adapter, note that `CdkWallet.mu` is held across FFI calls in `GetBalanceByMint`, `GetAllMintBalances` and `getOrCreateWallet`, and that all state is behind a single mutex. Then measure: run N concurrent balance reads while a mint operation is in flight and record p95 latency of the cheap reads, with the core count recorded.
- **Blocker threshold (PROPOSED):** a cheap local read (`GetBalance`) stalling for the duration of a remote operation (> 1 s p95), on any target. Basis: on a single-core-equivalent device this serialises the whole merchant path behind a network round-trip.
- **Third-party reproduction:** the micro-benchmark + `/proc/cpuinfo`.

#### C3 — Monitor goroutine vs user action
- **Procedure:** the merchant runs a per-quote monitor goroutine and periodic tickers (`merchant/lightning.go`, 2 s data-usage and 1 min mint-health tickers in `merchant.go`). Concurrently trigger a user-facing operation and assert neither starves nor corrupts: run the quote monitor against a slow/blackholed mint while a receive is performed; verify the receive completes and the monitor stops cleanly when its quote settles.
- **Blocker threshold:** starvation of the user path beyond the P2 threshold, or a monitor goroutine that outlives its quote and keeps polling.
- **Third-party reproduction:** scripted scenario with a controllable fake mint.

### Engineering

#### E1 — Direct + transitive dependency count and licences
- **Procedure:** `go list -m all` (and `go mod graph`) for the Go side; `cargo tree -e normal,no-dev` (and `cargo metadata`) for the Rust side; for each dependency record name, version, licence, and whether the licence is compatible with GPL-3.0. Count only the *actually linked* feature set, and state the feature flags used. Compare against the gonuts dependency graph of the same commit, noting that the router already pulls a large Lightning graph transitively.
- **Blocker threshold (PROPOSED):** any dependency with no licence grant, a non-GPL-3.0-compatible licence in a linked position, or a copyleft/attribution obligation that the product cannot satisfy; or a linked-dependency count so large that vendoring/reviewing is impractical.
- **Third-party reproduction:** the exact `cargo tree`/`go list -m all` output and feature flags committed verbatim.

#### E2 — Language/runtime licence compatibility with GPL-3.0
- **Procedure:** read the candidate's `LICENSE*` (and, where the repo reports `NOASSERTION`, the actual file text); compare with TollGate's `LICENSE`. Where a repository has **no licence file**, record that as *no grant of rights by default* and treat it as blocking until the author grants one in writing (store the grant in the archive).
- **Blocker threshold:** no licence, or a licence incompatible with GPL-3.0 distribution of a combined work. Hard blocker (there is no performance number that outranks it).
- **Third-party reproduction:** licence file hash + verbatim text quoted in the artifact; for nucula, the licence-absence check must be re-runnable against a named commit.

#### E3 — Build reproducibility of the candidate itself
- **Procedure:** build the same revision twice in clean environments (same toolchain, different paths/UIDs/timestamps); compare `sha256sum` of the wallet artifact. Then attempt to reproduce a *published* release binary of the candidate under the same toolchain; report byte-identity, `-trimpath`-only identity, or non-reproducible.
- **Blocker threshold (PROPOSED):** non-reproducible builds with no documented method, or a build that silently embeds absolute paths, host names, or timestamps in the shipped binary.
- **Third-party reproduction:** two clean builds + `sha256sum`; the container/image digests recorded.

#### E4 — Cross-compile effort for the musl arches (patch count)
- **Procedure:** with the OpenWrt SDK for the target (`SDK_TAG`, e.g. the `mediatek-filogic-25.12.0-rc4` example in-tree), attempt to produce a working wallet artifact for `aarch64` (musl) and `mipsel` (musl, softfloat). Record: toolchain triple, patches required, files patched, and whether the result runs on-device (not just links).
- **Blocker threshold (PROPOSED):** more than a small, reviewable patch set (state a number, e.g. > 3 patches touching third-party sources), or any arch where the artifact links but does not run (wrong FP ABI, wrong TLS model, missing syscalls).
- **Third-party reproduction:** the SDK tag, the patch files, and the on-device `uname`/smoke-test output.

#### E5 — CI integration cost: does it fit the existing matrix?
- **Procedure:** determine whether the required build can be produced **inside the current build matrix**. Evidence to cite: `.github/workflows/build-package.yml` sets `CGO_ENABLED: "0"` and builds `arm64`, `armv7`, `mipsle-softfloat`, `mips-softfloat`, `amd64`; `packaging/local-build-ipk.sh` also sets `CGO_ENABLED=0` for `aarch64_cortex-a53`. Therefore any cgo-based integration requires changing the CI contract (toolchain image, `CC` per arch, static Rust libs per arch), not just adding a file.
- **Blocker threshold (PROPOSED):** any solution that cannot be built by a single documented CI job for **all five** arches, or that makes one arch unbuildable.
- **Third-party reproduction:** the workflow file lines cited + a green CI run link + the per-arch artifact hashes.

#### E6 — API churn / breaking changes over the last N releases
- **Procedure:** take the last N (say 10) releases of the candidate; diff the public API surface (Rust: `cargo public-api` or `cargo doc` JSON; Go: exported symbols; CLI: `--help` diff); count breaking changes and classify (cosmetic, signature, semantics). Also count releases in the window to get cadence.
- **Blocker threshold (PROPOSED):** breaking change on the wallet API surface in more than half the releases, or any release window where our adapter would have needed a code change more than once a month. Basis: the entire motivation is to stop maintaining a divergent fork; churn that we cannot absorb is a fork by another name.
- **Third-party reproduction:** the tag list, the diff tooling, and the raw diff summaries.

#### E7 — Governance: maintainers, merge rights, release cadence
- **Procedure:** `git shortlog -sne` over the last 12 months; number of distinct authors touching the wallet crates; who has merge/release rights; release dates; issue/PR close latency; presence/absence of a funded parent org. Record facts with dates.
- **Blocker threshold:** single-maintainer project with no succession and no licence grant (see E2), or a cadence that has already stalled relative to the fork we are trying to escape.
- **Third-party reproduction:** the `shortlog`/API output committed with the query date.

#### E8 — Test story: fake-mint/regtest harness runnable in our CI
- **Procedure:** identify whether the candidate ships a mock/fake mint or a reproducible test environment (for CDK: `cdk-fake-wallet`/`cdk-integration-tests` are present in the workspace; for a CLI: whether it can point at an arbitrary mint URL). Prove it by running the candidate's own test suite **and** a routing test of ours against it, on the build host, and record coverage of the wallet-visible paths (mint quote, mint, swap, melt, restore, checkstate).
- **Blocker threshold (PROPOSED):** no runnable harness, or a suite that only runs against live public mints (non-hermetic CI).
- **Third-party reproduction:** the test command, the pinned fake-mint revision, and the pass/fail output.

#### E9 — Debuggability on device
- **Procedure:** demonstrate four things on the router: (1) log lines for each wallet transition with a level control, and where the logs go (`logread`/ring buffer) with a bound on log bytes written to flash; (2) a symbolised panic/crash report path; (3) the ability to reproduce a failure from a committed script; (4) the ability to inspect wallet state on-device (a `status`/`balance` command) without a full debug build. For a static Rust lib with cgo, check that a Rust panic is attributable to a code location in the log rather than an opaque abort.
- **Blocker threshold (PROPOSED):** no on-device state inspection, or crash logs that cannot distinguish "mint rejected" from "our code panicked". Basis: a wallet failure on a customer's router is unsupportable if the device cannot tell us which of those happened.
- **Third-party reproduction:** the `logread` capture and the reproduction script, committed.

#### E10 — NUT coverage delta (user-visible behaviour)
- **Procedure:** build a matrix: NUT number → supported by gonuts at the pinned revision → supported by the candidate → whether the merchant/lightning code path depends on it. Start from the observed adapter gaps: `MeltToLightning` returns *"not yet wired"*, `SendWithOverpayment` refuses non-zero overpayment caps, and untrusted-mint swap-to-trusted is not implemented (`mintAccepted` accepts untrusted tokens as-is, without swapping). Each row is a user-visible behaviour change or not; mark it.
- **Blocker threshold:** **any** gap on a path the product currently exercises (receiving a token from an untrusted mint, or melting to Lightning) counts as a blocker for the substitution *as-is*, or forces an explicit, written acceptance of the behaviour change.
- **Third-party reproduction:** the matrix with code citations (file:line) on both sides.

#### E11 — Secrets at rest
- **Procedure:** locate where key material and proofs live; measure `chmod`/`stat -c %a`; check whether the seed is encrypted at rest or plaintext; check whether the seed file is within the `keep.d/tollgate` preserved set (`/etc/tollgate`) so it survives `sysupgrade`; check what a factory reset / config wipe does to the seed and whether that means permanent loss of funds; check backup/restore instructions exist and are tested. Record the mnemonic's presence in any log or crash dump.
- **Blocker threshold (PROPOSED):** seed in plaintext **and** world/group-readable, or a seed whose loss is silent and undocumented, or a seed included in log output. Basis: proof loss is unrecoverable value loss for a customer.
- **Third-party reproduction:** `stat` output, `grep` of logs for the seed, and a restore test from a backup in a clean environment.

#### E12 — Panic/abort behaviour at a language boundary
- **Procedure:** force a failure across the integration seam: (a) invalid/malformed input into the FFI entry points, (b) a Rust-side panic path if reachable (e.g. unwrap on malformed data), (c) Go-side invalid use (double `Close()` / use-after-`Destroy()` on a token, concurrent `Destroy` with a call in flight). Observe: does the process abort (panic=abort kills the router service), unwind into Go, return a typed error, or corrupt memory? Confirm empirically — do not read the docs for this one.
- **Blocker threshold:** **any** input that aborts or double-frees the router process, or any use-after-free that the race detector or a repro can demonstrate. Hard blocker.
- **Third-party reproduction:** the reproducer, the crash output, and the build flags (`panic=abort` vs `unwind`) recorded.

### Migration

#### M1 — Migration of existing users' stored proofs and keys
- **Procedure:** take a real captured gonuts wallet directory (proof/quote store + any key material) from a production-like device, run the migration on a copy, and verify **independently**: (a) sum of migrated proofs equals sum of source proofs (amount by amount, proof by proof where possible); (b) every migrated proof is accepted by the mint (NUT-07 checkstate: expected `UNSPENT`, not `SPENT`); (c) quotes in `quotes.json` remain readable (the dual-format `MintQuoteState` unmarshaller exists for exactly this); (d) the wallet can spend the migrated balance end-to-end.
- **Blocker threshold:** any lost satoshi, any duplicate minting, any proof whose mint state contradicts the local record, or a migration that requires the user to re-enter a seed.
- **Third-party reproduction:** the captured (redacted) fixture + the migration script + the verification script, all committed.

#### M2 — Rollback: previous binary reads new data
- **Procedure:** after M1, replace the new binary with the previous release binary on the same device/data and confirm the old code still starts and can at least read its own view (or fails **cleanly and loudly** without touching data). Additionally verify the reverse direction is not silently destructive.
- **Blocker threshold:** the previous binary corrupting or deleting data on a downgrade, with no documented "one-way door" statement. If the migration is irreversible, that must be stated in the release notes and in `RECOMMENDATION.md` — an undocumented one-way upgrade is a blocker.
- **Third-party reproduction:** both binary hashes, the downgrade procedure, and the before/after store checksums.

#### M3 — `sysupgrade`/`apk upgrade` interruption and data survival
- **Procedure:** on a QEMU instance first, then on hardware: interrupt a `sysupgrade` and an `apk upgrade` mid-write (power off / `kill -9` of the package manager), then boot and verify the wallet store is either intact or cleanly recoverable. Confirm the data path is inside the `keep.d/tollgate` preserved set (`/etc/config/network`, `/etc/config/wireless`, `/etc/tollgate`) and that a wallet store living *outside* that set is documented as wipe-on-upgrade.
- **Blocker threshold:** a normal upgrade path that wipes the wallet store, or a half-upgrade that leaves the store unreadable with no recovery.
- **Third-party reproduction:** the interruption script + boot logs + store checksums.

---

## 5. Fault-injection scenario set — **mandatory and blocking**

These eight scenarios are the minimum evidence that a wallet is safe on a router.
They must be run on ENV-ROUTER (and rehearsed on ENV-QEMU). A candidate that has
not been through all eight is reported as `NOT FAULT-TESTED` and cannot be
recommended.

### 5.0 Harness requirements (build these before any injection)

| Harness | What it is | Why |
|---|---|---|
| `mock-mint` | a controllable HTTP server that speaks enough Cashu for the wallet path and can be switched between modes: `ok`, `http-500`, `malformed-json`, `truncated`, `slow` (no response), `blackhole` (drop), `bad-state` (NUT-07 answers disagree with local records) | makes INJ-1/2/3/9 hermetic and re-runnable. **Start from what exists in-tree** rather than building from scratch: `tests/cloud-lab/` already runs a mint container (`Dockerfile.mint`, `docker-compose.yml`) with failure scenarios (`test_mint_failure.py`) and a payment smoke test (`test_smoke_payment.py`); CDK's workspace also ships `cdk-fake-wallet`/`cdk-integration-tests`. Whichever harness is chosen must be extendable to the modes above — an existing mint container is not yet a *fault-injecting* mint. |
| `oracle` | an **independent** implementation that can ask the mint about a proof (NUT-07 checkstate) and restore from the seed (NUT-09) — e.g. a `cdk-cli`/`cashu-ts` script kept out of the wallet's process | the whole point: never let the wallet be the judge of its own correctness |
| `net-cut` | scripted `nftables`/`iptables` rules to `REJECT`, `DROP`, or `DELAY` traffic to the mint, and to block NTP (udp/123) | INJ-1 and INJ-4 |
| `state-dump` | prints store size, store checksum, per-mint balances, and the proof set on demand | before/after comparison for every injection |
| `pay-loop` | scripted payment cycle (quote → pay against the fake mint → mint → swap), N iterations, with timestamps | the standard workload that injections interrupt |

Every injection run records: the mode, the exact interruption point, `state-dump`
before and after, the oracle verdict, the log, and `env.txt`.

**Target existence (for a router):** whatever the wallet is inside —
`tollgate-wrt`, or a separate wallet daemon — must be defined per architecture in
§7, because "kill -9 the wallet" means different PIDs in the cgo and sidecar
designs.

### 5.1 Mandatory scenarios

| ID | Scenario | Correct behaviour | Violation detector |
|---|---|---|---|
| INJ-1 | **Mint unreachable** (DNS fails / connection refused / blackholed) | operation returns a typed error inside the configured timeout; **no** proof is deleted or marked spent locally; stored proofs are byte-identical (checksum) to before; retry after the mint returns succeeds; the router keeps forwarding and serving existing sessions (degraded mode, cf. `merchant_degraded.go`) | store checksum changed; balance decreased without a mint-side confirmation; process crash/crash-loop; hang beyond timeout; forwarding loss |
| INJ-2 | **Mint returns HTTP 500** | 5xx treated as a retryable failure, **not** as "spent"/"failed" terminal; error is typed and distinguishable from 4xx; no partial write; bounded retry with backoff and a ceiling; no log spam that fills flash | state changed as if the operation succeeded; unbounded retry loop; 5xx collapsed into `ErrTokenAlreadySpent` or a success; log growth without a bound |
| INJ-3 | **Mint returns malformed JSON** (truncated body, wrong types, wrong NUT version, unexpected `state` value) | typed parse error, operation aborted before any state change; **no panic/abort** across the integration seam; no partial garbage persisted; no full payload or key material logged | panic or process abort; store written with garbage; log containing the raw secret-bearing payload; the error being indistinguishable from a network error |
| INJ-4 | **Clock skew / wrong timezone** (RTC at epoch, NTP blocked, TZ flipped, clock jumped forward past a quote expiry) | quote expiry comes from the mint's own expiry field and is stored in UTC; a paid quote is never permanently lost to a local-clock judgement; melt state follows the mint, not the local clock; bad clock produces a clear TLS/clock error rather than corrupt state; changing `TZ` does not shift stored timestamps | a paid quote marked expired and unrecoverable; a melt marked paid when the mint says unpaid (or vice versa); stored timestamps shifting with `TZ`; silent failure with no log |
| INJ-5 | **Power loss / `kill -9` mid-swap** | on restart, the store is either wholly pre-swap or wholly post-swap; a proof spent at the mint but absent locally is recoverable via NUT-09 restore from the seed; no double-spend attempt; store passes its own integrity check; service starts in bounded time | unreadable/corrupt store; crash-loop on boot; balance that disagrees with the mint and cannot be reconciled; duplicate proofs offered to the mint; startup blocked on the mint being reachable |
| INJ-6 | **Disk full / read-only flash** (`ENOSPC` on write; overlay remounted `ro`) | the failing write returns an error to the caller; **existing** proofs are not truncated or lost; the operation does not report success; no corruption; error is visible in logs and to the user; process stays running and the rest of the router is unaffected | truncation of the existing store; success reported on a failed write; DB corruption; wallet crash that takes the router service down |
| INJ-7 | **Corrupted proof store** (byte flip inside the DB, truncated tail, hostile/invalid file, one corrupt per-mint DB among several) | corruption is **detected**, and the wallet fails closed (refuses to operate / recovers from seed) rather than silently reporting a wrong balance; one corrupt mint DB does not take down the others; no panic; a documented recovery path exists | silently wrong balance reported as valid; crash/abort; total loss of the non-corrupt mints; no detection at all (best-effort read that ignores the damage) |
| INJ-8 | **Interrupted upgrade** (`sysupgrade`/`apk upgrade` killed mid-write; then boot) | old or new code starts, store is intact or cleanly recoverable, and the previously documented rollback (M2) still holds; data inside `/etc/tollgate` survives per `keep.d/tollgate`; any schema change is either backward-readable or documented as one-way | store wiped; a version combination that cannot start; silent schema change that corrupts data for the old binary; data outside the preserved set lost without warning |

### 5.2 The cross-cutting oracle: reconciliation

Every injection ends with the same three checks, and these three checks are what
turn "it looked fine" into evidence:

1. **Local consistency:** store integrity check passes; the sum of per-mint
   balances equals the store's own ledger.
2. **Mint agreement:** for every proof the wallet holds, ask the mint (NUT-07 via
   the independent oracle) — every held proof must be `UNSPENT`; every proof the
   wallet wrote off as spent must be `SPENT`.
3. **Recoverability:** in a scratch environment, restore from the seed (NUT-09)
   and assert the restored balance covers the wallet's claimed balance. If the
   candidate has no restore path, that is itself a finding (record it as such —
   it is the only defence against INJ-5/INJ-7).

Any disagreement that no scripted procedure can resolve is a **blocker**.

### 5.3 Additional injections (recommended, not part of the mandatory eight)

- **Read-only/`ENOSPC` on the *log* path** (does log writing break payments?).
- **Two wallets, one store** (the router service started twice by mistake via
  hotplug `95-tollgate-restart`; does locking prevent double-spend?).
- **Restart storm** (10 service restarts in 60 s with quotes outstanding;
  are monitors re-launched exactly once — cf. the restore path in
  `merchant/lightning.go`?).
- **Seam-specific:** for cgo, a Rust panic forced on the FFI boundary (E12); for
  a sidecar, the daemon killed while the Go process has an in-flight request,
  and the daemon returning a partial line on the socket.
- **Clock skew during TLS handshake** (clock at 1970 → certificate "not yet
  valid"); must be a clear error, not a state change.

---

## 6. Recording template for baseline runs

One file per candidate per environment, at minimum containing:

```yaml
candidate: gonuts-tollgate            # or cdk / cdk-go / nucula
revision: <sha>
adapter_file: src/tollwallet/gonuts_wallet.go
env: ENV-ROUTER-A64
date_utc: 2026-09-13T12:00:00Z
metrics:
  F1: {value: "", unit: bytes, status: UNMEASURED}
  F5: {idle_rss: "", peak_rss: "", unit: KiB, status: UNMEASURED}
  S2: {bytes_per_payment: "", method: diskstats-delta, status: UNMEASURED}
faults:
  INJ-1: {mode: reject, result: "", oracle: {local: "", mint: "", recover: ""}}
artifacts: [env.txt, raw/…]
notes: ""
```

`status` is one of `MEASURED`, `UNMEASURED`, `ESTIMATE`, `N/A`.

---

## 7. Integration-architecture comparison (no verdict)

> **No verdict.** This section exists to make the trade-offs comparable, and to
> name the questions that measurement must close before a winner can be chosen.
> Every claim below about the current tree is cited; everything else is
> ASSUMPTION with its basis.

### 7.1 The options

- **(a) cgo + static Rust lib via `cdk-go`/`cdk-ffi`** — the in-tree path:
  `src/tollwallet/cdk_wallet.go` (`//go:build cdk_wallet`) imports
  `github.com/cashubtc/cdk-go/bindings/cdkffi` and holds `*cdk_ffi.Wallet`
  handles behind a mutex. `cdk-go v0.17.3` is a real, published module
  (present in `src/go.mod` and `src/tollwallet/go.mod`).
- **(b) Sidecar process (`cdk-cli` or a small Rust daemon) over a local socket** —
  the Go router keeps a `WalletPort` implementation that speaks a stable
  line/JSON protocol over a unix socket to a supervised Rust process.
- **(c) Stay on gonuts but un-fork** — stop maintaining
  `github.com/OpenTollGate/gonuts-tollgate`; either land the fork's delta
  upstream and track `elnosh/gonuts`, or vendor a pinned upstream release and
  carry the delta as a small patch set.

### 7.2 Trade-off table

| Dimension | (a) cgo + static Rust lib | (b) Sidecar process | (c) Un-fork gonuts |
|---|---|---|---|
| **Build complexity** | Highest: needs a per-arch musl cross toolchain for the Rust staticlib **and** a cgo-capable Go cross build; the two toolchains must agree on the target ABI. Note the current CI sets `CGO_ENABLED: "0"` (`.github/workflows/build-package.yml`) and `packaging/local-build-ipk.sh` likewise — so (a) changes the build contract, not just a file. | Medium: Rust cross-compile produces a self-contained binary (harder: it must be a *working* musl static binary for each arch); Go side stays `CGO_ENABLED=0` and unchanged. Two artifacts to package, sign, and supervise. | Lowest: it is the build we already have; no new toolchain. Risk moves to patch maintenance against upstream. |
| **CI cost** | 5 arches × 2 toolchains; a cgo cache that rarely hits; the highest chance of one arch silently breaking. Cross-compiled FFI is the classic "links but does not run" case → E4 must include on-device smoke tests per arch. | 5 arches for one extra binary; build can be independent of the Go matrix (a Rust job producing artifacts that the Go job downloads), so a Rust failure does not block Go builds. | None added; CI stays as-is. |
| **Debuggability on device** | Worst on paper: a Rust panic crossing an FFI boundary is opaque unless explicitly mapped; the adapter already converts `FfiErrorCdk` codes to Go errors, but a *panic* is a different thing (E12). Core dumps/backtraces on a router are hard. | Best: the daemon is a separate process with its own log, its own restart, and can be tested with `cdk-cli`-style commands by hand; a crash takes down the wallet, not the router daemon. But you now have two logs to correlate and a protocol to debug. | Best of all: it is Go, in one process, with the existing logs and the existing tests — no seam to debug. |
| **Failure modes** | In-process panic/abort can kill `tollgate-wrt` (routing down); memory-safety bugs in the seam become use-after-free (the adapter's `Token.Close()`/`Destroy()` lifecycle and the `GetBalanceByMint` comment about a concurrent `Shutdown` show the authors already worried about exactly this); a single mutex serialises local reads behind remote calls (C2). | Failure is contained: the daemon dies, the Go side sees a socket error; needs a supervisor (procd) plus a defined "wallet down" degraded mode — which the product already has a shape for (`merchant_degraded.go`). New failure modes: socket deadlock, partial writes, protocol version skew between the two artifacts. | Failure modes are the ones we already know and have tests for. Residual risk: upstream drift we have not rebased, and the fork delta that motivated the fork in the first place (offline receive behaviour — the `tollwallet.go` TODO says the fork exists for "the offline functionality we need"). |
| **Upgrade path** | One package, one version: the Go binary and the staticlib ship together, so no version skew is possible. Downgrade = one artifact. | Two artifacts can skew (old Go + new daemon). Needs a protocol version handshake and a rule that they ship together; upgrade ordering matters on `apk upgrade`/`sysupgrade`. | One package, one version; the lowest-skew option, and `keep.d/tollgate` already preserves the data path. |
| **Runtime resource cost** | One process; no IPC; but PPID/FD/thread accounting must attribute Rust threads (a tokio runtime may be created inside the lib — F7 exists for this) inside the Go process's totals. | Two processes: +RSS for the daemon, +1 socket pair, +supervision. But the wallet can be resource-capped independently (`procd` limits, nice/ionice), and the seam is measurable (P6). | One process, the known baseline; the least new RAM. |
| **Implication for the in-tree `cdk_wallet.go`** | Mostly reused **if** the FFI surface is stable and cross-compiles: the file already maps `WalletPort`, holds the mnemonic file (0600), and has unit tests. The gaps are functional, not structural: `MeltToLightning` is "not yet wired", `SendWithOverpayment` refuses non-zero caps, untrusted-mint swap-to-trusted is not implemented, and it uses one sqlite file per mint. | The protocol layer is *new work*; the existing adapter becomes a reference for the semantics (error mapping, dual-format state, mnemonic handling) rather than the implementation. Its `WalletPort` shape is exactly what a socket-backed adapter would implement, so the seam survives. | `gonuts_wallet.go` stays; the work moves entirely to upstream/patches. The `cdk_wallet.go` file becomes dead code behind the build tag (a maintenance decision to record explicitly, not a silent one). |

### 7.3 Questions that must be answered by measurement before a winner

1. **E5/F10:** can the existing CI matrix (`CGO_ENABLED=0`; `arm64`, `armv7`,
   `mipsle-softfloat`, `mips-softfloat`, `amd64`) produce the chosen integration
   for **all** arches, in one documented job? (For (a), the honest answer requires
   actually changing the workflow and getting a green run.)
2. **E4:** does the cgo static-library path produce a binary that *runs* on
   `aarch64` (musl) and `mipsel` (musl, softfloat), not merely links? Count the
   patches.
3. **F7/F5:** does the wallet pull in an async runtime (tokio-style), and how
   many threads and how much RSS does it add on the target? This decides whether
   "cgo is free at runtime" is true.
4. **E12:** what happens on a Rust panic or a malformed input crossing the seam —
   error, unwind into Go, or process abort? Measured, not read from docs.
5. **C2:** is a single mutex held across remote/FFI calls long enough to stall
   local balance reads on the target CPU?
6. **S2/S5:** bytes written to flash per payment for the sqlite-per-mint design
   vs gonuts's current store, and the resulting wear budget on the actual device.
7. **E10/M1:** what is the user-visible behaviour delta (melt wiring, overpayment
   caps, untrusted-mint swap) and can existing proofs be migrated and rolled back?
8. **P1:** does the candidate make wallet-ready independent of mint reachability
   — the specific failure the current fork was created to work around?
9. **E6/E7:** what is the candidate's real churn and governance track record over
   the last year (the "will we fork again" test)? Note the tension: CDK is
   self-described **ALPHA** ("the api will change"), so the answer may be *worse*
   than the fork we are leaving — which is the strongest argument for (c).
10. **Cost of being wrong in either direction:** quantify the asymmetry. A wrong
    wallet loses customer funds (irreversible); a wrong architecture costs
    engineering time (recoverable). This asymmetry belongs in the recommendation
    as a stated weighting, not hidden inside the metric list.

---

## 8. Assumptions register

| # | Assumption | Basis |
|---|---|---|
| A1 | RAM class is 64–128 MB and the device also runs hostapd/uhttpd/nftables | Operator's statement of the product class; to be replaced by a measured `free -m` from the target |
| A2 | Flash is 16–32 MB with ~10⁵ erase cycles per ~64–128 KiB block | Typical of the device class; **must be replaced by the actual flash datasheet / `/proc/mtd` + `df`** before any wear-budget number is quoted |
| A3 | At least one supported target behaves as single-core-equivalent for the wallet's critical path | The `mipsel` softfloat class is typically low-core-count; to be confirmed per device via `/proc/cpuinfo` |
| A4 | All numeric blocker thresholds in §4 are proposals, calibrated to the device class, not measurements | No baseline run is recorded for this module yet |
| A5 | The cgo path cannot be built by the current CI as written | Direct evidence: `CGO_ENABLED: "0"` in `.github/workflows/build-package.yml` and `packaging/local-build-ipk.sh` |
| A6 | A sidecar is supervisable via `procd` and can be resource-capped | Standard OpenWrt capability; not yet demonstrated in-tree |
| A7 | The gonuts fork exists primarily for offline-receive behaviour | The `tollwallet.go` TODO: *"because of our hacky fork of gonuts for the offline functionality we need"* |

---

## Rule (unchanged, restated)

A metric that cannot be reproduced from the measurement harness does not go in the report.
Anything estimated rather than measured is labelled **ESTIMATE** with its basis.
Anything not yet determined is labelled **UNMEASURED** or **OPEN** — never guessed.

---

## Annex A — two findings from the current-wallet (RC) audit

Both were established while auditing the wallet that ships today. They are
recorded here because they change what a replacement backend has to preserve,
and because one of them silently inflates any measurement taken with the
documented command.

### A1. The seed and the proofs live in the same database file (E11)

On a running device, `wallet.db` is mode `0600` and lives in `/etc/tollgate`,
which `packaging/files/lib/upgrade/keep.d/tollgate` preserves across
`sysupgrade`. The **mnemonic is stored inside that same database** (a `seed`
bucket, ≈141 B of values) — there is no separate, differently-protected key
file. Consequences a replacement must not regress:

- any read of the proof store is a read of the wallet seed;
- corruption of the store is corruption of the seed, so NUT-09 restore from a
  written-down seed is the only recovery path (see INJ-5/INJ-7 and S4);
- a config wipe or factory reset is a silent loss of funds unless the seed has
  been written down;
- whether the mnemonic can appear in logs or a crash dump is **UNMEASURED** —
  measure it (E11's procedure), do not assume.

### A2. The documented CDK verify command is a false green (E4/E5)

The command — as documented in the wallet-migration archive's
candidate study [`01-candidates/cdk.md`](https://github.com/felixfelix-bot/tollgate-wallet-migration-research/blob/main/01-candidates/cdk.md)
(and nowhere on `main`; nothing in this repository teaches it):

```
$ cd src && GOFLAGS=-buildvcs=false go build -tags cdk_wallet ./...
(no output)  EXIT: 0
```

**does not compile the adapter.** `src/tollwallet/` contains its own `go.mod`,
so it is a nested module excluded from `src`'s `./...`; the exit status is green
and nothing is built. The correct invocation runs *inside* the module:

```
$ cd src/tollwallet
$ GOFLAGS=-buildvcs=false go build -tags cdk_wallet ./...
$ GOFLAGS=-buildvcs=false go vet   -tags cdk_wallet ./...
```

Anyone — a reviewer or a CI job — who validates the CDK path with the first
command gets a green tick for work that was never done. Any measurement that
exercises the cgo/CDK path must therefore record the exact build command that
produced the binary (reproducibility contract, §1 fact 3).
