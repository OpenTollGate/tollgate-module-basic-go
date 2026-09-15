#!/usr/bin/env bash
# T5c — reproduce the cgo + cdk-go cross-compile test for OpenWrt musl (aarch64).
#
# EXPECTED: FAIL. This is the evidence for rejecting the in-process cgo adapter.
# See ./output.txt for the captured result and ./env.txt for the environment.
#
# Requires: docker, host Go (GOROOT + GOPATH mounted), and the OpenWrt SDK image
# openwrt/sdk:mediatek-filogic-v25.12.5 (provides the aarch64 musl toolchain).
set -euo pipefail

WORK="${WORK:-/tmp/r2-cdk-cross}"
SDK_IMAGE="${SDK_IMAGE:-openwrt/sdk:mediatek-filogic-v25.12.5}"
GOROOT_HOST="${GOROOT_HOST:-/usr/lib/go-1.24}"   # host Go is mounted read-only
TC=/builder/staging_dir/toolchain-aarch64_cortex-a53_gcc-14.3.0_musl/bin

mkdir -p "$WORK"
if [ ! -d "$WORK/tollgate-go" ]; then
  git clone https://github.com/felixfelix-bot/tollgate-module-basic-go.git "$WORK/tollgate-go"
fi
git -C "$WORK/tollgate-go" fetch -q origin research/wallet-migration
git -C "$WORK/tollgate-go" checkout -q -B research/wallet-migration origin/research/wallet-migration

echo "=== cdk-go prebuilt native libs (glibc-only) ==="
ls -1 "$WORK/tollgate-go"/../../go/pkg/mod/github.com/cashubtc/cdk-go@v0.17.3/bindings/cdkffi/native/ 2>/dev/null || true

echo "=== final link: build service with -tags cdk_wallet (aarch64 musl) ==="
docker run --rm \
  -v "$WORK:/work" \
  -v "$HOME/go:/go" \
  -v "$GOROOT_HOST:/usr/lib/go-1.24:ro" \
  -w /work/tollgate-go/src \
  -e GOPATH=/go -e GOMODCACHE=/go/pkg/mod -e GOFLAGS=-mod=mod \
  -e STAGING_DIR=/builder/staging_dir \
  -e CGO_ENABLED=1 -e GOOS=linux -e GOARCH=arm64 \
  -e CC=$TC/aarch64-openwrt-linux-musl-gcc \
  -e CXX=$TC/aarch64-openwrt-linux-musl-g++ \
  "$SDK_IMAGE" \
  bash -c 'GO=/usr/lib/go-1.24/bin/go; $GO build -tags cdk_wallet -o /tmp/tollgate-wrt-cdk .'
