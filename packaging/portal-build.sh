#!/usr/bin/env bash
# Build the captive-portal bundle from OpenTollGate/tollgate-captive-portal-site.
#
# Reproducible mode (default): the portal revision comes from
# packaging/build-inputs.json (an immutable SHA); node and npm versions are
# verified against the manifest. A floating PORTAL_REF is rejected unless
# PORTAL_ALLOW_FLOATING=1 is set explicitly (dev only — output is then NOT
# byte-reproducible).
#
# The pinned portal tree owns FIVE build products and the module ships all five
# (see docs/architecture/captive-portal-bundle-location-decision.md):
#
#   1. guest SPA         build/*                         -> packaging/files/tollgate-captive-portal-site/
#   2. admin SPA         build/admin/*                   -> packaging/files/tollgate-admin/   (→ /www/tollgate)
#   3. rpcd plugin       openwrt/rpcd/tollgate           -> packaging/files/usr/libexec/rpcd/tollgate
#   4. rpcd ACL          openwrt/rpcd/tollgate_acl.json  -> packaging/files/usr/share/rpcd/acl.d/tollgate.json
#   5. admin uci-default packaging/files/etc/uci-defaults/92-tollgate-admin-setup
#                                                        -> same path, __ADMIN_HOME__ substituted
#
# A source artifact missing at the pin is a hard error: deriving the bundle from
# a pin that cannot produce it — or shipping a partial one — is exactly the
# stale-vendoring regression this script exists to prevent (a stale copy once
# shipped the pre10 admin board). Every staged copy is a build product and is
# gitignored: never commit built JS (upstream issue #335).
#
# Usage: [PORTAL_DIR=… OUTPUT_DIR=… ADMIN_OUTPUT_DIR=… ADMIN_HOME=… PORTAL_REF=<sha>] bash packaging/portal-build.sh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

# shellcheck source=build-env.sh
. "$REPO_ROOT/packaging/build-env.sh"

PORTAL_DIR="${PORTAL_DIR:-/tmp/tollgate-captive-portal-site}"
OUTPUT_DIR="${OUTPUT_DIR:-packaging/files/tollgate-captive-portal-site}"
ADMIN_OUTPUT_DIR="${ADMIN_OUTPUT_DIR:-packaging/files/tollgate-admin}"
# Brand webroot the shipped 92-tollgate-admin-setup points at. TollGate is the
# default brand; a net4sats build passes ADMIN_HOME=/www/net4sats.
ADMIN_HOME="${ADMIN_HOME:-/www/tollgate}"
PORTAL_REF="${PORTAL_REF:-$PORTAL_COMMIT}"

