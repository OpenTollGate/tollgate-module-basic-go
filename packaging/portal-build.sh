#!/usr/bin/env bash
#
# portal-build.sh — Build captive portal site from source at release time.
#
# WHY: The Go repo used to commit minified JS/CSS compiler output from the
# tollgate-captive-portal-site repo. This made portal PRs unreviewable (95%
# of the diff was minified bundles). Instead, we gitignore the assets/ dir
# and build from source when packaging.
#
# WORKFLOW:
#   1. Clone (or pull) tollgate-captive-portal-site from GitHub
#   2. npm ci — install dependencies
#   3. npm run build — Vite build (outputs to build/)
#   4. Copy build/ output to packaging/files/tollgate-captive-portal-site/
#
# USAGE:
#   make portal-build
#   # or with overrides:
#   PORTAL_BRANCH=dev PORTAL_DIR=/tmp/portal bash packaging/portal-build.sh
#
# Portal source changes are reviewed in the tollgate-captive-portal-site repo.
# Go repo PRs should only touch splash.html, balance.html, locales/ — source files.
#
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
