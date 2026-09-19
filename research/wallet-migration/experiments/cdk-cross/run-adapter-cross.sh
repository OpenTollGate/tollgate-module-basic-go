#!/usr/bin/env bash
# T5c — adapter cross-compile test: can the in-process cgo adapter
# (src/tollwallet, build tag cdk_wallet, cgo -> cdk-go -> libcdk_ffi.so) be
# built for the two router arches the wallet design must cover?
#
#   aarch64_cortex-a53  (GOARCH=arm64)   via openwrt-sdk-25.12.0-mediatek-filogic
#   mipsel_24kc         (GOARCH=mipsle)  via openwrt-sdk-25.12.0-ramips-mt7621
#
# EXPECTED: both FAIL at the final link. This script exists to produce the raw
# output for that verdict, not to fix it.
#
# Usage:  bash run-adapter-cross.sh <aarch64-sdk-dir> <mipsel-sdk-dir> <repo-src-dir> <outdir>
#   e.g.  bash run-adapter-cross.sh \
#           ../sdk/openwrt-sdk-25.12.0-mediatek-filogic_gcc-14.3.0_musl.Linux-x86_64 \
#           ../sdk/openwrt-sdk-25.12.0-ramips-mt7621_gcc-14.3.0_musl.Linux-x86_64 \
#           ../tmbg/src/tollwallet ../raw
#
# Requires: an extracted OpenWrt 25.12.0 SDK per target, host Go, and the cdk-go
# module in the Go module cache (GOFLAGS=-buildvcs=false is required inside a
# git worktree — see the worktree-build-verification skill, Pitfall 1).
set -uo pipefail

SDK_A=$(readlink -f "${1:?aarch64 SDK dir}")
SDK_M=$(readlink -f "${2:?mipsel SDK dir}")
SRC=$(readlink -f "${3:?path to <repo>/src/tollwallet}")
OUT=$(readlink -f "${4:?output dir}")
mkdir -p "$OUT"

TC_A=$SDK_A/staging_dir/toolchain-aarch64_cortex-a53_gcc-14.3.0_musl/bin
TC_M=$SDK_M/staging_dir/toolchain-mipsel_24kc_gcc-14.3.0_musl/bin

run_arch() { # $1=label $2=GOARCH $3=GOARM/GOMIPS env $4=SDK staging_dir $5=CC $6=CXX
  local label=$1 goarch=$2 extra=$3 staging=$4 cc=$5 cxx=$6
  echo "################ $label ################"
  echo "--- toolchain: $cc"
  STAGING_DIR=$staging "$cc" --version | head -1

  echo
  echo "=== [$label 1/2] library-level build (NO final link): go build -tags cdk_wallet ./... ==="
  ( cd "$SRC" && env STAGING_DIR=$staging GOFLAGS=-buildvcs=false CGO_ENABLED=1 \
      GOOS=linux GOARCH=$goarch $extra CC=$cc CXX=$cxx \
      go build -tags cdk_wallet ./... ) >"$OUT/${label}-build.txt" 2>&1
  echo "exit=$?  (log: $OUT/${label}-build.txt, $(wc -l <"$OUT/${label}-build.txt") lines)"

  echo
  echo "=== [$label 2/2] FORCED LINK: go test -c -tags cdk_wallet (links a real binary) ==="
  ( cd "$SRC" && env STAGING_DIR=$staging GOFLAGS=-buildvcs=false CGO_ENABLED=1 \
      GOOS=linux GOARCH=$goarch $extra CC=$cc CXX=$cxx \
      go test -c -tags cdk_wallet -o "$OUT/tollwallet-${label}.test" ) \
      >"$OUT/${label}-link.txt" 2>&1
  local rc=$?
  echo "exit=$rc  (log: $OUT/${label}-link.txt, $(wc -l <"$OUT/${label}-link.txt") lines)"
  echo "distinct undefined symbols reported: $(grep -o 'undefined reference to .[^ ]*' "$OUT/${label}-link.txt" | sort -u | wc -l)"
  echo "GLIBC-versioned refs:                $(grep -o '@GLIBC_[0-9.]*' "$OUT/${label}-link.txt" | sort -u | tr '\n' ' ')"
  echo "first 5 error lines:"
  grep -m5 'undefined reference to' "$OUT/${label}-link.txt" | sed 's/^.*ld: //' || true
}
# note: `local rc=$?` must be captured on the same statement in bash; assignment
# after a subshell redirect works because $? is read before any other command.
run_arch aarch64-musl arm64 "" "$SDK_A/staging_dir" \
  "$TC_A/aarch64-openwrt-linux-musl-gcc" "$TC_A/aarch64-openwrt-linux-musl-g++"
echo
run_arch mipsel-musl mipsle "GOMIPS=softfloat" "$SDK_M/staging_dir" \
  "$TC_M/mipsel-openwrt-linux-musl-gcc" "$TC_M/mipsel-openwrt-linux-musl-g++"
echo
echo "################ done ################"
