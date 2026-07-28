#!/usr/bin/env bash
set -euo pipefail

PORTAL_REPO="https://github.com/OpenTollGate/tollgate-captive-portal-site.git"
PORTAL_BRANCH="${PORTAL_BRANCH:-main}"
PORTAL_DIR="${PORTAL_DIR:-/tmp/tollgate-captive-portal-site}"
OUTPUT_DIR="${OUTPUT_DIR:-packaging/files/tollgate-captive-portal-site}"

echo "Building captive portal site from source..."

# Clone or pull portal source
if [ -d "$PORTAL_DIR/.git" ]; then
  cd "$PORTAL_DIR" && git pull origin "$PORTAL_BRANCH"
else
  git clone --depth 1 --branch "$PORTAL_BRANCH" "$PORTAL_REPO" "$PORTAL_DIR"
fi

cd "$PORTAL_DIR"
npm ci
npm run build

# Copy built output to packaging directory
mkdir -p "$OUTPUT_DIR/assets"
cp -r build/* "$OUTPUT_DIR/"

echo "Portal site built and copied to $OUTPUT_DIR"