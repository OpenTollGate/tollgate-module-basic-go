#!/usr/bin/env bash
# T5c — the DEPLOYMENT link: build the actual router service (src/main.go, which
# imports src/merchant -> src/tollwallet) with the cdk_wallet build tag, for each
# OpenWrt router arch. This is the strongest form of the question: not "does the
# library compile" but "does the artifact we would ship link".
#
# src/go.mod carries `replace .../tollwallet => ./tollwallet`, so ./... at the src
# level DOES pull the nested module when the tag is set — unlike the tollwallet
# directory itself, which `cd src && go build ./...` cannot reach (see
# 05-false-green.txt).
#
# Usage: bash run-service-link.sh <aarch64-sdk> <mipsel-sdk> <repo-src-dir> <outdir>
set -uo pipefail

SDK_A=$(readlink -f "${1:?aarch64 SDK dir}")
SDK_M=$(readlink -f "${2:?mipsel SDK dir}")
SRC=$(readlink -f "${3:?<repo>/src}")
OUT=$(readlink -f "${4:?outdir}")
mkdir -p "$OUT"
TCA=$SDK_A/staging_dir/toolchain-aarch64_cortex-a53_gcc-14.3.0_musl/bin
TCM=$SDK_M/staging_dir/toolchain-mipsel_24kc_gcc-14.3.0_musl/bin

one() { # label, GOARCH, extra-env, staging, cc, cxx, tags
  local label=$1 arch=$2 extra=$3 staging=$4 cc=$5 cxx=$6 tags=${7-}
  echo "################ service link: $label  (tags: ${tags:-<none>}) ################"
  ( cd "$SRC" && env STAGING_DIR=$staging GOFLAGS=-buildvcs=false CGO_ENABLED=1 \
      GOOS=linux GOARCH=$arch $extra CC=$cc CXX=$cxx \
      go build -tags "$tags" -o "$OUT/tollgate-wrt-$label" . ) \
      >"$OUT/14-service-link-$label.txt" 2>&1
  local rc=$?
  echo "exit=$rc  log=$OUT/14-service-link-$label.txt  lines=$(wc -l <"$OUT/14-service-link-$label.txt")"
  echo "distinct undefined symbols: $(grep -o 'undefined reference to .[^ ]*' "$OUT/14-service-link-$label.txt" | sort -u | wc -l)"
  echo "distinct GLIBC versions:    $(grep -o '@GLIBC_[0-9.]*' "$OUT/14-service-link-$label.txt" | sort -u | tr '\n' ' ')"
  grep -m3 'undefined reference to' "$OUT/14-service-link-$label.txt" | sed 's/^.*ld: //' || true
  [ -f "$OUT/tollgate-wrt-$label" ] && echo "PRODUCED: $(stat -c%s "$OUT/tollgate-wrt-$label") bytes"
  echo
}
one aarch64-musl arm64 "" "$SDK_A/staging_dir" \
  "$TCA/aarch64-openwrt-linux-musl-gcc" "$TCA/aarch64-openwrt-linux-musl-g++" cdk_wallet
one mipsel-musl mipsle "GOMIPS=softfloat" "$SDK_M/staging_dir" \
  "$TCM/mipsel-openwrt-linux-musl-gcc" "$TCM/mipsel-openwrt-linux-musl-g++" cdk_wallet
# Baseline: the same build WITHOUT the cdk_wallet tag must LINK CLEAN on both arches,
# otherwise the failures above would not be attributable to the cdk path.
one aarch64-musl-baseline arm64 "" "$SDK_A/staging_dir" \
  "$TCA/aarch64-openwrt-linux-musl-gcc" "$TCA/aarch64-openwrt-linux-musl-g++" ""
one mipsel-musl-baseline mipsle "GOMIPS=softfloat" "$SDK_M/staging_dir" \
  "$TCM/mipsel-openwrt-linux-musl-gcc" "$TCM/mipsel-openwrt-linux-musl-g++" ""
echo "done"
