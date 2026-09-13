#!/bin/sh

set -eu

SDK_TAG="${SDK_TAG:-}"
PACKAGE_FORMAT="${PACKAGE_FORMAT:-apk}"
PACKAGE_VERSION="${PACKAGE_VERSION:-}"
ARTIFACT_DIR="${ARTIFACT_DIR:-/tmp/tollgate-build-artifacts}"
EXPECTED_ARCH="${EXPECTED_ARCH:-}"
GOARCH="${GOARCH:-}"
GOMIPS="${GOMIPS:-}"
GOARM="${GOARM:-}"

case "$PACKAGE_FORMAT" in
    apk|ipk) ;;
    *)
        printf 'ERROR: PACKAGE_FORMAT must be either apk or ipk, got %s\n' "$PACKAGE_FORMAT" >&2
        exit 1
        ;;
esac

PACKAGE_EXTENSION="$PACKAGE_FORMAT"

infer_target_settings() {
    case "$SDK_TAG" in
        mediatek-filogic-*)
            : "${EXPECTED_ARCH:=aarch64_cortex-a53}"
            : "${GOARCH:=arm64}"
            ;;
        ath79-generic-*)
            : "${EXPECTED_ARCH:=mips_24kc}"
            : "${GOARCH:=mips}"
            : "${GOMIPS:=softfloat}"
            ;;
        ramips-mt7621-*)
            : "${EXPECTED_ARCH:=mipsel_24kc}"
            : "${GOARCH:=mipsle}"
            : "${GOMIPS:=softfloat}"
            ;;
        bcm27xx-bcm2711-*)
            : "${EXPECTED_ARCH:=aarch64_cortex-a72}"
            : "${GOARCH:=arm64}"
            ;;
        bcm27xx-bcm2709-*)
            : "${EXPECTED_ARCH:=arm_cortex-a7}"
            : "${GOARCH:=arm}"
            : "${GOARM:=7}"
            ;;
        x86-64-*)
            : "${EXPECTED_ARCH:=x86_64}"
            : "${GOARCH:=amd64}"
            ;;
        *)
            if [ -z "$EXPECTED_ARCH" ]; then
                printf 'ERROR: Could not infer EXPECTED_ARCH from SDK_TAG=%s\n' "$SDK_TAG" >&2
                printf '%s\n' 'Set EXPECTED_ARCH explicitly, e.g. EXPECTED_ARCH=aarch64_cortex-a53' >&2
                exit 1
            fi
            if [ -z "$GOARCH" ]; then
                printf 'ERROR: Could not infer GOARCH from SDK_TAG=%s\n' "$SDK_TAG" >&2
                printf '%s\n' 'Set GOARCH explicitly for unknown SDK targets.' >&2
                exit 1
            fi
            ;;
    esac
}

if [ -z "$SDK_TAG" ]; then
    printf '%s\n' 'ERROR: SDK_TAG is required for local builds.' >&2
    printf '%s\n' 'Refusing to guess a default target because that can silently build the wrong architecture.' >&2
    printf '%s\n' 'Example: SDK_TAG=mediatek-filogic-25.12.0-rc4 ./scripts/build-sdk-package.sh' >&2
    exit 1
fi

infer_target_settings

ARTIFACT_SUBDIR="${ARTIFACT_SUBDIR:-${PACKAGE_FORMAT}-${SDK_TAG}}"
HOST_ARTIFACT_PATH="$ARTIFACT_DIR/$ARTIFACT_SUBDIR"
HOST_LOG_PATH="$HOST_ARTIFACT_PATH/build.log"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# VERSION at the repository root is the single source of truth for the release
# version (see AGENTS.md, "Version single source of truth"). CI passes the tag
# down as PACKAGE_VERSION; a local run with no override gets exactly the string
# the release tag must equal. The 0.0.0-r0 stub is only for a tree with no
# VERSION file (this script copied out of the repository).
if [ -z "$PACKAGE_VERSION" ]; then
    if [ -f "$REPO_ROOT/VERSION" ]; then
        PACKAGE_VERSION="$(tr -d '[:space:]' < "$REPO_ROOT/VERSION")"
    else
        PACKAGE_VERSION="0.0.0-r0"
    fi
fi
STAGE_DIR="$(mktemp -d)"
trap 'rm -rf "$STAGE_DIR"' EXIT

