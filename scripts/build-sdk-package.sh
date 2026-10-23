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
            # The bcm2709 SDK packages under arm_cortex-a7_neon-vfpv4,
            # not the bare arm_cortex-a7 — the short name fails the
            # staged-packages dir check and kills the copy step.
            : "${EXPECTED_ARCH:=arm_cortex-a7_neon-vfpv4}"
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
STAGE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/tollgate-build.XXXXXX")"
trap 'rm -rf "$STAGE_DIR"' EXIT

# Reproducible-build environment: pinned SDK digest, SOURCE_DATE_EPOCH,
# deterministic BUILD_TIME_UTC and ldflags helpers.
# shellcheck source=../packaging/build-env.sh
TG_ROOT="$REPO_ROOT"
export TG_ROOT
. "$REPO_ROOT/packaging/build-env.sh"

# Fail-closed portal preflight: the recipe stages FIVE gitignored build
# products (packaging/portal-build.sh header, products 1-5) that only
# `make portal-build` produces. Building without them fails ~10 minutes
# deep inside the SDK container with an install error naming a
# container-internal path — so every input is verified here, first, and a
# missing or stale bundle either gets rebuilt from the pin or aborts the
# build naming the exact remediation (never EACCES-in-npm, never a
# truncated-staging phantom).
verify_portal_products() {
    local guest="$REPO_ROOT/packaging/files/tollgate-captive-portal-site"
    local admin="$REPO_ROOT/packaging/files/tollgate-admin"
    local plugin="$REPO_ROOT/packaging/files/usr/libexec/rpcd/tollgate"
    local acl="$REPO_ROOT/packaging/files/usr/share/rpcd/acl.d/tollgate.json"
    local admin_setup="$REPO_ROOT/packaging/files/etc/uci-defaults/999-tollgate-admin-setup"
    local stamp="$REPO_ROOT/packaging/portal-resolved.sha"
    [ -f "$guest/splash.html" ] && [ -d "$guest/assets" ] \
        && [ -f "$admin/index.html" ] \
        && [ -x "$plugin" ] && [ -s "$acl" ] && [ -x "$admin_setup" ] \
        && [ -s "$stamp" ] && [ "$(cat "$stamp")" = "$PORTAL_COMMIT" ]
}

if ! verify_portal_products; then
    if [ "${SKIP_PORTAL_BUILD:-0}" = "1" ]; then
        printf '%s\n' "ERROR: portal bundle missing or stale (resolved sha != pinned $PORTAL_COMMIT), and SKIP_PORTAL_BUILD=1 forbids rebuilding." >&2
        printf '%s\n' "Run: make portal-build   (then retry; see packaging/portal-build.sh)" >&2
        exit 1
    fi
    printf '%s\n' "portal bundle missing or stale — building it from the pinned tree first"
    (cd "$REPO_ROOT" && bash packaging/portal-build.sh)
    if ! verify_portal_products; then
        printf '%s\n' "ERROR: portal build ran but the products still fail verification (node/npm pins? see output above)." >&2
        exit 1
    fi
fi

GO_BIN="${GO_BIN:-go}"
ACTIVE_GO="$("$GO_BIN" version | awk '{print $3}')"
if [ "$ACTIVE_GO" != "go$GO_VERSION" ] && [ "${TG_ALLOW_GO_MISMATCH:-0}" != "1" ]; then
    printf 'ERROR: go %s does not match pinned GO_VERSION=%s (packaging/build-inputs.json).\n' "$ACTIVE_GO" "$GO_VERSION" >&2
    printf 'Set TG_ALLOW_GO_MISMATCH=1 to proceed anyway (non-reproducible).\n' >&2
    exit 1
fi

# The packaging format selects the SDK era (25.12/apk vs 24.10/ipk)
export TG_PACKAGE_FORMAT="$PACKAGE_FORMAT"
SDK_IMAGE="$(sdk_image_ref "$SDK_TAG" "$PACKAGE_FORMAT")"

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
    # Build the whole package (.), not main.go alone: package main grew
    # sibling files (startup_gate.go, the api boot ordering) whose symbols
    # main.go references — single-file compilation has failed with
    # "undefined: requireStarted & co" since they landed, and nothing on
    # the SDK-local path had exercised the script since.
    env CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" GOMIPS="$GOMIPS" GOARM="$GOARM" \
        "$GO_BIN" build -o "$STAGE_DIR/tollgate-wrt" -trimpath -buildvcs=false -ldflags="$LDFLAGS" .
)
(
    cd "$REPO_ROOT/src/cmd/tollgate-cli"
    env CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" GOMIPS="$GOMIPS" GOARM="$GOARM" \
        "$GO_BIN" build -o "$STAGE_DIR/tollgate" -trimpath -buildvcs=false -ldflags="$CLI_LDFLAGS"
)

# The CVM control-plane daemon is its own module. Build it when the pinned
# tree carries it (main does; older tags predate it), matching the feed's
# presence-conditional CVM stanza. No version ldflags: the daemon's identity
# travels in its CEP-6 announcement pubkey, and packaging/Makefile stages it
# the same way as packaging/local-build-ipk.sh.
if [ -d "$REPO_ROOT/src/cmd/tollgate-cvm" ]; then
    (
        cd "$REPO_ROOT/src/cmd/tollgate-cvm"
        env CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" GOMIPS="$GOMIPS" GOARM="$GOARM" \
            "$GO_BIN" build -o "$STAGE_DIR/tollgate-cvm" -trimpath -buildvcs=false .
    )
fi

cp -r "$REPO_ROOT/packaging/." "$STAGE_DIR/"
cp "$REPO_ROOT/LICENSE" "$STAGE_DIR/LICENSE"

# Verify the staging copy landed complete: a truncated `cp -r` (full /tmp,
# quota) previously surfaced only inside the SDK as "No rule to make
# target 'package/tollgate-wrt/compile'" — ten minutes from its cause.
for _required in \
    "$STAGE_DIR/Makefile" \
    "$STAGE_DIR/LICENSE" \
    "$STAGE_DIR/tollgate-wrt" \
    "$STAGE_DIR/tollgate" \
    "$STAGE_DIR/files/etc/uci-defaults/99-tollgate-setup" \
    "$STAGE_DIR/files/etc/uci-defaults/999-tollgate-admin-setup" \
    "$STAGE_DIR/files/tollgate-captive-portal-site/splash.html"
do
    if [ ! -s "$_required" ]; then
        printf 'ERROR: staging incomplete: %s missing or empty (disk full under %s?)\n' \
            "${_required#"$STAGE_DIR"/}" "${TMPDIR:-/tmp}" >&2
        exit 1
    fi
done

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

# The packages dir uses the canonical arch name (underscores) for BOTH
# formats; the build_dir spells feature sets with "+" (e.g. target-
# arm_cortex-a7+neon-vfpv4_musl_eabi), so globbing it from EXPECTED_ARCH
# can only match arches without "+" — bcm2709 failed here after a
# successful build. The packages-dir test is the real "SDK produced
# packages for our arch" gate in both eras.
test -d "/builder/bin/packages/$EXPECTED_ARCH"

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
