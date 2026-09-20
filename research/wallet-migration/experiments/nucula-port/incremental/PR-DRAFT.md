# Incremental proof persistence: write only what changed

## Problem

`wallet_nvs.cpp` stores a wallet's whole proof set as **one JSON array blob**
under `proofs_<slot>`, and `Wallet::save_proofs()` re-serialises and rewrites
that entire blob at the end of *every* mutation (`swap`, `receive`, `mint`,
`melt`, spend). So every payment costs

* **O(total proofs) bytes written to flash**, and
* O(total proofs) serialisation, regardless of how small the payment is.

Measured on the native port harness of nucula's own core (`measure` mode, this
tree at `8ad0812`):

| proofs | bytes written per mutation |
|-------:|---------------------------:|
|    200 | **47 202 B** |
|    400 | **94 402 B** |

On the device that is not just slow, it is wear. The NVS partition is
`0x26000` (152 KiB, `partitions.csv`), and ESP-IDF NVS updates entries by
writing them to a page and, when a page is full, **erasing and rewriting a
whole 4 KiB page** (copy-on-write with a per-page index). A 47 kB blob is a
chain of ~13 chunked entries, so a single small payment dirties a large slice
of the partition every time. That is the strongest single objection to
running a wallet like this on a router or on any device where the same flash
is expected to survive years of tap-to-pay.

This patch makes persistence incremental: **one NVS entry per proof**, and a
save writes only the entries that actually changed. A spend that lands three
change outputs writes three entries; a spend with no change writes none at
all.

## Measurement (after)

Same harness, same harness code, only the storage layer differs (the harness
drives `cashu::proof_store::save/load`, which is what
`Wallet::save_proofs/load_proofs` now delegate to):

| operation | before | after |
|---|---:|---:|
| write the whole 200-proof set (seed / first save) | 47 224 B | 47 203 B |
| **payment at 200 proofs: spend 3 inputs, land 3 change outputs** | **47 202 B** | **708 B** (3 entry writes + 3 erases) |
| payment at 400 proofs, same 3-in/3-out | 94 402 B | **708 B** |
| spend 3 inputs, no change (200 proofs) | 46 494 B | **0 B** (3 erases) |
| re-save with nothing changed | 47 202 B | **0 B, 0 operations** |

* The after-rows are measured, not estimated: `pay` / `storetest` modes of the
  harness, run against this branch. The 708 B figure is the steady state (a
  payment whose outputs land in slots freed by the inputs it spends); the
  first payment after a fresh seed writes 711 B because its three outputs have
  no holes yet and land above the mark, which then stops growing.
* The before-rows are the harness's `measure` mode (the whole-blob write, i.e.
  exactly what `save_proofs()` did at `8ad0812`) and, for the "spend 3" row,
  the *real* wallet path `Wallet::remove_proofs()` on a 200-proof wallet in
  the unmodified tree (46 494 B in one blob write).
* Byte counts are payload bytes written (the harness's NVS shim counts what a
  `nvs_set_*` writes); erases are counted separately because on a real device
  an erase also dirties a page. The point of the change is the *bytes per
  payment* dropping from a whole-set rewrite to the affected entries — the
  flash-block cost on hardware is `erase-block × pages touched`, which this
  patch reduces by the same order.

Footprint and resources:

* Seeding 200 proofs writes 47 203 B (the same payload as the 47 201 B blob).
* RSS ~5.4 MB and 1 thread — unchanged (harness, `spend`/`pay`).
* nucula's own suites: `SELFTEST_RESULT suites=4 failures=0` before and after.
* On the target: `idf.py build` (ESP-IDF v5.4.1) succeeds with no warnings and
  `nucula.bin` grows by 3 632 B (1 168 192 → 1 171 824 B, +0.31%); the app
  partition keeps 38% free.
* CPU: the diff pass reads the stored entries (wear-free) and re-serialises
  the proofs it is comparing, so a save is still O(n) CPU but O(changed)
  flash. On the harness (file-backed NVS shim, shared host, noisy) a
  3-in/3-out payment costs 12–18 ms at 200 proofs and 15–31 ms at 400 across
  runs, but that is dominated by the shim's per-entry `open`+`fsync`+`rename`;
  on the device the same save is one NVS item lookup per proof plus one entry
  write per landed output. The device cost was **not** measured (no hardware
  in this task). The measurement that is comparable between the two trees
  (`spend` mode: load the wallet, remove 3 proofs, save) shows no regression:
  4.2 ms before, 2.7 ms after.

