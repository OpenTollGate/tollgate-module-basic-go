/*
 * http.h — Linux replacement for nucula's ESP-IDF HTTP client header.
 *
 * Part of the nucula OpenWrt port spike. OUR shim. The ABI is deliberately
 * identical to nucula's main/http.h so that wallet_flows.cpp,
 * wallet_keysets.cpp and commands_wallet.cpp compile with no edits at all.
 *
 * ESP-IDF side (nucula main/http.c, 296 lines) does four things:
 *   1. esp_http_client with a persistent-connection cache keyed on base URL;
 *   2. esp_crt_bundle_attach() for TLS trust (Mozilla roots compiled in);
 *   3. a mutex (SemaphoreHandle_t) serialising the connection cache;
 *   4. a prewarm background task (xTaskCreate) fetching /v1/info.
 *
 * Linux side: two interchangeable backends, selected at compile time —
 *   - NUCULA_HTTP_CURL: libcurl (real TLS, system trust store, connection
 *     reuse) — the production-shaped choice;
 *   - default (no define): blocking POSIX sockets, PLAIN HTTP ONLY. Used to
 *     measure the port's size floor and to prove the link closure without
 *     dragging a TLS stack in. https:// URLs fail with ESP_FAIL in this mode,
 *     which is recorded as a deliberate gap in the port verdict.
 *
 * The response body is heap-allocated exactly like nucula's (caller frees via
 * http_response_free), so ownership rules at the call sites are unchanged.
 */
#pragma once

#include "esp_err.h"
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct {
    int status;
    char *body;
    size_t body_len;
} http_response_t;

void http_init(void);
void http_prewarm(const char *base_url);
void http_close_all(void);

esp_err_t http_get(const char *url, http_response_t *resp);
esp_err_t http_post_json(const char *url, const char *json_body,
                         http_response_t *resp);
esp_err_t http_post_json_timeout(const char *url, const char *json_body,
                                 http_response_t *resp, int timeout_ms);
void http_response_free(http_response_t *resp);

/* Shim-only: which backend got compiled in ("curl" or "raw"). */
const char *nucula_http_backend(void);

#ifdef __cplusplus
}
#endif
