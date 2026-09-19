# `experiments/baseline-gonuts/` — how the baseline numbers were produced

This directory holds the scripts **and** the raw output behind
`03-baseline/gonuts.md`. Every number in that report comes from here; a number
that is not produced by a script in this directory is labelled `UNMEASURED` or
`ESTIMATE` there, never presented as measured.

Owner task: **T1a** (`../TASKS.md`). Method: `../02-method/measurement-protocol.md`.

## What it measures

| Metric | Phase | Where the raw output lands |
|---|---|---|
| F1 stripped/unstripped size, F2 sections, F4 linkage, E1 dependency count, F10 build time | `build` | `raw/build-report.txt`, `raw/build/`, `raw/build-golist-mall.txt` |
| F5 RSS, F7 threads, F8 FDs (idle) | `idle` | `raw/idle-samples-*.csv`, `raw/idle-maps-*.txt` |
| F6 PSS | `idle` (proves it is unmeasurable here) | `raw/idle-*.log` |
| P1 startup to wallet-ready | `startup` | `raw/startup-poll-*.txt`, `raw/startup-logread-*.txt` |
| S1/S2/S3 store format + flash writes + growth, P2/P3 latency, P4 CPU, peak F5/F7 | `payments` | `raw/payments-cycles-*.csv` (per receive, with outcome), `raw/payments-samples-*.csv`, `raw/payments-store-timeline-*.txt` |
| INJ-1/2/3 (mint unreachable, 5xx, malformed) | `faults` | `raw/faults-*.csv`, `raw/faults-mocklog-*.log` |
| USER_HZ (the constant behind P4) | `env`, `payments` | `raw/env.txt`, `raw/user-hz-*.txt` |
| Reproducibility facts (env.txt, six-fact contract) | `env` | `raw/env.txt`, `raw/router-*` |
| **every statistic quoted in the report, recomputed from the above** | `analyze` | `raw/analysis-*.txt` |

## Reproduce it

```sh
# 0. workstation deps: openssh client, sshpass, curl, jq, binutils (readelf/size),
#    Go toolchain, python3, and the `cashu` CLI (pip install cashu) for token minting.
cd research/wallet-migration/experiments/baseline-gonuts

./run.sh env                     # six-fact env.txt + deployed-artifact facts
SRC=~/worktrees/baseline-build-d0cb92f2 ./run.sh build
IDLE_SECS=300 ./run.sh idle      # 5 min idle window
./run.sh startup                 # restarts the service once (P1)
CYCLES=20 ./run.sh payments      # INJECTS a test mint, then restores config.json
./run.sh faults                  # mock-mint injections over the LAN, then restores
./run.sh analyze                 # recompute 03-baseline/gonuts.md's numbers -> raw/analysis-*.txt
```

`analyze` is the audit path: it recomputes every pooled statistic quoted in
`03-baseline/gonuts.md` from `raw/`, printing the input file and `n` for each
row. If a number in the report is not printed by it, the report labels that
number `UNMEASURED` or `ESTIMATE`.

Overrides: `ROUTER_IP`, `ROUTER_PW` (defaults to the lab default `password`),
`OUT`, `CYCLES`, `IDLE_SECS`, `SAMPLE_INT`, `TESTMINT`, `MOCKPORT`, `HOST_IP`,
`BURN` (USER_HZ calibration seconds), `SKIP_RESTORE` (debug only).

## Router interventions, and how they are undone

Two phases deliberately change router state, because the wallet **rejects**
tokens from mints that are not in `accepted_mints`
(`src/tollwallet/tollwallet.go:126`, `src/merchant/merchant.go:126` passes
`allowAndSwapUntrustedMints=false`) and there is no CLI/API override:

* `payments` adds `testnut.cashu.space` (a FakeWallet mint, so the ecash is
  free) to `accepted_mints` with `min_payout_amount` raised so the payout
  scheduler never fires on the test balance;
* `faults` adds a LAN URL served by `lib/mock-mint.py`.

