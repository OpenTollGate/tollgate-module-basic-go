# Candidate: nucula (`zeugmaster/nucula`)

## What it is (verified from the README)

> A Cashu ecash wallet for the **ESP32-C3** with **NFC tap-to-pay**.

Feature claims: store ecash from multiple mints; receive over NFC; mint new
tokens via Lightning invoices; melt to pay Lightning invoices; tokens received
offline are stashed and redeemed automatically when WiFi returns.

Reference hardware: **Seeed XIAO ESP32-C3**; peripherals on one I2C bus —
PN7160 NFC controller (card emulation), SSD1309 128x64 OLED, PCF8574 keypad
expander. Pins/addresses in `main/board.h`; NCI control pins in
`components/pn7160/include/nci.h`. Build: **ESP-IDF v5.x**; `main/wifi_config.h`
holds credentials (edited from an example).

## Assessment so far

| Dimension | Finding |
|---|---|
| Runtime target | ESP32-C3 firmware. **Not** a Linux/OpenWrt service. |
| Language | C++ (CMake/ESP-IDF build) |
| Licence | **No LICENSE file in the repo** (verified via the GitHub contents API). Absent a licence there is no grant of rights — cannot be vendored or linked into TollGate without the author's explicit permission. |
| Maturity | Personal project, 6 stars, last push 2026-07-20. Bus factor 1. |
| Security posture | No published audit found; embedded wallet holding real ecash keys on-device. |

## What it could be relevant to

If TollGate wants an **end-user tap-to-pay device** (board-side wallet), nucula
is a candidate *implementation* to study or fork — but that is a different
product decision from "replace the router's wallet library", and it inherits the
licence question plus ESP-IDF toolchain and hardware dependencies (NFC + OLED +
keypad, all I2C).

## Open questions (owner: consultant A, see TASKS.md T2)

- [ ] NUT coverage actually implemented vs claimed (cite code, not README)
- [ ] Licence intent: ask the author; until answered, treat as all-rights-reserved
- [ ] Proof storage on flash: format, wear implications, crash consistency
- [ ] Key handling: where the seed lives, whether it is encrypted at rest
- [ ] Real memory footprint (IRAM/DRAM/flash) from a build, and whether an
      ESP32-C3 (400 KB SRAM class) is actually comfortable with multi-mint ecash
- [ ] Portability of the *logic* (not the firmware) to a Linux daemon: what is
      reusable C++ vs what is ESP-IDF bound

## Router applicability: assessed

nucula is **firmware for a specific ESP32-C3 board** (Seeed XIAO) with three I2C
peripherals, built with ESP-IDF v5.x. It is not a Linux/OpenWrt program, and the
module already contains a working Go↔Cashu seam that a Rust/C++ firmware wallet
cannot plug into. Unless the goal is redefined as *a device-side wallet product*,
nucula is **not a router substitution candidate**, and research effort on it
should be capped at the assessment in this file plus the licence question.


## Consultant A findings (2026-09-13)

_Recovered from the consultant's report: it reached its tool budget during the build/measurement phase and did not write this section itself. Evidence is reproducible from `~/repos/nucula-consult/build-log/` (build commands, IDF 5.4.1, rev 8ad0812, raw logs) and `~/repos/nucula-consult/src/build/`. Manager note: committed verbatim; not independently re-run._

**Revision assessed:** `main` @ `8ad081219c74f3e8372551738da37a08834835a9` (2026‑07‑20), tarball sha256 `53e6d5096a13b091f4755d3d6d588aca845c208da3bf72e503256aa43357c274`, unpacked to `~/repos/nucula-consult/src`.

### 1. NUT coverage (from source, not README)

