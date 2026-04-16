#!/bin/sh

set -eu

SDK_TAG="${SDK_TAG:-ath79-generic-25.12.0}"
PACKAGE_VERSION="${PACKAGE_VERSION:-0.0.0-r0}"
ARTIFACT_DIR="${ARTIFACT_DIR:-/tmp/tollgate-build-artifacts}"

mkdir -p "$ARTIFACT_DIR"

printf 'Using SDK_TAG=%s\n' "$SDK_TAG"
printf 'Using PACKAGE_VERSION=%s\n' "$PACKAGE_VERSION"

docker run --rm -u root \
    -v "$PWD":/builder/package/tollgate-wrt \
    -v "$ARTIFACT_DIR":/artifacts \
    -e PACKAGE_VERSION="$PACKAGE_VERSION" \
    "openwrt/sdk:${SDK_TAG}" \
    sh -lc '
        printf "deb https://deb.debian.org/debian bookworm-backports main\n" > /etc/apt/sources.list.d/backports.list &&
        apt-get update &&
        apt-get install -y -t bookworm-backports golang-go &&
        cd /builder &&
        make defconfig &&
        printf "CONFIG_USE_APK=y\nCONFIG_PACKAGE_tollgate-wrt=y\nCONFIG_PACKAGE_nodogsplash=y\nCONFIG_PACKAGE_luci=y\nCONFIG_PACKAGE_jq=y\n" >> .config &&
        make defconfig &&
        env PACKAGE_VERSION="$PACKAGE_VERSION" make -j1 V=s package/tollgate-wrt/compile &&
        rm -rf /artifacts/apk-25_12 &&
        mkdir -p /artifacts/apk-25_12 &&
        cp -R /builder/bin/packages/. /artifacts/apk-25_12/
    '
