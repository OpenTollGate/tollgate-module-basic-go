#!/bin/sh
# Read (or verify) the official Go toolchain of an OpenWrt release.
#
# The OpenWrt SDK does not pin a Go version itself: its packages feed does.
# Every release line has a feed branch (openwrt/packages "openwrt-<series>")
# whose lang/golang defines the toolchain that line builds Go packages with,
# and every point release publishes that toolchain to
# downloads.openwrt.org/releases/<release>/packages/ — the exact feed the
# digest-pinned SDK image resolves against. This script reads both, so the
# manifest's view (packaging/build-inputs.json .openwrt_sdk.go_per_release
# and .go.version) is never trusted blindly: it can always be re-derived
# from the SDK's own sources.
#
#   print <series>       official golang on the feed branch of a release
#                        line (e.g. 24.10 -> branch openwrt-24.10). This is
#                        the alignment target: what the next point release
#                        of that line will build with.
#   released [release]   golang actually published in the released feed of a
#                        point release (default: the manifest's pinned
#                        .openwrt_sdk.release). This is the ground truth of
#                        the digest-pinned SDK image.
#   check                verify the manifest against the live feeds: every
#                        go_per_release entry must match its branch, and the
#                        pinned .go.version must match the released feed of
#                        the pinned SDK release. Exit 1 on drift.
#   update               rewrite drifted go_per_release entries from the
#                        live branches. The .go.version pin is NOT touched —
#                        bumping the build toolchain is an intentional
#                        decision (see docs/reproducible-builds.md).
#
# Requirements: curl, jq (same as scripts/update-build-inputs.sh).
#
# Feed layouts handled:
#   <= 24.10 (single version): lang/golang/golang/Makefile carries
#     GO_VERSION_MAJOR_MINOR / GO_VERSION_PATCH.
#   >= 25.12 (multi version): lang/golang/golang-values.mk carries
#     GO_DEFAULT_VERSION=<major.minor>, and the per-version directory
#     lang/golang/golang<major.minor>/Makefile carries GO_VERSION_PATCH
#     (and GO_VERSION_RC for pre-releases).
set -eu

MODE="${1:-check}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TG_ROOT="$SCRIPT_DIR/.."
export TG_ROOT
. "$TG_ROOT/packaging/build-env.sh"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

usage() {
    sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//' >&2
    exit 2
}

feed_raw() { # <branch> <path>
    curl -sf --max-time 30 "https://raw.githubusercontent.com/openwrt/packages/$1/$2" || true
}

mk_var() { # <makefile-text> <VAR>
    printf '%s' "$1" | sed -n "s/^$2:=\(.*\)\$/\1/p" | head -1
}

# Official golang of a release line's feed branch. Prints empty (rather than
# dying) when the branch is unreachable, so check can report it as drift.
branch_go() { # <series>
    _branch="openwrt-$1"
    _values="$(feed_raw "$_branch" lang/golang/golang-values.mk)"
    _mm="$(mk_var "$_values" GO_DEFAULT_VERSION)"
    if [ -n "$_mm" ]; then
        _mf="$(feed_raw "$_branch" "lang/golang/golang$_mm/Makefile")"
        _rc="$(mk_var "$_mf" GO_VERSION_RC)"
        _patch="$(mk_var "$_mf" GO_VERSION_PATCH)"
        if [ -n "$_rc" ]; then printf '%s.0rc%s' "$_mm" "$_rc"
        elif [ -n "$_patch" ]; then printf '%s.%s' "$_mm" "$_patch"
        else printf '%s' "$_mm"; fi
    else
        _mf="$(feed_raw "$_branch" lang/golang/golang/Makefile)"
        _mm="$(mk_var "$_mf" GO_VERSION_MAJOR_MINOR)"
        [ -n "$_mm" ] || return 1
        _patch="$(mk_var "$_mf" GO_VERSION_PATCH)"
        if [ -n "$_patch" ]; then printf '%s.%s' "$_mm" "$_patch"
        else printf '%s' "$_mm"; fi
    fi
}

