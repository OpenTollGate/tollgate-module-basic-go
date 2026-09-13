/*
 * freertos/FreeRTOS.h — Linux/musl replacement for the FreeRTOS kernel types.
 *
 * Part of the nucula OpenWrt port spike. OUR shim.
 *
 * nucula uses four FreeRTOS primitives and nothing else from the kernel:
 *   - recursive mutexes (wallet_store.cpp — guards the wallet slot array)
 *   - a binary semaphore (http.c connection cache)
 *   - tasks (peripheral drivers, console reader — NOT part of the core port)
 *   - short vTaskDelay() sleeps
 *
 * Design note that matters for the port verdict: on ESP-IDF, FreeRTOS
 * mutex/semaphore calls BLOCK THE CALLING TASK without a syscall to a kernel
 * scheduler; on Linux they become futex syscalls. Both are cheap, but the
 * Linux variant can be interrupted by signals and can be priority-inverted,
 * which is why the shim backs them with pthread_mutex_t (already
 * priority-inheriting-capable, and recursive-mutex aware) rather than
 * hand-rolled futexes.
 *
 * Tick types are mapped 1 tick == 1 millisecond, matching
 * configTICK_RATE_HZ=1000 in nucula's sdkconfig.defaults, so pdMS_TO_TICKS()
 * is the identity and no call site needs touching.
 */
#pragma once

#include <stdint.h>
#include <stdbool.h>

typedef uint32_t TickType_t;
typedef int32_t  BaseType_t;
typedef uint32_t UBaseType_t;

/* Opaque handle types live here, not in the individual headers, because
 * nucula's translation units include task.h / semphr.h / queue.h in varying
 * orders and subsets (crypto_test.c includes only FreeRTOS.h + task.h). */
typedef void *SemaphoreHandle_t;
typedef void *QueueHandle_t;
typedef void *TaskHandle_t;

#define pdTRUE  ((BaseType_t)1)
#define pdFALSE ((BaseType_t)0)
#define pdPASS  pdTRUE
#define pdFAIL  pdFALSE

/* configTICK_RATE_HZ == 1000 in nucula's sdkconfig.defaults. */
#define configTICK_RATE_HZ 1000
#define pdMS_TO_TICKS(ms)  ((TickType_t)(ms))
#define portTICK_PERIOD_MS (1000 / configTICK_RATE_HZ)

/* portMAX_DELAY is ~2^32-1 ticks on FreeRTOS; on Linux it means "block
 * forever", so the shim maps it to the sentinel the pthread helper reads. */
#define portMAX_DELAY ((TickType_t)0xFFFFFFFFu)
#define portTICK_TYPE_IS_ATOMIC 1

#define pdTICKS_TO_MS(t) ((TickType_t)(t))

#ifdef __cplusplus
extern "C" {
#endif

/* FreeRTOS schedules one task per logical core on Linux; expose the count so
 * call sites can log it, but nothing in the core depends on it. */
uint32_t nucula_shim_cpu_count(void);

#ifdef __cplusplus
}
#endif
