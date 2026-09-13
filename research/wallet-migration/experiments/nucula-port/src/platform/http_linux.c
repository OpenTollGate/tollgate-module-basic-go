/*
 * http_linux.c — Linux replacement for nucula's ESP-IDF HTTP client (main/http.c).
 *
 * Part of the nucula OpenWrt port spike. OUR code.
 *
 * Two backends, chosen at compile time:
 *
 *   -DNUCULA_HTTP_CURL   libcurl (easy interface). Real TLS, system trust
 *                        store, connection reuse, redirects. This is the
 *                        production-shaped choice on a router: OpenWrt ships
 *                        libcurl4 (with OpenSSL or mbedTLS or wolfSSL).
 *
 *   (default)            Blocking POSIX sockets, PLAIN HTTP/1.1 only.
 *                        Chosen as the *size floor* measurement and as proof
 *                        that the link closure needs nothing but libc. https://
 *                        URLs return ESP_FAIL — deliberate, documented gap:
 *                        nucula's real mints are HTTPS, so this backend is NOT
 *                        shippable, only measurable.
 *
 * Where nucula's http.c blocks its calling FreeRTOS task on
 * esp_http_client_perform(), this shim blocks the calling pthread on
 * recv(2)/curl_easy_perform(). Both are fully-blocking designs; the port
 * verdict flags that a router daemon serving several operations at once
 * should not inherit this without either a worker thread pool or a non-blocking
 * client, because the wallet_store recursive mutex is held across network I/O
 * in wallet_flows.cpp.
 */
#include "http.h"
#include "esp_log.h"
#include "esp_timer.h"

#include <ctype.h>
#include <errno.h>
#include <netdb.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/types.h>
#include <unistd.h>

#ifdef NUCULA_HTTP_CURL
#include <curl/curl.h>
#endif

#define TAG "http"

#ifdef NUCULA_HTTP_CURL

const char *nucula_http_backend(void) { return "curl"; }

struct write_ctx {
    char  *buf;
    size_t len;
};

static size_t curl_write_cb(char *ptr, size_t size, size_t nmemb, void *userdata)
{
    struct write_ctx *w = (struct write_ctx *)userdata;
    size_t n = size * nmemb;
    char *nb = (char *)realloc(w->buf, w->len + n + 1);
    if (!nb) return 0;
    w->buf = nb;
    memcpy(w->buf + w->len, ptr, n);
    w->len += n;
    w->buf[w->len] = '\0';
    return n;
}

void http_init(void) { curl_global_init(CURL_GLOBAL_DEFAULT); }
void http_prewarm(const char *base_url) { (void)base_url; }
void http_close_all(void) { /* easy handles are per-request here */ }

static esp_err_t do_request(const char *url, const char *body, int timeout_ms,
                            http_response_t *resp)
{
    if (!url || !resp) return ESP_ERR_INVALID_ARG;
    resp->status = 0; resp->body = NULL; resp->body_len = 0;

    CURL *c = curl_easy_init();
    if (!c) return ESP_ERR_NO_MEM;

    struct write_ctx w = {0};
    w.buf = (char *)malloc(1);
    if (!w.buf) { curl_easy_cleanup(c); return ESP_ERR_NO_MEM; }
    w.buf[0] = '\0';

    char errbuf[CURL_ERROR_SIZE] = {0};
    curl_easy_setopt(c, CURLOPT_URL, url);
    curl_easy_setopt(c, CURLOPT_WRITEFUNCTION, curl_write_cb);
    curl_easy_setopt(c, CURLOPT_WRITEDATA, &w);
    curl_easy_setopt(c, CURLOPT_FOLLOWLOCATION, 1L);
    curl_easy_setopt(c, CURLOPT_TIMEOUT_MS, (long)(timeout_ms > 0 ? timeout_ms : 15000));
    curl_easy_setopt(c, CURLOPT_ERRORBUFFER, errbuf);
    /* TLS trust comes from the system store (ca-bundle on OpenWrt), the
     * equivalent of ESP-IDF's esp_crt_bundle_attach(). */
    curl_easy_setopt(c, CURLOPT_CAINFO, "/etc/ssl/certs/ca-certificates.crt");
    if (body) {
        curl_easy_setopt(c, CURLOPT_POST, 1L);
        curl_easy_setopt(c, CURLOPT_POSTFIELDS, body);
        curl_easy_setopt(c, CURLOPT_POSTFIELDSIZE, (long)strlen(body));
        struct curl_slist *h = NULL;
        h = curl_slist_append(h, "Content-Type: application/json");
        curl_easy_setopt(c, CURLOPT_HTTPHEADER, h);
    }

    CURLcode rc = curl_easy_perform(c);
    long code = 0;
    curl_easy_getinfo(c, CURLINFO_RESPONSE_CODE, &code);
    resp->status = (int)code;
    resp->body = w.buf;
    resp->body_len = w.len;
    curl_easy_cleanup(c);
    if (rc != CURLE_OK) {
        ESP_LOGE(TAG, "curl %s failed: %s", url, errbuf[0] ? errbuf : curl_easy_strerror(rc));
        return ESP_FAIL;
    }
    return ESP_OK;
}

