#!/usr/bin/env bash
# T5c — "possible with N patches" probe for aarch64 musl.
#
# The two blockers measured for the from-source path are:
#   P1  cdk-go's bindings/cdkffi/link_linux_arm64.go always links
#       ${SRCDIR}/native/linux_arm64/libcdk_ffi.so — a GLIBC object. A musl build
#       has to put a musl object there instead.
#   P2  on musl, rustc drops `cdylib` unless -C target-feature=-crt-static is set,
#       so the musl object must be produced with that flag (see
#       run-cdkffi-rust-build.sh).
#
# This script applies both to a THROWAWAY copy of the module (module cache is
# read-only) and re-links the shipped service. It does NOT touch the research
# branch: everything happens in a scratch worktree and a scratch copy.
#
# Usage: bash run-patched-link-probe.sh <cdk-ffi-musl.so> <scratch-copy-dir> \
#                                         <repo-worktree-src> <openwrt-aarch64-sdk> <outdir>
set -uo pipefail

MUSL_SO=$(readlink -f "${1:?libcdk_ffi.so built for aarch64-unknown-linux-musl}")
COPY=$(readlink -f "${2:?scratch dir for the writable cdk-go copy}")
SRC=$(readlink -f "${3:?<repo-worktree>/src}")
SDK=$(readlink -f "${4:?openwrt aarch64 SDK}")
OUT=$(readlink -f "${5:?outdir}")
mkdir -p "$OUT"
TC=$SDK/staging_dir/toolchain-aarch64_cortex-a53_gcc-14.3.0_musl/bin
GO_MOD_DIR=$(go env GOMODCACHE)/github.com/cashubtc/cdk-go@v0.17.3

{
  echo "### patched-link probe (aarch64 musl)"
  echo "musl object : $MUSL_SO  $(stat -c%s "$MUSL_SO") bytes  sha256=$(sha256sum "$MUSL_SO" | cut -d' ' -f1)"
  echo "replacing   : $GO_MOD_DIR/bindings/cdkffi/native/linux_arm64/libcdk_ffi.so"
  echo "               ($(stat -c%s "$GO_MOD_DIR/bindings/cdkffi/native/linux_arm64/libcdk_ffi.so") bytes, glibc)"
  echo
} >"$OUT/18-patched-link-probe.txt"

# 1. writable copy of the module, with the musl object in place of the glibc one
mkdir -p "$COPY"
cp -r "$GO_MOD_DIR/." "$COPY/"
chmod -R u+w "$COPY"
cp "$MUSL_SO" "$COPY/bindings/cdkffi/native/linux_arm64/libcdk_ffi.so"

# 2. main module carries the replace (exit status kept: state it, don't assume)
if ! grep -q '^replace github.com/cashubtc/cdk-go' "$SRC/go.mod"; then
  printf '\nreplace github.com/cashubtc/cdk-go => %s\n' "$COPY" >>"$SRC/go.mod"
  echo "appended replace directive to $SRC/go.mod" >>"$OUT/18-patched-link-probe.txt"
else
  echo "replace directive already present in $SRC/go.mod" >>"$OUT/18-patched-link-probe.txt"
fi
tail -3 "$SRC/go.mod" >>"$OUT/18-patched-link-probe.txt"

# 3. re-link the service
echo >>"$OUT/18-patched-link-probe.txt"
echo "### go build -tags cdk_wallet (aarch64 musl, patched)" >>"$OUT/18-patched-link-probe.txt"
( cd "$SRC" && env STAGING_DIR=$SDK/staging_dir GOFLAGS='-buildvcs=false -mod=mod' \
    CGO_ENABLED=1 GOOS=linux GOARCH=arm64 \
    CC=$TC/aarch64-openwrt-linux-musl-gcc CXX=$TC/aarch64-openwrt-linux-musl-g++ \
    go build -tags cdk_wallet -o "$OUT/tollgate-wrt-aarch64-musl-cdk-patched" . ) \
    >>"$OUT/18-patched-link-probe.txt" 2>&1
rc=$?
echo "exit=$rc" >>"$OUT/18-patched-link-probe.txt"
if [ -f "$OUT/tollgate-wrt-aarch64-musl-cdk-patched" ]; then
  {
    echo "PRODUCED: $(stat -c%s "$OUT/tollgate-wrt-aarch64-musl-cdk-patched") bytes"
    echo "--- readelf -d (note the RUNPATH into the build machine — the packaging landmine)"
    readelf -d "$OUT/tollgate-wrt-aarch64-musl-cdk-patched" | grep -E 'NEEDED|RUNPATH|RPATH'
    echo "--- file"
    file "$OUT/tollgate-wrt-aarch64-musl-cdk-patched"
  } >>"$OUT/18-patched-link-probe.txt" 2>&1
fi
echo "exit=$rc  see $OUT/18-patched-link-probe.txt"
