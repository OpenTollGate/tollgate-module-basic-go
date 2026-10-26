#!/usr/bin/env bash
# manual-build-check — execute docs/building-from-source.md from a clean cache.
#
# This is deliberately the same sequence the manual tells a human to run:
#   portal build (unless skipped) -> all-module Go battery in a FRESH module
#   cache -> optional package build.
#
# The fresh GOMODCACHE/GOPATH is the point: a warm cache hides missing go.sum
# entries, which is how #1036 reached a user while cached CI stayed green.
#
# Usage:
#   scripts/manual-build-check.sh                 # portal + cold battery
#   scripts/manual-build-check.sh --battery-only  # cold battery only
#   scripts/manual-build-check.sh --no-portal     # same as --battery-only
#   MANUAL_CHECK_PACKAGE=1 ARCH=x86_64 scripts/manual-build-check.sh
#
# Prerequisites: the pinned Go toolchain (packaging/build-inputs.json), jq,
# and for the default path the pinned Node/npm. The package step additionally
# needs the OpenWrt SDK/docker path used by packaging/local-build-ipk.sh.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

BATTERY_ONLY=0
BUILD_PACKAGE="${MANUAL_CHECK_PACKAGE:-0}"
for arg in "$@"; do
    case "$arg" in
        --battery-only|--no-portal) BATTERY_ONLY=1 ;;
        --package) BUILD_PACKAGE=1 ;;
        -h|--help)
            sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//'
            exit 0
            ;;
        *) echo "ERROR: unknown option: $arg" >&2; exit 2 ;;
    esac
done

# Reuse the canonical pin loader. TG_ROOT is pre-set so sourcing from this
# script cannot derive the wrong root.
TG_ROOT="$ROOT" . "$ROOT/packaging/build-env.sh"

active_go="$(go version 2>/dev/null | awk '{print $3}' | sed 's/^go//')"
if [ "$active_go" != "$GO_VERSION" ]; then
    echo "ERROR: go ${active_go:-missing} != pinned $GO_VERSION (packaging/build-inputs.json)" >&2
    echo "       install the pinned toolchain or put it under TG_TOOLS/go/bin" >&2
    exit 1
fi
echo "toolchain: go $active_go (pinned), jq $(jq --version 2>/dev/null || echo missing)"

if [ "$BATTERY_ONLY" = 0 ]; then
    active_node="$(node --version 2>/dev/null || true)"
    active_npm="$(npm --version 2>/dev/null || true)"
    if [ "$active_node" != "v$NODE_VERSION" ] || [ "$active_npm" != "$NPM_VERSION" ]; then
        echo "ERROR: node ${active_node:-missing}/npm ${active_npm:-missing} != pinned v$NODE_VERSION/$NPM_VERSION" >&2
        echo "       install the pinned Node/npm, put them under TG_TOOLS/node/bin," >&2
        echo "       or re-run with --battery-only to skip the portal build" >&2
        exit 1
    fi
    echo "portal: building with node $active_node / npm $active_npm"
    bash packaging/portal-build.sh
fi

tmp="$(mktemp -d "${TMPDIR:-/tmp}/tollgate-manual-check.XXXXXX")"
# Go's module cache is read-only (dirs 0555, files 0444). A plain rm -rf
# cannot descend into it; make the tree writable first, and never let cleanup
# mask a successful battery with a permission error.
cleanup() {
    chmod -R u+w "$tmp" 2>/dev/null || true
    rm -rf "$tmp" 2>/dev/null || true
}
trap cleanup EXIT
export GOMODCACHE="$tmp/gomod"
export GOPATH="$tmp/gopath"
export GOTOOLCHAIN=local
mkdir -p "$GOMODCACHE" "$GOPATH"
echo "cold caches: GOMODCACHE=$GOMODCACHE GOPATH=$GOPATH"

bash scripts/go-battery.sh

if [ "$BUILD_PACKAGE" = 1 ]; then
    ARCH="${ARCH:-x86_64}"
    echo "building ipk for ARCH=$ARCH"
    ARCH="$ARCH" bash packaging/local-build-ipk.sh
    ls -lh packaging/tollgate-wrt_*_"$ARCH".ipk 2>/dev/null | tail -3 || true
fi

echo
echo "MANUAL-CHECK-PASS"
echo "Build a package with: make package-ipk ARCH=<arch>"
echo "                 or: make package-apk SDK_TAG=<target-os-line>"