| NUT | Status | Proof |
|---|---|---|
| 00 BDHKE + token codec (V3 `cashuA` / V4 `cashuB`) | implemented | `main/crypto.c:138 cashu_verify_dleq`, `main/cashu_json.cpp:459 deserialize_token_v3`, `main/cashu_cbor.cpp:144 "cashuB"` |
| 01 unit | implemented | `main/unit.cpp:29` (`sat/msat/btc/usd/eur/gbp/chf/jpy` + free-form) |
| 02 keysets, v1/v2 id derivation, fees | implemented | `main/keyset.cpp:22-70`, `main/wallet_keysets.cpp:146`, `main/wallet_selftest.cpp:55` |
| 03 swap | implemented | `main/wallet_flows.cpp:305 swap()` → `/v1/swap` (`:371`) |
| 04 mint | implemented (method-generic) | `wallet_flows.cpp:544/595/618`, `/v1/mint/quote/{method}` `:576`, `/v1/mint/{method}` `:648` |
| 05 melt | implemented | `wallet_flows.cpp:669/721/742`, `/v1/melt/quote/{method}` `:703`, `/v1/melt/{method}` `:797` |
| 06 mint info | implemented (RAM cache, not persisted) | `wallet_flows.cpp:28` `/v1/info`, `wallet.hpp:19-23` |
| **07 state check** | **ABSENT** | zero hits for `checkstate`/`state` endpoint anywhere in `main/` |
| 08 overpaid fee / blank change outputs | implemented (melt side only) | `wallet_flows.cpp:291 blank_output_count`, `:776` |
| **09 restore** | **ABSENT** | zero hits for `/v1/restore`; `commands_seed.cpp:64 restore` is BIP‑39 mnemonic restore, and it **erases all wallets** (`:79-88`) — no proof recovery from seed |
| 10 spending conditions | implemented (parse + simple lock) | `main/nut10.cpp:6 parse_nut10_secret` |
| 11 P2PK | implemented, **outgoing side only** — witness attach for own locks + refuse inputs locked to others (`wallet_flows.cpp:229-282`); does **not** verify incoming P2PK itself, delegates to mint |  |
| 12 DLEQ | implemented, required; Carol-side forwarded-proof verify present | `crypto.c:138/188/426/445`, `wallet_flows.cpp:483 verify_forwarded_dleq` |
| 13 deterministic secrets | implemented, V2 HMAC-SHA256 test vectors | `crypto.c:261-324`, `crypto_test.c:217`, `wallet_identity.cpp:28-107` |
| **14 HTLC** | **ABSENT** | zero hits |
| 18 payment requests | implemented (CBOR `creqA`, NFC/NDEF transport) | `cashu_cbor.cpp:391-674`, `ndef.cpp`, `nfc.cpp:164-187` |
| **19 cached endpoints / 20 quote signatures** | **ABSENT** | no `signature`/`pubkey` on `MintQuote`/`MeltQuote` (`cashu.hpp`); `cashu_json.hpp:11` states parse-only |
| 23 bolt11 | implemented, plus experimental PR#382 custom methods | `wallet_flows.cpp`, `cashu.hpp:132` |
| multi-mint | implemented, **max 3** (`MAX_MINTS=3`), one slot per mint, per-unit balances | `wallet_store.hpp:15`, `wallet_store.cpp:76` |
| offline receive stash/redeem | implemented | `wallet_nvs.cpp:339-483` (`pendn_/pend_`, `PEND_MAX=8`), drained in `nucula.cpp:31` |
| keyset v3 / BLS12‑381 | **scaffold only** (`can_mint=false`, stub ops) | `keyset.cpp:53-58`, `crypto_bls.c:83` |

### 2. Licence — no grant of rights
- Repo metadata `license = None`; `GET /repos/zeugmaster/nucula/license` → **404**.
- Checked commit history for `LICENSE`, `LICENSE.md`, `LICENSE.txt`, `COPYING`, `COPYING.md`, `license`, `LICENCE` → **0 commits on every path**: no licence file has ever existed, so none was removed.
- Zero `SPDX`/`Copyright`/`License` headers in first-party sources (grep over all `main/`). No `.github/` CI either.
- **Legal meaning:** default copyright (Berne + EU) — all rights reserved. No permission to copy, modify, link or redistribute. The repo *is* forkable on GitHub (ToS grant) but that grant expires the moment it leaves GitHub; it cannot be vendored into TollGate, and TollGate's own GPL‑3.0 does not cure this.
- **Minimal correct next step:** a GitHub issue to @zeugmaster requesting an explicit OSI licence (MIT or Apache‑2.0 for GPL‑3.0 compatibility; GPL‑3.0 acceptable; **GPL‑2.0‑only would be incompatible**). Until then, treat as unreadable. Note also the **secp256k1 submodule** is MIT (`COPYING` in `bitcoin-core/secp256k1` @ `ac561601b8a3…`) and Espressif's CBOR component is Apache‑2.0 — those do not cover nucula's own code.

