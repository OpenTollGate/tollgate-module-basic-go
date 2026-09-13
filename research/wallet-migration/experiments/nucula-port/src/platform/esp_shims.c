/*
 * esp_shims.c — platform services the nucula wallet core expects from ESP-IDF,
 * reimplemented for Linux/musl.
 *
 * Part of the nucula OpenWrt port spike. OUR code.
 *
 * Contents: logging, esp_err_to_name, the RNG, the monotonic timer, heap
 * introspection, the CA-bundle attach stub and esp_restart. Everything here is
 * a *replacement*, not a reimplementation of ESP-IDF internals: each function
 * documents the ESP-IDF primitive it stands in for and what semantics are
 * lost. Where semantics are lost in a way that could affect funds or
 * correctness, the comment says so and the port verdict carries it.
 */
#define _GNU_SOURCE
#include "esp_err.h"
#include "esp_log.h"
#include "esp_random.h"
#include "esp_timer.h"
#include "esp_heap_caps.h"
#include "esp_system.h"
#include "esp_crt_bundle.h"

#include <malloc.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>

#if defined(__linux__)
#include <sys/random.h>
#include <sys/syscall.h>
#endif

/* ------------------------------------------------------------------ */
/* Logging                                                            */
/* ------------------------------------------------------------------ */

static int g_log_level = -1;

int nucula_shim_log_level(void)
{
    if (g_log_level < 0) {
        const char *env = getenv("NUCULA_LOG_LEVEL");
        g_log_level = env ? atoi(env) : 3; /* info */
        if (g_log_level < 0) g_log_level = 0;
        if (g_log_level > 5) g_log_level = 5;
    }
    return g_log_level;
}

void esp_log_level_set(const char *tag, esp_log_level_t level)
{
    (void)tag;
    g_log_level = (int)level;
}

void nucula_shim_log(char lvl, const char *tag, const char *fmt, ...)
{
    static const char letters[6] = { 'N', 'E', 'W', 'I', 'D', 'V' };
    int want;
    switch (lvl) {
        case 'E': want = 1; break;
        case 'W': want = 2; break;
        case 'I': want = 3; break;
        case 'D': want = 4; break;
        default:  want = 5; break;
    }
    if (nucula_shim_log_level() < want) return;

    char line[1024];
    int n = snprintf(line, sizeof line, "%c (%s) ", letters[want],
                     tag ? tag : "-");
    if (n < 0) return;
    va_list ap;
    va_start(ap, fmt);
    vsnprintf(line + n, sizeof(line) - (size_t)n, fmt, ap);
    va_end(ap);

    /* ESP-IDF emits one record per line; keep that so log greps still work. */
    fputs(line, stderr);
    fputc('\n', stderr);
    fflush(stderr);
}

/* ------------------------------------------------------------------ */
/* esp_err_to_name                                                    */
/* ------------------------------------------------------------------ */

const char *esp_err_to_name(esp_err_t code)
{
    switch (code) {
        case ESP_OK: return "ESP_OK";
        case ESP_FAIL: return "ESP_FAIL";
        case ESP_ERR_NO_MEM: return "ESP_ERR_NO_MEM";
        case ESP_ERR_INVALID_ARG: return "ESP_ERR_INVALID_ARG";
        case ESP_ERR_INVALID_STATE: return "ESP_ERR_INVALID_STATE";
        case ESP_ERR_INVALID_SIZE: return "ESP_ERR_INVALID_SIZE";
        case ESP_ERR_NOT_FOUND: return "ESP_ERR_NOT_FOUND";
        case ESP_ERR_NOT_SUPPORTED: return "ESP_ERR_NOT_SUPPORTED";
        case ESP_ERR_TIMEOUT: return "ESP_ERR_TIMEOUT";
        case ESP_ERR_INVALID_RESPONSE: return "ESP_ERR_INVALID_RESPONSE";
        case ESP_ERR_NVS_NOT_INITIALIZED: return "ESP_ERR_NVS_NOT_INITIALIZED";
        case ESP_ERR_NVS_NOT_FOUND: return "ESP_ERR_NVS_NOT_FOUND";
        case ESP_ERR_NVS_TYPE_MISMATCH: return "ESP_ERR_NVS_TYPE_MISMATCH";
        case ESP_ERR_NVS_READ_ONLY: return "ESP_ERR_NVS_READ_ONLY";
        case ESP_ERR_NVS_NOT_ENOUGH_SPACE: return "ESP_ERR_NVS_NOT_ENOUGH_SPACE";
        case ESP_ERR_NVS_INVALID_NAME: return "ESP_ERR_NVS_INVALID_NAME";
        case ESP_ERR_NVS_INVALID_HANDLE: return "ESP_ERR_NVS_INVALID_HANDLE";
        case ESP_ERR_NVS_REMOVE_FAILED: return "ESP_ERR_NVS_REMOVE_FAILED";
        case ESP_ERR_NVS_KEY_TOO_LONG: return "ESP_ERR_NVS_KEY_TOO_LONG";
        case ESP_ERR_NVS_INVALID_STATE: return "ESP_ERR_NVS_INVALID_STATE";
        case ESP_ERR_NVS_INVALID_LENGTH: return "ESP_ERR_NVS_INVALID_LENGTH";
        case ESP_ERR_NVS_NO_FREE_PAGES: return "ESP_ERR_NVS_NO_FREE_PAGES";
        case ESP_ERR_NVS_VALUE_TOO_LONG: return "ESP_ERR_NVS_VALUE_TOO_LONG";
        case ESP_ERR_NVS_PART_NOT_FOUND: return "ESP_ERR_NVS_PART_NOT_FOUND";
        case ESP_ERR_NVS_NEW_VERSION_FOUND: return "ESP_ERR_NVS_NEW_VERSION_FOUND";
        case ESP_ERR_WIFI_CONN: return "ESP_ERR_WIFI_CONN";
        default: return "UNKNOWN ERROR";
    }
}

