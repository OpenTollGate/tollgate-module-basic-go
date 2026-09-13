/*
 * freertos/semphr.h — Linux/musl replacement for FreeRTOS semaphores.
 *
 * Part of the nucula OpenWrt port spike. OUR shim, backed by pthreads.
 *
 * ABI compatibility is the point: nucula's call sites are unchanged. The
 * handle types differ only in name.
 */
#pragma once

#include "FreeRTOS.h"

#ifdef __cplusplus
extern "C" {
#endif

/* Recursive mutex (wallet_store.cpp). pthread_mutexattr_settype(
 * PTHREAD_MUTEX_RECURSIVE). */
SemaphoreHandle_t xSemaphoreCreateRecursiveMutex(void);
BaseType_t xSemaphoreTakeRecursive(SemaphoreHandle_t h, TickType_t ticks);
BaseType_t xSemaphoreGiveRecursive(SemaphoreHandle_t h);

/* Plain mutex / binary semaphore. */
SemaphoreHandle_t xSemaphoreCreateMutex(void);
SemaphoreHandle_t xSemaphoreCreateBinary(void);
BaseType_t xSemaphoreTake(SemaphoreHandle_t h, TickType_t ticks);
BaseType_t xSemaphoreGive(SemaphoreHandle_t h);

void vSemaphoreDelete(SemaphoreHandle_t h);

#ifdef __cplusplus
}
#endif
