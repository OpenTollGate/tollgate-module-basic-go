#!/usr/bin/env bash
# Assert that a built tollgate-wrt package actually ships the runtime files
# that live in the source tree under packaging/files/.
#
# The packaging recipes have historically installed source files one by one,
# so adding a file to packaging/files/ did not add it to the package: the
# firewall ruleset shipped without etc/nftables.d/30-backend-firewall.nft and
# left the backend API on :2121 exposed on every non-br-lan interface. This
# script encodes the invariant so that drift fails the build instead of
# reaching a router: the packaged etc/nftables.d/ set must EQUAL the source
# set (no missing, no stray extra file) and each ruleset file must match its
# source byte-for-byte in size (an empty or truncated file is a failure, not
# a pass).
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
        # Expand the same payload so individual file sizes can be checked.
        ARTIFACT_ROOT="$WORK/data"
        mkdir -p "$ARTIFACT_ROOT"
        tar xzOf "$PKG" ./data.tar.gz | tar xzf - -C "$ARTIFACT_ROOT"
        ;;
    *.apk)
        APK_BIN=${APK_BIN:-apk}
        # apk-tools 3 "manifest" needs an installed-package database, which a
        # build container (and a plain checkout) does not have. Extract the
        # package instead and read the real on-disk file list.
        ARTIFACT_ROOT="$WORK/apk-extract"
        mkdir -p "$ARTIFACT_ROOT"
        "$APK_BIN" extract --allow-untrusted --destination "$ARTIFACT_ROOT" "$PKG" >/dev/null
        ( cd "$ARTIFACT_ROOT" && find . \( -type f -o -type l \) ) \
            | sed -e 's#^\./##' -e 's#/$##' | grep -v '^$' | sort -u > "$LIST"
        ;;
    *)
        echo "FAIL: unsupported package type (expected .ipk or .apk): $PKG" >&2
        exit 2
        ;;
esac

# Size in bytes of <relative-path> inside the artifact; empty when absent.
artifact_size() {
    if [ -f "$ARTIFACT_ROOT/$1" ]; then
        wc -c < "$ARTIFACT_ROOT/$1" | tr -d ' '
    fi
}

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
    src_size=$(wc -c < "$f" | tr -d ' ')
    if ! grep -F -x "$rel" "$LIST" >/dev/null; then
        echo "  MISSING $rel  (source: packaging/files/$rel)"
        rc=1
        continue
    fi
    art_size=$(artifact_size "$rel")
    if [ -z "$art_size" ] || [ "$art_size" -eq 0 ]; then
        echo "  EMPTY   $rel  (packaged copy is missing or zero bytes)"
        rc=1
    elif [ "$art_size" -ne "$src_size" ]; then
        echo "  SIZE    $rel  (packaged $art_size bytes, source $src_size bytes)"
        rc=1
    else
        echo "  ok   $rel ($art_size bytes)"
    fi
done < <(find "$NFT_DIR" -maxdepth 1 -type f -name '*.nft' | sort)

if [ "$nft_expected" -eq 0 ]; then
    echo "FAIL: no *.nft ruleset files found in $NFT_DIR" >&2
    exit 1
fi

# The other direction: set equality. A file under etc/nftables.d/ that has no
# counterpart in packaging/files/etc/nftables.d/ means the packaging path
# copied more than the source tree intends (README, *.nft.bak, editor backup,
# *.rpmnew, ...). Nothing that cannot be traced to a source file may ship.
unexpected=0
while IFS= read -r rel; do
    [ -n "$rel" ] || continue
    base=$(basename "$rel")
    if [ ! -f "$NFT_DIR/$base" ]; then
        echo "  UNEXPECTED  $rel  (no such file in packaging/files/etc/nftables.d/)"
        unexpected=1
    fi
done < <(grep -E '^etc/nftables\.d/' "$LIST" || true)

if [ "$rc" -ne 0 ]; then
    echo
    echo "FAIL: the package does not ship the full nftables ruleset intact."
    echo "      A source file in packaging/files/etc/nftables.d/ was left out,"
    echo "      emptied, or truncated by the packaging recipe, so the rules it"
    echo "      carries are not enforced on the installed device."
fi

if [ "$unexpected" -ne 0 ]; then
    echo
    echo "FAIL: the package ships file(s) under etc/nftables.d/ with no source"
    echo "      counterpart in packaging/files/etc/nftables.d/. Strays such as"
    echo "      editor backups or READMEs must not reach a router; the packaging"
    echo "      paths install *.nft files only."
fi

if [ "$rc" -ne 0 ] || [ "$unexpected" -ne 0 ]; then
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
echo "PASS: all $nft_expected etc/nftables.d/*.nft ruleset file(s) are packaged intact (and nothing else is)."
