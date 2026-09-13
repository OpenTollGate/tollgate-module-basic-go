#!/bin/sh
# Build an OpenWrt-compatible .ipk from a payload tree and metadata.
# Uses standard ar + tar — no OpenWrt SDK required.
#
# Usage: build-ipk.sh <payload_dir> <output.ipk>
#
# <payload_dir>: the root of the target filesystem to install. Everything
#   under this directory becomes data.tar.gz (e.g. payload_dir/usr/bin/foo
#   installs as /usr/bin/foo on target).
#
# Metadata is read from environment:
#   PKG_NAME, PKG_VERSION, ARCH                  (required)
#   MAINTAINER, LICENSE, DEPENDS, PROVIDES,
#   REPLACES, DESCRIPTION                         (optional)
#
# preinst / postinst scripts are copied from the same directory as this
# script if they exist.

set -eu

PAYLOAD_DIR=${1:?payload dir required}
OUTPUT=${2:?output ipk path required}

: "${PKG_NAME:?PKG_NAME required}"
: "${PKG_VERSION:?PKG_VERSION required}"
: "${ARCH:?ARCH required}"

[ -d "$PAYLOAD_DIR" ] || { echo "error: payload dir missing: $PAYLOAD_DIR" >&2; exit 1; }

# ar is invoked from inside $WORK (so member names are bare), which means
# OUTPUT must be absolute or it resolves relative to the wrong cwd.
mkdir -p "$(dirname "$OUTPUT")"
OUTPUT="$(cd "$(dirname "$OUTPUT")" && pwd)/$(basename "$OUTPUT")"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Canonical reproducible-build environment: SOURCE_DATE_EPOCH (and strictness
# flags) come from packaging/build-env.sh + build-inputs.json. TG_ROOT is
# pre-set because $0 inside a sourced POSIX-sh context is the caller's path.
# shellcheck source=build-env.sh
TG_ROOT="$SCRIPT_DIR/.."
export TG_ROOT
. "$SCRIPT_DIR/build-env.sh"

# Prefer GNU tar (gtar on macOS) — BSD tar lacks --sort / --owner flags
# needed for deterministic output.
if command -v gtar >/dev/null 2>&1; then
    TAR=gtar
else
    TAR=tar
fi

# gzip -n: no filename, no timestamp in the gzip header. tar's internal -z
# does not expose this, so every archive is written through an explicit
# `tar -cf - | gzip -n` pipe. Member mtimes are pinned by --mtime below, so
# normalizing the intermediate files is unnecessary.
GZIP_BIN=gzip

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

mkdir "$WORK/CONTROL"

# 1. control file
{
    printf 'Package: %s\n' "$PKG_NAME"
    printf 'Version: %s\n' "$PKG_VERSION"
    printf 'Architecture: %s\n' "$ARCH"
    [ -n "${MAINTAINER:-}" ]  && printf 'Maintainer: %s\n'  "$MAINTAINER"
    [ -n "${LICENSE:-}" ]     && printf 'License: %s\n'     "$LICENSE"
    [ -n "${DEPENDS:-}" ]     && printf 'Depends: %s\n'     "$DEPENDS"
    [ -n "${PROVIDES:-}" ]    && printf 'Provides: %s\n'    "$PROVIDES"
    [ -n "${REPLACES:-}" ]    && printf 'Replaces: %s\n'    "$REPLACES"
    [ -n "${DESCRIPTION:-}" ] && printf 'Description: %s\n' "$DESCRIPTION"
} > "$WORK/CONTROL/control"

# 2. preinst / postinst
for s in preinst postinst; do
    if [ -f "$SCRIPT_DIR/$s" ]; then
        cp "$SCRIPT_DIR/$s" "$WORK/CONTROL/$s"
        chmod 0755 "$WORK/CONTROL/$s"
    fi
done

# 3. control.tar.gz
( cd "$WORK/CONTROL" && \
  "$TAR" --sort=name --mtime="@$SOURCE_DATE_EPOCH" --owner=0 --group=0 --numeric-owner \
    -cf - . | "$GZIP_BIN" -n > "$WORK/control.tar.gz" )

# 4. data.tar.gz
( cd "$PAYLOAD_DIR" && \
  "$TAR" --sort=name --mtime="@$SOURCE_DATE_EPOCH" --owner=0 --group=0 --numeric-owner \
    -cf - . | "$GZIP_BIN" -n > "$WORK/data.tar.gz" )

# 5. debian-binary
printf '2.0\n' > "$WORK/debian-binary"

# 6. Wrap as gzipped tar — this is the OpenWrt opkg-build ipk format
# (NOT the Debian ar format). opkg itself reads either, but the
# OpenWrt-side tooling in the wild assumes tar.gz wrapping
# (e.g. `tar -xzOf foo.ipk ./control.tar.gz`), so we have to match.
# Deterministic: fixed member order, mtimes pinned to SOURCE_DATE_EPOCH,
# owner/group normalized, gzip header stripped of timestamp.
rm -f "$OUTPUT"
( cd "$WORK" && \
  "$TAR" --mtime="@$SOURCE_DATE_EPOCH" --owner=0 --group=0 --numeric-owner \
    -cf - ./debian-binary ./data.tar.gz ./control.tar.gz \
    | "$GZIP_BIN" -n > "$OUTPUT" )

size=$(wc -c < "$OUTPUT" | tr -d ' ')
printf 'Built %s (%s bytes)\n' "$OUTPUT" "$size"