# Reproducible-build environment: pinned SDK digest, SOURCE_DATE_EPOCH,
# deterministic BUILD_TIME_UTC and ldflags helpers.
# shellcheck source=../packaging/build-env.sh
TG_ROOT="$REPO_ROOT"
export TG_ROOT
. "$REPO_ROOT/packaging/build-env.sh"

GO_BIN="${GO_BIN:-go}"
ACTIVE_GO="$("$GO_BIN" version | awk '{print $3}')"
if [ "$ACTIVE_GO" != "go$GO_VERSION" ] && [ "${TG_ALLOW_GO_MISMATCH:-0}" != "1" ]; then
    printf 'ERROR: go %s does not match pinned GO_VERSION=%s (packaging/build-inputs.json).\n' "$ACTIVE_GO" "$GO_VERSION" >&2
    printf 'Set TG_ALLOW_GO_MISMATCH=1 to proceed anyway (non-reproducible).\n' >&2
    exit 1
fi

SDK_IMAGE="$(sdk_image_ref "$SDK_TAG")"

mkdir -p "$ARTIFACT_DIR"
rm -rf "$HOST_ARTIFACT_PATH"
mkdir -p "$HOST_ARTIFACT_PATH"

printf 'Using SDK_TAG=%s\n' "$SDK_TAG"
printf 'Using SDK image=%s\n' "$SDK_IMAGE"
printf 'Using PACKAGE_FORMAT=%s\n' "$PACKAGE_FORMAT"
printf 'Expected package arch=%s\n' "$EXPECTED_ARCH"
printf 'Using PACKAGE_VERSION=%s\n' "$PACKAGE_VERSION"
printf 'Using GOARCH=%s GOMIPS=%s GOARM=%s\n' "$GOARCH" "$GOMIPS" "$GOARM"
printf 'Using SOURCE_DATE_EPOCH=%s (BuildTime %s)\n' "$SOURCE_DATE_EPOCH" "$BUILD_TIME_UTC"
printf 'Using ARTIFACT_DIR=%s\n' "$ARTIFACT_DIR"
printf 'Artifact subdirectory=%s\n' "$ARTIFACT_SUBDIR"
printf 'Build log path=%s\n' "$HOST_LOG_PATH"

TG_GIT_COMMIT="$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || printf 'unknown\n')"
export TG_GIT_COMMIT
LDFLAGS="$(go_ldflags "$PACKAGE_VERSION")"
CLI_LDFLAGS="$(cli_ldflags "$PACKAGE_VERSION")"

printf '%s\n' 'Building target binaries locally before invoking the OpenWrt SDK.'
(
    cd "$REPO_ROOT/src"
    env CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" GOMIPS="$GOMIPS" GOARM="$GOARM" \
        "$GO_BIN" build -o "$STAGE_DIR/tollgate-wrt" -trimpath -buildvcs=false -ldflags="$LDFLAGS" main.go
)
(
    cd "$REPO_ROOT/src/cmd/tollgate-cli"
    env CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" GOMIPS="$GOMIPS" GOARM="$GOARM" \
        "$GO_BIN" build -o "$STAGE_DIR/tollgate" -trimpath -buildvcs=false -ldflags="$CLI_LDFLAGS"
)

cp -r "$REPO_ROOT/packaging/." "$STAGE_DIR/"
cp "$REPO_ROOT/LICENSE" "$STAGE_DIR/LICENSE"

# Staged source and payload mtimes feed SDK apk packaging: pin them to
# SOURCE_DATE_EPOCH before the container reads them.
normalize_mtime "$STAGE_DIR"

if ! docker run --rm -i -u root \
    -v "$STAGE_DIR":/builder/package/tollgate-wrt \
    -v "$ARTIFACT_DIR":/artifacts \
    -e SDK_TAG="$SDK_TAG" \
    -e SOURCE_DATE_EPOCH="$SOURCE_DATE_EPOCH" \
    -e LC_ALL=C \
    -e LANG=C \
    -e TZ=UTC \
    -e PACKAGE_FORMAT="$PACKAGE_FORMAT" \
    -e PACKAGE_EXTENSION="$PACKAGE_EXTENSION" \
    -e PACKAGE_VERSION="$PACKAGE_VERSION" \
    -e EXPECTED_ARCH="$EXPECTED_ARCH" \
    -e ARTIFACT_SUBDIR="$ARTIFACT_SUBDIR" \
    "$SDK_IMAGE" \
    /bin/bash -se <<'EOF'
