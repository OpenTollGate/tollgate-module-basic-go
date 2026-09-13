/*
 * nvs_flash.h — Linux/musl replacement for the ESP-IDF NVS flash init header.
 *
 * Part of the nucula OpenWrt port spike. OUR shim.
 *
 * ESP-IDF's nvs_flash_init() finds the "nvs" partition, mounts it, and
 * migrates the page format on version bumps. On a router there is no
 * partition table: the shim points the store at NUCULA_NVS_DIR (default
 * ./nvs), which is where a real port would put a procd-owned persistent
 * directory, e.g. /etc/tollgate/nucula-nvs (covered by
 * /lib/upgrade/keep.d/tollgate so sysupgrade preserves it).
 */
#pragma once

#include "nvs.h"
