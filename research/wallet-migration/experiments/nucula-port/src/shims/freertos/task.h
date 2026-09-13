/*
 * freertos/task.h — Linux/musl replacement for FreeRTOS tasks.
 *
 * Part of the nucula OpenWrt port spike. OUR shim, backed by pthreads.
 *
 * IMPORTANT for the port verdict: every xTaskCreate() call site in nucula is
 * in a peripheral or platform file (display.cpp, nfc.cpp, ui.cpp, console.cpp,
 * commands_wallet.cpp), NOT in the wallet/protocol core. The core is
 * single-threaded and blocking. This shim exists so those files *could* be
 * ported to a threaded Linux daemon later; the port spike itself never calls
 * xTaskCreate() from the core.
 */
#pragma once

#include "FreeRTOS.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef void (*TaskFunction_t)(void *);
BaseType_t xTaskCreate(TaskFunction_t fn, const char *name, uint32_t stack_words,
                       void *arg, UBaseType_t prio, TaskHandle_t *out);
void vTaskDelete(TaskHandle_t h);
void vTaskDelay(TickType_t ticks);
TickType_t xTaskGetTickCount(void);
void taskYIELD(void);
const char *pcTaskGetName(TaskHandle_t h);

#ifdef __cplusplus
}
#endif
