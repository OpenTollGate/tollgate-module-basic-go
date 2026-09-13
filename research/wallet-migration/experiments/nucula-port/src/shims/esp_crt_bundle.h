/*
 * esp_crt_bundle.h — Linux/musl replacement for ESP-IDF's Mozilla CA bundle.
 *
 * Part of the nucula OpenWrt port spike. OUR shim.
 *
 * ESP-IDF: esp_crt_bundle_attach() installs a compiled-in Mozilla root CA
 * bundle (~130 roots, tens of KB of flash) onto a TLS session. nucula's
 * http.c attaches it to every mint connection.
 *
 * Linux/musl: TLS is provided by a real TLS library. With OpenSSL/libcurl the
 * equivalent object is an X509_STORE, and the equivalent *policy* decision is
 * "use the system trust store" (/etc/ssl/certs/ca-certificates.crt, which
 * OpenWrt ships via the `ca-bundle` package) rather than an embedded bundle.
 *
 * The signature is kept so unported call sites compile; the Linux HTTP shim
 * ignores the returned value.
 */
#pragma once

#include "esp_err.h"

#ifdef __cplusplus
extern "C" {
#endif

esp_err_t esp_crt_bundle_attach(void *conf);

#ifdef __cplusplus
}
#endif
