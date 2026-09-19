#!/usr/bin/env bash
# Reproducibility test: build the same artifact twice in genuinely
# independent clean roots (fresh tree copies, isolated HOMEs and caches)
# and require byte-identical output.
#
# Usage: scripts/repro-test.sh <target> [arch]
#   target: binaries | portal | ipk | ipk-upx | apk
#   arch:   x86_64 (default) | aarch64_cortex-a53 | arm_cortex-a7 |
#           mips_24kc | mipsel_24kc | aarch64_cortex-a72
#
# Env:
#   SOURCE_DATE_EPOCH  optional override; default = HEAD commit timestamp
#   PKG_VERSION        default = VERSION at the repository root
#   TG_GIT_COMMIT      optional override; default = `git rev-parse --short
#                      HEAD` of this repository (never a placeholder, so the
#                      stamped GitCommit matches the release lane's)
#   TG_TOOLS           dir with pinned go/node (subdirs go/ node/);
#                      default ~/.cache/tollgate-tools
#   KEEP=1             keep the two build roots for inspection
#
# Heavy work is expected to run on a beefy build host (docs name ai-legion);
# nothing here is machine-specific otherwise.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

TARGET="${1:?usage: scripts/repro-test.sh <binaries|portal|ipk|ipk-upx|apk> [arch]}"
ARCH="${2:-x86_64}"
# The release version comes from the repository-root VERSION file, the single
# source of truth (see CONTRIBUTING.md). The clean-root copies below include
# the file, so the default resolves identically in both roots.
PKG_VERSION="${PKG_VERSION:-$(cat "$REPO_ROOT/VERSION")}"
TG_TOOLS="${TG_TOOLS:-$HOME/.cache/tollgate-tools}"
export TG_TOOLS

# SOURCE_DATE_EPOCH is exported BEFORE sourcing build-env so both roots get
# the identical epoch even though neither copy carries git history. An
# environment value always wins; the git derivation is best-effort so a
# checkout without usable git metadata (act job containers, clean-root
# copies) fails with a message naming what to export, instead of dying
# mid-pipeline with git's own fatal error before anything is compared.
if [ -z "${SOURCE_DATE_EPOCH:-}" ]; then
    SOURCE_DATE_EPOCH="$(git -C "$REPO_ROOT" log -1 --format=%ct HEAD 2>/dev/null || true)"
    if [ -n "$SOURCE_DATE_EPOCH" ]; then
        export SOURCE_DATE_EPOCH
    else
        echo "ERROR: SOURCE_DATE_EPOCH is not set and $REPO_ROOT has no usable git history." >&2
        echo "       Export SOURCE_DATE_EPOCH (and TG_GIT_COMMIT), e.g. from the triggering" >&2
        echo "       commit in CI — see .ngit/act/workflows/repro-check.yml for the cascade." >&2
        exit 1
    fi
fi
TG_ROOT="$REPO_ROOT"
export TG_ROOT
# shellcheck source=../packaging/build-env.sh
. "$REPO_ROOT/packaging/build-env.sh"

# GitCommit is stamped from the enclosing repository, not a placeholder: the
# release lane stamps the same `git rev-parse --short HEAD` through the same
# helper, so a harness build and a release build of one commit embed the same
# commit and can produce identical bytes. TG_GIT_COMMIT still wins if set.
# Resolved here, once, before either clean root is built, so both roots get
# the identical value even though neither copy carries git metadata.
TG_GIT_COMMIT="${TG_GIT_COMMIT:-$(tg_git_commit)}"
export TG_GIT_COMMIT

case "$ARCH" in
    x86_64)                GOARCH=amd64;      SDK_TARGET=x86-64 ;;
    aarch64_cortex-a53)    GOARCH=arm64;      SDK_TARGET=mediatek-filogic ;;
    aarch64_cortex-a72)    GOARCH=arm64;      SDK_TARGET=bcm27xx-bcm2711 ;;
    arm_cortex-a7)         GOARCH=arm; GOARM=7; SDK_TARGET=bcm27xx-bcm2709 ;;
    mips_24kc)             GOARCH=mips; GOMIPS=softfloat; SDK_TARGET=ath79-generic ;;
    mipsel_24kc)           GOARCH=mipsle; GOMIPS=softfloat; SDK_TARGET=ramips-mt7621 ;;
    *) echo "unsupported arch $ARCH" >&2; exit 2 ;;
esac

BASE="$(mktemp -d -t tg-repro.XXXXXX)"
cleanup() {
    if [ "${KEEP:-0}" = "1" ]; then
        echo "KEEP=1 - roots kept under $BASE"
        return 0
    fi
    # Go marks extracted module files read-only; without this a non-root
    # user cannot delete them, the EXIT trap's rm fails, and its failure
    # masks the harness verdict (exit 1 despite REPRODUCIBLE: YES).
    chmod -R u+w "$BASE" 2>/dev/null || true
    rm -rf "$BASE"
}
trap cleanup EXIT