set -euo pipefail

mkdir -p "/artifacts/$ARTIFACT_SUBDIR"
BUILD_LOG="/artifacts/$ARTIFACT_SUBDIR/build.log"
STATUS_FILE="/artifacts/$ARTIFACT_SUBDIR/status.txt"
PACKAGE_LIST_FILE="/artifacts/$ARTIFACT_SUBDIR/package-paths.txt"

cleanup() {
    local code=$?
    if [ "$code" -eq 0 ]; then
        printf 'success\n' > "$STATUS_FILE"
    else
        printf 'failed (%s)\n' "$code" > "$STATUS_FILE"
        printf '[error] Build failed before artifact copy completed.\n'
    fi
}

trap cleanup EXIT
exec > >(tee -a "$BUILD_LOG") 2>&1

printf '[preflight] SDK_TAG=%s\n' "$SDK_TAG"
printf '[preflight] PACKAGE_FORMAT=%s\n' "$PACKAGE_FORMAT"
printf '[preflight] EXPECTED_ARCH=%s\n' "$EXPECTED_ARCH"
printf '[preflight] PACKAGE_VERSION=%s\n' "$PACKAGE_VERSION"
printf '%s\n' '[preflight] Packaging staged recipe at /builder/package/tollgate-wrt'

cd /builder
make defconfig

if [ "$PACKAGE_FORMAT" = "apk" ]; then
    printf 'CONFIG_USE_APK=y\n' >> .config
fi

printf 'CONFIG_PACKAGE_tollgate-wrt=y\nCONFIG_PACKAGE_nodogsplash=y\nCONFIG_PACKAGE_luci=y\nCONFIG_PACKAGE_jq=y\n' >> .config
make defconfig

JOBS=$(nproc)
printf '[preflight] Using parallel jobs=%s\n' "$JOBS"

env PACKAGE_VERSION="$PACKAGE_VERSION" make -j"$JOBS" V=s package/tollgate-wrt/compile

if [ "$PACKAGE_FORMAT" = "ipk" ]; then
    test -d "/builder/bin/packages/$EXPECTED_ARCH"
else
    compgen -G "/builder/build_dir/target-${EXPECTED_ARCH}_*" > /dev/null
fi

mapfile -t PACKAGE_PATHS < <(find /builder/bin/packages /builder/bin/targets -type f -name "tollgate-wrt*.${PACKAGE_EXTENSION}" 2>/dev/null | sort)
if [ "${#PACKAGE_PATHS[@]}" -eq 0 ]; then
    printf '[error] No tollgate-wrt package with extension .%s was found under /builder/bin.\n' "$PACKAGE_EXTENSION"
    exit 1
fi

printf '[post-build] Located package files:\n'
printf '  %s\n' "${PACKAGE_PATHS[@]}"
printf '%s\n' "${PACKAGE_PATHS[@]#/builder/bin/}" > "$PACKAGE_LIST_FILE"

for package_path in "${PACKAGE_PATHS[@]}"; do
    cp "$package_path" "/artifacts/$ARTIFACT_SUBDIR/$(basename "$package_path")"
done

mapfile -t PACKAGE_DIRS < <(printf '%s\n' "${PACKAGE_PATHS[@]}" | xargs -n1 dirname | sort -u)
for package_dir in "${PACKAGE_DIRS[@]}"; do
    rel_dir="${package_dir#/builder/bin/}"
    dest_dir="/artifacts/$ARTIFACT_SUBDIR/$rel_dir"
    mkdir -p "$dest_dir"
    cp -R "$package_dir/." "$dest_dir/"
done
EOF
then
    printf '[error] Build failed. Check %s for details.\n' "$HOST_LOG_PATH" >&2
    exit 1
fi

if [ ! -f "$HOST_LOG_PATH" ] || [ ! -f "$HOST_ARTIFACT_PATH/status.txt" ]; then
    printf '[error] Build container exited without producing expected log metadata in %s\n' "$HOST_ARTIFACT_PATH" >&2
    exit 1
fi

printf '[done] Artifacts copied to %s\n' "$HOST_ARTIFACT_PATH"
printf '[done] Build log: %s\n' "$HOST_LOG_PATH"
printf '[done] Copied package paths are listed in %s/package-paths.txt\n' "$HOST_ARTIFACT_PATH"
printf '[done] Convenience package copy available at %s/<package-file>\n' "$HOST_ARTIFACT_PATH"