## Design

Schema, per wallet slot (namespace `wallet`):

```
pn_<slot>      u16     number of proof slots — a HIGH-WATER MARK, not a live
                       count: indices below it may be holes, and holes are
                       reused before the mark grows
p<slot>_<i>    blob    one serialized Proof at slot i < pn_<slot>, in exactly
                       the JSON encoding the old format stored inside its array
proofs_<slot>  blob    LEGACY single-blob set; still readable, retired once the
                       per-proof store is durable
```

`save(slot, proofs)`:

1. Serialise-validate every proof up front (an unserialisable proof aborts
   before anything is written — `[]` is a legitimate empty set, not a
   substitute for a failed one) and fingerprint each.
2. Pair stored slots with the in-RAM set. The fingerprint only *prunes*
   candidates; equality is decided by comparing the bytes, so a fingerprint
   collision can cost a redundant write but can never leave a proof
   unpersisted. Paired slots are byte-identical to what is on flash: **not
   rewritten**. Unpaired slots are stale (spent), unpaired proofs are new.
3. Write new proofs into the lowest free slots (holes first, then above the
   mark), then the mark, then erase the stale slots.

`load(slot)` reads the mark and the entries below it, skipping holes, and
still reads a legacy blob when there is no mark. Keys stay short — `p2_65535`
is 8 characters against NVS's 15.

### Crash consistency

Two things make this safe without a journal:

1. **NVS per-key atomicity.** An entry is written and CRC'd before the page
   index is updated, so every key on flash is either its old value or its new
   one — there is no half-written entry.
2. **The write order** (new proofs → mark → erases). Appended proofs sit above
   the mark and are invisible until it is raised; erases come last, so an
   interruption can leave *stale* proofs behind but can never remove a proof
   that was already stored. After any interruption the stored set S satisfies

   ```
   old ∩ new  ⊆  S  ⊆  old ∪ new
   ```

   Stale proofs are spent proofs: the mint rejects them on the next attempt
   (and a NUT-07 state check could clean them up). Losing a proof would be
   losing money; leaving one behind is not.

The old format had a different property — a mutation was all-or-nothing —
but it bought that by rewriting everything on every mutation. This patch keeps
the safety and drops the amplification: the only states a torn save can reach
are "the old set" and "the old set plus proofs that are either fresh outputs
we were writing or inputs we were retiring", both of which are spendable-or-
harmless.

**Migration is one-way but not lossy.** A legacy `proofs_<slot>` blob still
loads, and is rewritten into the new format by the next successful save. The
blob is retired only *after* the per-proof store is durable (a second
`nvs_commit`), so an interrupted migration always leaves the complete legacy
copy readable. A firmware *downgrade* after migration would not see the
proofs; note this if OTA rollback ever matters.

`erase_nvs()` (seed wipe, slot reset) erases both stores.

## Tests

* **ESP32-C3 build (the real target).** Built both revisions in one run with
  ESP-IDF v5.4.1, same project and build directory, only the two storage-layer
  files swapped, so the delta is exactly this patch's object code:

  | build | `wallet_nvs.cpp` sha256 | `nucula.bin` |
  |---|---:|---:|
  | upstream `8ad0812` | `085e14ba…` | 1 168 192 B (0x11d340) |
  | this branch `dbd1eb0` | `bf0600e9…` | 1 171 824 B (0x11e170) |

  `IDF_BUILD_EXIT=0` both times, no warnings; +3 632 B (+0.31%), app partition
  keeps 38% free (`nucula.bin binary size 0x11e170 bytes. Smallest app
  partition is 0x1d0000 bytes. 0xb1e90 bytes (38%) free.`). The 7 wallet
  translation units that include `wallet_internal.hpp` recompile; nothing else
  in the build changes.
* nucula's own on-device suites: `SELFTEST_RESULT suites=4 failures=0`, before
  and after (crypto NUT-00/11/12/13 vectors, pure codec, wallet math, JSON
  contract).
