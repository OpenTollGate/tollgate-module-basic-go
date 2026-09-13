/*
 * freertos_shim.c — FreeRTOS kernel primitives on pthreads.
 *
 * Part of the nucula OpenWrt port spike. OUR code.
 *
 * Only the primitives nucula actually calls are implemented. The mutex family
 * is backed by PTHREAD_MUTEX_RECURSIVE because wallet_store.cpp takes the
 * wallet lock recursively (wallet_store_guard nests inside functions that are
 * themselves called under a guard) and FreeRTOS's
 * xSemaphoreCreateRecursiveMutex has exactly those semantics.
 *
 * Tick == millisecond (configTICK_RATE_HZ 1000 in nucula's sdkconfig), so
 * pdMS_TO_TICKS is the identity and portMAX_DELAY maps to "no timeout".
 */
#define _GNU_SOURCE
#include "freertos/FreeRTOS.h"
#include "freertos/semphr.h"
#include "freertos/task.h"
#include "freertos/queue.h"

#include <errno.h>
#include <pthread.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

/* ---------------- semaphores / mutexes ---------------- */

typedef struct {
    pthread_mutex_t m;
    pthread_cond_t  c;
    int             is_binary;
    int             taken;     /* for the binary-semaphore behaviour */
} shim_sem_t;

static shim_sem_t *as_sem(SemaphoreHandle_t h) { return (shim_sem_t *)h; }

static int ticks_to_abs_timeout(TickType_t ticks, struct timespec *ts)
{
    if (ticks == portMAX_DELAY) return 0; /* block forever */
    clock_gettime(CLOCK_REALTIME, ts);
    ts->tv_sec  += (time_t)(ticks / 1000u);
    ts->tv_nsec += (long)(ticks % 1000u) * 1000000L;
    if (ts->tv_nsec >= 1000000000L) { ts->tv_sec++; ts->tv_nsec -= 1000000000L; }
    return 1;
}

SemaphoreHandle_t xSemaphoreCreateRecursiveMutex(void)
{
    shim_sem_t *s = (shim_sem_t *)calloc(1, sizeof *s);
    if (!s) return NULL;
    pthread_mutexattr_t a;
    pthread_mutexattr_init(&a);
    pthread_mutexattr_settype(&a, PTHREAD_MUTEX_RECURSIVE);
    if (pthread_mutex_init(&s->m, &a) != 0) { free(s); return NULL; }
    pthread_mutexattr_destroy(&a);
    pthread_cond_init(&s->c, NULL);
    return (SemaphoreHandle_t)s;
}

BaseType_t xSemaphoreTakeRecursive(SemaphoreHandle_t h, TickType_t ticks)
{
    shim_sem_t *s = as_sem(h);
    if (!s) return pdFALSE;
    if (ticks == portMAX_DELAY) {
        return pthread_mutex_lock(&s->m) == 0 ? pdTRUE : pdFALSE;
    }
    struct timespec ts;
    if (!ticks_to_abs_timeout(ticks, &ts))
        return pthread_mutex_lock(&s->m) == 0 ? pdTRUE : pdFALSE;
    int rc = pthread_mutex_timedlock(&s->m, &ts);
    return rc == 0 ? pdTRUE : pdFALSE;
}

BaseType_t xSemaphoreGiveRecursive(SemaphoreHandle_t h)
{
    shim_sem_t *s = as_sem(h);
    if (!s) return pdFALSE;
    return pthread_mutex_unlock(&s->m) == 0 ? pdTRUE : pdFALSE;
}

SemaphoreHandle_t xSemaphoreCreateMutex(void)
{
    return xSemaphoreCreateRecursiveMutex();
}

SemaphoreHandle_t xSemaphoreCreateBinary(void)
{
    shim_sem_t *s = (shim_sem_t *)calloc(1, sizeof *s);
    if (!s) return NULL;
    pthread_mutex_init(&s->m, NULL);
    pthread_cond_init(&s->c, NULL);
    s->is_binary = 1;
    s->taken = 1;   /* FreeRTOS binary semaphores start EMPTY: first Take
                     * blocks until a Give. */
    return (SemaphoreHandle_t)s;
}

BaseType_t xSemaphoreTake(SemaphoreHandle_t h, TickType_t ticks)
{
    shim_sem_t *s = as_sem(h);
    if (!s) return pdFALSE;
    if (!s->is_binary) return xSemaphoreTakeRecursive(h, ticks);

    pthread_mutex_lock(&s->m);
    while (s->taken) {
        if (ticks == 0) { pthread_mutex_unlock(&s->m); return pdFALSE; }
        struct timespec ts;
        int rc;
        if (ticks == portMAX_DELAY) {
            rc = pthread_cond_wait(&s->c, &s->m);
        } else {
            ticks_to_abs_timeout(ticks, &ts);
            rc = pthread_cond_timedwait(&s->c, &s->m, &ts);
        }
        if (rc != 0) { pthread_mutex_unlock(&s->m); return pdFALSE; }
    }
    s->taken = 1;
    pthread_mutex_unlock(&s->m);
    return pdTRUE;
}

BaseType_t xSemaphoreGive(SemaphoreHandle_t h)
{
    shim_sem_t *s = as_sem(h);
    if (!s) return pdFALSE;
    if (!s->is_binary) return xSemaphoreGiveRecursive(h);
    pthread_mutex_lock(&s->m);
    s->taken = 0;
    pthread_cond_signal(&s->c);
    pthread_mutex_unlock(&s->m);
    return pdTRUE;
}

void vSemaphoreDelete(SemaphoreHandle_t h)
{
    shim_sem_t *s = as_sem(h);
    if (!s) return;
    pthread_mutex_destroy(&s->m);
    pthread_cond_destroy(&s->c);
    free(s);
}

