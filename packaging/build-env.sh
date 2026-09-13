#!/bin/sh
# packaging/build-env.sh — canonical reproducible-build environment.
#
# Single source of truth for every byte-affecting build input. Source this
# from every build script; do NOT recompute versions, epochs, or ldflags
# independently elsewhere. See docs/reproducible-builds.md.
#
# POSIX sh compatible (sourced by bash and /bin/sh scripts alike).
#
# Exports:
#   TG_ROOT             repository root
#   SOURCE_DATE_EPOCH   deterministic epoch; env override wins, otherwise
#                       the TollGate HEAD commit timestamp (git %ct)
#   BUILD_TIME_UTC      human-readable UTC rendering of SOURCE_DATE_EPOCH
#   GO_VERSION NODE_VERSION NPM_VERSION UPX_VERSION
#   PORTAL_REPO PORTAL_COMMIT
#   TG_STRICT_INPUTS    "1" = die on any unpinned input (default when
#                       SOURCE_DATE_EPOCH came from the environment)
#
# Helpers (functions, not exports):
#   sdk_image_ref <target>     openwrt/sdk image pinned by digest
#   go_ldflags  <pkg-version>  service binary ldflags (no wall clock)
#   cli_ldflags <pkg-version>  go_ldflags + main.version for the CLI module
#   normalize_mtime <path>...  recursive touch to SOURCE_DATE_EPOCH
#   tg_die <msg>               stderr + exit 1

# ---- locate repo -----------------------------------------------------------

# TG_ROOT may be pre-set by POSIX-sh callers (where $0 during sourcing is
# the caller's path, not this file's). Otherwise derive from $0, which is
# correct for the common `bash packaging/<script>` and direct invocations.
TG_ROOT="${TG_ROOT:-$(cd "$(dirname "$0")/.." && pwd)}"
TG_BUILD_INPUTS="$TG_ROOT/packaging/build-inputs.json"

tg_die() { printf 'build-env: ERROR: %s\n' "$1" >&2; exit 1; }

# Pinned toolchains, when installed under TG_TOOLS, take precedence over
# whatever happens to be on PATH (see scripts/fetch-upx.sh, repro-test, and
# docs/reproducible-builds.md for the expected layout: TG_TOOLS/go/bin,
# TG_TOOLS/node/bin).
if [ -n "${TG_TOOLS:-}" ]; then
    for _d in "$TG_TOOLS/go/bin" "$TG_TOOLS/node/bin"; do
        [ -d "$_d" ] && case ":$PATH:" in
            *":$_d:"*) ;;
            *) PATH="$_d:$PATH" ;;
        esac
    done
    export PATH
fi

command -v jq >/dev/null 2>&1 || tg_die "jq is required to read $TG_BUILD_INPUTS"
[ -f "$TG_BUILD_INPUTS" ] || tg_die "missing $TG_BUILD_INPUTS"

# Locale and timezone affect tool output (collation order behind sort,
# tar member ordering, date rendering) and therefore artifact bytes.
# OpenWrt's own scripts/get_source_date_epoch.sh pins the same variables;
# same-host two-root repro tests cannot catch a locale difference.
LANG=C
LC_ALL=C
TZ=UTC
export LANG LC_ALL TZ

# ---- pinned tool versions --------------------------------------------------

GO_VERSION="$(jq -r '.go.version' "$TG_BUILD_INPUTS")"
NODE_VERSION="$(jq -r '.node.version' "$TG_BUILD_INPUTS")"
NPM_VERSION="$(jq -r '.npm.version' "$TG_BUILD_INPUTS")"
UPX_VERSION="$(jq -r '.upx.version' "$TG_BUILD_INPUTS")"
PORTAL_REPO="$(jq -r '.portal.repo' "$TG_BUILD_INPUTS")"
PORTAL_COMMIT="$(jq -r '.portal.commit' "$TG_BUILD_INPUTS")"
SDK_RELEASE="$(jq -r '.openwrt_sdk.release' "$TG_BUILD_INPUTS")"

