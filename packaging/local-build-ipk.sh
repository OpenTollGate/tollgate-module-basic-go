#!/usr/bin/env bash
# Local .ipk builder — replicates CI pipeline for one architecture.
# Usage: [ARCH=… PKG_VERSION=…] bash packaging/local-build-ipk.sh
# Deterministic given the inputs pinned in packaging/build-inputs.json.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

# Reproducible-build environment: pinned tool versions, SOURCE_DATE_EPOCH,
# BUILD_TIME_UTC, ldflags helpers (packaging/build-env.sh).
# shellcheck source=build-env.sh
. "$REPO_ROOT/packaging/build-env.sh"

PKG_NAME="tollgate-wrt"
# The release version comes from the repository-root VERSION file, the single
# source of truth (see CONTRIBUTING.md). CI ignores this script and derives the same
# string from the git tag; set PKG_VERSION to override for a one-off build.
PKG_VERSION="${PKG_VERSION:-$(cat "$REPO_ROOT/VERSION")}"
ARCH="${ARCH:-aarch64_cortex-a53}"

case "$ARCH" in
  aarch64_cortex-a53|aarch64_cortex-a72) COMPILE_KEY="arm64";      GOARCH="arm64" ;;
  arm_cortex-a7)                         COMPILE_KEY="armv7";      GOARCH="arm"; GOARM="7" ;;
  mipsel_24kc)                           COMPILE_KEY="mipsle-sf";  GOARCH="mipsle"; GOMIPS="softfloat" ;;
  mips_24kc)                             COMPILE_KEY="mips-sf";    GOARCH="mips"; GOMIPS="softfloat" ;;
  x86_64)                                COMPILE_KEY="amd64";      GOARCH="amd64" ;;
  *) echo "ERROR: unsupported ARCH=$ARCH" >&2; exit 1 ;;
esac
GOARM="${GOARM:-}"; GOMIPS="${GOMIPS:-}"

GO_BIN="${GO_BIN:-go}"
ACTIVE_GO="$("$GO_BIN" version | awk '{print $3}')"
if [ "$ACTIVE_GO" != "go$GO_VERSION" ]; then
  if [ "${TG_ALLOW_GO_MISMATCH:-0}" = "1" ]; then
    echo "WARNING: go $ACTIVE_GO != pinned $GO_VERSION (TG_ALLOW_GO_MISMATCH=1); output NOT reproducible" >&2
  else
    echo "ERROR: go $ACTIVE_GO does not match pinned GO_VERSION=$GO_VERSION." >&2
    echo "Install the pinned toolchain (see packaging/build-inputs.json) or set GO_BIN." >&2
    echo "Set TG_ALLOW_GO_MISMATCH=1 to proceed anyway (non-reproducible)." >&2
    exit 1
  fi
fi

TG_GIT_COMMIT="${TG_GIT_COMMIT:-$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || printf 'unknown\n')}"
export TG_GIT_COMMIT

echo "=== Building Go binaries (GOOS=linux GOARCH=$GOARCH, SOURCE_DATE_EPOCH=$SOURCE_DATE_EPOCH) ==="

LDFLAGS="$(go_ldflags "$PKG_VERSION")"
# The tollgate CLI is a separate Go module (module tollgate-cli) that does
# not link the src/cli package, so its version must be injected via main.version.
CLI_LDFLAGS="$(cli_ldflags "$PKG_VERSION")"

mkdir -p "bin/$COMPILE_KEY"

CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH GOARM=$GOARM GOMIPS=$GOMIPS \
  "$GO_BIN" build -C src -o "$REPO_ROOT/bin/$COMPILE_KEY/tollgate-wrt" \
  -trimpath -buildvcs=false -ldflags="$LDFLAGS" main.go

CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH GOARM=$GOARM GOMIPS=$GOMIPS \
  "$GO_BIN" build -C src/cmd/tollgate-cli -o "$REPO_ROOT/bin/$COMPILE_KEY/tollgate" \
  -trimpath -buildvcs=false -ldflags="$CLI_LDFLAGS"

ls -lh "bin/$COMPILE_KEY/"

if [ "${USE_UPX:-0}" = "1" ]; then
  UPX_BIN="${UPX_BIN:-upx}"
  UPX_FLAGS="${UPX_FLAGS:---ultra-brute}"
  command -v "$UPX_BIN" >/dev/null 2>&1 || { echo "ERROR: USE_UPX=1 but $UPX_BIN not found" >&2; exit 1; }
  ACTIVE_UPX="$("$UPX_BIN" --version 2>/dev/null | head -1 | awk '{print $2}')"
  if [ "$ACTIVE_UPX" != "$UPX_VERSION" ] && [ "${TG_ALLOW_UPX_MISMATCH:-0}" != "1" ]; then
    echo "ERROR: upx $ACTIVE_UPX != pinned $UPX_VERSION (packaging/build-inputs.json). Use scripts/fetch-upx.sh." >&2
    exit 1
  fi
  echo "=== Compressing with UPX $ACTIVE_UPX $UPX_FLAGS ==="
  "$UPX_BIN" --force $UPX_FLAGS "bin/$COMPILE_KEY/tollgate-wrt" "bin/$COMPILE_KEY/tollgate" >/dev/null
  ls -lh "bin/$COMPILE_KEY/"