* Native harness `storetest`, run against this branch — **`checks_done=18
  failures=0`**:
  * round trips: empty set, 1 proof, 200 proofs, after a payment, after a
    spend (the `pay` mode additionally re-reads a 200- and a 400-proof store
    after every payment);
  * *"an unchanged save writes nothing"* asserted for: identical set (0 B, 0
    ops), a spend of 3 (0 B written, 3 erases), a payment into a full store
    (three proof entries + the 3-byte count = 711 B), and a steady-state
    payment (exactly three proof entries, no count write = 708 B);
  * the high-water mark stays bounded under churn (40 payments at 200 proofs →
    mark 203, i.e. it does not creep);
  * **exhaustive interruption sweeps**: the save is cut off after operation k,
    for every k from "nothing lands" to "everything lands", checking the
    `old∩new ⊆ S ⊆ old∪new` invariant each time — for a payment, a spend-only
    save, a clear, the first migration (legacy blob present), and with a
    lingering legacy blob. 0 states lost a proof, 0 showed an unknown proof.
    The cuts are injected by the harness's NVS shim (fault injection after
    operation k), which is stricter than a power cut: it can lose any subset
    of the writes, not just a whole commit.
  * legacy compatibility and retirement: a blob-only store loads; a successful
    save converts and retires it; the migrated store reads back as the new
    set;
  * one damaged entry no longer takes the whole set down (the damaged proof is
    skipped and logged, the others load);
  * after an injected mid-save failure, a retry converges on the exact set;
  * `erase_nvs()` removes both stores.

## Trade-offs considered and rejected

* **Append-only journal + periodic compaction.** Exact per-mutation atomicity
  (append records, commit with one marker key) and O(changed) writes, but it
  needs generations, compaction, and cleanup of superseded segments — a lot
  more code in the path that holds the money, for a property this design
  already has in the only form that matters (no proof loss).
* **Packing several proofs per entry (~2 kB blocks).** 20× fewer bytes than
  the blob with less per-entry overhead, but the write unit becomes a block
  and slot bookkeeping is fuzzier. One proof = one entry keeps the code and
  the invariants simple.
* **Content-addressed keys** (`p<slot>_<hash>`). No index or diff needed, but
  15-character key limit forces a short hash, and a collision would silently
  drop a proof — unacceptable for bearer money.
* **A RAM mirror of the persisted state** to skip the read pass. Fewer reads,
  ~4 B/proof more RAM and another piece of state to keep consistent. Reads are
  wear-free, so this is a later optimisation, not a correctness requirement.
* **Keeping the legacy blob forever** for downgrades: costs a second copy of
  the wallet (47 kB at 200 proofs) and shows stale proofs after a rollback.
  Retired instead.

## Observed while doing this, deliberately NOT bundled

* **NUT-07 (state check) is still absent.** With this change a torn save can
  leave spent proofs in the store until they are tried and rejected. NUT-07
  is exactly the tool that would clear them, and the per-proof store makes it
  natural (one entry = one state check). Separate concern, separate PR.
* **NUT-09 (restore) is still absent.**
* **`int64_t` format audit (17 `%lld`/`%llu` call sites).** Visible when
  nucula's core is built with a non-IDF toolchain (`int64_t` is `long` on
  gcc-14/x86_64, `long long` on IDF/riscv). Not touched here — the harness
  builds warn about it on the host in equal numbers before and after.
* **Flash footprint of one entry per proof.** The payload bytes are the same
  (47 203 B vs 47 201 B at 200 proofs), but each entry also carries ESP-IDF's
  32-byte item header (`nvs_types.hpp`: `Item::rawData[32]`), which the
  single-blob format amortises. Expect on the order of 32 B × proofs (~6.4 kB
  at 200 proofs, ~13%) more flash for the same wallet; worth confirming with
  `nvs_get_stats()` on hardware before someone ships a several-thousand-proof
  wallet. NVS also needs a free page to shuffle entries, so the partition
  should keep headroom — which is also why the write amplification was worth
  fixing.

## Notes for review

* The diff touches **only the storage layer**: `main/wallet_nvs.cpp` (the
  store plus thin `save_proofs`/`load_proofs` wrappers) and
  `main/wallet_internal.hpp` (declarations for `cashu::proof_store::`). No
  change to flows, UI, NFC/display/keypad, the HTTP layer, the ESP32 build
  files, or the on-disk *encoding* of a proof.
* The store is now addressable as a plain function of (slot, proof set), which
  is what let the native (non-ESP32) port harness drive the exact code path
  and run the interruption sweeps above. `Wallet::save_proofs()` and
  `load_proofs()` are one-line delegates.
