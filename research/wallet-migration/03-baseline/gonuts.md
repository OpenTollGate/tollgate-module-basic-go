# Baseline: `gonuts` — measured on a real router

```yaml
candidate: gonuts-tollgate
revision: d0cb92f21261ff57d39bcb28e86ce2bb15fdc4ad   # v0.6.0-alpha2-gd0cb92f
adapter_file: src/tollwallet/gonuts_wallet.go
env: ENV-ROUTER-A64
date_utc: 2026-09-19
provenance: on-device (no emulated numbers in this file)
experiment: experiments/baseline-gonuts/   # scripts + raw output
```

This is **T1a** from `../TASKS.md`. It replaces the anecdote that
`04-reports/RECOMMENDATION.md` currently rests on. The procedure is
`../02-method/measurement-protocol.md`; the scripts that produced every number
here are in `../experiments/baseline-gonuts/`, and the raw output they emitted
is committed next to them. A number with no script is marked `UNMEASURED` — no
value in this file is a guess.

**Superseding the 2026-09-14 first pass.** An earlier 52-line version of this
file (commit `d12b51d2`, kept in history) sketched the service footprint from a
handful of `ssh` one-liners. Two of its numbers do not survive measurement and
one is a mislabel: it reported `wallet.db` as **128 KB sqlite** (it is a
**262,144 B bbolt** file — wrong size and wrong medium, §3.2), **8 threads**
(9 at idle over 297 samples, §3.3), and service RSS 26,764 kB (consistent with
the 26,848 KiB measured here). Its CDK-sidecar size table compares a whole
service against a single unconfigured CLI invocation, which its own text says
must not be read as a like-for-like ratio; that comparison belongs to T5c, not
here. The CLI's size from that pass (7,146,944 B) is re-measured above as
**7,146,976 B** from the running device.

**How to re-run:** `../experiments/baseline-gonuts/README.md`.

---

## 1. Environment facts (the reproducibility contract)

Full detail: `experiments/baseline-gonuts/raw/env.txt`.

