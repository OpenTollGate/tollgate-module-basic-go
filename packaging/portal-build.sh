#!/usr/bin/env bash
set -euo pipefail

PORTAL_REPO="https://github.com/OpenTollGate/tollgate-captive-portal-site.git"
PORTAL_REF="${PORTAL_REF:-main}"
PORTAL_DIR="${PORTAL_DIR:-/tmp/tollgate-captive-portal-site}"
OUTPUT_DIR="${OUTPUT_DIR:-packaging/files/tollgate-captive-portal-site}"

echo "Building captive portal from source (ref: $PORTAL_REF)..."

if [ -d "$PORTAL_DIR/.git" ]; then
  cd "$PORTAL_DIR"
  git fetch --depth 1 origin "$PORTAL_REF"
  git checkout FETCH_HEAD
else
  git init "$PORTAL_DIR"
  cd "$PORTAL_DIR"
  git remote add origin "$PORTAL_REPO"
  git fetch --depth 1 origin "$PORTAL_REF"
  git checkout FETCH_HEAD
fi

npm ci
npm run build

mkdir -p "$OUTPUT_DIR/assets"
cp -r build/* "$OUTPUT_DIR/"

echo "Portal built and copied to $OUTPUT_DIR"