TREE_TAR="$BASE/tree.tgz"
tar czf "$TREE_TAR" -C "$REPO_ROOT" \
    --exclude=./.git --exclude=./bin --exclude=./artifacts \
    --exclude='./packaging/*.ipk' \
    --exclude=./node_modules --exclude=./*.tgz .

for x in a b; do
    ROOT="$BASE/$x"
    mkdir -p "$ROOT/tree" "$ROOT/home"
    tar xzf "$TREE_TAR" -C "$ROOT/tree"
done
rm -f "$TREE_TAR"

run_in_root() {
    ROOT="$1"
    (
        cd "$ROOT/tree"
        export HOME="$ROOT/home"
        export PATH="$TG_TOOLS/go/bin:$TG_TOOLS/node/bin:$PATH"
        export GOCACHE="$ROOT/home/.cache/go-build"
        export GOMODCACHE="$ROOT/home/go/pkg/mod"
        export npm_config_cache="$ROOT/home/.npm"
        export TG_STRICT_INPUTS=1
        case "$TARGET" in
            binaries)
                mkdir -p out
                LDFLAGS="$(go_ldflags "$PKG_VERSION")"
                CLI_LDFLAGS="$(cli_ldflags "$PKG_VERSION")"
                CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH GOARM=${GOARM:-} GOMIPS=${GOMIPS:-} \
                  go build -C src -o "$ROOT/out/tollgate-wrt" \
                  -trimpath -buildvcs=false -ldflags="$LDFLAGS" main.go
                CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH GOARM=${GOARM:-} GOMIPS=${GOMIPS:-} \
                  go build -C src/cmd/tollgate-cli -o "$ROOT/out/tollgate" \
                  -trimpath -buildvcs=false -ldflags="$CLI_LDFLAGS"
                ;;
            portal)
                PORTAL_DIR="$ROOT/portal-src" OUTPUT_DIR="$ROOT/out-portal" \
                  bash packaging/portal-build.sh
                ;;
            ipk)
                ARCH="$ARCH" PKG_VERSION="$PKG_VERSION" \
                  bash packaging/local-build-ipk.sh
                mkdir -p "$ROOT/out"
                cp "packaging/tollgate-wrt_${PKG_VERSION}_${ARCH}.ipk" "$ROOT/out/"
                ;;
            ipk-upx)
                UPX_DIR="$(bash scripts/fetch-upx.sh)"
                export UPX_BIN="$UPX_DIR/upx"
                ARCH="$ARCH" PKG_VERSION="$PKG_VERSION" USE_UPX=1 UPX_FLAGS="--ultra-brute" \
                  bash packaging/local-build-ipk.sh
                mkdir -p "$ROOT/out"
                cp "packaging/tollgate-wrt_${PKG_VERSION}_${ARCH}.ipk" "$ROOT/out/"
                ;;
            apk)
                ARTIFACT_DIR="$ROOT/art" PACKAGE_FORMAT=apk PACKAGE_VERSION="$PKG_VERSION" \
                  SDK_TAG="${SDK_TARGET}-${SDK_RELEASE}" \
                  bash scripts/build-sdk-package.sh || { tail -30 "$ROOT"/art/*/build.log; exit 1; }
                mkdir -p "$ROOT/out"
                find "$ROOT/art" -name 'tollgate-wrt*.apk' -exec cp {} "$ROOT/out/" \;
                ;;
            *) echo "unknown target $TARGET" >&2; exit 2 ;;
        esac
    )
}

artifact_for() {
    ROOT="$BASE/$1"
    case "$TARGET" in
        binaries) printf '%s\n' "$ROOT/out/tollgate-wrt" "$ROOT/out/tollgate" ;;
        portal)   printf '%s\n' "$ROOT/out-portal" ;;
        apk)      find "$ROOT/out" -name 'tollgate-wrt*.apk' -type f | sort ;;
        *)        printf '%s\n' "$ROOT/out/tollgate-wrt_${PKG_VERSION}_${ARCH}.ipk" ;;
    esac
}

# The path a target searches under one root, for the "matched nothing" report.
artifact_desc() {
    ROOT="$BASE/$1"
    case "$TARGET" in
        binaries) printf '%s' "$ROOT/out/tollgate-wrt and $ROOT/out/tollgate" ;;
        portal)   printf '%s' "$ROOT/out-portal" ;;
        apk)      printf '%s' "$ROOT/out/tollgate-wrt*.apk" ;;
        *)        printf '%s' "$ROOT/out/tollgate-wrt_${PKG_VERSION}_${ARCH}.ipk" ;;
    esac
}

