/*
 * esp_timer.h — Linux/musl replacement for the ESP-IDF high-resolution timer.
 *
 * Part of the nucula OpenWrt port spike. OUR shim.
 *
 * ESP-IDF: esp_timer_get_time() returns monotonic microseconds since boot,
 * backed by the systimer/LAC timer. Linux: CLOCK_MONOTONIC via
 * clock_gettime(2), which musl reads through vDSO — same cost class.
 *
 * NOTE (monotonicity): ESP-IDF's value is microseconds since *boot*; ours is
 * microseconds since the *epoch of the monotonic clock*, i.e. roughly since
 * boot too. nucula only diffs the value (http.c timeout accounting,
 * crypto_test.c benchmark), so the absolute origin is irrelevant.
 */
#pragma once

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

int64_t esp_timer_get_time(void);

#ifdef __cplusplus
}
#endif
