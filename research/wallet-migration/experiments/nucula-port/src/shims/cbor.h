/*
 * cbor.h — Linux/musl replacement for Espressif's CBOR component.
 *
 * Part of the nucula OpenWrt port spike. OUR shim: forwards to TinyCBOR.
 *
 * nucula depends on `espressif/cbor: ^0.6.0` (main/idf_component.yml), which
 * is Espressif's packaging of Intel's TinyCBOR plus a small port layer
 * (managed_components/espressif__cbor: upstream VERSION 0.6.0, component
 * version 0.6.1~4). It is used for NUT-00 V4 tokens (`cashuB`) and NUT-18
 * payment requests (`creqA`) in main/cashu_cbor.cpp.
 *
 * Port decision: link upstream TinyCBOR directly instead of the Espressif
 * wrapper. All of nucula's short cbor_* helpers (cbor_find_in_map,
 * cbor_get_string, cbor_get_bytes_as_hex, cbor_get_uint, cbor_get_int,
 * cbor_get_bool) are file-static helpers inside cashu_cbor.cpp — verified by
 * grep — so the only library dependency is the public TinyCBOR API:
 * cbor_parser_init, cbor_value_*, cbor_encoder_*, cbor_encode_*.
 *
 * Verified equivalence: those symbols are present and unchanged between
 * TinyCBOR 0.6.0 (Espressif's pin) and 0.6.1 (the distro package), which is
 * the version this shim links on the build host.
 *
 * RISK CARRIED INTO THE VERDICT: OpenWrt does not package TinyCBOR. A router
 * port must vendor it (4 C files: cborencoder.c, cborencoder_close_container_checked.c,
 * cborparser.c, cborerrorstrings.c — plus cborparser_dup_string.c and
 * cborencoder_float.c if the float/dup paths are linked) or grow a package.
 * That is real, but bounded, work: TinyCBOR is MIT and builds with no
 * configuration.
 */
#pragma once

#include <tinycbor/cbor.h>
