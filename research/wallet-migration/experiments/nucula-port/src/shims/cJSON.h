/*
 * cJSON.h — Linux/musl replacement for the ESP-IDF bundled cJSON.
 *
 * Part of the nucula OpenWrt port spike. OUR shim: one line that forwards to
 * the system cJSON header.
 *
 * nucula parses NUT-04/05/06/23 JSON with cJSON (main/cashu_json.cpp,
 * wallet_flows.cpp, wallet_keysets.cpp) and links it from ESP-IDF's `json`
 * component, which is a vendored copy of DaveGamble/cJSON.
 *
 * Port decision: use the *distribution* cJSON rather than vendoring, because
 * cJSON is a public API-stable library and OpenWrt packages it as `cjson`
 * (packages/libs/cjson, libcjson.so.1.7.x). On musl the header path is
 * <cjson/cJSON.h>; this shim keeps nucula's `#include <cJSON.h>` working.
 *
 * Behavioural note for the verdict: cJSON's malloc failure mode is "return
 * NULL and log", identical to ESP-IDF's copy, so parse-path semantics carry
 * over. The ESP-IDF copy is pinned at 1.7.15-ish; Ubuntu and OpenWrt ship
 * >=1.7.17, and cJSON_GetObjectItemCaseSensitive / cJSON_ParseWithLength /
 * cJSON_ArrayForEach are all stable across that range.
 */
#pragma once

#include <cjson/cJSON.h>