Both then **restore `config.json` from a byte-verified on-router backup and
re-verify the sha256**, restart the service, and assert the original mint count
and advertisement come back. `SKIP_RESTORE=1` leaves the injection in place and
is for debugging only. Nothing here reflashes or upgrades firmware; the only
device mutation outside the store is a service restart plus a reverted config
edit.

Two corrections to the first version of this, both forced by measurement:

* `faults` injects **two** URLs, the mock's and a dead port's (`:MOCKPORT+1`).
  Injecting only the mock meant the dead-port token failed the *trust* check
  before any dial, so the "connection refused" case measured the wrong thing
  (`03-baseline/gonuts.md` §3.4.1).
* **The config restore is not a full undo of the store.** The wallet keeps the
  keyset rows it registered for the injected mint (measured: 5,983 B for the
  test mint), so the store grows by that much per injected mint and keeps it.
  It affects no production mint, but do not expect the store sha256 to return to
  its pre-run value — it does not, and the sweeps record it each time.

## Image quirks that shaped the scripts (all measured 2026-09-18)

These are the reasons several obvious approaches do **not** work on this
target, recorded so the next person does not re-derive them:

* **No `stat(1)`, no `pkill(1)`, no `nohup(1)`** on the OpenWrt 25.12.5 busybox.
  Sizes come from `wc -c <`, process control from a pidfile the sampler writes
  itself, and detaching from `setsid(1)`.
* **`sleep(1)` rejects fractional seconds** (`sleep: invalid number '0.5'`), so
  the sampler's interval is integer seconds. Per-operation peaks are read from
  `VmHWM`, a cumulative high-water mark that does not depend on sampling
  resolution.
* **`setsid sh -s` cannot work**: `sh -s` needs the ssh channel on stdin, but
  detaching requires `</dev/null`. The sampler is therefore *staged as a file*
  and launched by path.
* **`/proc/PID/smaps` and `/proc/PID/smaps_rollup` do not exist** — this kernel
  is built without `CONFIG_PROC_PAGE_MONITOR` — so **F6 (PSS) cannot be measured
  by the protocol's procedure on this image**. `idle` proves the absence rather
  than guessing a value.
* **The overlay is F2FS on a loop device over eMMC** (`/dev/loop0` → F2FS,
  backed by `mmcblk0p7`), not JFFS2/UBIFS on NOR: `/proc/mtd` is empty. The
  protocol's `A2` assumption (16–32 MB NOR, ~10⁵ erase cycles/block) does not
  describe this device, which has a 7.6 GB eMMC and 1 GB RAM.
* **The `cashu` CLI fails against `nofee.testnut.cashu.space`** (pydantic
  `KeysResponse` requires `active`, which that Nutshell build omits), so token
  minting uses `testnut.cashu.space` (cdk-mintd + FakeWallet) instead.
* **No `/proc/PID/schedstat` and no `/proc/config.gz`** — so there is no
  kernel-independent CPU-time clock and no HZ sysctl on this image. P4's tick
  deltas therefore need a *calibrated* `USER_HZ`; see `lib/hzprobe.sh` (measured
  99, not the assumed 100).
* **The `amount_sats` column is not a reliable outcome signal** across script
  revisions: the 2026-09-18 run wrote `na` for all 20 credited rows. `analyze`
  classifies on the row's CLI JSON (`Successfully funded wallet`) instead.

## Pitfalls that cost this card a full sweep

Full narrative in `03-baseline/gonuts.md` §6.2; the short list, because each one
produced a green result that was meaningless:

1. **`timeout` cannot exec a shell function.** `timeout 75 rssh …` fails with
   `failed to execute process: No such file or directory` and runs nothing. Use
   `rssh_t 75 …` from `lib/common.sh`, which applies the timeout to the `ssh`
   *binary* inside the function. The first `faults` run recorded five perfect
   "store unchanged" rows for five injections that never happened.
2. **`set -e` (set by `lib/common.sh`) plus `[ test ] && x`**: a failing
   AND-list is itself a non-zero command and kills the script. Same for
   `VAR=$(timeout …)` when the timeout fires. Write `if` blocks and
   `VAR=$(cmd) || rc=$?`.