### 3. Storage & key handling (NVS, plaintext)
- All wallet state = **ESP‑IDF NVS**, namespace `"wallet"` (`wallet_internal.hpp:17`), partition 0x9000 size **0x26000 (152 KiB)** (`partitions.csv`).
- Proofs: **single JSON blob** under `proofs_<slot>` (`wallet_nvs.cpp:81-129`) — rewritten whole on every mutation.
- Keysets: `kn_<slot>` count + `k_<slot>_<i>` blobs, cap `MAX_KEYSETS=10` with eviction (`wallet_internal.hpp:75`, `wallet_nvs.cpp:131-211`).
- Offline queue: `pendn_<slot>` u8 + `pend_<slot>_<i>` token strings, cap 8 (`wallet_nvs.cpp:305-311`).
- **Seed: plaintext NVS blob `"seed"` (64 bytes) plus the mnemonic as plaintext string `"mnemonic"`** (`wallet_identity.cpp:31-85`). `seed show` prints the mnemonic to the console (`commands_seed.cpp:24-33`).
- **No encryption at rest anywhere**: no AES/NVS‑encryption/secure‑boot calls in first-party code or `sdkconfig.defaults`; mbedTLS used only for SHA256/PBKDF2/base64.
- NUT‑11 P2PK key: independent of the seed, plaintext NVS `p2pk_priv`, generated once with `esp_fill_random` (`wallet_identity.cpp:110-178`) — and deliberately **not** erased by `seed wipe` (`:97`).
- NUT‑13 counters: `c_<13 hex chars of keyset id>` u32, committed per increment (`wallet_identity.cpp:194-220`).
- **Wear/crash consistency:** every op is a full-blob rewrite + `nvs_commit` (512‑byte sectors, copy-on-write blob index). NVS survives sudden power loss between entries because the index is written last, but **there is no atomic "commit proofs + increment counter" and no crash-consistent rollback**: a reset mid‑`swap` leaves the counter un-advanced with the old proofs still stored — safe for mint-side double-spend but not a journaled wallet. Blob size limit and write-amplification figures for NVS at multi-hundred-proof scale are **UNVERIFIED** (I could not find a first-party proof cap; `MAX_PROOFS` does not exist).

### 4. Footprint — **MEASURED** (build succeeded)
- Toolchain: ESP‑IDF **v5.4.1** at `~/esp/esp-idf` (pre‑existing). I ran `./install.sh esp32c3` — it downloaded `riscv32-esp-elf 14.2.0_20241119` (~298 MB), riscv GDB, openocd, rom‑elfs (~320 MB in `~/.espressif/dist`, 2.6 GB installed). `cmake`/`ninja` already present.
- Procedure: `cp main/wifi_config.example.h main/wifi_config.h`, `idf.py set-target esp32c3`, `idf.py build` → **EXIT=0**, clean build, no source patches needed (submodule fetched from `bitcoin-core/secp256k1` @ `ac561601b8a3…`). Logs: `~/repos/nucula-consult/build-log/{set-target,build}.log` (cwd `~/repos/nucula-consult/src`; build ran on **x86_64 Linux, IDF 5.4.1, rev 8ad0812**).
- **`build/nucula.bin` = 1 168 192 B (1.114 MiB)** sha256 `4f8f1ea5d523e572d51c6f2ba6b5af284be0ba721c21644b5c5762fb191fe765`; **`build/nucula.elf` = 16 326 920 B** sha256 `c8cf9ef890b07b71a38fc1e56d35452f5c02f06487e07c8098c1e4aacbb79023`; bootloader 20 832 B; partition table 3 072 B.
- Context: app partition is 0x1D0000 = **1 900 544 B**, so the image uses ~**61%**; `sdkconfig.defaults:28` claims ~40% headroom, so the reference `.bin` is larger than the author's last recorded figure (**UNVERIFIED** whether their number was pre- or post-optimisation).
- **Per-component sizes: UNVERIFIED** — I did not capture `idf.py size-components` before hitting the cap. Re-run needed for that column.

### 5. Portability verdict
- 11 449 LOC in `main/` (~12 167 including headers/components). Dependency classification I ran (include-header scan per file):
  - **Portable-ish / platform-agnostic core (~2 000 LOC excluding the 2 051-line BIP‑39 wordlist):** `cashu.hpp` (194), `wallet.hpp` (177), `crypto.h/c` (595), `hex` (58), `keyset.hpp` (51), `unit.hpp`, `cashu_suite.h` (83), `bip39.h`, `cashu_cbor.hpp`/`nut10.hpp`/`ndef.hpp`.
  - **Logic that is platform-neutral but uses esp logging / cJSON / esp_random:** `wallet_flows.cpp` (752), `cashu_cbor.cpp` (583 — uses TinyCBOR, portable lib), `cashu_json.cpp` (489 — uses cJSON, portable), `wallet_keysets.cpp`, `wallet_blind.cpp`, `wallet.cpp`, `keyset.cpp`, `unit.cpp`.
  - **Hard ESP‑IDF bound:** `wallet_nvs.cpp` (435, raw `nvs.h`), `wallet_identity.cpp` (NVS + `esp_random`), `http.c` (261, `esp_http_client` + `esp_crt_bundle`), `wifi.c` (163), `nfc.cpp`/`nci.c` (PN7160 NCI over `driver/i2c_master.h`), `display.cpp` (SSD1309), `keypad.c` (PCF8574), `console.cpp` (USB serial JTAG VFS), `wallet_store.cpp`/`ui.cpp` (FreeRTOS mutex/task), `nucula.cpp`.
