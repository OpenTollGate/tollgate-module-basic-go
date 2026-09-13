# CMakeLists.txt — build nucula's wallet/protocol core as a plain Linux program.
#
# Part of the nucula OpenWrt port spike. OUR file.
#
# This build system has NO ESP-IDF in it. It compiles nucula's core sources
# straight from a checkout of the recorded upstream revision, with our shim
# headers taking the place of the ESP-IDF ones (see src/shims/), and links
# against distribution libraries.
#
# The point of the spike is to find out which of nucula's core files compile
# verbatim and which need a substitution — so the file list below is the
# interesting artifact, not the compiler flags.
#
# Usage:
#   cmake -S . -B <builddir> -DNUCULA_SRC=/path/to/nucula/src [options]
#   cmake --build <builddir>
#
# Options:
#   -DNUCULA_SRC=<path>       unpacked nucula source tree (main/ lives here). REQUIRED.
#   -DNUCULA_HTTP_CURL=ON     use libcurl for the HTTP shim instead of raw sockets
#   -DNUCULA_LINK_SYSTEM_SECP=ON (default) link libsecp256k1 from the host;
#                             OFF builds the vendored bitcoin-core/secp256k1 submodule
#                             from NUCULA_SRC/components/secp256k1/libsecp256k1

cmake_minimum_required(VERSION 3.16)
project(nucula_core_port C CXX)

set(CMAKE_C_STANDARD 11)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_C_STANDARD_REQUIRED ON)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)

if(NOT DEFINED NUCULA_SRC OR NUCULA_SRC STREQUAL "")
  message(FATAL_ERROR "Set -DNUCULA_SRC=/path/to/unpacked/nucula/src")
endif()
if(NOT EXISTS "${NUCULA_SRC}/main/crypto.c")
  message(FATAL_ERROR "NUCULA_SRC does not look like nucula: ${NUCULA_SRC}")
endif()

option(NUCULA_HTTP_CURL "Use libcurl for the HTTP shim" OFF)
option(NUCULA_LINK_SYSTEM_SECP "Link the host's libsecp256k1" ON)

set(SHIM_DIR ${CMAKE_CURRENT_SOURCE_DIR}/src/shims)
set(PLATFORM_DIR ${CMAKE_CURRENT_SOURCE_DIR}/src/platform)

# ---------------------------------------------------------------------------
# The port's core file list. This is the answer to "what is the wallet core":
# every source here is a nucula file compiled UNCHANGED. Files deliberately
# absent (and why) are listed in nucula-port-map.md.
# ---------------------------------------------------------------------------
set(NUCULA_CORE_SRCS
    # crypto / codecs
    ${NUCULA_SRC}/main/crypto.c
    ${NUCULA_SRC}/main/crypto_bls.c
    ${NUCULA_SRC}/main/crypto_test.c
    ${NUCULA_SRC}/main/hex.c
    ${NUCULA_SRC}/main/base64url.cpp
    ${NUCULA_SRC}/main/cashu_json.cpp
    ${NUCULA_SRC}/main/cashu_cbor.cpp
    ${NUCULA_SRC}/main/keyset.cpp
    ${NUCULA_SRC}/main/unit.cpp
    ${NUCULA_SRC}/main/nut10.cpp
    ${NUCULA_SRC}/main/bip39.c
    # wallet state machine
    ${NUCULA_SRC}/main/wallet.cpp
    ${NUCULA_SRC}/main/wallet_flows.cpp
    ${NUCULA_SRC}/main/wallet_keysets.cpp
    ${NUCULA_SRC}/main/wallet_blind.cpp
    ${NUCULA_SRC}/main/wallet_identity.cpp
    ${NUCULA_SRC}/main/wallet_nvs.cpp
    ${NUCULA_SRC}/main/wallet_store.cpp
    ${NUCULA_SRC}/main/wallet_selftest.cpp
    ${NUCULA_SRC}/main/selftest.cpp
)

add_library(nucula_core STATIC ${NUCULA_CORE_SRCS})
target_include_directories(nucula_core PUBLIC ${NUCULA_SRC}/main)
target_include_directories(nucula_core PRIVATE ${SHIM_DIR})

# Mirror the ESP-IDF project's optimisation split (project-wide -Os with -O2 on
# the hot codec/crypto files) so size numbers are comparable with the device
# build rather than accidentally better.
target_compile_options(nucula_core PRIVATE -Os -ffunction-sections -fdata-sections)
set_source_files_properties(
    ${NUCULA_SRC}/main/crypto.c
    ${NUCULA_SRC}/main/hex.c
    ${NUCULA_SRC}/main/cashu_cbor.cpp
    PROPERTIES COMPILE_OPTIONS "-O2")

# Warnings are informative here: nucula is built with IDF's defaults, and every
# warning below is a place a musl/gcc-14 build differs from the ESP-IDF one.
target_compile_options(nucula_core PRIVATE -Wall -Wno-unused-parameter -Wno-unused-function)

