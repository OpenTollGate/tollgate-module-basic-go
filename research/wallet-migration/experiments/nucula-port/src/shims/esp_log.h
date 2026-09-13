/*
 * esp_log.h — Linux/musl replacement for the ESP-IDF logging macros.
 *
 * Part of the nucula OpenWrt port spike. OUR shim.
 *
 * ESP-IDF's ESP_LOGx(TAG, fmt, ...) macros expand to a call into the IDF log
 * subsystem (per-tag level filtering, task name, timestamp, colour codes).
 * On a router none of that exists, so the shim writes to stderr and filters
 * on a single process-wide threshold, overridable with NUCULA_LOG_LEVEL
 * (0=none 1=error 2=warn 3=info 4=debug 5=verbose, default 3).
 *
 * Call-site compatibility note: nucula passes a TAG that is usually a plain
 * string literal but in places a `static const char*`. The macro therefore
 * takes the tag as an expression and passes it as const char*.
 */
#pragma once

#include <stdarg.h>
#include <stdio.h>

typedef enum {
    ESP_LOG_NONE = 0,
    ESP_LOG_ERROR,
    ESP_LOG_WARN,
    ESP_LOG_INFO,
    ESP_LOG_DEBUG,
    ESP_LOG_VERBOSE,
} esp_log_level_t;

#ifdef __cplusplus
extern "C" {
#endif

/* Returns the effective threshold (env-parsed once, then cached). */
int nucula_shim_log_level(void);
void esp_log_level_set(const char *tag, esp_log_level_t level);

/* Not for direct use; the macros below call it. */
void nucula_shim_log(char lvl, const char *tag, const char *fmt, ...)
    __attribute__((format(printf, 3, 4)));

#ifdef __cplusplus
}
#endif

#define ESP_LOGE(tag, fmt, ...) nucula_shim_log('E', (tag), (fmt), ##__VA_ARGS__)
#define ESP_LOGW(tag, fmt, ...) nucula_shim_log('W', (tag), (fmt), ##__VA_ARGS__)
#define ESP_LOGI(tag, fmt, ...) nucula_shim_log('I', (tag), (fmt), ##__VA_ARGS__)
#define ESP_LOGD(tag, fmt, ...) nucula_shim_log('D', (tag), (fmt), ##__VA_ARGS__)
#define ESP_LOGV(tag, fmt, ...) nucula_shim_log('V', (tag), (fmt), ##__VA_ARGS__)

/* ESP-IDF convenience: ESP_ERROR_CHECK(expr) aborts on non-ESP_OK. */
#define ESP_ERROR_CHECK(x)                                                     \
    do {                                                                       \
        esp_err_t err_rc_ = (x);                                               \
        if (err_rc_ != ESP_OK) {                                               \
            nucula_shim_log('E', "ESP_ERROR_CHECK",                            \
                            "%s failed: %s", #x, esp_err_to_name(err_rc_));    \
            __builtin_trap();                                                  \
        }                                                                      \
    } while (0)

#define ESP_ERROR_CHECK_WITHOUT_ABORT(x) (x)
