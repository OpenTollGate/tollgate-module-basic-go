#!/usr/bin/env bash
# Local .ipk builder — replicates CI pipeline for aarch64_cortex-a53.
# Usage: bash packaging/local-build-ipk.sh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

PKG_NAME="tollgate-wrt"
PKG_VERSION="v0.7.0-alpha10"
ARCH="aarch64_cortex-a53"
COMPILE_KEY="arm64"
GOARCH="arm64"

echo "=== Building Go binaries (GOOS=linux GOARCH=$GOARCH) ==="

BUILD_TIME=$(date -u '+%Y-%m-%d %H:%M:%S UTC')
GIT_COMMIT=$(git rev-parse --short HEAD)
LDFLAGS="-s -w \
  -X 'github.com/OpenTollGate/tollgate-module-basic-go/src/cli.Version=$PKG_VERSION' \
  -X 'github.com/OpenTollGate/tollgate-module-basic-go/src/cli.GitCommit=$GIT_COMMIT' \
  -X 'github.com/OpenTollGate/tollgate-module-basic-go/src/cli.BuildTime=$BUILD_TIME' \
  -X 'github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager.GitBranch=main'"

mkdir -p "bin/$COMPILE_KEY"

CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH \
  go build -C src -o "$REPO_ROOT/bin/$COMPILE_KEY/tollgate-wrt" \
  -trimpath -ldflags="$LDFLAGS" main.go

CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH \
  go build -C src/cmd/tollgate-cli -o "$REPO_ROOT/bin/$COMPILE_KEY/tollgate" \
  -trimpath -ldflags="$LDFLAGS"

ls -lh "bin/$COMPILE_KEY/"

echo "=== Assembling payload ==="

PAYLOAD=$(mktemp -d)
trap 'rm -rf "$PAYLOAD"' EXIT

install -D -m 0755 "bin/$COMPILE_KEY/tollgate-wrt" "$PAYLOAD/usr/bin/tollgate-wrt"
install -D -m 0755 "bin/$COMPILE_KEY/tollgate"     "$PAYLOAD/usr/bin/tollgate"

install -D -m 0755 packaging/files/etc/init.d/tollgate-wrt                           "$PAYLOAD/etc/init.d/tollgate-wrt"
install -D -m 0755 packaging/files/etc/uci-defaults/90-tollgate-captive-portal-symlink "$PAYLOAD/etc/uci-defaults/90-tollgate-captive-portal-symlink"
install -D -m 0755 packaging/files/etc/uci-defaults/99-tollgate-setup                 "$PAYLOAD/etc/uci-defaults/99-tollgate-setup"
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

env \
  PKG_NAME="$PKG_NAME" \
  PKG_VERSION="$PKG_VERSION" \
  ARCH="$ARCH" \
  MAINTAINER="TollGate <tollgate@tollgate.me>" \
  LICENSE="CC0-1.0" \
  DEPENDS="libc" \
  PROVIDES="nodogsplash-files" \
  REPLACES="nodogsplash, base-files" \
  DESCRIPTION="TollGate Basic Module for OpenWrt" \
  bash packaging/build-ipk.sh "$PAYLOAD" "packaging/$PACKAGE_FILENAME"

echo "=== Done ==="
ls -lh "packaging/$PACKAGE_FILENAME"
file "packaging/$PACKAGE_FILENAME"
