#!/usr/bin/env bash
# Variance testing on top of the two-clean-roots harness:
# reprotest rebuilds the same tree under hostile environment variations
# (umask, timezone, locales, file ordering) that a single machine cannot
# introduce between two of its own builds, then diffs the artifacts with
# diffoscope. The two-clean-roots harness proves independent roots agree;
# this proves the build survives environments that differ from ours — the
# umask leak fixed in 0acd0bf shipped precisely because nothing varied it.
#
# Usage: scripts/repro-variance.sh [target] [arch]
#   target: ipk (default — the artifact whose bytes include filesystem
#           metadata; the Go binaries are covered by the clean-roots harness)
#   arch:   x86_64 (default) | aarch64_cortex-a53 | arm_cortex-a7 | ...
#
# Env:
#   VARIATIONS   reprotest variation set (default:
#                +umask,+timezone,+locales,+fileordering; +all for everything
#                including build path, kernel, user/group)
#   REPROTEST    reprotest binary (default: reprotest on PATH)
#   PKG_VERSION  default = VERSION at the repository root
#   SOURCE_DATE_EPOCH / TG_GIT_COMMIT  env-first, like scripts/repro-test.sh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

TARGET="${1:-ipk}"
ARCH="${2:-x86_64}"
# Default variations run on a plain host with no extra setup. The extended
# set needs Debian-class tooling: +fileordering uses disorderfs (FUSE) and
# +locales wants generated locales (locales-all); without them reprotest's
# build step dies with 127 inside the variation. VARIATIONS=+all for everything.
VARIATIONS="${VARIATIONS:-+umask,+timezone}"
REPROTEST_BIN="${REPROTEST:-reprotest}"

command -v "$REPROTEST_BIN" >/dev/null 2>&1 || {
    echo "ERROR: reprotest not found on PATH (REPROTEST overrides)." >&2
    echo "       Install it in a venv:  python3 -m venv .reprotest-venv && .reprotest-venv/bin/pip install reprotest" >&2
    echo "       See docs/reproducible-builds.md, 'Variance testing'." >&2
    exit 1
}

PKG_VERSION="${PKG_VERSION:-$(cat "$REPO_ROOT/VERSION")}"

if [ -z "${SOURCE_DATE_EPOCH:-}" ]; then
    SOURCE_DATE_EPOCH="$(git -C "$REPO_ROOT" log -1 --format=%ct HEAD 2>/dev/null || true)"
    [ -n "$SOURCE_DATE_EPOCH" ] || { echo "ERROR: SOURCE_DATE_EPOCH unset and no usable git history at $REPO_ROOT" >&2; exit 1; }
    export SOURCE_DATE_EPOCH
fi
TG_GIT_COMMIT="${TG_GIT_COMMIT:-$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || printf 'unknown\n')}"
export TG_GIT_COMMIT

case "$TARGET" in
    ipk)
        BUILD_CMD="ARCH=$ARCH bash packaging/local-build-ipk.sh"
        ARTIFACT="packaging/tollgate-wrt_${PKG_VERSION}_${ARCH}.ipk"
        ;;
    *)
        echo "unsupported target: $TARGET (ipk only — binaries are covered by scripts/repro-test.sh)" >&2
        exit 2
        ;;
esac

# GOCACHE/GOMODCACHE are pinned to the invoking user's real caches so the
# variance builds stay warm and reprotest's copied trees stay small; reprotest
# varies the environment it runs the command in, not these explicit values.
echo "=== reprotest variance run: target=$TARGET arch=$ARCH variations=$VARIATIONS epoch=$SOURCE_DATE_EPOCH commit=$TG_GIT_COMMIT"
exec "$REPROTEST_BIN" --variations="$VARIATIONS" \
    "SOURCE_DATE_EPOCH=$SOURCE_DATE_EPOCH TG_GIT_COMMIT=$TG_GIT_COMMIT GOCACHE=$HOME/.cache/go-build GOMODCACHE=$HOME/go/pkg/mod $BUILD_CMD" \
    "$ARTIFACT"
