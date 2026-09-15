#!/usr/bin/env bash
# Spike N — build the nucula core deps + link the harness for aarch64-musl, then
# (optionally) run it on the router. Proved 2026-09-15 (see output-link.txt).
#
# Prereqs (on the host, outside the container):
#   $WORK/nucula/src                       (nucula @ pinned rev; fetch-nucula.sh)
#   $WORK/nucula/deps/{tinycbor,secp256k1,mbedtls}
#   $WORK/nucula/deps/cjson/{cJSON.c,cJSON.h}
#   $WORK/nucula/deps/include/{mbedtls,psa,cJSON.h,tinycbor/cbor.h}
#   $WORK/tg-research/.../experiments/nucula-port/src/{shims,platform,harness}
set -euo pipefail

WORK="${WORK:-/home/c03rad0r/r2-work}"
SDK=openwrt/sdk:mediatek-filogic-v25.12.5
PORT=/work/tg-research/research/wallet-migration/experiments/nucula-port

docker run --rm -v "$WORK:/work" -e STAGING_DIR=/builder/staging_dir "$SDK" bash -c '
set -u
TC=/builder/staging_dir/toolchain-aarch64_cortex-a53_gcc-14.3.0_musl/bin
NC=$TC/aarch64-openwrt-linux-musl-gcc; NX=$TC/aarch64-openwrt-linux-musl-g++
M=/work/nucula/src/main; SH='"'$PORT'"'/src/shims; PLAT='"'$PORT'"'/src/platform
HARN='"'$PORT'"'/src/harness/nucula_core_harness.cpp
DEPS=/work/nucula/deps; OS=$DEPS/out-aarch64; OBJ=/work/nucula/link-aarch64
INC="-I$M -I$SH -I$DEPS/include -I$DEPS/tinycbor/src -I$DEPS/secp256k1/include -I$DEPS/cjson"
DEF="-DENABLE_MODULE_SCHNORRSIG=1 -DENABLE_MODULE_EXTRAKEYS=1"
mkdir -p "$OS" "$OBJ"

# 1. deps
( cd $DEPS/mbedtls/library && make CC=$NC AR=$TC/aarch64-openwrt-linux-musl-gcc-ar \
    CFLAGS="-Os -fPIC" libmbedcrypto.a libmbedx509.a )
cp $DEPS/mbedtls/library/libmbedcrypto.a "$OS/"
( cd $DEPS/tinycbor/src && $NC -Os -fPIC -I. -c cborparser.c cborparser_dup_string.c \
    cborerrorstrings.c cborpretty.c cborpretty_stdio.c cborencoder.c \
    cborencoder_close_container_checked.c && \
    $TC/aarch64-openwrt-linux-musl-gcc-ar rcs "$OS/libtinycbor.a" ./*.o )
$NC -Os -fPIC -I$DEPS/cjson -c $DEPS/cjson/cJSON.c -o "$OS/cJSON.o"
S=$DEPS/secp256k1
$NC -Os -fPIC $DEF -I$S/include -I$S/src -I$S -c $S/src/secp256k1.c -o "$OS/secp256k1.o"
$NC -Os -fPIC -I$S/include -I$S/src -I$S -c $S/src/precomputed_ecmult.c -o "$OS/precomputed_ecmult.o"
$NC -Os -fPIC -I$S/include -I$S/src -I$S -c $S/src/precomputed_ecmult_gen.c -o "$OS/precomputed_ecmult_gen.o"
$TC/aarch64-openwrt-linux-musl-gcc-ar rcs "$OS/libsecp256k1.a" "$OS"/secp256k1.o "$OS"/precomputed_ecmult*.o

# 2. platform shims (PTHREAD_STACK_MIN fix for musl)
$NC -Os -fPIC $INC -c $PLAT/esp_shims.c -o "$OBJ/esp_shims.o"
$NC -Os -fPIC -D_GNU_SOURCE -DPTHREAD_STACK_MIN=16384 $INC -c $PLAT/freertos_shim.c -o "$OBJ/freertos_shim.o"
$NX -Os -fPIC $INC -c $PLAT/nvs_file.cpp -o "$OBJ/nvs_file.o"
$NC -Os -fPIC $INC -c $PLAT/http_linux.c -o "$OBJ/http_linux.o"

# 3. core objects
for f in crypto.c crypto_bls.c crypto_test.c hex.c base64url.cpp cashu_json.cpp \
         cashu_cbor.cpp keyset.cpp unit.cpp nut10.cpp bip39.c wallet.cpp \
         wallet_flows.cpp wallet_keysets.cpp wallet_blind.cpp wallet_identity.cpp \
         wallet_nvs.cpp wallet_store.cpp wallet_selftest.cpp selftest.cpp; do
  case "$f" in *.cpp) C=$NX;; *) C=$NC;; esac
  $C -Os -fPIC $DEF $INC -c "$M/$f" -o "$OBJ/${f%.*}.o"
done

# 4. harness + link (static libstdc++, dynamic libgcc_s + musl present on the router)
$NX -Os $INC -c "$HARN" -o "$OBJ/harness.o"
$NX -Os -static-libstdc++ -o "$OS/nucula_core_harness" "$OBJ"/harness.o "$OBJ"/wallet*.o \
  "$OBJ"/selftest.o "$OBJ"/crypto*.o "$OBJ"/hex.o "$OBJ"/base64url.o "$OBJ"/cashu_*.o \
  "$OBJ"/keyset.o "$OBJ"/unit.o "$OBJ"/nut10.o "$OBJ"/bip39.o "$OBJ"/esp_shims.o \
  "$OBJ"/freertos_shim.o "$OBJ"/nvs_file.o "$OBJ"/http_linux.o "$OS/cJSON.o" \
  -L"$OS" -lmbedcrypto -ltinycbor -lsecp256k1 -lpthread -Wl,--gc-sections
$TC/aarch64-openwrt-linux-musl-strip --strip-all "$OS/nucula_core_harness"
echo "built: $OS/nucula_core_harness ($(stat -c%s "$OS/nucula_core_harness") bytes)"
'
