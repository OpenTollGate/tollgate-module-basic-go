/*
 * nvs.h — Linux/musl replacement for the ESP-IDF Non-Volatile Storage API.
 *
 * Part of the nucula OpenWrt port spike. OUR shim (implementation lives in
 * src/platform/nvs_file.cpp).
 *
 * This is the single largest semantic substitution in the port. What changes:
 *
 *  ESP-IDF NVS                          |  this shim
 *  -------------------------------------|-----------------------------------
 *  key/value store inside a flash       |  one directory per namespace, one
 *  partition (0x9000, 0x26000 = 152 KiB)|  file per key under NUCULA_NVS_DIR
 *  wear-levelled pages + COW blob index |  the filesystem's (JFFS2/UBIFS on a
 *  nvs_commit() flushes a dirty page    |  router) own journaling; commit is
 *                                       |  a no-op because writes are
 *                                       |  write-through + fsync'd
 *  keys are <=15 chars, no '/'          |  keys are hex-encoded into filenames
 *  blob writes are atomic at page level |  atomic via write-temp + rename(2)
 *
 * Everything the shim does NOT reproduce (and that the port verdict must
 * carry): flash wear accounting, the 152 KiB partition ceiling, the NVS
 * "no space" failure mode, encrypted NVS, and the crash-consistency
 * guarantees of copy-on-write pages. A file store is *not* a drop-in
 * replacement for a wear-levelled flash store on a router whose flash has a
 * ~10^5 erase-cycle budget — see nucula-port-map.md.
 *
 * The C ABI below is API-identical to ESP-IDF v5.4.1 for the functions nucula
 * calls, so no nucula call site is edited.
 */
#pragma once

#include <stddef.h>
#include <stdint.h>
#include "esp_err.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef uint32_t nvs_handle_t;

typedef enum {
    NVS_READONLY = 0,
    NVS_READWRITE = 1,
} nvs_open_mode_t;

/* Namespace lifecycle. */
esp_err_t nvs_flash_init(void);
esp_err_t nvs_flash_erase(void);
esp_err_t nvs_flash_deinit(void);

esp_err_t nvs_open(const char *namespace_name, nvs_open_mode_t open_mode,
                   nvs_handle_t *out_handle);
void nvs_close(nvs_handle_t handle);

/* ESP-IDF flushes dirty state; the shim is write-through, so this only
 * verifies the handle. */
esp_err_t nvs_commit(nvs_handle_t handle);

esp_err_t nvs_erase_key(nvs_handle_t handle, const char *key);
esp_err_t nvs_erase_all(nvs_handle_t handle);

/* Strings: *length is the buffer size in, bytes-written-including-NUL out.
 * With out_value == NULL the call returns ESP_OK and *length = strlen+1. */
esp_err_t nvs_get_str(nvs_handle_t handle, const char *key, char *out_value,
                      size_t *length);
esp_err_t nvs_set_str(nvs_handle_t handle, const char *key, const char *value);

/* Blobs: *length is capacity in, bytes-written out; probe form as above. */
esp_err_t nvs_get_blob(nvs_handle_t handle, const char *key, void *out_value,
                       size_t *length);
esp_err_t nvs_set_blob(nvs_handle_t handle, const char *key, const void *value,
                       size_t length);

/* Fixed-width integers (stored native-endian, like ESP-IDF). */
esp_err_t nvs_get_u8(nvs_handle_t handle, const char *key, uint8_t *out_value);
esp_err_t nvs_set_u8(nvs_handle_t handle, const char *key, uint8_t value);
esp_err_t nvs_get_u16(nvs_handle_t handle, const char *key, uint16_t *out_value);
esp_err_t nvs_set_u16(nvs_handle_t handle, const char *key, uint16_t value);
esp_err_t nvs_get_u32(nvs_handle_t handle, const char *key, uint32_t *out_value);
esp_err_t nvs_set_u32(nvs_handle_t handle, const char *key, uint32_t value);
esp_err_t nvs_get_i32(nvs_handle_t handle, const char *key, int32_t *out_value);
esp_err_t nvs_set_i32(nvs_handle_t handle, const char *key, int32_t value);

/* Shim-only introspection used by the measurement harness. */
uint64_t nucula_nvs_bytes_written(void);
void nucula_nvs_reset_stats(void);
const char *nucula_nvs_dir(void);

#ifdef __cplusplus
}
#endif
