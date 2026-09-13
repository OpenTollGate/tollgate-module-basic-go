/*
 * esp_err.h — Linux/musl replacement for the ESP-IDF error-code header.
 *
 * Part of the nucula OpenWrt port spike (research/wallet-migration,
 * experiments/nucula-port). This file is OUR shim, not nucula's code: it
 * reproduces just enough of the ESP-IDF ABI for the wallet core to compile
 * unchanged. Nothing here is a claim about ESP-IDF's real implementation.
 *
 * Upstream (ESP-IDF v5.4.1) semantics preserved:
 *   - esp_err_t is a signed int; ESP_OK == 0, every error is non-zero.
 *   - ESP_ERR_NVS_* live in the 0x1100 block.
 *   - esp_err_to_name() returns a stable human string used by nucula's log
 *     lines (wallet_keysets.cpp uses it verbatim).
 */
#pragma once

#include <stdint.h>

typedef int esp_err_t;

#define ESP_OK                  0
#define ESP_FAIL                (-1)

/* Generic error block (ESP-IDF esp_err.h, 0x100 range). */
#define ESP_ERR_NO_MEM          0x101
#define ESP_ERR_INVALID_ARG     0x102
#define ESP_ERR_INVALID_STATE   0x103
#define ESP_ERR_INVALID_SIZE    0x104
#define ESP_ERR_NOT_FOUND       0x105
#define ESP_ERR_NOT_SUPPORTED   0x106
#define ESP_ERR_TIMEOUT         0x107
#define ESP_ERR_INVALID_RESPONSE 0x108
#define ESP_ERR_INVALID_CRC     0x109
#define ESP_ERR_INVALID_VERSION 0x10A
#define ESP_ERR_INVALID_MAC     0x10B

/* NVS error block (ESP-IDF nvs.h, 0x1100 base). */
#define ESP_ERR_NVS_BASE                    0x1100
#define ESP_ERR_NVS_NOT_INITIALIZED         (ESP_ERR_NVS_BASE + 0x01)
#define ESP_ERR_NVS_NOT_FOUND               (ESP_ERR_NVS_BASE + 0x02)
#define ESP_ERR_NVS_TYPE_MISMATCH           (ESP_ERR_NVS_BASE + 0x03)
#define ESP_ERR_NVS_READ_ONLY               (ESP_ERR_NVS_BASE + 0x04)
#define ESP_ERR_NVS_NOT_ENOUGH_SPACE        (ESP_ERR_NVS_BASE + 0x05)
#define ESP_ERR_NVS_INVALID_NAME            (ESP_ERR_NVS_BASE + 0x06)
#define ESP_ERR_NVS_INVALID_HANDLE          (ESP_ERR_NVS_BASE + 0x07)
#define ESP_ERR_NVS_REMOVE_FAILED           (ESP_ERR_NVS_BASE + 0x08)
#define ESP_ERR_NVS_KEY_TOO_LONG            (ESP_ERR_NVS_BASE + 0x09)
#define ESP_ERR_NVS_PAGE_FULL               (ESP_ERR_NVS_BASE + 0x0A)
#define ESP_ERR_NVS_INVALID_STATE           (ESP_ERR_NVS_BASE + 0x0B)
#define ESP_ERR_NVS_INVALID_LENGTH          (ESP_ERR_NVS_BASE + 0x0C)
#define ESP_ERR_NVS_NO_FREE_PAGES           (ESP_ERR_NVS_BASE + 0x0D)
#define ESP_ERR_NVS_VALUE_TOO_LONG          (ESP_ERR_NVS_BASE + 0x0E)
#define ESP_ERR_NVS_PART_NOT_FOUND          (ESP_ERR_NVS_BASE + 0x0F)
#define ESP_ERR_NVS_NEW_VERSION_FOUND       (ESP_ERR_NVS_BASE + 0x10)

/* Wi-Fi block (ESP-IDF esp_wifi.h). Kept only so the header is complete. */
#define ESP_ERR_WIFI_BASE       0x3000
#define ESP_ERR_WIFI_CONN       (ESP_ERR_WIFI_BASE + 0x100)

#ifdef __cplusplus
extern "C" {
#endif

const char *esp_err_to_name(esp_err_t code);

#ifdef __cplusplus
}
#endif