/* ------------------------------------------------------------------ */
/* RNG: esp_random(3) -> getrandom(2)                                 */
/* ------------------------------------------------------------------ */

uint32_t esp_random(void)
{
    uint32_t v = 0;
#if defined(__linux__)
    /* flags=0: block until the entropy pool is initialised. This is the
     * behaviour NUT-13 blinding needs; GRND_NONBLOCK would risk a low-entropy
     * secret/branch at early boot. */
    ssize_t got = getrandom(&v, sizeof v, 0);
    if (got == (ssize_t)sizeof v) return v;
    /* getrandom(2) is in musl since 1.1.20 and glibc 2.25; fall back only if
     * the kernel is older than the syscall (pre-3.17). */
    long r = syscall(SYS_getrandom, &v, sizeof v, 0);
    if (r == (long)sizeof v) return v;
#endif
    /* Last-resort fallback. /dev/urandom is the same CSPRNG on Linux. */
    FILE *f = fopen("/dev/urandom", "rb");
    if (f) {
        if (fread(&v, 1, sizeof v, f) != sizeof v) v = 0;
        fclose(f);
    }
    if (v == 0) {
        /* Never return a constant: mix in time and pid. Still weak, and the
         * harness aborts if this path is ever taken. */
        struct timespec ts = {0, 0};
        clock_gettime(CLOCK_REALTIME, &ts);
        v = (uint32_t)(ts.tv_nsec ^ (long)getpid());
    }
    return v;
}

void esp_fill_random(void *buf, size_t len)
{
    size_t off = 0;
    unsigned char *p = (unsigned char *)buf;
    /* ESP-IDF fills word-at-a-time; keep the same shape (4-byte granularity)
     * so the byte counts the call sites rely on are unchanged. */
    while (off < len) {
        uint32_t w = esp_random();
        size_t n = len - off;
        if (n > 4) n = 4;
        memcpy(p + off, &w, n);
        off += n;
    }
}

/* ------------------------------------------------------------------ */
/* Timer: esp_timer_get_time() -> CLOCK_MONOTONIC                     */
/* ------------------------------------------------------------------ */

int64_t esp_timer_get_time(void)
{
    struct timespec ts;
    /* CLOCK_MONOTONIC is vDSO-backed on aarch64/mipsel Linux; the read does
     * not enter the kernel, matching the systimer read cost class. */
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000000LL + (int64_t)ts.tv_nsec / 1000LL;
}

/* ------------------------------------------------------------------ */
/* Heap introspection                                                 */
/* ------------------------------------------------------------------ */

size_t esp_get_free_heap_size(void)
{
#if defined(__GLIBC__)
    struct mallinfo2 mi = mallinfo2();
    return (size_t)mi.fordblks + (size_t)mi.fsmblks;
#else
    /* musl has no mallinfo; report the process's heap high-water proxy from
     * /proc/self/statm instead. Reporting-only (see esp_heap_caps.h). */
    FILE *f = fopen("/proc/self/statm", "r");
    if (!f) return 0;
    long size = 0, rss = 0;
    if (fscanf(f, "%ld %ld", &size, &rss) != 2) rss = 0;
    fclose(f);
    return (size_t)rss * (size_t)sysconf(_SC_PAGESIZE);
#endif
}

static size_t g_min_free_heap = (size_t)-1;

size_t esp_get_minimum_free_heap_size(void)
{
    size_t now = esp_get_free_heap_size();
    if (now < g_min_free_heap) g_min_free_heap = now;
    return g_min_free_heap == (size_t)-1 ? now : g_min_free_heap;
}

size_t heap_caps_get_largest_free_block(uint32_t caps)
{
    (void)caps;
#if defined(__GLIBC__)
    struct mallinfo2 mi = mallinfo2();
    return (size_t)mi.fordblks; /* single largest-block proxy */
#else
    return esp_get_free_heap_size();
#endif
}

size_t heap_caps_get_free_size(uint32_t caps)
{
    (void)caps;
    return esp_get_free_heap_size();
}

/* ------------------------------------------------------------------ */
/* CA bundle / restart                                                */
/* ------------------------------------------------------------------ */

esp_err_t esp_crt_bundle_attach(void *conf)
{
    /* ESP-IDF installs its compiled-in Mozilla root set onto the TLS session.
     * Linux TLS backends load a trust store themselves (libcurl/OpenSSL:
     * /etc/ssl/certs/ca-certificates.crt, shipped on OpenWrt by the
     * `ca-bundle` package), so there is nothing to attach here. Returning
     * ESP_OK keeps unported call sites honest without pretending we verified
     * anything. */
    (void)conf;
    return ESP_OK;
}

void esp_restart(void)
{
    /* ESP-IDF resets the SoC. A router daemon should exit non-zero and let
     * procd restart it (or watchdog-reset the device) instead. */
    fputs("E (esp_system) esp_restart(): exiting for procd to restart\n", stderr);
    _exit(1);
}

uint32_t nucula_shim_cpu_count(void)
{
    long n = sysconf(_SC_NPROCESSORS_ONLN);
    return n > 0 ? (uint32_t)n : 1u;
}