fi

echo "=== Assembling payload ==="

PAYLOAD=$(mktemp -d)
trap 'rm -rf "$PAYLOAD"' EXIT

install -D -m 0755 "bin/$COMPILE_KEY/tollgate-wrt" "$PAYLOAD/usr/bin/tollgate-wrt"
install -D -m 0755 "bin/$COMPILE_KEY/tollgate"     "$PAYLOAD/usr/bin/tollgate"

install -D -m 0755 packaging/files/etc/init.d/tollgate-wrt                           "$PAYLOAD/etc/init.d/tollgate-wrt"
install -D -m 0755 packaging/files/etc/uci-defaults/90-tollgate-captive-portal-symlink "$PAYLOAD/etc/uci-defaults/90-tollgate-captive-portal-symlink"
# The setup script's version marker is the release version; substitute the
# placeholder exactly as the CI .ipk staging and the SDK Makefile do.
mkdir -p "$PAYLOAD/etc/uci-defaults"
sed "s|__TOLLGATE_VERSION__|$PKG_VERSION|g" \
  packaging/files/etc/uci-defaults/99-tollgate-setup > "$PAYLOAD/etc/uci-defaults/99-tollgate-setup"
chmod 0755 "$PAYLOAD/etc/uci-defaults/99-tollgate-setup"
install -D -m 0755 packaging/files/usr/local/bin/first-login-setup                   "$PAYLOAD/usr/local/bin/first-login-setup"
install -D -m 0755 packaging/files/usr/bin/check_package_path                        "$PAYLOAD/usr/bin/check_package_path"
install -D -m 0755 packaging/files/usr/bin/tollgate-apply-ssl                        "$PAYLOAD/usr/bin/tollgate-apply-ssl"
install -D -m 0755 packaging/files/usr/bin/tollgate-remove-ssl                       "$PAYLOAD/usr/bin/tollgate-remove-ssl"
install -D -m 0644 packaging/files/lib/upgrade/keep.d/tollgate                        "$PAYLOAD/lib/upgrade/keep.d/tollgate"
install -D -m 0755 packaging/files/etc/hotplug.d/iface/95-tollgate-restart           "$PAYLOAD/etc/hotplug.d/iface/95-tollgate-restart"
install -D -m 0644 packaging/files/etc/nftables.d/20-nds-enforce.nft                 "$PAYLOAD/etc/nftables.d/20-nds-enforce.nft"
install -D -m 0644 packaging/files/etc/nftables.d/30-backend-firewall.nft            "$PAYLOAD/etc/nftables.d/30-backend-firewall.nft"

# Man pages
mkdir -p "$PAYLOAD/usr/share/man/man8"
for f in packaging/files/man/man8/*.8; do
  install -D -m 0644 "$f" "$PAYLOAD/usr/share/man/man8/$(basename "$f")"
done

# Captive portal site
mkdir -p "$PAYLOAD/etc/tollgate/tollgate-captive-portal-site" \
         "$PAYLOAD/etc/tollgate/ecash" \
         "$PAYLOAD/etc/crontabs"
cp -r packaging/files/tollgate-captive-portal-site/. "$PAYLOAD/etc/tollgate/tollgate-captive-portal-site/"

# License
install -D -m 0644 LICENSE "$PAYLOAD/usr/share/doc/${PKG_NAME}/LICENSE"

# preinst and postinst scripts
if [ -f packaging/preinst ]; then
  cp packaging/preinst "$PAYLOAD/../preinst" 2>/dev/null || true
fi

echo "Payload tree:"
find "$PAYLOAD" -maxdepth 3 -type f | head -30
echo "..."

PACKAGE_FILENAME="${PKG_NAME}_${PKG_VERSION}_${ARCH}.ipk"
echo "=== Building .ipk: $PACKAGE_FILENAME ==="

# Normalize payload mtimes to SOURCE_DATE_EPOCH: the packer pins archive
# mtimes itself, but normalized source files keep any future packaging
# path (e.g. the OpenWrt SDK) byte-stable too.
normalize_mtime "$PAYLOAD"

# nodogsplash is a RUNTIME dependency, never a replaced package: `Replaces:`
# makes opkg purge the daemon the module gates the network with, which is how
# an install used to end up with no captive portal at all. base-files stays —
# only the payload-file ownership overlap with it is intentional.
env \
  PKG_NAME="$PKG_NAME" \
  PKG_VERSION="$PKG_VERSION" \
  ARCH="$ARCH" \
  MAINTAINER="TollGate <tollgate@tollgate.me>" \
  LICENSE="GPL-3.0-only" \
  DEPENDS="libc, nodogsplash, jq" \
  PROVIDES="nodogsplash-files" \
  REPLACES="base-files" \
  DESCRIPTION="TollGate Basic Module for OpenWrt" \
  bash packaging/build-ipk.sh "$PAYLOAD" "packaging/$PACKAGE_FILENAME"

echo "=== Done ==="
ls -lh "packaging/$PACKAGE_FILENAME"
file "packaging/$PACKAGE_FILENAME"
