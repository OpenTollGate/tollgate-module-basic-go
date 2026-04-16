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

mkdir -p "$ARTIFACT_DIR"

printf 'Using SDK_TAG=%s\n' "$SDK_TAG"
printf 'Using PACKAGE_FORMAT=%s\n' "$PACKAGE_FORMAT"
printf 'Expected package arch=%s\n' "$EXPECTED_ARCH"
printf 'Using PACKAGE_VERSION=%s\n' "$PACKAGE_VERSION"
printf 'Using ARTIFACT_DIR=%s\n' "$ARTIFACT_DIR"
printf 'Artifact subdirectory=%s\n' "$ARTIFACT_SUBDIR"
printf '%s\n' 'Local source mode is enabled; the Docker SDK will build from the current working tree.'

docker run --rm -u root \
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
    /bin/sh -s <<'EOF'
set -eu

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

ACTUAL_ARCH=$(find /builder/bin/packages -mindepth 1 -maxdepth 1 -type d -exec basename {} \; | sort | tr '\n' ' ' | sed 's/[[:space:]]*$//')
printf '[post-build] Produced package arch directories: %s\n' "$ACTUAL_ARCH"

test -d "/builder/bin/packages/$EXPECTED_ARCH"
PACKAGE_PATH=$(find "/builder/bin/packages/$EXPECTED_ARCH" -name "tollgate-wrt*.${PACKAGE_EXTENSION}" -print | head -n 1)
test -n "$PACKAGE_PATH"
printf '[post-build] Verified package: %s\n' "$PACKAGE_PATH"

rm -rf "/artifacts/$ARTIFACT_SUBDIR"
mkdir -p "/artifacts/$ARTIFACT_SUBDIR"
cp -R /builder/bin/packages/. "/artifacts/$ARTIFACT_SUBDIR/"
EOF

printf '[done] Artifacts copied to %s/%s\n' "$ARTIFACT_DIR" "$ARTIFACT_SUBDIR"
printf '[done] Expected package directory: %s/%s/%s/base\n' "$ARTIFACT_DIR" "$ARTIFACT_SUBDIR" "$EXPECTED_ARCH"
