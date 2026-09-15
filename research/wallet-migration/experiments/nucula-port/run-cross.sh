#!/usr/bin/env bash
# Spike N — cross-compile nucula's core file list for an OpenWrt arch (objects).
# Proves compilation portability; does NOT link (deps' target builds are extra).
#
# Usage: ./run-cross.sh <mipsel|aarch64>
# See ./output-cross.txt and ./env-cross.txt.
set -euo pipefail

ARCH="${1:?usage: $0 <mipsel|aarch64>}"
WORK="${WORK:-/home/c03rad0r/r2-work}"
REPO="${REPO:-$WORK/tollgate-go/research/wallet-migration/experiments/nucula-port}"

case "$ARCH" in
  mipsel)  SDK=openwrt/sdk:ramips-mt7621-v25.12.5
           TC=/builder/staging_dir/toolchain-mipsel_24kc_gcc-14.3.0_musl/bin
           PREFIX=mipsel-openwrt-linux-musl ;;
  aarch64) SDK=openwrt/sdk:mediatek-filogic-v25.12.5
           TC=/builder/staging_dir/toolchain-aarch64_cortex-a53_gcc-14.3.0_musl/bin
           PREFIX=aarch64-openwrt-linux-musl ;;
  *) echo "arch must be mipsel or aarch64" >&2; exit 2 ;;
esac

# Dependencies fetched separately (nucula's tarball vendors none of them).
#   $WORK/nucula/deps/{tinycbor,secp256k1}
#   $WORK/nucula/deps/include/{mbedtls,psa,cJSON.h,tinycbor/cbor.h}
docker run --rm -v "$WORK:/work" -e STAGING_DIR=/builder/staging_dir "$SDK" bash -c "
set -u
TC=$TC; PREFIX=$PREFIX
M=/work/nucula/src/main
SH=/work/tollgate-go/research/wallet-migration/experiments/nucula-port/src/shims
DEPS=/work/nucula/deps
OUT=/work/nucula/build-$ARCH; rm -rf \"\$OUT\"; mkdir -p \"\$OUT\"
INC=\"-I\$M -I\$SH -I\$DEPS/include -I\$DEPS/tinycbor/src -I\$DEPS/secp256k1/include\"
DEF=\"-DENABLE_MODULE_SCHNORRSIG=1 -DENABLE_MODULE_EXTRAKEYS=1\"
CFLAGS=\"-Os -Wall -Wno-unused-parameter -Wno-unused-function\"
ok=0; fail=0
for f in crypto.c crypto_bls.c crypto_test.c hex.c base64url.cpp cashu_json.cpp cashu_cbor.cpp keyset.cpp unit.cpp nut10.cpp bip39.c wallet.cpp wallet_flows.cpp wallet_keysets.cpp wallet_blind.cpp wallet_identity.cpp wallet_nvs.cpp wallet_store.cpp wallet_selftest.cpp selftest.cpp; do
  case \"\$f\" in *.cpp) CC=\$TC/\$PREFIX-g++;; *) CC=\$TC/\$PREFIX-gcc;; esac
  if \$CC \$CFLAGS \$DEF \$INC -c \"\$M/\$f\" -o \"\$OUT/\${f%.*}.o\" 2> \"\$OUT/\${f%.*}.err\"; then ok=\$((ok+1)); else fail=\$((fail+1)); echo \"FAIL \$f\"; fi
done
echo \"$ARCH objects: ok=\$ok fail=\$fail\"
grep -rhE 'expects argument of type' \"\$OUT\"/*.err 2>/dev/null | wc -l | sed 's/^/int64 format warnings: /'
"