/* ---------------- tasks ---------------- */

typedef struct {
    TaskFunction_t fn;
    void          *arg;
    char           name[16];
} shim_task_t;

static void *task_trampoline(void *p)
{
    shim_task_t *t = (shim_task_t *)p;
    TaskFunction_t fn = t->fn;
    void *arg = t->arg;
    free(t);
    fn(arg);
    return NULL;
}

BaseType_t xTaskCreate(TaskFunction_t fn, const char *name, uint32_t stack_words,
                       void *arg, UBaseType_t prio, TaskHandle_t *out)
{
    (void)prio;
    shim_task_t *t = (shim_task_t *)calloc(1, sizeof *t);
    if (!t) return pdFALSE;
    t->fn = fn;
    t->arg = arg;
    if (name) {
        strncpy(t->name, name, sizeof t->name - 1);
        t->name[sizeof t->name - 1] = '\0';
    }
    pthread_t th;
    pthread_attr_t a;
    pthread_attr_init(&a);
    /* ESP-IDF stack is in words; pthread wants bytes. Never shrink below the
     * musl default, which is already generous. */
    size_t bytes = (size_t)stack_words * sizeof(uint32_t);
    if (bytes >= PTHREAD_STACK_MIN) pthread_attr_setstacksize(&a, bytes);
    int rc = pthread_create(&th, &a, task_trampoline, t);
    pthread_attr_destroy(&a);
    if (rc != 0) { free(t); return pdFALSE; }
    pthread_detach(th);
    if (out) *out = (TaskHandle_t)(uintptr_t)th;
    return pdTRUE;
}

void vTaskDelete(TaskHandle_t h)
{
    if (h) return;   /* detached threads: only self-delete is meaningful */
    pthread_exit(NULL);
}

void vTaskDelay(TickType_t ticks)
{
    struct timespec ts;
    ts.tv_sec = (time_t)(ticks / 1000u);
    ts.tv_nsec = (long)(ticks % 1000u) * 1000000L;
    nanosleep(&ts, NULL);
}

TickType_t xTaskGetTickCount(void)
{
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (TickType_t)((int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000);
}

void taskYIELD(void) { sched_yield(); }

const char *pcTaskGetName(TaskHandle_t h) { (void)h; return "shim"; }

/* ---------------- queues ---------------- */

typedef struct {
    pthread_mutex_t m;
    pthread_cond_t  not_empty;
    pthread_cond_t  not_full;
    unsigned char  *buf;
    UBaseType_t     len, item_size, head, count;
} shim_queue_t;

QueueHandle_t xQueueCreate(UBaseType_t length, UBaseType_t item_size)
{
    shim_queue_t *q = (shim_queue_t *)calloc(1, sizeof *q);
    if (!q) return NULL;
    q->buf = (unsigned char *)calloc(length ? length : 1, item_size ? item_size : 1);
    if (!q->buf) { free(q); return NULL; }
    pthread_mutex_init(&q->m, NULL);
    pthread_cond_init(&q->not_empty, NULL);
    pthread_cond_init(&q->not_full, NULL);
    q->len = length;
    q->item_size = item_size;
    return (QueueHandle_t)q;
}

BaseType_t xQueueSend(QueueHandle_t hv, const void *item, TickType_t ticks)
{
    shim_queue_t *q = (shim_queue_t *)hv;
    if (!q) return pdFALSE;
    struct timespec ts;
    int have_deadline = ticks != portMAX_DELAY && ticks_to_abs_timeout(ticks, &ts);
    pthread_mutex_lock(&q->m);
    while (q->count == q->len) {
        if (ticks == 0) { pthread_mutex_unlock(&q->m); return pdFALSE; }
        int rc = have_deadline ? pthread_cond_timedwait(&q->not_full, &q->m, &ts)
                               : pthread_cond_wait(&q->not_full, &q->m);
        if (rc != 0) { pthread_mutex_unlock(&q->m); return pdFALSE; }
    }
    memcpy(q->buf + ((q->head + q->count) % q->len) * q->item_size, item, q->item_size);
    q->count++;
    pthread_cond_signal(&q->not_empty);
    pthread_mutex_unlock(&q->m);
    return pdTRUE;
}

BaseType_t xQueueSendToBack(QueueHandle_t q, const void *item, TickType_t ticks)
{
    return xQueueSend(q, item, ticks);
}

BaseType_t xQueueReceive(QueueHandle_t hv, void *out, TickType_t ticks)
{
    shim_queue_t *q = (shim_queue_t *)hv;
    if (!q) return pdFALSE;
    struct timespec ts;
    int have_deadline = ticks != portMAX_DELAY && ticks_to_abs_timeout(ticks, &ts);
    pthread_mutex_lock(&q->m);
    while (q->count == 0) {
        if (ticks == 0) { pthread_mutex_unlock(&q->m); return pdFALSE; }
        int rc = have_deadline ? pthread_cond_timedwait(&q->not_empty, &q->m, &ts)
                               : pthread_cond_wait(&q->not_empty, &q->m);
        if (rc != 0) { pthread_mutex_unlock(&q->m); return pdFALSE; }
    }
    memcpy(out, q->buf + q->head * q->item_size, q->item_size);
    q->head = (q->head + 1) % q->len;
    q->count--;
    pthread_cond_signal(&q->not_full);
    pthread_mutex_unlock(&q->m);
    return pdTRUE;
}

void vQueueDelete(QueueHandle_t hv)
{
    shim_queue_t *q = (shim_queue_t *)hv;
    if (!q) return;
    pthread_mutex_destroy(&q->m);
    pthread_cond_destroy(&q->not_empty);
    pthread_cond_destroy(&q->not_full);
    free(q->buf);
    free(q);
}
