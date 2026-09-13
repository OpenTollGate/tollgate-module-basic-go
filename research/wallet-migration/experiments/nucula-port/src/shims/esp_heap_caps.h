/*
 * esp_heap_caps.h — Linux/musl replacement for the ESP-IDF heap introspection.
 *
 * Part of the nucula OpenWrt port spike. OUR shim.
 *
 * ESP-IDF exposes several heaps (internal DRAM, IRAM, PSRAM, RTC) selected by
 * MALLOC_CAP_* flags, plus size queries. On Linux there is exactly one malloc
 * arena, so:
 *   - esp_get_free_heap_size() / esp_get_minimum_free_heap_size() report the
 *     process's own allocator state (mallinfo2) rather than a device heap;
 *     nucula uses them only for informational logging (commands_system.cpp).
 *   - heap_caps_get_largest_free_block() reports the largest single free
 *     block, which is the closest honest Linux analogue (it is what glibc's
 *     top-chunk / a mmap'd arena would give).
 *   - MALLOC_CAP_* flags are accepted and ignored.
 *
 * This is a *reporting* shim: nothing in the wallet's correctness path
 * depends on these numbers, so degradation here is not a port blocker.
 */
#pragma once

#include <stddef.h>
#include <stdint.h>

#define MALLOC_CAP_8BIT      (1 << 2)
#define MALLOC_CAP_DMA       (1 << 3)
#define MALLOC_CAP_SPIRAM    (1 << 10)
#define MALLOC_CAP_INTERNAL  (1 << 11)
#define MALLOC_CAP_DEFAULT   (1 << 12)
#define MALLOC_CAP_IRAM_8BIT (1 << 13)

#ifdef __cplusplus
extern "C" {
#endif

size_t esp_get_free_heap_size(void);
size_t esp_get_minimum_free_heap_size(void);
size_t heap_caps_get_largest_free_block(uint32_t caps);
size_t heap_caps_get_free_size(uint32_t caps);

#ifdef __cplusplus
}
#endif