# Everything is staged INTO this repository, but the build below runs inside the
# portal clone (we `cd` into it). Resolve every destination against the
# repository root up front: a relative path left as-is silently lands in the
# portal checkout instead of here.
tg_repo_path() {
    case "$1" in
        /*) printf '%s' "$1" ;;
        *) printf '%s/%s' "$REPO_ROOT" "$1" ;;
    esac
}
OUTPUT_DIR="$(tg_repo_path "$OUTPUT_DIR")"
ADMIN_OUTPUT_DIR="$(tg_repo_path "$ADMIN_OUTPUT_DIR")"
RPC_PLUGIN_DEST="$(tg_repo_path "packaging/files/usr/libexec/rpcd/tollgate")"
RPC_ACL_DEST="$(tg_repo_path "packaging/files/usr/share/rpcd/acl.d/tollgate.json")"
ADMIN_SETUP_DEST="$(tg_repo_path "packaging/files/etc/uci-defaults/92-tollgate-admin-setup")"

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

# ---- stage every artifact of the bundle -------------------------------------
#
# Verify the pin against the whole bundle BEFORE touching the staging tree, so a
# stale or truncated pin fails loudly instead of shipping a partial bundle.
for _src in \
    "$PORTAL_DIR/build" \
    "$PORTAL_DIR/build/admin/index.html" \
    "$PORTAL_DIR/openwrt/rpcd/tollgate" \
    "$PORTAL_DIR/openwrt/rpcd/tollgate_acl.json" \
    "$PORTAL_DIR/packaging/files/etc/uci-defaults/92-tollgate-admin-setup"
do
    if [ ! -e "$_src" ]; then
        echo "ERROR: portal $PORTAL_REPO @ $RESOLVED_SHA does not provide '$_src'." >&2
        echo "The pinned portal.commit must point at a revision whose tree owns the whole" >&2
        echo "bundle (guest SPA, admin/, openwrt/rpcd/, packaging/). Re-pin it in" >&2
        echo "packaging/build-inputs.json — do not ship a partially derived bundle." >&2
        exit 1
    fi
done

# 1. Guest portal SPA. build/admin is a SEPARATE webroot (/www/<brand>) and is
#    staged below, so it must not be swept into the portal tree.
mkdir -p "$OUTPUT_DIR"
rm -rf "$OUTPUT_DIR/assets" "$OUTPUT_DIR/admin" \
       "$OUTPUT_DIR"/*.html "$OUTPUT_DIR"/*.json "$OUTPUT_DIR"/*.ico 2>/dev/null || true
for _entry in "$PORTAL_DIR"/build/*; do
    [ "$(basename "$_entry")" = "admin" ] && continue
    cp -r "$_entry" "$OUTPUT_DIR/"
done

# 2. Admin board (Preact SPA) — served at the root of its own uhttpd instance
#    (:8090) from /www/<brand>, so it needs a clean, complete webroot.
rm -rf "$ADMIN_OUTPUT_DIR"
mkdir -p "$ADMIN_OUTPUT_DIR"
cp -r "$PORTAL_DIR"/build/admin/. "$ADMIN_OUTPUT_DIR/"

# 3. rpcd plugin backing the `tollgate` ubus object (must be executable).
mkdir -p "$(dirname "$RPC_PLUGIN_DEST")"
install -m 0755 "$PORTAL_DIR/openwrt/rpcd/tollgate" "$RPC_PLUGIN_DEST"

# 4. The plugin's ACL (read/exec permissions for the admin board).
mkdir -p "$(dirname "$RPC_ACL_DEST")"
install -m 0644 "$PORTAL_DIR/openwrt/rpcd/tollgate_acl.json" "$RPC_ACL_DEST"

# 5. Admin uci-default (creates/repairs the dedicated :8090 uhttpd instance)
#    with the brand webroot substituted for __ADMIN_HOME__. The script falls back
#    to /www/tollgate when the token is still present, but substituting here
#    keeps the shipped file self-describing for the brand that was built.
mkdir -p "$(dirname "$ADMIN_SETUP_DEST")"
sed "s|__ADMIN_HOME__|$ADMIN_HOME|g" \
    "$PORTAL_DIR/packaging/files/etc/uci-defaults/92-tollgate-admin-setup" > "$ADMIN_SETUP_DEST"
chmod 0755 "$ADMIN_SETUP_DEST"
if grep -q '__ADMIN_HOME__' "$ADMIN_SETUP_DEST"; then
    echo "ERROR: __ADMIN_HOME__ substitution failed for $ADMIN_SETUP_DEST" >&2
    exit 1
fi

# Normalize mtimes so downstream packaging (ipk/apk) sees deterministic
# timestamps regardless of when the portal build happened.
normalize_mtime "$OUTPUT_DIR" "$ADMIN_OUTPUT_DIR" "$RPC_PLUGIN_DEST" "$RPC_ACL_DEST" "$ADMIN_SETUP_DEST"

echo "Portal built and staged from $OUTPUT_DIR (resolved SHA $RESOLVED_SHA):"
echo "  guest SPA    $OUTPUT_DIR"
echo "  admin SPA    $ADMIN_OUTPUT_DIR"
echo "  rpcd plugin  $RPC_PLUGIN_DEST"
echo "  rpcd ACL     $RPC_ACL_DEST"
echo "  uci-default  $ADMIN_SETUP_DEST (ADMIN_HOME=$ADMIN_HOME)"