# ---------------------------------------------------------------------------
# Platform replacement layer (our code)
# ---------------------------------------------------------------------------
add_library(nucula_platform STATIC
    ${PLATFORM_DIR}/esp_shims.c
    ${PLATFORM_DIR}/freertos_shim.c
    ${PLATFORM_DIR}/nvs_file.cpp
    ${PLATFORM_DIR}/http_linux.c)
target_include_directories(nucula_platform PUBLIC ${SHIM_DIR})
target_compile_options(nucula_platform PRIVATE -Os -Wall -Wno-format-truncation)
if(NUCULA_HTTP_CURL)
  target_compile_definitions(nucula_platform PRIVATE NUCULA_HTTP_CURL)
endif()

# ---------------------------------------------------------------------------
# Third-party dependencies
# ---------------------------------------------------------------------------
find_package(PkgConfig REQUIRED)

# mbedTLS: SHA-256, HMAC, PBKDF2, base64 — nucula's crypto.c and bip39.c use a
# small, stable subset (mbedtls_sha256_*, mbedtls_md_hmac,
# mbedtls_pkcs5_pbkdf2_hmac_ext, mbedtls_base64_*).
pkg_check_modules(MBEDTLS REQUIRED mbedtls)
# pkg-config for mbedTLS lists only -lmbedtls (the TLS layer); the primitives
# nucula needs (sha256, md/hmac, pkcs5, base64) live in libmbedcrypto, which
# libmbedtls links against privately.
find_library(MBEDCRYPTO_LIB NAMES mbedcrypto REQUIRED)
set(MBED_LIBS ${MBEDTLS_LIBRARIES} ${MBEDCRYPTO_LIB})

# cJSON: JSON parsing for NUT-04/05/06/23.
find_library(CJSON_LIB NAMES cjson REQUIRED)

# TinyCBOR: stand-in for Espressif's cbor component.
pkg_check_modules(TINYCBOR REQUIRED tinycbor)

set(THIRD_PARTY_LIBS ${MBED_LIBS} ${CJSON_LIB} ${TINYCBOR_LIBRARIES})

if(NUCULA_LINK_SYSTEM_SECP)
  pkg_check_modules(SECP256K1 REQUIRED libsecp256k1)
  list(APPEND THIRD_PARTY_LIBS ${SECP256K1_LIBRARIES})
else()
  # Build bitcoin-core/secp256k1 from the submodule nucula pins, the way the
  # ESP-IDF component does (ENABLE_MODULE_SCHNORRSIG + EXTRAKEYS).
  set(SECP_DIR ${NUCULA_SRC}/components/secp256k1/libsecp256k1)
  add_library(secp256k1_vendored STATIC
      ${SECP_DIR}/src/secp256k1.c
      ${SECP_DIR}/src/precomputed_ecmult.c
      ${SECP_DIR}/src/precomputed_ecmult_gen.c)
  target_include_directories(secp256k1_vendored PUBLIC ${SECP_DIR}/include)
  target_include_directories(secp256k1_vendored PRIVATE ${SECP_DIR}/src ${SECP_DIR})
  target_compile_definitions(secp256k1_vendored PRIVATE
      ENABLE_MODULE_SCHNORRSIG=1 ENABLE_MODULE_EXTRAKEYS=1
      ECMULT_GEN_PREC_BITS=4 ECMULT_WINDOW_SIZE=8)
  target_compile_options(secp256k1_vendored PRIVATE -Os -Wno-unused-function)
  list(APPEND THIRD_PARTY_LIBS secp256k1_vendored)
endif()

if(NUCULA_HTTP_CURL)
  pkg_check_modules(CURL REQUIRED libcurl)
  list(APPEND THIRD_PARTY_LIBS ${CURL_LIBRARIES})
endif()

find_package(Threads REQUIRED)

# ---------------------------------------------------------------------------
# Harness
# ---------------------------------------------------------------------------
add_executable(nucula_core_harness src/harness/nucula_core_harness.cpp)
target_include_directories(nucula_core_harness PRIVATE ${SHIM_DIR} ${NUCULA_SRC}/main)
target_link_options(nucula_core_harness PRIVATE -Wl,--gc-sections)
target_link_libraries(nucula_core_harness PRIVATE
    nucula_core nucula_platform
    ${THIRD_PARTY_LIBS}
    Threads::Threads)
if(NUCULA_HTTP_CURL)
  target_link_directories(nucula_core_harness PRIVATE ${CURL_LIBRARY_DIRS})
endif()

# A pure static archive of the core + platform layer, for `size` measurement
# without the harness's own main().
add_library(nucula_port_bundle STATIC ${NUCULA_CORE_SRCS})
target_include_directories(nucula_port_bundle PRIVATE ${SHIM_DIR} ${NUCULA_SRC}/main)
target_compile_options(nucula_port_bundle PRIVATE -Os -ffunction-sections -fdata-sections)
