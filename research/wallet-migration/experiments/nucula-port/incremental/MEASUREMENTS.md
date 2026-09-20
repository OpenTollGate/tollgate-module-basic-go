# Measurements: whole-blob proof store (before) vs one entry per proof (after)

Everything here was produced by the port harness in this directory on the host
recorded in `measurements/2026-09-20-native/00-env.txt`. Nothing is estimated;
where a figure is *not* measured it says so.

* **before** = upstream `zeugmaster/nucula` `8ad0812` (the revision the port
  spike pinned), `main/wallet_nvs.cpp` sha256 `085e14ba…`
* **after** = `felixfelix-bot/nucula` `feat/incremental-proof-store`
  @ `dbd1eb0e30d7b26713261b890d5c61c41a601d92`, `main/wallet_nvs.cpp`
  sha256 `bf0600e9…` (see `FORK-BRANCH.txt`)
* the two trees differ in **only** those two storage-layer files — the harness
  run prints that `diff -rq` into `00-env.txt`.

## What the old code wrote per operation

`Wallet::save_proofs()` serialised the entire `proofs_` vector into one JSON
array and wrote it as a single NVS blob under `proofs_<slot>`, then committed.
It is called at the end of every mutation: `swap()` (`wallet_flows.cpp:451`),
`mint_tokens()` (`:661`), `melt_tokens()` (`:811`), `clear_proofs()` and
`remove_proofs()` (`wallet.cpp:196,202`). Atomicity came from that single key:
either the whole old blob or the whole new one was on flash.

## The metric

The harness's NVS shim counts, per operation: **payload bytes written** (what a
`nvs_set_*` puts on the medium, plus the shim's 1-byte type tag), **successful
write operations**, and **successful erase operations**. Erases are reported
separately because on a real device an erase also dirties a page — this patch
trades "many bytes, one op" for "few bytes, a few ops", so both are shown.

## Before / after

| operation | before | after | source |
|---|---:|---:|---|
| write a 200-proof set from scratch | 47 224 B | 47 203 B | `measure` / `pay` seed |
| **payment at 200 proofs: spend 3 inputs, land 3 change outputs** | **47 202 B** | **708 B** (3 writes + 3 erases) | `measure` / `pay` |
| first payment after a fresh seed (no holes to reuse yet) | 47 202 B | 711 B (3 writes + 1 mark write + 3 erases) | `pay` |
| the same 3-in/3-out payment at 400 proofs | 94 402 B | 708 B (3 writes + 3 erases) | `measure` / `pay` |
| spend 3 inputs, no change (200 proofs) | 46 494 B | 0 B written + 3 erases | `spend` (both trees) |
| re-save with nothing changed | 47 202 B | 0 B, 0 ops | `measure` / `storetest` |
| threads / RSS during a payment | 1 / 5 428 kB | 1 / 5 324 kB | `spend` |
| load a 200-proof store + init the wallet store | 13.1 ms | 12.8 ms | `measure` / `measure` (legacy path) |

Notes on how each row was obtained, because the two trees do not offer the
same doors:

* The `measure` mode seeds and rewrites the store by writing the legacy blob
  directly — byte-for-byte what `save_proofs()` did at `8ad0812`
  (`proofs_to_json(proofs_)` → `nvs_set_blob` → commit). It still works in the
  *after* build, which is why the after column keeps those rows: they document
  that the legacy blob (a) still loads and (b) is still written the same way if
  anything ever writes it.
* The `spend` row is the **real wallet path** in both trees:
  `Wallet::remove_proofs()` on a wallet loaded from a 200-proof store.
* The `pay` rows drive `cashu::proof_store::save()`, which is the function
  `Wallet::save_proofs()` now calls (one-line delegate); each payment is
  followed by a re-read and an exact multiset comparison against the in-RAM
  set.
* SELFTEST_RESULT suites=4 failures=0 in both trees (`selftest` mode: crypto
  NUT-00/11/12/13 vectors, codec, wallet math, JSON contract).
