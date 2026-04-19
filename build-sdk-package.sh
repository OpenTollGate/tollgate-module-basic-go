#!/bin/sh

set -eu

SDK_TAG="${SDK_TAG:-}"
PACKAGE_FORMAT="${PACKAGE_FORMAT:-apk}"
PACKAGE_VERSION="${PACKAGE_VERSION:-0.0.0-r0}"
ARTIFACT_DIR="${ARTIFACT_DIR:-/tmp/tollgate-build-artifacts}"
EXPECTED_ARCH="${EXPECTED_ARCH:-}"

case "$PACKAGE_FORMAT" in
    apk|ipk) ;;
    *)
        printf 'ERROR: PACKAGE_FORMAT must be either apk or ipk, got %s\n' "$PACKAGE_FORMAT" >&2
        exit 1
        ;;
esac

PACKAGE_EXTENSION="$PACKAGE_FORMAT"

if [ -z "$SDK_TAG" ]; then
    printf '%s\n' 'ERROR: SDK_TAG is required for local builds.' >&2
    printf '%s\n' 'Refusing to guess a default target because that can silently build the wrong architecture.' >&2
    printf '%s\n' 'Example: SDK_TAG=mediatek-filogic-25.12.0-rc4 ./build-sdk-package.sh' >&2
    exit 1
fi

if [ -z "$EXPECTED_ARCH" ]; then
    case "$SDK_TAG" in
        mediatek-filogic-*) EXPECTED_ARCH='aarch64_cortex-a53' ;;
        ath79-generic-*) EXPECTED_ARCH='mips_24kc' ;;
        ramips-mt7621-*) EXPECTED_ARCH='mipsel_24kc' ;;
        bcm27xx-bcm2711-*) EXPECTED_ARCH='aarch64_cortex-a72' ;;
        bcm27xx-bcm2709-*) EXPECTED_ARCH='arm_cortex-a7' ;;
        *)
            printf 'ERROR: Could not infer EXPECTED_ARCH from SDK_TAG=%s\n' "$SDK_TAG" >&2
            printf '%s\n' 'Set EXPECTED_ARCH explicitly, e.g. EXPECTED_ARCH=aarch64_cortex-a53' >&2
            exit 1
            ;;
    esac
fi

ARTIFACT_SUBDIR="${ARTIFACT_SUBDIR:-${PACKAGE_FORMAT}-${SDK_TAG}}"
HOST_ARTIFACT_PATH="$ARTIFACT_DIR/$ARTIFACT_SUBDIR"
HOST_LOG_PATH="$HOST_ARTIFACT_PATH/build.log"

mkdir -p "$ARTIFACT_DIR"
mkdir -p "$HOST_ARTIFACT_PATH"

printf 'Using SDK_TAG=%s\n' "$SDK_TAG"
printf 'Using PACKAGE_FORMAT=%s\n' "$PACKAGE_FORMAT"
printf 'Expected package arch=%s\n' "$EXPECTED_ARCH"
printf 'Using PACKAGE_VERSION=%s\n' "$PACKAGE_VERSION"
printf 'Using ARTIFACT_DIR=%s\n' "$ARTIFACT_DIR"
printf 'Artifact subdirectory=%s\n' "$ARTIFACT_SUBDIR"
printf 'Build log path=%s\n' "$HOST_LOG_PATH"
printf '%s\n' 'Local source mode is enabled; the Docker SDK will build from the current working tree.'

if ! docker run --rm -i -u root \
    -v "$PWD":/builder/package/tollgate-wrt \
    -v "$ARTIFACT_DIR":/artifacts \
    -e SDK_TAG="$SDK_TAG" \
    -e PACKAGE_FORMAT="$PACKAGE_FORMAT" \
    -e PACKAGE_EXTENSION="$PACKAGE_EXTENSION" \
    -e PACKAGE_VERSION="$PACKAGE_VERSION" \
    -e TOLLGATE_LOCAL_SOURCE=1 \
    -e EXPECTED_ARCH="$EXPECTED_ARCH" \
    -e ARTIFACT_SUBDIR="$ARTIFACT_SUBDIR" \
    "openwrt/sdk:${SDK_TAG}" \
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
printf '%s\n' '[preflight] Building from mounted local source at /builder/package/tollgate-wrt'

printf 'deb https://deb.debian.org/debian bookworm-backports main\n' > /etc/apt/sources.list.d/backports.list
apt-get update
apt-get install -y -t bookworm-backports golang-go

cd /builder
make defconfig

if [ "$PACKAGE_FORMAT" = "apk" ]; then
    printf 'CONFIG_USE_APK=y\n' >> .config
fi

printf 'CONFIG_PACKAGE_tollgate-wrt=y\nCONFIG_PACKAGE_nodogsplash=y\nCONFIG_PACKAGE_luci=y\nCONFIG_PACKAGE_jq=y\n' >> .config
make defconfig

JOBS=$(nproc)
printf '[preflight] Using parallel jobs=%s\n' "$JOBS"

env PACKAGE_VERSION="$PACKAGE_VERSION" TOLLGATE_LOCAL_SOURCE="$TOLLGATE_LOCAL_SOURCE" make -j"$JOBS" V=s package/tollgate-wrt/compile

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
