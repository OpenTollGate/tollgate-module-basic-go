/*
 * esp_system.h — Linux/musl replacement for the ESP-IDF system header.
 *
 * Part of the nucula OpenWrt port spike. OUR shim. Covers only what the
 * wallet core touches: esp_restart() (nucula.cpp's panic path) and the heap
 * queries re-exported by the IDF umbrella header.
 */
#pragma once

#include "esp_heap_caps.h"

#ifdef __cplusplus
extern "C" {
#endif

/* Never returns. Exists so the core links; a router daemon should exit and
 * let procd restart it rather than reset the SoC. */
void esp_restart(void) __attribute__((noreturn));

#ifdef __cplusplus
}
#endif