* Harness build warnings: 6 per build, **identical in both trees**, all
  `-Wformat` on our shim's `ESP_LOG*` macro
  (`src/shims/esp_log.h:48`, `%lld` vs `int64_t`) — the host-toolchain face of
  the `int64_t` audit the card keeps out of scope. This change adds none.

## ESP32-C3 build (the real target)

`idf.py build` with ESP-IDF v5.4.1, **both trees in one attributable run** with
the same build directory, swapping only the two storage-layer files (so the
before/after delta is exactly the patches' object code):

| build | `wallet_nvs.cpp` sha256 | `nucula.bin` | sha256 |
|---|---|---:|---|
| upstream `8ad0812` | `085e14ba…` | 1 168 192 B (0x11d340) | `4f8f1ea5…` |
| patched `dbd1eb0` | `bf0600e9…` | 1 171 824 B (0x11e170) | `b92af17d…` |

`IDF_BUILD_EXIT=0` both times, no warnings; +3 632 B (+0.31%), app partition
38% free after (39% before). Log with the sha256 of the files it compiled and of
each binary: `measurements/2026-09-20-native/40-idf-build.log`, produced by
`idf-before-after.sh` (this directory) against the same project and build
directory for both trees, with the project's `main/` restored afterwards. The
binaries stay in that project's `build/`; only their hashes and sizes are
recorded, because nucula is unlicensed and no nucula artifact is committed here.

The upstream binary hash `4f8f1ea5…` is the same baseline the port spike
recorded (`01-candidates/nucula-port-map.md`), which is what makes the before
row a check on this run rather than a number of its own.

## Consistency suite (after only): `storetest`

Round trips (empty / 1 / 200 / after a payment / after a spend), the "what a
save writes" assertions, the slot-allocation bound, five **exhaustive
interruption sweeps** (the save is cut off after operation k, for every k),
legacy migration and retirement, damaged-entry isolation, retry convergence and
`erase_nvs()` coverage. Raw output: `measurements/2026-09-20-native/30-storetest.txt`.

Result: `STORETEST_RESULT checks_done=18 failures=0` (exit 0).

The two checks that pin the wear claim, with the numbers they assert (200-proof
wallet, 236-byte entries):

```
[PASS] a payment into a full store writes one entry per landed output plus the count
       -- 711 bytes, 4 write ops, 3 erase ops for 3 change outputs (236 B per entry)
[PASS] a steady-state payment writes one entry per landed output, no count
       -- 708 bytes, 3 write ops, 3 erase ops for 3 change outputs (236 B per entry)
```

Disclosure, because the raw log of an earlier run reported one failure: the
first version of the steady-state check asserted three writes against a store
that had **no free slots to reuse** (every slot below the mark occupied), where
the mark legitimately has to be written to publish the three appended outputs —
the check's own comment described the case correctly and the assertion did not.
It is now split into the two checks above (no holes / holes available), both
passing, and the report line no longer prints the failure count twice
(`checks_done=18`). The tree under test did not change.

The sweeps are the part that matters for "no proof loss on interruption". The
zero-cost way to reason about it is the write order; the way to *check* it is
to enumerate every cut point of a real save and assert

```
old ∩ new  ⊆  stored  ⊆  old ∪ new
```

with both the intersection and the union taken as multisets of serialised
proofs, for the resulting store after a cut, for every k.

## What was NOT measured

* **On hardware.** No router or ESP32 was touched (task constraint): no
  `nvs_get_stats()`, no erase-cycle accounting, no power-cut test on the device,
  no device-level timing. The ESP32 figure above is a build, not a run.
* **NVS-level page cost.** The shim is a file store: it cannot reproduce
  ESP-IDF's 4 KiB page copy-on-write, so the flash-erase saving is argued from
  the bytes-per-mutation reduction, not measured at the block level.
* **Per-entry flash overhead.** The payload bytes are level (47 203 B vs
  47 201 B at 200 proofs), but each NVS item carries a 32-byte header
  (`nvs_types.hpp`: `Item::rawData[32]`) that the single blob amortised. Expect
  roughly 32 B × proofs (~6.4 kB at 200 proofs, ~13%) more flash; **estimate,
  not measured** — the shim has no item overhead to measure.
