#!/bin/sh

# Local developer helper for ad hoc OpenWrt 25.12 apk builds.
set -eu

SDK_TAG="${SDK_TAG:-ath79-generic-25.12.0}"
PACKAGE_VERSION="${PACKAGE_VERSION:-0.0.0-r0}"
ARTIFACT_DIR="${ARTIFACT_DIR:-/tmp/tollgate-build-artifacts}"
TOLLGATE_PKG_SOURCE_URL="${TOLLGATE_PKG_SOURCE_URL:-}"

mkdir -p "$ARTIFACT_DIR"

# Anchor to repo root so the docker mount is correct regardless of CWD.
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Resolve the source SHA on the host, where git works. The Makefile's
# $(shell git rev-parse HEAD) runs at parse time inside the SDK container
# with cwd=/builder (not a git repo), so it returns empty and the SDK
# silently writes a zero-byte source tarball. Override on the make command
# line so the SDK clones the right commit from TOLLGATE_PKG_SOURCE_URL.
if ! PKG_SOURCE_VERSION="$(git -C "$REPO_ROOT" rev-parse HEAD 2>/dev/null)"; then
    printf 'error: %s is not a git checkout — cannot determine PKG_SOURCE_VERSION\n' "$REPO_ROOT" >&2
    exit 1
fi

printf 'Using SDK_TAG=%s\n' "$SDK_TAG"
printf 'Using PACKAGE_VERSION=%s\n' "$PACKAGE_VERSION"
printf 'Using PKG_SOURCE_VERSION=%s\n' "$PKG_SOURCE_VERSION"
if [ -n "$TOLLGATE_PKG_SOURCE_URL" ]; then
    printf 'Using TOLLGATE_PKG_SOURCE_URL=%s\n' "$TOLLGATE_PKG_SOURCE_URL"
fi

# Only forward TOLLGATE_PKG_SOURCE_URL when the caller actually set it.
# Forwarding it as an empty string defeats the Makefile's ?= default and
# leaves PKG_SOURCE_URL empty, which silently breaks the SDK source fetch.
docker_env_args="-e PACKAGE_VERSION=$PACKAGE_VERSION"
if [ -n "$TOLLGATE_PKG_SOURCE_URL" ]; then
    docker_env_args="$docker_env_args -e TOLLGATE_PKG_SOURCE_URL=$TOLLGATE_PKG_SOURCE_URL"
fi

# Use a double-quoted sh -lc string so the host shell expands
# $PKG_SOURCE_VERSION into a literal SHA in the command. Make's command-
# line override only takes effect at parse time when the value is a real
# string, not an env-var reference resolved later inside the container.
docker run --rm -u root \
    -v "$REPO_ROOT":/builder/package/tollgate-wrt \
    -v "$ARTIFACT_DIR":/artifacts \
    $docker_env_args \
    "openwrt/sdk:${SDK_TAG}" \
    sh -lc "
        printf 'deb https://deb.debian.org/debian bookworm-backports main\n' > /etc/apt/sources.list.d/backports.list &&
        apt-get update &&
        apt-get install -y -t bookworm-backports golang-go &&
        cd /builder &&
        make defconfig &&
        printf 'CONFIG_USE_APK=y\nCONFIG_PACKAGE_tollgate-wrt=y\nCONFIG_PACKAGE_nodogsplash=y\nCONFIG_PACKAGE_luci=y\nCONFIG_PACKAGE_jq=y\n' >> .config &&
        make defconfig &&
        make -j1 V=s PKG_SOURCE_VERSION=$PKG_SOURCE_VERSION package/tollgate-wrt/compile &&
        rm -rf /artifacts/apk-25_12 &&
        mkdir -p /artifacts/apk-25_12 &&
        cp -R /builder/bin/packages/. /artifacts/apk-25_12/
    "
