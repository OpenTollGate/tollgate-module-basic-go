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
# the identical epoch even though neither copy carries git history.
if [ -z "${SOURCE_DATE_EPOCH:-}" ]; then
    SOURCE_DATE_EPOCH="$(git -C "$REPO_ROOT" log -1 --format=%ct HEAD)"
    export SOURCE_DATE_EPOCH
fi
TG_ROOT="$REPO_ROOT"
export TG_ROOT
# shellcheck source=../packaging/build-env.sh
. "$REPO_ROOT/packaging/build-env.sh"

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
cleanup() { [ "${KEEP:-0}" = "1" ] && echo "KEEP=1 - roots kept under $BASE" || rm -rf "$BASE"; }
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
        export TG_GIT_COMMIT="${TG_GIT_COMMIT:-repro}"
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

echo "=== reproducibility test: target=$TARGET arch=$ARCH epoch=$SOURCE_DATE_EPOCH"
echo "=== build 1/2"
run_in_root "$BASE/a"
echo "=== build 2/2"
run_in_root "$BASE/b"

fail=0
if [ "$TARGET" = portal ]; then
    h1="$(cd "$BASE/a/out-portal" && find . -type f | sort | xargs sha256sum | sha256sum | awk '{print $1}')"
    h2="$(cd "$BASE/b/out-portal" && find . -type f | sort | xargs sha256sum | sha256sum | awk '{print $1}')"
    echo "BUILD 1 (tree hash): $h1"
    echo "BUILD 2 (tree hash): $h2"
    if [ "$h1" != "$h2" ]; then
        echo "MISMATCH in portal tree - diffing:"
        diff -r "$BASE/a/out-portal" "$BASE/b/out-portal" | head -40 || true
        fail=1
    fi
else
    mapfile -t arts_a < <(artifact_for a)
    mapfile -t arts_b < <(artifact_for b)
    for i in "${!arts_a[@]}"; do
        s1="$(sha256sum "${arts_a[$i]}" | awk '{print $1}')"
        s2="$(sha256sum "${arts_b[$i]}" | awk '{print $1}')"
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

echo
if [ "$fail" = 0 ]; then
    echo "REPRODUCIBLE: YES"
else
    echo "REPRODUCIBLE: NO"
    KEEP=1
    cleanup
    trap - EXIT
    exit 1
fi
