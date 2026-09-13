#!/usr/bin/env bash
# Build the captive-portal SPA from OpenTollGate/tollgate-captive-portal-site.
#
# Reproducible mode (default): the portal revision comes from
# packaging/build-inputs.json (an immutable SHA); node and npm versions are
# verified against the manifest. A floating PORTAL_REF is rejected unless
# PORTAL_ALLOW_FLOATING=1 is set explicitly (dev only — output is then NOT
# byte-reproducible).
#
# Usage: [PORTAL_DIR=… OUTPUT_DIR=… PORTAL_REF=<sha>] bash packaging/portal-build.sh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

# shellcheck source=build-env.sh
. "$REPO_ROOT/packaging/build-env.sh"

PORTAL_DIR="${PORTAL_DIR:-/tmp/tollgate-captive-portal-site}"
OUTPUT_DIR="${OUTPUT_DIR:-packaging/files/tollgate-captive-portal-site}"
PORTAL_REF="${PORTAL_REF:-$PORTAL_COMMIT}"

if [ "$PORTAL_REF" != "$PORTAL_COMMIT" ]; then
    if [ "${PORTAL_ALLOW_FLOATING:-0}" = "1" ]; then
        echo "WARNING: building floating portal ref '$PORTAL_REF' (manifest pins $PORTAL_COMMIT); output NOT reproducible" >&2
    else
        echo "ERROR: PORTAL_REF='$PORTAL_REF' does not match the pinned portal commit '$PORTAL_COMMIT'." >&2
        echo "Reproducible builds must use the manifest SHA. To update it, change packaging/build-inputs.json." >&2
        echo "Set PORTAL_ALLOW_FLOATING=1 for throwaway dev builds." >&2
        exit 1
    fi
fi

ACTIVE_NODE="$(node --version 2>/dev/null || true)"
ACTIVE_NPM="$(npm --version 2>/dev/null || true)"
if [ "${TG_ALLOW_NODE_MISMATCH:-0}" != "1" ]; then
    [ "$ACTIVE_NODE" = "v$NODE_VERSION" ] || { echo "ERROR: node $ACTIVE_NODE != pinned v$NODE_VERSION (see packaging/build-inputs.json)" >&2; exit 1; }
    [ "$ACTIVE_NPM" = "$NPM_VERSION" ] || { echo "ERROR: npm $ACTIVE_NPM != pinned $NPM_VERSION (see packaging/build-inputs.json)" >&2; exit 1; }
fi

echo "Building captive portal from $PORTAL_REPO @ $PORTAL_REF (node $ACTIVE_NODE, npm $ACTIVE_NPM, SOURCE_DATE_EPOCH=$SOURCE_DATE_EPOCH)..."

if [ -d "$PORTAL_DIR/.git" ]; then
  cd "$PORTAL_DIR"
  git fetch --depth 1 origin "$PORTAL_REF"
  git checkout --detach FETCH_HEAD
else
  git init "$PORTAL_DIR"
  cd "$PORTAL_DIR"
  git remote add origin "$PORTAL_REPO"
  git fetch --depth 1 origin "$PORTAL_REF"
  git checkout --detach FETCH_HEAD
fi

RESOLVED_SHA="$(git rev-parse HEAD)"
echo "$RESOLVED_SHA" > "$REPO_ROOT/packaging/portal-resolved.sha"
printf '{\n  "portal_commit": "%s",\n  "pinned_commit": "%s",\n  "node": "%s",\n  "npm": "%s",\n  "source_date_epoch": %s\n}\n' \
  "$RESOLVED_SHA" "$PORTAL_COMMIT" "$ACTIVE_NODE" "$ACTIVE_NPM" "$SOURCE_DATE_EPOCH" \
  > "$REPO_ROOT/packaging/portal-build-inputs.json"

npm ci
npm run build

mkdir -p "$OUTPUT_DIR/assets"
rm -rf "$OUTPUT_DIR/assets" "$OUTPUT_DIR"/*.html "$OUTPUT_DIR"/*.json "$OUTPUT_DIR"/*.ico 2>/dev/null || true
cp -r build/* "$OUTPUT_DIR/"

# Normalize mtimes so downstream packaging (ipk/apk) sees deterministic
# timestamps regardless of when the portal build happened.
normalize_mtime "$OUTPUT_DIR"

echo "Portal built and copied to $OUTPUT_DIR (resolved SHA $RESOLVED_SHA)"
