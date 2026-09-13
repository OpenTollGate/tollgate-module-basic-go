/*
 * freertos/queue.h — Linux/musl replacement for FreeRTOS queues.
 *
 * Part of the nucula OpenWrt port spike. OUR shim.
 *
 * nucula uses exactly one queue (keypad.c → ui.cpp key events). It is not on
 * the wallet core path; the shim is a bounded circular byte queue guarded by a
 * mutex + condition variable, enough for the call sites to compile and run.
 */
#pragma once

#include "FreeRTOS.h"

#ifdef __cplusplus
extern "C" {
#endif

QueueHandle_t xQueueCreate(UBaseType_t length, UBaseType_t item_size);
BaseType_t xQueueSend(QueueHandle_t q, const void *item, TickType_t ticks);
BaseType_t xQueueSendToBack(QueueHandle_t q, const void *item, TickType_t ticks);
BaseType_t xQueueReceive(QueueHandle_t q, void *out, TickType_t ticks);
void vQueueDelete(QueueHandle_t q);

#ifdef __cplusplus
}
#endif