[ -n "$GO_VERSION" ] && [ "$GO_VERSION" != "null" ] || tg_die "go version missing from manifest"
[ -n "$PORTAL_COMMIT" ] && [ "$PORTAL_COMMIT" != "null" ] || tg_die "portal.commit missing from manifest"
# A pinned portal commit must look like a SHA, not a floating ref.
case "$PORTAL_COMMIT" in
    [0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]*) ;;
    *) tg_die "portal.commit '$PORTAL_COMMIT' is not a git SHA — refusing floating ref" ;;
esac

export GO_VERSION NODE_VERSION NPM_VERSION UPX_VERSION PORTAL_REPO PORTAL_COMMIT

# ---- SOURCE_DATE_EPOCH -----------------------------------------------------

if [ -n "${SOURCE_DATE_EPOCH:-}" ]; then
    TG_STRICT_INPUTS="${TG_STRICT_INPUTS:-1}"
    export TG_STRICT_INPUTS
else
    if command -v git >/dev/null 2>&1 && git -C "$TG_ROOT" rev-parse HEAD >/dev/null 2>&1; then
        SOURCE_DATE_EPOCH="$(git -C "$TG_ROOT" log -1 --format=%ct HEAD)"
        [ -n "$SOURCE_DATE_EPOCH" ] || tg_die "could not read HEAD commit timestamp"
        export SOURCE_DATE_EPOCH
    else
        tg_die "SOURCE_DATE_EPOCH not set and no git history available to derive it; export it explicitly"
    fi
fi
case "$SOURCE_DATE_EPOCH" in
    ''|*[!0-9]*) tg_die "SOURCE_DATE_EPOCH must be a unix epoch integer, got '$SOURCE_DATE_EPOCH'" ;;
esac

# Human-readable, deterministic. GNU date first, BSD fallback.
BUILD_TIME_UTC="$(date -u -d "@$SOURCE_DATE_EPOCH" '+%Y-%m-%d %H:%M:%S UTC' 2>/dev/null \
    || date -u -r "$SOURCE_DATE_EPOCH" '+%Y-%m-%d %H:%M:%S UTC')" || tg_die "cannot render epoch"
export BUILD_TIME_UTC

# ---- helpers ---------------------------------------------------------------

sdk_image_ref() {
    # Accepts either a bare target (e.g. mediatek-filogic) or a full tag
    # (mediatek-filogic-25.12.0); normalizes to bare + pinned release.
    _tgt="$1"
    case "$_tgt" in
        *-"$SDK_RELEASE") _tgt="${_tgt%-$SDK_RELEASE}" ;;
    esac
    _digest="$(jq -r --arg t "$_tgt" '.openwrt_sdk.targets[$t].digest' "$TG_BUILD_INPUTS")"
    [ -n "$_digest" ] && [ "$_digest" != "null" ] \
        || tg_die "no SDK digest pinned for target '$_tgt' in build-inputs.json"
    printf '%s:%s@%s' "$(jq -r '.openwrt_sdk.image' "$TG_BUILD_INPUTS")" "$_tgt-$SDK_RELEASE" "$_digest"
}

# Deterministic ldflags for the service binaries (module
# github.com/OpenTollGate/tollgate-module-basic-go). BuildTime comes from
# SOURCE_DATE_EPOCH via $BUILD_TIME_UTC — never from the wall clock.
# GitBranch is pinned to "main": release builds are main-line; a branch
# build that embedded its real name would vary per checkout.
go_ldflags() {
    _ver="$1"
    printf "%s" "-s -w \
-X 'github.com/OpenTollGate/tollgate-module-basic-go/src/cli.Version=$_ver' \
-X 'github.com/OpenTollGate/tollgate-module-basic-go/src/cli.GitCommit=${TG_GIT_COMMIT:-unknown}' \
-X 'github.com/OpenTollGate/tollgate-module-basic-go/src/cli.BuildTime=$BUILD_TIME_UTC' \
-X 'github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager.GitBranch=main'"
}

# The tollgate CLI is a separate Go module that does not link the src/cli
# package, so its version must be injected via main.version.
cli_ldflags() {
    printf "%s %s" "$(go_ldflags "$1")" "-X 'main.version=$1'"
}

# Normalize mtimes recursively so packaging layers that read file mtimes
# (OpenWrt SDK apk packaging in particular) see deterministic values.
normalize_mtime() {
    for _p in "$@"; do
        [ -e "$_p" ] || continue
        find "$_p" -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +
    done
}
