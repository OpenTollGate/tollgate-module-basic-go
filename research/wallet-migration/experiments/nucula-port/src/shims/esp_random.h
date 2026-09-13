/*
 * esp_random.h — Linux/musl replacement for the ESP-IDF hardware RNG.
 *
 * Part of the nucula OpenWrt port spike. OUR shim.
 *
 * ESP-IDF: esp_random() reads the ESP32-C3's Wi-Fi/Bluetooth hardware RNG
 * (RNG_DATA_REG), which the SDK guarantees is a true entropy source seeded
 * from RF noise; esp_fill_random() loops over it in words.
 *
 * Linux/musl: getrandom(2) (musl implements it as the syscall, falling back
 * to /dev/urandom on old kernels). Blocking semantics are appropriate here:
 * nucula calls esp_fill_random() to produce the NUT-13 blinded secret and
 * blinding factor, where a predictable value is a funds-loss bug, so we use
 * flags=0 (block until the pool is initialised) rather than GRND_NONBLOCK.
 */
#pragma once

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

uint32_t esp_random(void);
void esp_fill_random(void *buf, size_t len);

#ifdef __cplusplus
}
#endif