# golang published in the released feed a point release's SDK resolves
# against. The version is arch-independent; x86_64 always exists.
released_go() { # <release>
    _url="https://downloads.openwrt.org/releases/$1/packages/x86_64/packages/"
    _listing="$(curl -sf --max-time 30 "$_url" || true)"
    [ -n "$_listing" ] || return 1
    # golang_1.21.13-1_x86_64.ipk (<= 24.10) / golang1.26-1.26.8-r1.apk
    # (>= 25.12). Sibling packages (golang-doc, golang-github-*, …) carry
    # the same version; sort -u collapses them.
    printf '%s' "$_listing" \
        | grep -oE 'golang(_[0-9]+\.[0-9]+\.[0-9]+|[0-9]+\.[0-9]+-[0-9]+\.[0-9]+\.[0-9]+)-' \
        | sed -e 's/^golang_//' -e 's/^golang[0-9.]*-//' -e 's/-$//' \
        | sort -u | head -1
}

drift=0
report() { # <what> <manifest> <live>
    if [ "$2" != "$3" ]; then
        drift=$((drift + 1))
        printf 'DRIFT %s: manifest=%s live=%s\n' "$1" "$2" "$3" >&2
    fi
}

map_series() { # series keys of go_per_release, comment fields skipped
    jq -r '.openwrt_sdk.go_per_release | keys[] | select(startswith("_") | not)' "$TG_BUILD_INPUTS"
}

case "$MODE" in
print)
    [ $# -ge 2 ] || usage
    _v="$(branch_go "$2")" || true
    [ -n "$_v" ] || tg_die "cannot resolve the official golang for openwrt/$2 (branch or feed layout not found)"
    printf '%s\n' "$_v"
    ;;
released)
    _rel="${2:-$SDK_RELEASE}"
    _v="$(released_go "$_rel")" || true
    [ -n "$_v" ] || tg_die "cannot resolve the golang published in the released feed of OpenWrt $_rel"
    printf '%s\n' "$_v"
    ;;
check|update)
    for _series in $(map_series); do
        _live="$(branch_go "$_series")" || true
        _pinned="$(jq -r --arg s "$_series" '.openwrt_sdk.go_per_release[$s]' "$TG_BUILD_INPUTS")"
        if [ "$MODE" = update ] && [ -n "$_live" ] && [ "$_live" != "$_pinned" ]; then
            jq --arg s "$_series" --arg v "$_live" \
                '.openwrt_sdk.go_per_release[$s] = $v' "$TG_BUILD_INPUTS" > "$TMP/bi.json" \
                && mv "$TMP/bi.json" "$TG_BUILD_INPUTS"
            printf 'UPDATED go_per_release %s -> %s\n' "$_series" "$_live"
        else
            report "sdk_go:$_series" "$_pinned" "${_live:-<unreachable>}"
        fi
    done

    # The active pin must follow the SDK release actually in use.
    _series="$(sdk_series "$SDK_RELEASE")"
    _pinned="$(jq -r --arg s "$_series" '.openwrt_sdk.go_per_release[$s] // empty' "$TG_BUILD_INPUTS")"
    [ -n "$_pinned" ] || tg_die "go_per_release has no entry for '$_series' (the series of the pinned SDK release $SDK_RELEASE) — add one"
    _released="$(released_go "$SDK_RELEASE")" || true
    report "go-version-vs-sdk:$SDK_RELEASE" "$GO_VERSION" "${_released:-<unreachable>}"

    if [ "$_pinned" != "$_released" ] && [ -n "$_released" ]; then
        printf 'NOTE sdk_go:%s: branch head %s is ahead of the released %s feed (%s) — the next point release of that line picks it up.\n' \
            "$_series" "$_pinned" "$SDK_RELEASE" "$_released"
    fi

    if [ "$drift" -gt 0 ]; then
        printf '%d SDK Go pin(s) drifted from the live feeds. Run: scripts/sdk-go-version.sh update\n' "$drift" >&2
        exit 1
    fi
    echo "SDK Go pins match the live OpenWrt feeds."
    ;;
*)
    usage
    ;;
esac
