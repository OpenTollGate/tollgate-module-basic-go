# nucula proof store: whole-blob rewrite → one entry per proof

Storage-layer change for `zeugmaster/nucula` (Cashu ecash wallet for ESP32-C3),
measured on the native port harness in this directory.

**Status:** implemented, built and tested on a fork; **not** proposed upstream
yet — the manager/operator sends it, because it interacts with the open
licence question (upstream issues #7/#8).

**Fork branch:** `felixfelix-bot/nucula`, branch `feat/incremental-proof-store`
(commit SHA in `FORK-BRANCH.txt`, filled in when the branch was pushed).

**Nothing from nucula is copied into this repository**: no source, no patch,
no diff. This directory holds only our harness, our scripts, our raw
measurements and the PR draft, and references the fork by branch + SHA.

## What changed

`main/wallet_nvs.cpp` + `main/wallet_internal.hpp` only. The proof set used to
be one JSON array blob (`proofs_<slot>`) re-serialised on every mutation; now
each proof is its own NVS entry (`p<slot>_<i>` under a high-water mark
`pn_<slot>`), and a save writes only the entries that changed. The old blob
stays readable and is retired after the first successful save in the new
format. See `PR-DRAFT.md` for the design, the crash-consistency argument and
the rejected alternatives.

## Headline result (measured, 200-proof wallet)

| operation | whole-blob store | per-proof store |
|---|---:|---:|
| payment: spend 3 inputs, land 3 change outputs | 47 202 B | **708 B** (3 writes + 3 erases) |
| the same payment at 400 proofs | 94 402 B | **708 B** |
| spend 3 inputs, no change | 46 494 B | **0 B** (3 erases) |
| re-save with nothing changed | 47 202 B | **0 B, 0 ops** |
| seed the whole 200-proof set | 47 224 B | 47 203 B |

`SELFTEST_RESULT suites=4 failures=0` in both trees. RSS/threads unchanged.
The consistency suite enumerates *every* interruption point of a save and
asserts the no-proof-loss invariant `old ∩ new ⊆ S ⊆ old ∪ new`
(`STORETEST_RESULT checks_done=18 failures=0`).

## Files

| path | what |
|---|---|
| `measure.sh` | builds the harness against two source trees and runs every mode into a results dir |
| `idf-before-after.sh` | builds the ESP32-C3 firmware for both trees in one run (same build dir, only the storage layer swapped), recording the compiled file hashes and the resulting `nucula.bin` — the target build, not the harness |
| `measurements/` | raw outputs of the runs referenced in `PR-DRAFT.md` |
| `PR-DRAFT.md` | the upstream PR description |
| `FORK-BRANCH.txt` | fork URL, branch and commit SHA of the change |
| `../src/harness/nucula_core_harness.cpp` | the harness: `selftest` / `measure` / `spend` / `pay` / `storetest` modes |
| `../src/platform/nvs_file.cpp`, `../src/shims/nvs.h` | the NVS shim, extended with operation counters and fault injection (cut a save off after operation k) |

## Reproduce

```sh
# 1. an unmodified nucula (upstream main at the time of writing)
git clone https://github.com/zeugmaster/nucula /var/tmp/nucula-before
git -C /var/tmp/nucula-before checkout 8ad0812
git -C /var/tmp/nucula-before submodule update --init --recursive   # not needed for the native harness

# 2. the change
git clone -b feat/incremental-proof-store https://github.com/felixfelix-bot/nucula /var/tmp/nucula-after

# 3. measure (needs libmbedcrypto, libcjson, tinycbor, libsecp256k1 on the host)
BEFORE_SRC=/var/tmp/nucula-before AFTER_SRC=/var/tmp/nucula-after ./measure.sh
```

The ESP32-C3 firmware build (`idf.py build`) is covered separately by
`idf-before-after.sh` (log in `measurements/2026-09-20-native/40-idf-build.log`,
the table in `MEASUREMENTS.md`); the harness build above is host-native, and the
diff touches no device-specific file.