echo "=== reproducibility test: target=$TARGET arch=$ARCH epoch=$SOURCE_DATE_EPOCH"
echo "=== build 1/2"
run_in_root "$BASE/a"
echo "=== build 2/2"
run_in_root "$BASE/b"

fail=0
# Artifact pairs actually compared. REPRODUCIBLE: YES is gated on this counter
# so that a target whose artifact glob matched nothing can never pass
# vacuously: "we compared nothing and found no differences" is not evidence.
comparisons=0

# A root that yields no artifact is a failed check, not a pass. An artifact
# glob with no matches (the apk target) or an empty portal output directory
# leaves the two builds uncompared, which is exactly the state this script
# exists to rule out.
no_artifacts() {
    echo "no artifacts found for target=$TARGET: $1" >&2
    fail=1
}

if [ "$TARGET" = portal ]; then
    for x in a b; do
        PORTAL_OUT="$BASE/$x/out-portal"
        files=0
        if [ -d "$PORTAL_OUT" ]; then
            files="$(find "$PORTAL_OUT" -type f | wc -l)"
        fi
        # Hash a non-empty tree or nothing at all: an empty tree hashes to the
        # same constant in both roots and would report REPRODUCIBLE: YES.
        if [ "$files" -eq 0 ]; then
            no_artifacts "build $x produced no files under $PORTAL_OUT (nothing to hash)"
        fi
    done
    if [ "$fail" = 0 ]; then
        h1="$(cd "$BASE/a/out-portal" && find . -type f | sort | xargs sha256sum | sha256sum | awk '{print $1}')"
        h2="$(cd "$BASE/b/out-portal" && find . -type f | sort | xargs sha256sum | sha256sum | awk '{print $1}')"
        comparisons=$((comparisons + 1))
        echo "BUILD 1 (tree hash): $h1"
        echo "BUILD 2 (tree hash): $h2"
        if [ "$h1" != "$h2" ]; then
            echo "MISMATCH in portal tree - diffing:"
            diff -r "$BASE/a/out-portal" "$BASE/b/out-portal" | head -40 || true
            fail=1
        fi
    fi
else
    mapfile -t arts_a < <(artifact_for a)
    mapfile -t arts_b < <(artifact_for b)
    if [ "${#arts_a[@]}" -eq 0 ]; then
        no_artifacts "build a matched nothing under $(artifact_desc a)"
    fi
    if [ "${#arts_b[@]}" -eq 0 ]; then
        no_artifacts "build b matched nothing under $(artifact_desc b)"
    fi
    # Equal counts, checked in both directions: a pair that does not line up
    # file for file has not been compared, and the loop below would silently
    # compare a subset of one root against a subset of the other.
    if [ "${#arts_a[@]}" -gt "${#arts_b[@]}" ]; then
        echo "artifact count mismatch for target=$TARGET: build a produced ${#arts_a[@]}, build b only ${#arts_b[@]}" >&2
        fail=1
    fi
    if [ "${#arts_b[@]}" -gt "${#arts_a[@]}" ]; then
        echo "artifact count mismatch for target=$TARGET: build b produced ${#arts_b[@]}, build a only ${#arts_a[@]}" >&2
        fail=1
    fi
    if [ "$fail" = 0 ]; then
        for i in "${!arts_a[@]}"; do
            s1="$(sha256sum "${arts_a[$i]}" | awk '{print $1}')"
            s2="$(sha256sum "${arts_b[$i]}" | awk '{print $1}')"
            comparisons=$((comparisons + 1))
            echo "BUILD 1: ${arts_a[$i]##*/}  $s1"
            echo "BUILD 2: ${arts_b[$i]##*/}  $s2"
            if [ "$s1" != "$s2" ]; then
                echo "MISMATCH on ${arts_a[$i]##*/} - diagnosing:"
                cmp "${arts_a[$i]}" "${arts_b[$i]}" || true
                command -v diffoscope >/dev/null 2>&1 && diffoscope "${arts_a[$i]}" "${arts_b[$i]}" | head -60
                fail=1
            fi
        done
    fi
fi

# Last line of defence: the all-clear requires at least one real comparison.
if [ "$fail" = 0 ] && [ "$comparisons" -eq 0 ]; then
    echo "no comparisons were made for target=$TARGET - refusing to report REPRODUCIBLE" >&2
    fail=1
fi

echo
if [ "$fail" = 0 ]; then
    echo "compared $comparisons artifact pair(s)"
    echo "REPRODUCIBLE: YES"
else
    echo "REPRODUCIBLE: NO"
    KEEP=1
    cleanup
    trap - EXIT
    exit 1
fi