| Fact | Value |
|---|---|
| Device / `board_name` | GL.iNet GL-MT6000 — `glinet,gl-mt6000`, `/proc/device-tree/model` = `GL.iNet GL-MT6000` |
| SoC / CPU | MediaTek Filogic (CPU part `0xd03` = Cortex-A53), **4 cores** |
| OS image | OpenWrt **25.12.5** `r33051-f5dae5ece4`, target `mediatek/filogic`, arch `aarch64_cortex-a53` |
| Kernel | `6.12.94` (SMP) |
| Package manager | `apk` (no `opkg` present) |
| RAM | **1,010,032 kB** total, **0 swap** (`free -m`) |
| Flash | eMMC `mmcblk0` 7,634,944 KiB; `/proc/mtd` **empty** — this is **not** a NOR/NAND‑MTD device |
| Overlay | `/dev/loop0` → **F2FS**, 7.2 G, on `mmcblk0p7`; mounted as `overlayfs:/overlay /` |
| Installed package | `tollgate-wrt-0.6.0_alpha2_pre7-r1 aarch64_cortex-a53 {/feed/net/tollgate-wrt}` (GPL-3.0-only) |
| Deployed binary | `/usr/bin/tollgate-wrt`, **12,085,824 B**, sha256 `0d567fb5fee9e45f6c7dcb3b6831429b2be6e2b4c38146010e1c2ffae1f96b8c` |
| CLI binary | `/usr/bin/tollgate`, **7,146,976 B**, sha256 `caa4a37579a18f5c2f6ed6119dfb8c3c47847c62109e6f3ab32589ac1006e69b` (the customer/operator surface used for every latency number below) |
| Binary provenance | Go build info: **go1.27.1**; `vcs` = `d0cb92f2…`; CLI reports `v0.6.0-alpha2-gd0cb92f`, build_time `1789661957` |
| Wallet library under test | `github.com/OpenTollGate/gonuts-tollgate` **v0.11.2** (from the binary's embedded module list) |
| Build host | Ubuntu 26.04 LTS, x86_64, `go1.26.0` — used for size/build/dependency facts only |
| Clock ticks per second | `USER_HZ` **99** — **calibrated on the device** (`lib/hzprobe.sh`: 998 ticks / 10.00 s; a second sample gave 100), not assumed. `/proc/PID/schedstat` and `/proc/config.gz` do not exist on this image. |
| Provenance | measured 2026-09-18T22:04Z–22:40Z and 2026-09-19T00:16Z–00:38Z UTC by `worker-tollgate`, on the physical router (no QEMU anywhere in this file) |

**Two protocol assumptions do not hold on this device, and both matter:**

* **A1 (RAM 64–128 MB)** — measured total is **1,010 MB**, ~8–16× the assumption.
  RSS thresholds must therefore be judged against the measured total, and the
  "wallet starves hostapd" argument that A1 supports does not arise here.
* **A2 (16–32 MB NOR, ~10⁵ erase cycles/block)** — measured storage is a
  **7.6 GB eMMC** and the overlay is **F2FS on a loop device**, not JFFS2/UBIFS
  on NOR. S5's wear arithmetic therefore has a completely different input, and
  the *erase-block* framing (a small append costs a whole block erase)
  describes the wrong medium. This does not make S2 unimportant — it changes
  who it is important **for** (§3.2, §5).

---

## 2. Metric status summary

| ID | Metric | Status | Headline value (this device) |
|---|---|---|---|
| F1 | Stripped binary size | `MEASURED` | deployed 12,085,824 B; documented-build 11,796,642 B |
| F2 | Unstripped size + sections | `MEASURED` | 16,673,953 B unstripped → stripping removes 4.88 MB (29.3 %) |
| F3 | Packaged `.apk` contribution | `UNMEASURED` | needs the OpenWrt SDK (E5/T5c); binary share measured (12,085,824 B) |
| F4 | Dynamic deps / libc linkage | `MEASURED` | deployed: **dynamically linked**, `NEEDED libgcc_s.so.1 + libc.so`. Documented build: **static** |
| F5 | RSS idle / peak | `MEASURED` | idle VmRSS 26,848 KiB (max 27,224); VmHWM 27,780 KiB; peak under payments 27,580 KiB (run 1) / 27,424 KiB peak, 27,740 KiB VmHWM (run 3) |
| F6 | PSS | `UNMEASURED` | **impossible on this image**: no `/proc/PID/smaps{,_rollup}` (kernel without `CONFIG_PROC_PAGE_MONITOR`) |
| F7 | Threads idle / during ops | `MEASURED` | **9** at idle (constant over 297 samples); **8** during payments (run 1) and **8→9** (run 3) — no growth under operations |
| F8 | Open FDs / sockets | `MEASURED` | idle 10 (max 13); payments min 6 / max 13; `Max open files` 4095 soft / 4096 hard |
| F9 | Size trend per release | `UNMEASURED` | needs 6 tagged builds |
| F10 | Build time / CI minutes | `MEASURED` (host) | 1 m 33.2 s real for the documented aarch64 build (warm module cache) |
| S1 | Store format + bytes per token | `MEASURED` | bbolt single file, 0600; **~544 B per proof record**; ~4.84 KB of keyset data per accepted mint |
| S2 | **Bytes written to flash per payment** | `MEASURED` | **success 49,152–57,344 B, mean 53,862 B (52.6 KiB), 14–19 write ops (n=20+2+13); failure 28,672 B / 10 ops (n=5+7)** |
| S3 | Store growth per operation | `MEASURED` (small n) | see §3.2.4 — logical bolt growth ≈ 6.1 KiB per receive (n=2), 521–577 B per proof record, and the file *capacity* never moved across 60+ payments |
| S4 | Crash consistency of the store | `PARTIAL` | store sha256 byte-identical across **all 5 injections in each of the 3 sweeps that ran after the harness fix** (15 rows, §3.4.3), and the balance is unchanged; INJ-5/6/7 (kill mid-swap, ENOSPC, corruption) `UNMEASURED` |
| S5 | Erase-cycle wear projection | `ESTIMATE` | formula + basis in §3.2; datasheet input `OPEN` |
| P1 | Startup time to wallet-ready | `MEASURED` | **12.35 s** (1 s poll granularity); the HTTP API is **down for 12.31 s** of it |
| P2 | mint-quote → mint latency | `UNMEASURED` | not reachable from the router's CLI/API surface (see §3.3) |
| P3 | receive latency p50/p95 | `MEASURED` | credited receives: n=35 over 3 runs, min 638, **p50 738**, **p95 857**, max 1174 ms; rejected receives are not faster (§3.3) |
| P3 | melt / `drain cashu` latency | `PARTIAL` | one call executed successfully (400 sats), not repeated → no distribution |
| P4 | CPU per receive (in the daemon) | `MEASURED` | **60–101 ms, p50 70 ms, mean 68.5 ms** (n=13) from `utime`+`stime` deltas with **USER_HZ=99 calibrated on-device** (§3.3) |
| P5 | Thermal / sustained load | `UNMEASURED` | not attempted |
| P6 | Seam latency (FFI vs socket) | `N/A` | no seam exists in this architecture |
| C1–C3 | Concurrency / lock contention | `UNMEASURED` | needs the §5.0 harness; no concurrent-operation driver exists yet |
| E1 | Dependency count + licences | `MEASURED` (count) | **333** modules in `go list -m all`; 14 `replace` directives; licence per module `UNMEASURED` |
| E2 | Licence compatibility | `MEASURED` | `gonuts-tollgate` is the OpenTollGate fork, GPL-3.0-compatible by construction; upstream `elnosh/gonuts` licence `UNMEASURED` here |
| E3 | Build reproducibility | `PARTIAL` | documented build reproduces in kind (static, ~11.8 MB) but **not byte-identical to the deployed artifact** — see §3.1 |
| E4/E5 | Cross-compile / CI cost | `UNMEASURED` | T5c |
| E11 | Secrets at rest | `MEASURED` (partial) | **seed and mnemonic live in the same bbolt file as the proofs**, mode 0600, inside `/etc/tollgate` (covered by `keep.d/tollgate`) |
| INJ‑1/2/3 | mint unreachable / 5xx / malformed | `MEASURED` | all 5 injections safe in 2 independent sweeps; see §3.4 (and the retry costs it exposes) |

---

## 3. Per-metric detail

### 3.1 Footprint (F1, F2, F4, F10, E3)

The wallet is **not a separate process** — it is linked into `tollgate-wrt`
(`src/merchant/merchant.go:126` constructs it in-process). So F5–F8 below are
the footprint of the *whole router daemon*, which is the honest number: there
is no wallet-only RSS to report for this architecture.

**F1 — size.** Two different artifacts, and the difference is the finding:

| Build | Size | Linkage |
|---|---|---|
| **Deployed** (`/usr/bin/tollgate-wrt`, go1.27.1) | **12,085,824 B** | dynamically linked, `/lib/ld-musl-aarch64.so.1`, `NEEDED libgcc_s.so.1`, `libc.so` |
| **Documented build path** (`packaging/local-build-ipk.sh` / `.github/workflows/build-package.yml`: `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" main.go`, go1.26.0) | **11,796,642 B** | **statically linked, no `NEEDED` entries** |
| Delta | **+289,182 B (+2.4 %)** on the deployed artifact | — |

**F4 — the deployed artifact would fail the protocol's own F4 blocker.** §4
proposes blocking on "**any** `NEEDED` entry that is not `libc.so` / `ld-musl-*`".
The shipped gonuts build needs **`libgcc_s.so.1`** as well. That is a property
of *how it is built*, not of gonuts, and it is measured, not inferred:

* the undocumented difference is real — `CGO_ENABLED=0` cannot produce a
  dynamically linked musl binary, so the feed's `.apk` is built some other way;
* `packaging/Makefile` in this repo says the SDK "only packages" CI-built
  binaries, and the feed's own `net/tollgate-wrt/Makefile` builds **from the
  release tarball with OpenWrt's `golang-package.mk`**, whose exported
  cross-compile environment includes `CGO_ENABLED` and `CC` (comment at
  lines 168–172);
* the local feed checkouts pin `0.6.0-alpha1` (`41465031`) and `089e876c`;
  **none of them pins the deployed `d0cb92f2`**, so the exact recipe for the
  artifact under test is *not* recoverable from this branch. That is itself a
  reproducibility finding and it is why E3 is only `PARTIAL`.

At runtime the process additionally maps `/lib/libsetlbf.so` (4,098 B), which
is not in the binary's `NEEDED` list and not in its dynamic-string table; it
arrives through the loader chain. Recorded because a static build would not
have it in its address space (`raw/idle-maps-*.txt`).

**F2 — stripping.** Unstripped 16,673,953 B vs stripped 11,796,642 B →
**4,877,311 B (29.3 %) removed**, dominated by `.debug_*`, `.symtab`,
`.strtab`. Consequence for E9: on-device symbolised debugging needs a separate
symbol artifact, because the shipped binary carries none.

**F10 — build time.** `time` on the documented command: **1 m 33.2 s real**
(2 m 23.9 s user, 15.3 s sys). Module cache was warm (dependencies already in
`$GOMODCACHE`); the compiler/build cache was cold for this revision. This is a
build-host number and must not be quoted as a router number.

**E3 — reproducibility.** The documented command was run twice (stripped and
unstripped) at the deployed revision and produced stable-size artifacts, but
**not a byte-identical replica of the deployed binary** — different Go
toolchain (1.26.0 vs 1.27.1), different link mode (static vs dynamic), and, per
above, an unrecoverable feed recipe. Recording this honestly is the point:
"reproducible" for the *deployed* artifact is currently **not demonstrated**.

### 3.2 Storage (S1, S2, S3, S5, E11)

**S1 — format.** `wallet/storage/bolt.go`: a single **bbolt** database at
`/etc/tollgate/wallet.db`, mode **0600**, buckets `keysets`, `proofs`,
`pending_proofs`, `mint_quotes`, `melt_quotes`, `invoices`, `seed`. Opened with
`bolt.Open(path, 0600, &bolt.Options{Timeout: 5s})` — **`NoSync` is not set**,
so every `Update` transaction fsyncs.

Measured contents (`raw/store-analysis-after-payments.txt`, taken after the
20-payment run, so `proofs` has been emptied by the drain):

| Bucket | Keys | Value bytes | Note |
|---|---|---|---|
| `keysets/<mint>` (8 mints) | 2–4 each | **38,732 B total** | **≈ 4.84 KB of keyset data per accepted mint**, written at registration |
| `pending_proofs` | 50 | 27,217 B (+1,650 B keys) | **≈ 544 B per proof record** (577 B incl. key) |
| `mint_quotes` | 1 | 546 B | |
| `seed` | 2 | 141 B | **the mnemonic lives in this same file** |
| `proofs` | 0 | 0 | drained |
| **logical DB size** | | **200,704 B** | file *capacity* is 262,144 B (bolt allocates in steps and never shrinks) |

Two consequences worth stating plainly:

1. **The store is keyset-dominated, not proof-dominated.** 8 mints of keysets
   cost ~38.7 KB; 50 proofs cost ~28.9 KB. Keyset data is written per accepted
   mint, so store size scales with the *mint list length* before a single
   payment happens.
2. **`wc -c` on `wallet.db` cannot show growth.** The file sat at exactly
   262,144 B for all 60 payments across both runs while its contents changed
   every time. Any future measurement of "store growth" that watches file size
   will report zero and be wrong. This is why `lib/boltdump` reads bolt's
   logical size instead.

**S2 — bytes written to flash per payment.** Measured by differencing
`/proc/diskstats` field 7 (sectors written × 512) for **both** `mmcblk0`
(the eMMC) and `loop0` (the overlay's F2FS device), sampled immediately before
and after each `tollgate wallet fund` call, with a 3 s settle between cycles.

*Successful* receives — 20 of them in the first run (all 20 succeeded; the
wallet drained 400 sats = 20 × 20 at the end, which is the proof that every
cycle landed):

| | n | min | mean | max |
|---|---|---|---|---|
| bytes written per payment (`mmcblk0`) | 20 | **49,152** | **53,862** | **57,344** |
| KiB per payment | 20 | 48.0 | **52.6** | 56.0 |
| write operations per payment | 20 | 14 | **16.8** | 19 |
| bytes written per payment (`loop0`) | 20 | — | equals the `mmcblk0` figure in all 20 rows | — |

Independently corroborated by the 2 successful receives in the second run:
**53,248 B / 18 ops** and **57,344 B / 16 ops** — both inside the first run's
range.

*Failed* receives are cheaper, and the difference is large enough that it must
be stated rather than averaged away: the 5 failed attempts in the second run
each wrote **28,672 B in 10 write ops** (the swap is rejected by the mint
before the proofs are persisted; only the counter update and the error path
reach the store). **A `bytes-per-payment` figure quoted without saying whether
the payment succeeded is meaningless** — the two differ by ~1.9×.

`loop0 == mmcblk0` in every row means the loop layer adds **no** amplification
at the block-accounting boundary — whatever F2FS wrote is what the eMMC was
asked to write. It says nothing about the eMMC's internal FTL/GC amplification,
which is not observable from the host.

**This is the headline storage number, and its ratio is the point:** ~52.6 KiB
written per *successful* payment, against **~1–3.4 KB of logical proof data**
actually stored and a payment worth **20 sats**. The write cost is dominated by
bolt page writes + F2FS metadata + fsync, i.e. by the *store design*, not by
the token.

**S3 — growth per operation.**

*Per proof record* (measured, two independent samples): **521 B** uniform
across the 13-record `proofs` bucket, and **577 B** mean across the 50-record
`pending_proofs` bucket (488–544 B of JSON value + 33 B of key). Quote it as
**~520–580 B per stored proof**.

*Per operation* (measured, n=2 — small, say so): the logical database went
**200,704 B → 212,992 B (+12,288 B)** across 2 successful receives that added
13 proof records (6,769 B). So ~6.1 KiB of logical growth per receive, of
which ~3.4 KiB is proof payload and the rest is bolt page/freelist accounting
and the keyset-counter update. A full growth curve (0/10/20/…/N) is a
`STORE_DUMP_EVERY=K` run of `phases/04-payments.sh`; the mechanism is committed
and the reason it was not completed here is a real interaction, documented in
§4.7 — a service restart makes the first few receive attempts fail.

*Capacity* (measured, strong): `wallet.db` sat at exactly **262,144 B** for all
60+ payments across every run while its contents changed on every one. The
file is allocated in steps and never shrinks.

**S5 — wear projection (ESTIMATE, basis stated).** Using the measured
`bytes_per_payment = 53,862`:

```
payments_budget = (usable_flash_bytes × P/E_cycles) / bytes_per_payment
```

At 1,000 payments/day (a busy hotspot) that is **53.9 MB/day ≈ 19.7 GB/year**
of writes. Against a 7.6 GB eMMC this is ~2.6 device-writes per year, so on
**this** hardware the wear budget is not a practical constraint for any
plausible P/E rating. **The datasheet figure is not available here and is
marked `OPEN`** — the protocol forbids quoting a wear budget without it, and
this is an illustrative computation, not a measurement.

The same arithmetic on the class the protocol assumed (16 MB NOR, 10⁵ cycles/block,
~64 KiB blocks) gives a far less comfortable picture, because there a 52.6 KiB
write is one erase block *and* F2FS/UBIFS metadata churn consumes the same
small partition. **S2 stays a blocking metric for the `mipsel`/NOR class even
though it is benign on the GL-MT6000.**

**E11 — secrets at rest (partial).** `wallet.db` is 0600 and lives in
`/etc/tollgate`, which `packaging/files/lib/upgrade/keep.d/tollgate` preserves
across `sysupgrade`. The **seed/mnemonic is stored inside the same database as
the proofs** (`seed` bucket, 141 B of values) — there is no separate,
differently-protected key file. Not measured here: whether the mnemonic can
appear in logs or a crash dump, and whether a factory reset is a silent,
undocumented loss of funds.

### 3.3 Behaviour (P1, P3, P4)

**P1 — startup to wallet-ready = 12.35 s, and the API is down for all of it.**

| Milestone | Time from restart |
|---|---|
| process present (`pidof`) | **0.10 s** |
| `:2121` answers a well-formed `kind:10021` advertisement | **12.31 s** |
| `tollgate -j status` reports `wallet_ok:true` | **12.35 s** |

Poll granularity is 1 s (busybox `sleep` rejects fractions); timestamps come
from `/proc/uptime` at centisecond resolution.

The `logread` capture shows exactly what the 12.3 s is spent on
(`raw/startup-logread-2026-09-18T22-23-55Z.txt`, PID 16270):

```
22:18:09  … 22:18:13   seven sequential `mint probe: url=…/v1/info status=200 elapsed=…` lines (≈4 s)
22:18:13               Setting up wallet...
22:18:13               TollWallet.New: Initializing wallet at path: /etc/tollgate
22:18:16  → 22:18:22   TollWallet: registered mint <one line per mint, ~1–3 s apart>  (≈9 s)
22:18:22               Wallet Balance: 0
22:18:22               Starting payout routine
22:18:22               Starting HTTP server on all interfaces...
```

So wallet-ready is **O(accepted_mints) serial network round-trips**
(two passes: the mint probe, then per-mint registration), and **the HTTP
server does not bind until they finish**. On a router that boots before WAN is
up — a *normal* boot condition per the protocol's §2.2 — those calls fail and
the penalty becomes the client timeout × mints, with the payment API
unavailable meanwhile. This is the same coupling the in-tree TODO describes
("the wallet DB does not unlock without a network connection"), and it is now a
number: **12.35 s, 7 mints, 4 cores.**

**P3 — receive latency** (`tollgate wallet fund <token>`, a real NUT-03 swap
against `testnut.cashu.space`). Three runs, and the outcome of each call
matters, so successes and rejections are reported separately:

| run | credited | rejected | min | p50 | p95 | max | mean |
|---|---|---|---|---|---|---|---|
| 2026-09-18T22:24Z | 20 | 0 | 647 ms | 719 ms | 812 ms | 856 ms | 728.5 ms |
| 2026-09-18T22:29Z | 2 | 5 | 780 ms | 780 ms | 820 ms | 820 ms | 800.0 ms |
| 2026-09-19T00:20Z | 13 | 7 | 638 ms | 747 ms | 1174 ms | 1174 ms | 781.0 ms |
| **all credited rows** | **35** | — | **638 ms** | **738 ms** | **857 ms** | **1174 ms** | **752.1 ms** |
| rejected rows (§3.4.1) | — | 12 | 699 ms | 800 ms | 944 ms | 944 ms | 817.6 ms |

Percentiles are nearest-rank (`k = ceil(p·n/100)`) on the pooled credited
sample, computed by `phases/06-analyze.sh`; at n=35 the p95 is one row, so read
it as "worst observed", not as a tail estimate.

Two things this table says that the earlier single-run version did not:

* **A rejected receive is not cheaper in time.** Rejections cost 770–944 ms
  against 638–1174 ms for credited ones — the same round-trip work (keyset
  fetch, DLEQ, swap attempt) happens before the mint refuses. Latency cannot be
  used as a proxy for success, and a UI cannot distinguish the two by feel.
* **The per-row outcome is the row's CLI JSON, not the `amount_sats` column.**
  The 2026-09-18 22:24Z run wrote `na` into that column for all 20 rows (a bug
  in that revision of the phase script) while the same rows carry
  `"message":"Successfully funded wallet with 20 sats"` and the run ended by
  draining 400 sats = 20 × 20. `phases/06-analyze.sh` therefore classifies on
  row text and prints the raw column distribution next to it.

The call covers: short-keyset-ID resolution, active-keyset fetch, DLEQ
verification (with a per-keyset key fetch), blinding, `POST /v1/swap`,
`IncrementKeysetCounter` (fsync) and `SaveProofs` (fsync). The 20 sats received
from a 21-sat token (`amount_received: 20`) reflects the test mint's
`input_fee_ppk=100`, not a router cost.

**melt / `drain cashu`** was executed once to return the wallet to zero
(400 sats → token, balance back to 0) and is `PARTIAL`: no distribution.

**P2 — mint-quote → mint is `UNMEASURED` and that is a finding, not a gap.**
The wallet's own NUT-04 path is not reachable from the device's CLI (`tollgate
wallet` exposes `balance`, `fund`, `drain`, `info`) or its HTTP advertisement
surface. The minting half of a payment is performed by the customer's wallet,
not the router's. Reporting a mint latency would mean reporting the *mint's*
latency, which is not a property of the thing we are replacing.

**P4 — CPU per receive = 60–101 ms, p50 70 ms, mean 68.5 ms (n=13).** Measured
as `utime`+`stime` deltas of the daemon around each `wallet fund` call, in the
2026-09-19 run only (the sampler grows the two tick columns in that run; the
earlier runs' samplers did not emit them, which is why P4 was `UNMEASURED` in
the first draft of this file).

Ticks → seconds needs `USER_HZ`, and this image gives no way to look it up:
`/proc/PID/schedstat` (nanoseconds, kernel-independent) **does not exist**
(`CONFIG_SCHEDSTATS` off — verified), `/proc/config.gz` is absent, and there is
no HZ sysctl. So `lib/hzprobe.sh` **calibrates** it: burn a busy loop for 10 s
and divide its tick delta by the `/proc/uptime` delta. Result: `ticks=998 /
burn_cs=1000` → **USER_HZ = 99** (a second sample gave 100; ±1 %), recorded in
`raw/user-hz-*.txt` and in `raw/env.txt`. Every CPU number here is derived with
that measured constant, not with an assumed 100.

For scale: ~70 ms of CPU per 20-sat receive, against ~750 ms of wall-clock
latency — i.e. the wallet is **network-waiting, not compute-bound** (≈9 % duty
cycle on one of four cores). A replacement does not need to be faster at crypto
to win on this metric; it needs to not add round-trips.

**F5/F7 under load.** Across the 20-payment window the sampler recorded
VmRSS 14,932 → 27,580 KiB and `VmHWM` max **27,580 KiB**, i.e. **peak RSS under
payments is indistinguishable from the 27,780 KiB idle peak**. Threads stayed
at **8** for the whole window (they sit at 9 with the full runtime spun up);
receives add goroutines, not OS threads. FDs moved 6→13.

*(The 14,932 KiB minimum is not a payment effect: it is the freshly restarted
process at the start of the window ramping back up to steady state — visible as
a step at sample 4 of `raw/payments-samples-*.csv`.)*

The 2026-09-19 run (326 samples) repeats this: VmRSS 17,876 → 27,424 KiB with
`VmHWM` **27,740 KiB**, fds 6→13, and the thread count at **9 for 324 of 326
samples** (8 for the first two, i.e. before the runtime is fully spun up). So
the idle and under-load footprints are the same within measurement noise, and
the 8-vs-9 difference between run 1 and run 3 is a startup artefact of the
window, not a load effect.

### 3.4 Fault behaviour (INJ-1, INJ-2, INJ-3)

Method: a controllable mint (`lib/mock-mint.py`) on the build host, reached from
the router over the LAN, plus a well-formed token whose mint URL was rewritten
to point at it (`lib/rewrite-token-mint.py`). Both the mock's URL **and** a
dead port are added to `accepted_mints` for the sweep (9 entries: 7 production +
mock + dead). Each injection records store size + sha256, wallet balance and
service PID before and after, plus the CLI's answer and the mock's request log.

The phase was run three times after the harness was fixed, and the three disagree
in exactly the ways the harness changed:

| sweep | INJ-1a measured | verdicts | kept as |
|---|---|---|---|
| 2026-09-19T00:25Z | the plain trust gate (dead port not yet injected) | 5 × PASS | `superseded-faults-2026-09-19T00-25-58Z.csv` |
| 2026-09-19T00:29Z | the trust gate, dead port injected | 5 × PASS | `superseded-faults-2026-09-19T00-29-55Z.csv` (its `expect` field carried a comma that shifted the analysis columns) |
| **2026-09-19T00:34Z** | **same as above** | **5 × PASS** | **`faults-2026-09-19T00-34-30Z.csv` — the table below** |

The 2026-09-18T22:36Z sweep is **void**: a harness bug meant no injection was
ever attempted (§6.2), and its rows are `superseded-faults-2026-09-18T22-36-56Z.csv`.

| Injection | Mode | Wallet requests seen by the mock | Elapsed | Store sha256 | Balance | PID | Error reported | Verdict |
|---|---|---|---|---|---|---|---|---|
| INJ-1a | connection refused (dead port, mint trusted) | **0** — rejected locally, see §3.4.1 | 0 s | unchanged | 0 → 0 | unchanged | yes | PASS |
| INJ-1b | accept-then-never-answer | 3 × `GET /v1/keys`, 61.8 s span (~31 s apart) | 75 s (our bound) | unchanged | 0 → 0 | unchanged | yes (rc=124) | PASS |
| INJ-2 | HTTP 500 on every request | **37** (17 `keys` + 20 `keysets`) in 44.2 s | 61 s | unchanged | 0 → 0 | unchanged | yes | PASS |
| INJ-3a | 200 with unparseable body | 4 in 6 ms | 0 s | unchanged | 0 → 0 | unchanged | yes | PASS |
| INJ-3b | 200 with body truncated mid-JSON | 4 in 5 ms | 0 s | unchanged | 0 → 0 | unchanged | yes | PASS |

Raw: `raw/faults-2026-09-19T00-34-30Z.csv` (the `verdict` column carries the
pass/fail per row) and `raw/faults-mocklog-2026-09-19T00-34-30Z.log` (exactly
which endpoints the wallet asked for, in order, with timestamps — the request
counts above are read from it, not estimated).

**3.4.1 The trust gate is "reachable-and-configured", not "configured".**

INJ-1a was built to dial a dead port: the token's mint URL was rewritten to
`http://192.168.1.2:18081` **and that URL was added to `accepted_mints`** so the
trust check would pass. It never dialled — 0 requests, 0 s, and the CLI
answered:

```
Token rejected. Token for mint http://192.168.1.2:18081 is not accepted and
wallet does not allow swapping of untrusted mints. Accepted: [https://mint.coinos.io …]
```

The mechanism is in the code, not in the mock: `src/merchant/merchant.go:111-126`
builds the wallet's mint list from `mintHealthTracker.GetReachableMintConfigs()`
— **only mints that answered the startup probe** — and
`src/tollwallet/tollwallet.go:129` rejects any token whose mint is not in that
list, comparing the token's `m` field literally. So a configured-but-unreachable
mint is silently absent from the trust list at startup, and its customers get:

1. a **0-second hard rejection with no dial** (the wallet does not even try), and
2. a message that says the mint is "not accepted" when in fact the operator
   accepted it — the message names the wrong cause.

Two honest consequences:

* The **connection-refused path is unreachable by construction** on this build.
  Exercising it needs a mint that registered (answered at startup) and then
  refused a dial — not attempted here, so INJ-1a is reported as what it measured
  (the trust gate) rather than as what it was designed to measure.
* The first sweep injected only the mock (not the dead URL), so its INJ-1a
  measured the plain trust gate; same outcome, retained as evidence.

**3.4.2 A broken mint costs real time, and 5xx costs the most.**

* Parse-level failures fail **fast**: INJ-3a/3b return typed errors in <10 ms
  with 4 requests and no retry delay.
* A mint that *hangs* costs 3 attempts spaced **30.7 s and 31.1 s** apart — i.e.
  a ~30 s per-attempt timeout with 3 attempts, so a customer waits ~93 s
  (our 75 s CLI bound fired first, while the third attempt was still in
  flight: the daemon was still waiting when we gave up).
* A mint that answers **500** is the expensive one: **37 requests in 44 s**
  before the swap gave up. Per customer attempt that is ~37 outbound HTTPS
  requests over the uplink — amplified traffic against a mint that is telling
  us it is broken, and a ~61 s stall in which the CLI is blocked.

So "how does the candidate behave when the mint is down" is not a footnote: the
difference between the best (0 requests, 0 s) and worst (37 requests, 61 s)
current behaviour is two orders of magnitude in requests, and a candidate's
**retry policy** is a first-class number to measure — not just its success
latency.

**3.4.3 The safety property held in every injection, in every sweep.**

In all **15** injected rows across the three post-fix sweeps: store sha256
byte-identical, balance unchanged, service PID unchanged, no panic or goroutine
dump in the CLI output, daemon alive, and `config.json` restored byte-identically
at the end (`RESTORE OK: config.json byte-identical`), with the advertisement
back to 7 mints and `full mode: yes`.

Source note, because it matters for anyone re-checking this: the per-row
`store_ok=yes` above comes from the sweep **logs**
(`... store_ok=yes balance_ok=yes alive=yes cli_rc=… err_reported=yes verdict=PASS`),
which are line-oriented and unambiguous. The **CSV** of the 2026-09-19T00:25Z
sweep cannot be parsed positionally at all — its `balance_*` columns hold raw
JSON containing commas — so column-indexed checks against that file report
"mismatch" for every row. That sweep's five rows are `FAIL` in its own verdict
column for two instrument reasons only (whole-JSON balance compare, §6.2 items 2
and 4); its `store_ok` is `yes` for all five.

One state caveat, measured rather than inferred: the store **does** pick up a
keyset row for the injected mint (the raw store analysis shows
`keysets/https://testnut.cashu.space`, 5,983 B of keyset data). That entry
persists after `config.json` is restored, so the measurement leaves the wallet
with keyset material for a mint it no longer accepts. Harmless here (test mint,
no funds), but it means "restore the config" is not a full undo of this harness,
and a real operator adding then removing a mint should expect the same residue.

### 3.5 Regenerating these numbers

Nothing in this file is hand-computed any more:

* `../experiments/baseline-gonuts/phases/06-analyze.sh` (run as
  `./run.sh analyze`) recomputes the per-outcome latency/flash/CPU tables, the
  footprint ranges, the store buckets and the fault table from `raw/`, and
  writes `raw/analysis-<stamp>.txt`. Its provenance line and `n=` on every row
  are the audit trail.
* `lib/hzprobe.sh` recalibrates `USER_HZ` on the device (the constant behind
  P4) — `raw/user-hz-*.txt`.
* Files prefixed `superseded-` in `raw/` are **retained evidence of harness
  defects, not results**: they document what the faulty revisions produced. The
  authoritative artefacts are the unprefixed ones.
* One trap for the next reader: the flash/latency tables must be read
  **per outcome**. A rejected receive costs 28,672 B / 10 write ops / ~815 ms;
  a credited one 49,152–57,344 B / 14–19 ops / ~752 ms. Averaging them produces
  a number that describes no customer experience.

---

## 4. What the baseline actually establishes

1. **Flash writes per payment are ~52.6 KiB** — 48 KiB minimum, 56 KiB maximum,
   16.8 write ops, on both the F2FS loop device and the eMMC. This is the
   first-class number the recommendation needs, and it is now measured rather
   than assumed. The *logical* proof data is ~1.4 KB; the rest is bolt page
   writes, F2FS metadata and fsyncs.

2. **Any replacement must be compared on write *design*, not store size.** A
   candidate whose store rewrites a whole file per payment, or fsyncs more than
   twice, loses on a metric worth ~38× the payload.

3. **Startup is coupled to mint reachability and it is serial**: 12.35 s to
   wallet-ready with 7 mints, with the payment API unavailable throughout.
   P1's blocker question ("does the candidate make wallet-ready independent of
   mint reachability") now has a concrete baseline to beat, and a concrete
   scaling rule — it grows with the accepted-mint list.

4. **The deployed artifact is not built the way the repo says it is built.**
   Static per `CGO_ENABLED=0` in CI and `packaging/local-build-ipk.sh`;
   dynamically linked with an extra `libgcc_s.so.1` dependency in fact.
   F4's blocker as written would disqualify the *baseline*. Either F4 is
   re-scoped to "no dependency beyond what the image already ships" or the
   build contract is fixed — a decision for the operator, not for this card.

5. **The protocol's hardware assumptions (A1/A2) are wrong for the test
   device.** RAM is 1,010 MB not 64–128 MB; storage is 7.6 GB eMMC with F2FS,
   not 16–32 MB NOR. Thresholds calibrated to A1/A2 must be re-derived, and
   S2/S5 stay blocking **for the mipsel/NOR class**, not for this one.

6. **The store's own file size is a decoy.** It stayed at exactly 262,144 B
   across 60 payments. Growth is only visible in bolt's logical size — a trap
   worth writing down before someone measures a candidate with `ls -l`.

7. **A service restart makes the first few receive attempts fail.** Found by
   accident (a second measurement pass was discarded for it) and then
   characterised, because it is exactly the kind of thing the migration must
   not re-import:

   | Observed | |
   |---|---|
   | First-ever use of a mint by this wallet | **20/20 receives succeeded** (run 1) |
   | After `/etc/init.d/tollgate-wrt restart`, same mint, same wallet | **5 consecutive receives failed** (run 2) and **7** (run 3) with `failed to receive token: could not swap proofs: Duplicate outputs`, then every later attempt in the run **succeeded** (run 2: attempts 6–7 credited; run 3: cycles 8–20 credited, 13 in a row) |
   | Persisted keyset counter for that mint afterwards | **63** (`raw/store-keyset-counters-injected.txt`) — the counter does advance and persist |
   | Persisted counters for the 7 production mints | **all 0** — this wallet has never used them, so the measurement left **no** production mint in the collision state |

   So the failure is a **transient post-restart window** on a mint whose
   blinded outputs have already been signed (5 failed attempts in one run, 7 in
   the next — the width of the window is not constant). It is not a permanent
   break: each failed attempt still advances the counter and the collision
   clears. It is also **silent from the customer's side only in the sense that
   the error is typed** — the receive genuinely fails, and every failed attempt
   still costs 28,672 B of flash (§3.2). Note this is *weaker* than the
   behaviour our own `physical-router-testing` notes record for the same class
   of bug ("purchase permanently broken until wallet state changes") — the
   difference is worth reconciling, because it decides whether a restart during
   a customer's payment is a lost sale or a seven-attempt hiccup.

   Why it matters beyond the baseline: restarts are **routine** in production —
   `procd` restart, the hotplug handler `95-tollgate-restart`, every config
   write, every `apk upgrade`. Any replacement that carries the same
   deterministic-output/counter coupling inherits this. It also means
   `bytes-per-payment` must be reported per outcome (§3.2): **success ~52.6 KiB,
   failure ~28 KiB**.

8. **The accepted-mint list is not what the operator configured** (§3.4.1).
   A mint that does not answer its startup probe is dropped from the wallet's
   trust list, so its tokens are refused in 0 s with a message that blames
   "not accepted". For a payment gateway the failure mode is the wrong one: the
   customer's ecash is fine, the router simply never looked. Any candidate
   must be compared on **what happens to a token from an accepted mint that is
   momentarily down** — and on whether the message a customer sees names the
   real cause.

9. **A broken mint costs two orders of magnitude more work than a hanging
   one** — 37 requests in 44 s against a 500, versus 3 requests at ~31 s
   intervals against a black hole, versus 4 requests in <10 ms against
   malformed JSON (§3.4.2). Retry policy is therefore a **headline** metric for
   the replacement, not a footnote: it decides both the uplink cost and how
   long a customer waits before being told "no".

---

## 5. Not measured, and why

| Gap | Why it is not in this file |
|---|---|
| F6 PSS | impossible on this image: `/proc/PID/smaps` and `smaps_rollup` do not exist (kernel without `CONFIG_PROC_PAGE_MONITOR`). The `idle` phase proves it every run rather than guessing. |
| F3 packaged `.apk` size | needs the OpenWrt SDK for `mediatek-filogic` (E5/T5c), not a measurement task. |
| F9 size trend | needs 6 tagged builds; a separate, cheap follow-up. |
| P2 mint-quote → mint | not reachable on-device (§3.3) — it is the customer's action, not the router's. |
| P4 CPU/payment | **now measured** (§3.3): 60–101 ms, p50 70 ms, with `USER_HZ` calibrated on-device. |
| P5 thermal | not attempted. |
| C1–C3 concurrency | needs the §5.0 harness and a driver that keeps two operations in flight; none exists yet. |
| S4 full crash consistency | INJ-5/6/7 (kill mid-swap, `ENOSPC`/read-only flash, corrupted store) were **not** run. Only the "no state change on a failed operation" half is evidenced (§3.4.3), and it is evidenced twice. |
| R1–R8 | the mandated fault set is only partly covered: the *mint-side* failures (INJ-1/2/3) are measured; the *store-side* and *power-side* ones are not. A candidate cannot be recommended on this alone, and neither can the baseline be declared safe. |
| INJ-1a as designed | the connection-refused dial is not reachable on this build (§3.4.1): the mint is dropped from the trust list at startup, so the wallet never dials. Exercising it needs a mock that answers the startup probe and then goes away. |
| P3 melt distribution | one melt only (400 sats); a distribution needs a repeated melt loop, which was out of scope for this card. |
| NUT coverage delta (E10) | code-reading work (T3/T5a). Note in passing: `src/tollwallet/tollwallet.go:126` **rejects** untrusted mints outright while `merchant.go:126` hardcodes `allowAndSwapUntrustedMints=false`, which contradicts the protocol's E10 note that untrusted tokens are "accepted as-is without swapping". Worth reconciling in T5a. |

---

## 6. Deviations, and the harness defects found while measuring

### 6.1 Deviations from the protocol's stated procedure

Recorded so the next reader does not mistake them for mistakes:

1. **S2 was taken with `diskstats` deltas only** — the protocol also asks for a
   `tmpfs`-bounded repeat to separate logical from amplified writes. Not done:
   moving the live wallet's store is a state change out of proportion to the
   information, and on this device `loop0 == mmcblk0` already shows the
   amplification is not at the loop boundary.
2. **Sampling is 1 s, not sub-second** — busybox `sleep` rejects fractional
   seconds. Per-operation peaks come from `VmHWM`, which is independent of
   sample resolution.
3. **`payments` and `faults` make a reverted config edit** (a test mint added to
   `accepted_mints`, byte-verified restore). The wallet rejects untrusted mints
   (`tollwallet.go:126`) and there is no CLI/API override, so this is the only
   way to exercise a real receive on real hardware without spending real sats.
   Documented in the phase headers; `SKIP_RESTORE=1` is debug-only.
4. **No firmware was flashed or upgraded.** The only device mutations were
   service restarts — one per config injection and one per restore, in each of
   the six sweeps that ran to completion, plus the one `startup`-phase restart
   — and the reverted config edit above. Every restart is visible in the phase
   logs (`--- restarting service for the injected config ---`,
   `--- restoring config.json verbatim ---`), and the restoration is verified
   by sha256 each time.
5. **Test ecash only** — `testnut.cashu.space` is a FakeWallet mint; no real
   bitcoin was moved.

### 6.2 Harness defects found and fixed (what `superseded-*` files are)

The first sweep of this card produced rows that looked like evidence and were
not. All seven defects below were found by inspecting the artefacts against the
mock's own logs, and all are fixed in the committed scripts. The artefacts the
faulty revisions produced are kept, prefixed `superseded-`, as the record of
what a "passing" measurement can look like when the instrumentation is wrong.

| # | Defect | What it would have claimed |
|---|---|---|
| 1 | `timeout 75 rssh …` — `timeout` cannot exec a shell *function* (`failed to execute process: No such file or directory`), so no injection ever ran | **"store unchanged, service alive" for all five injections** — a perfect score on a test that never executed. This is the single most dangerous failure mode in this file. |
| 2 | The same run compared the CLI's *whole* balance JSON, which embeds a timestamp, so `before != after` on every row | every injection marked "balance changed" — a false alarm that would have been read as a wallet bug |
| 3 | `set -e` (from `lib/common.sh`) plus `[ test ] && x` and `VAR=$(timeout …)`: a failing AND-list or a 124 exit kills the script | the sweep would have died silently at the **first expected timeout** (INJ-1b), losing all later rows |
| 4 | INJ-1a injected the mock's URL but not the dead port's, so the dead-port token failed the *trust* check | "connection refused" reported for a case where nothing was ever dialled (§3.4.1) |
| 5 | `USER_HZ` had no source on this image and would have been assumed as 100 | P4 in units of an assumed constant; the calibrated value is 99, i.e. ~1 % of quiet error in a metric nobody could audit |
| 6 | `expect` strings containing a comma in a mid-row CSV field | every later column shifted by one — the analysis printed `store_ok` values that were actually timestamps |
| 7 | The `amount_sats` column was written as `na` for all 20 rows of the 2026-09-18 payments run (script revision), while the row's own JSON said `Successfully funded wallet with 20 sats` | the pooled latency/flash statistics would have classified 20 credited receives as failures and reported the wrong success rate |

Two habits follow from these, and they are the point of this section:

* **A fault-injection result must prove that the fault was attempted.** The
  fixed `05-faults.sh` requires a reported failure (`"success": false` or an
  rc=124 bound) *and* an unchanged store — an unchanged store alone is also
  what doing nothing looks like.
* **Count the requests the mock actually received.** That is what turned rows 1
  and 4 from "pass" into "measuring the wrong thing", and it is where the
  retry-policy numbers in §3.4.2 come from.