esp_err_t http_get(const char *url, http_response_t *resp)
{
    return do_request(url, NULL, 15000, resp);
}

esp_err_t http_post_json(const char *url, const char *json_body, http_response_t *resp)
{
    return do_request(url, json_body, 15000, resp);
}

esp_err_t http_post_json_timeout(const char *url, const char *json_body,
                                 http_response_t *resp, int timeout_ms)
{
    return do_request(url, json_body, timeout_ms, resp);
}

#else /* !NUCULA_HTTP_CURL — raw blocking sockets, plain HTTP only */

const char *nucula_http_backend(void) { return "raw"; }

void http_init(void) { }
void http_prewarm(const char *base_url) { (void)base_url; }
void http_close_all(void) { }

struct url_parts {
    char scheme[8];
    char host[256];
    char port[8];
    char path[1024];
};

static int parse_url(const char *url, struct url_parts *out)
{
    memset(out, 0, sizeof *out);
    const char *p = strstr(url, "://");
    if (!p) return 0;
    size_t sl = (size_t)(p - url);
    if (sl >= sizeof out->scheme) return 0;
    memcpy(out->scheme, url, sl);
    out->scheme[sl] = '\0';
    p += 3;
    const char *slash = strchr(p, '/');
    const char *hostend = slash ? slash : p + strlen(p);
    const char *colon = memchr(p, ':', (size_t)(hostend - p));
    size_t hl = (size_t)((colon ? colon : hostend) - p);
    if (hl == 0 || hl >= sizeof out->host) return 0;
    memcpy(out->host, p, hl);
    out->host[hl] = '\0';
    if (colon) {
        size_t pl = (size_t)(hostend - colon - 1);
        if (pl == 0 || pl >= sizeof out->port) return 0;
        memcpy(out->port, colon + 1, pl);
        out->port[pl] = '\0';
    } else {
        snprintf(out->port, sizeof out->port, "%s",
                 strcmp(out->scheme, "https") == 0 ? "443" : "80");
    }
    snprintf(out->path, sizeof out->path, "%s", slash ? slash : "/");
    return 1;
}

