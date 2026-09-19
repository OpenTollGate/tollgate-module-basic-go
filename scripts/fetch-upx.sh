#!/bin/sh
# Fetch the pinned UPX binary (version + sha256 from
# packaging/build-inputs.json) into TG_TOOLS/upx and print the directory.
# Usage: eval or capture: UPX_DIR=$(bash scripts/fetch-upx.sh)
set -eu

TG_TOOLS="${TG_TOOLS:-$HOME/.cache/tollgate-tools}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TG_ROOT="$SCRIPT_DIR/.."
export TG_ROOT
. "$TG_ROOT/packaging/build-env.sh"

URL="$(jq -r '.upx.tarball_linux_amd64.url' "$TG_BUILD_INPUTS")"
SHA="$(jq -r '.upx.tarball_linux_amd64.sha256' "$TG_BUILD_INPUTS")"
DEST="$TG_TOOLS/upx/$UPX_VERSION"
BIN="$DEST/upx"

if [ -x "$BIN" ] && [ "$("$BIN" --version 2>/dev/null | head -1)" = "upx $UPX_VERSION" ]; then
    printf '%s\n' "$DEST"
    exit 0
fi

mkdir -p "$DEST"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
curl -sSL "$URL" -o "$TMP/upx.tar.xz"
printf '%s  %s\n' "$SHA" "$TMP/upx.tar.xz" | sha256sum -c - >&2
tar xJf "$TMP/upx.tar.xz" -C "$TMP"
cp "$TMP"/upx-"$UPX_VERSION"-amd64_linux/upx "$BIN"
chmod +x "$BIN"
"$BIN" --version | head -1 >&2
printf '%s\n' "$DEST"
