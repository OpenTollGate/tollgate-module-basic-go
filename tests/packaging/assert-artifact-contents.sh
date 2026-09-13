#!/usr/bin/env bash
# Assert that a built tollgate-wrt package actually ships the runtime files
# that live in the source tree under packaging/files/.
#
# The packaging recipes have historically installed source files one by one,
# so adding a file to packaging/files/ did not add it to the package: the
# firewall ruleset shipped without etc/nftables.d/30-backend-firewall.nft and
# left the backend API on :2121 exposed on every non-br-lan interface. This
# script encodes the invariant so that drift fails the build instead of
# reaching a router.
#
# Usage: tests/packaging/assert-artifact-contents.sh <package-file>
#
#   .ipk  -> read the data tarball with tar
#   .apk  -> read the package manifest with apk (override with APK_BIN=...)
#
# Exit 0 when the invariant holds, 1 otherwise.

set -euo pipefail

PKG=${1:-}
if [ -z "$PKG" ]; then
    echo "usage: $0 <package-file>" >&2
    exit 2
fi
if [ ! -f "$PKG" ]; then
    echo "FAIL: package not found: $PKG" >&2
    exit 2
fi

REPO_ROOT=$(cd -- "$(dirname -- "$0")/../.." && pwd)
FILES_DIR="$REPO_ROOT/packaging/files"
NFT_DIR="$FILES_DIR/etc/nftables.d"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
LIST="$WORK/artifact-files.txt"

# ---------------------------------------------------------------- file lists
case "$PKG" in
    *.ipk)
        # OpenWrt .ipk built by packaging/build-ipk.sh is a gzipped tar whose
        # data.tar.gz member is the installed filesystem.
        tar xzOf "$PKG" ./data.tar.gz | tar tzf - | sed -e 's#^\./##' -e 's#/$##' \
            | grep -v '^$' | sort -u > "$LIST"
        ;;
    *.apk)
        APK_BIN=${APK_BIN:-apk}
        # apk-tools 3 "manifest" needs an installed-package database, which a
        # build container (and a plain checkout) does not have. Extract the
        # package instead and read the real on-disk file list.
        EXTRACT="$WORK/apk-extract"
        mkdir -p "$EXTRACT"
        "$APK_BIN" extract --allow-untrusted --destination "$EXTRACT" "$PKG" >/dev/null
        ( cd "$EXTRACT" && find . \( -type f -o -type l \) ) \
            | sed -e 's#^\./##' -e 's#/$##' | grep -v '^$' | sort -u > "$LIST"
        ;;
    *)
        echo "FAIL: unsupported package type (expected .ipk or .apk): $PKG" >&2
        exit 2
        ;;
esac

echo "Artifact: $PKG"
echo "Files in artifact: $(wc -l < "$LIST" | tr -d ' ')"

# ------------------------------------------------------- the nftables ruleset
# INVARIANT (this is the regression): every ruleset file in the source tree is
# installed into /etc/nftables.d/ — the backend firewall included.
if [ ! -d "$NFT_DIR" ]; then
    echo "FAIL: source ruleset directory missing: $NFT_DIR" >&2
    exit 1
fi

nft_expected=0
rc=0
while IFS= read -r f; do
    rel="etc/nftables.d/$(basename "$f")"
    nft_expected=$((nft_expected + 1))
    if grep -F -x "$rel" "$LIST" >/dev/null; then
        echo "  ok   $rel"
    else
        echo "  MISSING $rel  (source: packaging/files/$rel)"
        rc=1
    fi
done < <(find "$NFT_DIR" -maxdepth 1 -type f -name '*.nft' | sort)

if [ "$nft_expected" -eq 0 ]; then
    echo "FAIL: no *.nft ruleset files found in $NFT_DIR" >&2
    exit 1
fi

if [ "$rc" -ne 0 ]; then
    echo
    echo "FAIL: the package does not ship the full nftables ruleset."
    echo "      A source file in packaging/files/etc/nftables.d/ was left out of"
    echo "      the packaging recipe, so the rules it carries are not enforced"
    echo "      on the installed device."
    exit 1
fi

# -------------------------------------------------- full runtime-set report
# The recipes also install other packaging/files/ entries by explicit path.
# Report any that this packaging path does not ship. This is informational:
# each divergence is its own defect and is tracked separately from the
# nftables regression this script guards.
MISSING="$WORK/missing.txt"
: > "$MISSING"
while IFS= read -r f; do
    rel=${f#"$FILES_DIR"/}
    case "$rel" in
        tollgate-captive-portal-site/*) rel="etc/tollgate/$rel" ;;
        man/man8/*) rel="usr/share/man/man8/$(basename "$rel")" ;;
    esac
    grep -F -x "$rel" "$LIST" >/dev/null || echo "$rel" >> "$MISSING"
done < <(find "$FILES_DIR" -type f | sort)

if [ -s "$MISSING" ]; then
    echo
    echo "WARNING: packaging/files entries not shipped by this packaging path:"
    sed 's/^/  - /' "$MISSING"
    echo "  (pre-existing divergence in this packaging path, not the nftables"
    echo "   ruleset invariant checked above)"
fi

echo
echo "PASS: all $nft_expected etc/nftables.d/*.nft ruleset file(s) are packaged."