static esp_err_t do_request(const char *url, const char *body, int timeout_ms,
                            http_response_t *resp)
{
    if (!url || !resp) return ESP_ERR_INVALID_ARG;
    resp->status = 0; resp->body = NULL; resp->body_len = 0;

    struct url_parts u;
    if (!parse_url(url, &u)) { ESP_LOGE(TAG, "bad url %s", url); return ESP_FAIL; }
    if (strcmp(u.scheme, "https") != 0) {
        /* TLS is out of scope for the raw backend by construction. */
        ESP_LOGE(TAG, "raw backend cannot speak %s:// (no TLS); use the curl backend",
                 u.scheme);
        return ESP_FAIL;
    }

    struct addrinfo hints, *res = NULL;
    memset(&hints, 0, sizeof hints);
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_STREAM;
    if (getaddrinfo(u.host, u.port, &hints, &res) != 0 || !res) {
        ESP_LOGE(TAG, "dns failed for %s", u.host);
        return ESP_FAIL;
    }

    int fd = socket(res->ai_family, res->ai_socktype, res->ai_protocol);
    if (fd < 0) { freeaddrinfo(res); return ESP_FAIL; }

    struct timeval tv;
    tv.tv_sec = (timeout_ms > 0 ? timeout_ms : 15000) / 1000;
    tv.tv_usec = ((timeout_ms > 0 ? timeout_ms : 15000) % 1000) * 1000;
    setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof tv);
    setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof tv);

    if (connect(fd, res->ai_addr, res->ai_addrlen) != 0) {
        ESP_LOGE(TAG, "connect %s:%s failed: %s", u.host, u.port, strerror(errno));
        close(fd); freeaddrinfo(res);
        return ESP_FAIL;
    }
    freeaddrinfo(res);

    char req[4096];
    int n;
    if (body) {
        n = snprintf(req, sizeof req,
                     "POST %s HTTP/1.1\r\nHost: %s\r\n"
                     "Content-Type: application/json\r\nContent-Length: %zu\r\n"
                     "Connection: close\r\n\r\n%s",
                     u.path, u.host, strlen(body), body);
    } else {
        n = snprintf(req, sizeof req,
                     "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n",
                     u.path, u.host);
    }
    if (n <= 0 || (size_t)n >= sizeof req) { close(fd); return ESP_FAIL; }
    if (send(fd, req, (size_t)n, 0) != n) { close(fd); return ESP_FAIL; }

    size_t cap = 8192, len = 0;
    char *buf = (char *)malloc(cap);
    if (!buf) { close(fd); return ESP_ERR_NO_MEM; }
    for (;;) {
        if (len + 4096 + 1 > cap) {
            cap *= 2;
            char *nb = (char *)realloc(buf, cap);
            if (!nb) { free(buf); close(fd); return ESP_ERR_NO_MEM; }
            buf = nb;
        }
        ssize_t r = recv(fd, buf + len, 4096, 0);
        if (r < 0) {
            if (errno == EINTR) continue;
            break;   /* timeout or error: keep what we have, like ESP-IDF does */
        }
        if (r == 0) break;
        len += (size_t)r;
    }
    close(fd);
    buf[len] = '\0';

    /* Status line: "HTTP/1.1 200 OK" */
    int status = 0;
    if (strncmp(buf, "HTTP/", 5) == 0) {
        const char *sp = strchr(buf, ' ');
        if (sp) status = atoi(sp + 1);
    }
    resp->status = status;

    char *bodyp = strstr(buf, "\r\n\r\n");
    if (bodyp) {
        size_t header_len = (size_t)(bodyp + 4 - buf);
        size_t blen = len - header_len;
        char *out = (char *)malloc(blen + 1);
        if (!out) { free(buf); return ESP_ERR_NO_MEM; }
        memcpy(out, bodyp + 4, blen);
        out[blen] = '\0';
        resp->body = out;
        resp->body_len = blen;
        free(buf);
    } else {
        resp->body = buf;
        resp->body_len = len;
    }
    return ESP_OK;
}

esp_err_t http_get(const char *url, http_response_t *resp)
{
    return do_request(url, NULL, 15000, resp);
}

esp_err_t http_post_json(const char *url, const char *json_body, http_response_t *resp)
{
    return do_request(url, json_body, 15000, resp);
}

esp_err_t http_post_json_timeout(const char *url, const char *json_body,
                                 http_response_t *resp, int timeout_ms)
{
    return do_request(url, json_body, timeout_ms, resp);
}

#endif /* NUCULA_HTTP_CURL */

void http_response_free(http_response_t *resp)
{
    if (!resp) return;
    free(resp->body);
    resp->body = NULL;
    resp->body_len = 0;
}
