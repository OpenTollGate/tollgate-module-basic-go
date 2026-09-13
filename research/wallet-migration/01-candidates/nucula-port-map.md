# nucula port map — ESP-IDF boundary inventory (measured, 2026-09-13)

Source: nucula rev `8ad081219c74f3e8372551738da37a08834835a9`, fetched by
`experiments/nucula-port/fetch-nucula.sh` (tarball sha256
`53e6d5096a13b091f4755d3d6d588aca845c208da3bf72e503256aa43357c274`).
No nucula source is committed to this branch — only our shims, scripts and logs.

## Portable vs entangled (from `experiments/nucula-port/raw/inventory.txt`)

| Measure | Value |
|---|---|
| `main/` `.c`/`.cpp` total | **9 163 lines** |
| ESP-IDF symbol references in `main/*` | **735** |
| Files including an ESP-IDF header | **35** |
| Files including none | 30 |
| **Files that compiled verbatim on Linux with ZERO source patches** | **20 files, ~5 470 lines** |
| Replacement layer we wrote | **~1 280 lines** (`esp_shims.c` ~280, `freertos_shim.c` ~330, `nvs_file.cpp` ~340, `http_linux.c` ~330) |
| Excluded as device-entangled | **~3 290 lines** (`wifi.c`, `nfc.cpp`, `ndef.cpp`, `display.cpp`, `keypad.c`, `i2c_bus.c`, `console.cpp`, `ui.cpp`, `nucula.cpp`, `commands_*.cpp`) |

**Compiled untouched on Linux** — the whole protocol/wallet layer:
`crypto.c`, `crypto_bls.c`, `crypto_test.c`, `hex.c`, `base64url.cpp`,
`cashu_json.cpp`, `cashu_cbor.cpp`, `keyset.cpp`, `unit.cpp`, `nut10.cpp`,
`bip39.c`, `wallet.cpp`, `wallet_flows.cpp`, `wallet_keysets.cpp`,
`wallet_blind.cpp`, `wallet_identity.cpp`, `wallet_nvs.cpp`, `wallet_store.cpp`,
`wallet_selftest.cpp`, `selftest.cpp`.

That is the headline result: **the wallet logic is portable; only the platform
services and the device peripherals are not.**

## The boundary, API by API

| ESP-IDF surface | Usage (verified call sites) | Our Linux replacement |
|---|---|---|
| `esp_log.h` | `ESP_LOGx` ×375, `esp_err_t` ×47 | `esp_shims.c` → stdio/syslog |
| `nvs.h` / `nvs_flash.h` | 71 refs, concentrated in `wallet_nvs.cpp` | `nvs_file.cpp` — a file-backed key/value store with NVS semantics |
| `esp_random.h` | `esp_random`, `esp_fill_random` | `getrandom(2)` |
| `esp_timer.h` | `esp_timer_get_time` | `clock_gettime(CLOCK_MONOTONIC)` |
| FreeRTOS | one recursive mutex + one queue | `freertos_shim.c` → pthread mutex/cond + a safe queue |
| `esp_http_client.h` / `esp_crt_bundle.h` | all mint traffic | `http_linux.c` → sockets; optional libcurl backend exists (`-DNUCULA_HTTP_CURL=ON`) but is untested |
| `mbedtls/*` | sha256, md, pkcs5, base64 | OpenWrt `libmbedtls21 3.6.7` (feed) or OpenSSL |
| `cJSON` | JSON parsing | OpenWrt `cJSON 1.7.19` (feed) |
| Espressif `cbor` | CBOR (V4 tokens) | **TinyCBOR 0.6.0** in ESP-IDF — note the feed ships `libcbor0 0.13.0`, which is a *different* library |
| `libsecp256k1` (submodule) | BDHKE, DLEQ | **not present in the OpenWrt feeds checked** → vendor from the submodule |

## Feed availability, OpenWrt 25.12.0 / aarch64_cortex-a53 (verified)

`cJSON 1.7.19` ✔ · `libcurl4 8.21.0` ✔ · `libmbedtls21 3.6.7` ✔ ·
`libopenssl3 3.5.7` ✔ · `libcbor0 0.13.0` ⚠ (not TinyCBOR) ·
`libsecp256k1` ✖ (vendor it).

## Hazards found (all with evidence, none estimated)

1. **`int64_t` format divergence.** On gcc-14/x86_64 `int64_t` is `long`; on
   IDF/riscv it is `long long`. **17 `%lld`/`%llu` call sites** therefore warn —
   undefined behaviour risk on variadic alignment. Not fixed; needs a portability
   audit before any cross-build is trusted.
2. **Whole-blob storage rewrite** (see the verdict in `01-candidates/nucula.md`):
   the entire proof set is re-serialised on every mutation, which is a flash-wear
   problem on a router, not just a performance one.
3. **TinyCBOR vs libcbor**: the feed's CBOR library is not the one nucula uses, so
   either the dependency is vendored or the CBOR layer is adapted.
4. **`libsecp256k1` must be vendored**, adding a build dependency to every router
   build.

## Remaining work on this map

- aarch64 musl cross-build (SDK obtained and extracted, build not run) and mipsel
- per-symbol audit of the 735 IDF references against this table
- `smaps_rollup`/PSS sampling, and a harness for the libcurl backend