- Concurrency is FreeRTOS semaphores throughout (`wallet_store.cpp:16`, `http.c:34`); no std::thread abstraction, so a Linux port would need a thread/mutex shim. `std::string`/`std::vector` and `emplace`-style C++ are fine on musl.
- **Verdict:** the *protocol* layer (NUT‑00/02/03/04/05/08/10/11/12/13/18/23 codecs and flow logic, roughly 3–4 kLOC) is genuinely reusable and shows unusually careful NUT understanding (per-unit handling, DLEQ‑required, method‑generic PR#382 quotes, definitely better than a naive firmware wallet). Turning it into a router‑side wallet, however, means rewriting: all persistence (NVS→file/sqlite), all key material handling (plaintext NVS→a real keystore), the HTTP transport (`esp_http_client`→libcurl), arg parsing (`commands_wallet.cpp`), and throwing away 100% of NFC/display/keypad/wifi, plus adding absent NUTs 07/09/14/19/20 — a C++→Go re‑implementation project, not a port. **It is fundamentally a device firmware product with a reusable Cashu logic core.**

### 6. Headline answer
**No — nucula cannot serve as a drop-in replacement for gonuts on the router, unambiguously.** It is a C++ ESP‑IDF firmware image for a Seeed XIAO ESP32‑C3 (`idf.py set-target esp32c3`, 1.11 MiB `.bin`), it has no Linux/OpenWrt build system, no C ABI, no licence, and it lacks NUT‑07 and NUT‑09 — which are precisely the recovery/consistency primitives the router's `WalletPort` acceptance contract will require. Its persistence is a 152 KiB NVS partition with plaintext seed+mnemonic and a whole-blob rewrite per operation.
**What nucula *is* good for:** (a) a reference implementation to read for a Go wallet's protocol edge cases (blank-output change sizing, keyset v1/v2 id derivation, per-unit proofs, offline P2PK stashing, forwarded-DLEQ Carol mode, method-generic NUT‑04/05); (b) a genuine end‑user tap‑to‑pay *device* product if TollGate ever wants one — but only after the licence question is settled in writing; (c) the BLS12‑381/v3‑keyset scaffold signals future NUT coverage that a Go wallet will eventually need. Recommended handling for this branch: cap nucula research here per the README's own guidance, and record the licence ask as the single blocking prerequisite.

**Bottom line for the manager:** nucula is out as a router substitution candidate; the reusable part is ideas, not code, until a licence exists. CDK stays the only serious router candidate, and the router‑wallet gaps nucula exposes (NUT‑07/09/restore + crash‑consistent storage + encrypted seed at rest) should be added to T1b's acceptance contract.

## Reframed scope (operator, 2026-09-13): port the wallet core to OpenWrt

The ESP32 assessment above answers a question we are no longer asking. The
operator's goal is explicit: **build the nucula wallet for OpenWrt targets, not
for ESP32.** So the deliverable is a *port*, and the research question becomes
"what does it cost to run nucula's wallet core as a Linux/musl program on the
router, and does it behave?"

That splits the codebase into three bands, which the work below must quantify:

| Band | What it is | Port treatment |
|---|---|---|
| **Protocol / wallet core** | `main/crypto.c`, `cashu_json.cpp`, `cashu_cbor.cpp`, `keyset.cpp`, `wallet_flows.cpp`, `unit.cpp` — NUT-00/01/02/03/04 and friends | the thing we actually want; must compile as a plain Linux library |
| **Platform services** | WiFi, HTTP client, NVS/flash storage, RNG, task/timer | must be replaced: sockets/curl, files, `getrandom`, pthreads |
| **Device peripherals** | PN7160 NFC, SSD1309 OLED, PCF8574 keypad menus | irrelevant on a router — excluded from the port, but their presence must not block the core build |

**Licence gate stays first.** No licence file has ever existed in that repo, so
nothing is shippable until the author grants rights. A port *spike* answers the
engineering question (cost, size, behaviour) and is worth doing; shipping any
ported code is not, until the licence question is settled. Task T2c covers asking.

**Port band map to produce:** every ESP-IDF header/API the core files touch,
with its proposed Linux replacement, and a count of how much of the core is
genuinely portable vs entangled. That inventory — not an opinion — is what tells
us whether this is a fortnight or a rewrite.