3. **A comma in a mid-row CSV field shifts every later column.** Keep
   `expect`-style strings comma-free, or quote them.
4. **A balance comparison must compare the number, not the JSON** — the CLI's
   JSON embeds a timestamp, so whole-string comparison always reports a change.
5. **Prove the fault was attempted.** An unchanged store is also what doing
   nothing looks like; require a reported failure as well, and check the mock's
   request log for the requests you expected.
6. **Artefacts of a faulty revision are kept, prefixed `superseded-`.** They are
   evidence of the defect, not results — the unprefixed files are authoritative.

## detect-secrets and the raw log dumps

The `detect-secrets` pre-commit hook flags three **public** values in the raw
log dumps: the router's merchant **pubkey** and two kind:10021 advertisement
**event ids** (seen in `Advertisement: {...}` log lines). They are published
values, not secrets, and are recorded as audited false positives in
`.secrets.baseline`. `lib/verify-baseline-hashes.sh` re-derives the sha1 that
detect-secrets stores and prints which public value each one is, so the claim
can be re-checked rather than believed.

## Environment (six-fact contract)

`raw/env.txt` is the authority; the summary is:

* **ENV-ROUTER-A64** — GL.iNet GL-MT6000 (`glinet,gl-mt6000`), OpenWrt 25.12.5
  `r33051-f5dae5ece4`, mediatek/filogic, `aarch64_cortex-a53`, 4 cores, 1010 MB
  RAM, 7.6 GB eMMC, `apk`. On-device, not emulated.
* Package `tollgate-wrt-0.6.0_alpha2_pre7-r1` from `/feed/net/tollgate-wrt`.
* Deployed binary `d0cb92f2` (`v0.6.0-alpha2-gd0cb92f`), go1.27.1, sha256
  `0d567fb5fee9e45f6c7dcb3b6831429b2be6e2b4c38146010e1c2ffae1f96b8c`,
  linking `github.com/OpenTollGate/gonuts-tollgate v0.11.2`.
* Build host: Ubuntu 26.04, x86_64, go1.26.0 (build-host numbers are size/build
  facts only and may never be quoted as router numbers).

## Files

```
run.sh                     dispatcher: env|build|idle|startup|payments|faults|analyze
lib/common.sh              ssh helpers (incl. rssh_t), size/sha helpers, CPU/USER_HZ, sampler control
lib/selftest.sh            checks the router helpers against the live device before a sweep
lib/router-sampler.sh      on-router sampler (staged + setsid-launched)
lib/hzprobe.sh             on-router USER_HZ calibration (the constant behind P4)
lib/mock-mint.py           controllable Cashu mint for fault injection
lib/rewrite-token-mint.py  repoint a V4 token at another mint URL
lib/boltdump/              Go tool: reads bolt's logical size + bucket totals (source only)
phases/00-env.sh           reproducibility facts -> raw/env.txt (incl. USER_HZ)
phases/01-build.sh         F1/F2/F3/F4/F10/E1 + deployed-artifact analysis
phases/02-idle.sh          F5/F6/F7/F8 idle window
phases/03-startup.sh       P1
phases/04-payments.sh      S1/S2/S3, P2/P3/P4, peaks
phases/05-faults.sh        INJ-1/2/3
phases/06-analyze.sh       recomputes every quoted statistic -> raw/analysis-*.txt
raw/                       every artefact these scripts produced
```

## What is *not* committed

Three artefact classes are deliberately expensive to commit and are excluded by
`.gitignore`; everything needed to reproduce or audit the numbers is text and
**is** committed:

* `raw/tollgate-wrt.device.bin` (12 MB) — the pulled deployed binary. Its
  sha256, `file`, `size`, `readelf -d` and `go version -m` output are committed
  as `raw/tollgate-wrt.device.*.txt`, which is what the report cites.
* `raw/build/` (11.8 MB stripped + 16.7 MB unstripped) — `raw/build-report.txt`
  carries the sizes, section table and `time` output.
* `raw/boltdump` — the compiled helper. Rebuild with
  `cd lib/boltdump && go build -o ../../raw/boltdump .`
