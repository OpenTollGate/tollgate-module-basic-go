#!/bin/sh

# Local developer helper for ad hoc OpenWrt 25.12 apk builds.
#
# Mirrors the CI pipeline: compile the Go binaries natively, stage them with
# packaging/Makefile + packaging/files/ into a temp package dir, then invoke
# the openwrt SDK to produce an .apk.
set -eu

SDK_TAG="${SDK_TAG:-ath79-generic-25.12.0}"
PACKAGE_VERSION="${PACKAGE_VERSION:-0.0.0-r0}"
ARTIFACT_DIR="${ARTIFACT_DIR:-/tmp/tollgate-build-artifacts}"
GOARCH="${GOARCH:-mips}"
GOMIPS="${GOMIPS:-softfloat}"
GOARM="${GOARM:-}"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STAGE_DIR="$(mktemp -d)"
trap 'rm -rf "$STAGE_DIR"' EXIT

mkdir -p "$ARTIFACT_DIR"

printf 'Using SDK_TAG=%s\n' "$SDK_TAG"
printf 'Using PACKAGE_VERSION=%s\n' "$PACKAGE_VERSION"
printf 'Using GOARCH=%s GOMIPS=%s GOARM=%s\n' "$GOARCH" "$GOMIPS" "$GOARM"

# Step 1: cross-compile Go binaries natively.
(
    cd "$REPO_ROOT/src"
    echo "Building tollgate-wrt service..."
    env GOOS=linux GOARCH="$GOARCH" GOMIPS="$GOMIPS" GOARM="$GOARM" \
        go build -o "$STAGE_DIR/tollgate-wrt" -trimpath -ldflags="-s -w" main.go
)
(
    cd "$REPO_ROOT/src/cmd/tollgate-cli"
    echo "Building tollgate CLI..."
    env GOOS=linux GOARCH="$GOARCH" GOMIPS="$GOMIPS" GOARM="$GOARM" \
        go build -o "$STAGE_DIR/tollgate" -trimpath -ldflags="-s -w"
)

# Step 2: stage packaging tree + LICENSE alongside binaries.
cp -r "$REPO_ROOT/packaging/." "$STAGE_DIR/"
cp "$REPO_ROOT/LICENSE" "$STAGE_DIR/LICENSE"

# Step 3: invoke SDK on the staged tree.
docker run --rm -u root \
    -v "$STAGE_DIR":/builder/package/tollgate-wrt \
    -v "$ARTIFACT_DIR":/artifacts \
    -e PACKAGE_VERSION="$PACKAGE_VERSION" \
    "openwrt/sdk:${SDK_TAG}" \
    sh -lc "
        cd /builder &&
        make defconfig &&
        printf 'CONFIG_USE_APK=y\nCONFIG_PACKAGE_tollgate-wrt=y\nCONFIG_PACKAGE_nodogsplash=y\nCONFIG_PACKAGE_luci=y\nCONFIG_PACKAGE_jq=y\n' >> .config &&
        make defconfig &&
        make -j1 V=s package/tollgate-wrt/compile &&
        rm -rf /artifacts/apk-25_12 &&
        mkdir -p /artifacts/apk-25_12 &&
        cp -R /builder/bin/packages/. /artifacts/apk-25_12/
    "
