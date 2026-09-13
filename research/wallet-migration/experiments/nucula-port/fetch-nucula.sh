#!/bin/sh
# fetch-nucula.sh — fetch nucula at the revision this spike measured.
#
# Part of the nucula OpenWrt port spike. OUR script.
#
# LEGAL HYGIENE: nucula has NO LICENSE file, so its source is not committed to
# this branch and is not redistributed by us. This script does not vendor it —
# it downloads the upstream archive at a pinned commit into a scratch directory
# OUTSIDE the research worktree, so anyone reproducing the spike fetches it
# from upstream themselves.
#
# Usage: ./fetch-nucula.sh [dest-dir]      (default: $HOME/repos/nucula-port)
set -eu

REV="8ad081219c74f3e8372551738da37a08834835a9"  # pragma: allowlist secret
# sha256 recorded by the earlier consultant for the tarball they unpacked. The
# GitHub-generated archive is regenerated on demand and its checksum is not
# guaranteed to be byte-identical across GitHub's archiver versions, so the
# authoritative pin is the COMMIT SHA; the archive hash is reported, not
# enforced. If a hash is supplied via EXPECTED_SHA256 it IS enforced.
PRIOR_CONSULTANT_SHA256="53e6d5096a13b091f4755d3d6d588aca845c208da3bf72e503256aa43357c274"  # pragma: allowlist secret
EXPECTED_SHA256="${EXPECTED_SHA256:-}"

DEST="${1:-$HOME/repos/nucula-port}"
ARCHIVE="$DEST/nucula-$REV.tar.gz"

mkdir -p "$DEST"

if [ ! -d "$DEST/src/main" ]; then
    if [ ! -f "$ARCHIVE" ]; then
        echo "fetching nucula @ $REV"
        curl -fsSL -o "$ARCHIVE" \
            "https://github.com/zeugmaster/nucula/archive/$REV.tar.gz"
    fi
    GOT=$(sha256sum "$ARCHIVE" | cut -d' ' -f1)
    echo "archive sha256      = $GOT"
    echo "prior consultant sh = $PRIOR_CONSULTANT_SHA256"
    if [ -n "$EXPECTED_SHA256" ] && [ "$GOT" != "$EXPECTED_SHA256" ]; then
        echo "FATAL: sha256 mismatch against EXPECTED_SHA256" >&2
        exit 1
    fi
    mkdir -p "$DEST/src"
    tar -xzf "$ARCHIVE" -C "$DEST/src" --strip-components=1
    echo "unpacked to $DEST/src"
else
    echo "already unpacked: $DEST/src"
fi

test -f "$DEST/src/main/crypto.c" || { echo "FATAL: no main/crypto.c" >&2; exit 1; }
echo "ok: $DEST/src  (rev $REV)"
