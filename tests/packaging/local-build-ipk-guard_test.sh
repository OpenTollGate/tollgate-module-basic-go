#!/usr/bin/env bash
# local-build-ipk-guard_test.sh — the portal-staging guard in
# packaging/local-build-ipk.sh must refuse to package a clean checkout
# (no built portal/admin bundles, #335) BEFORE any toolchain work,
# instead of silently producing an .ipk whose captive portal renders
# nothing — the exact footgun the happy-path suite (#544) caught as five
# phantom "portal broken" failures against a locally built artifact.
#
# Usage: bash tests/packaging/local-build-ipk-guard_test.sh   (repo root)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

TMP="$(mktemp -d)"
trap 'git -C "$REPO_ROOT" worktree remove --force "$TMP/wt" >/dev/null 2>&1 || rm -rf "$TMP"' EXIT
mkdir -p "$TMP/wt"

# A clean checkout of HEAD: committed files only, so the portal/admin
# build products staged by `make portal-build` (untracked, #335) are
# absent — the state an unsuspecting developer starts from.
git -C "$REPO_ROOT" worktree add --detach --quiet "$TMP/wt" HEAD

set +e
ERR="$(bash "$TMP/wt/packaging/local-build-ipk.sh" 2>&1 >/dev/null)"
RC=$?
set -e

fail() { echo "FAIL: $1"; exit 1; }

[ "$RC" -ne 0 ] || fail "script exited 0 on a clean checkout — the guard did not fire"
echo "$ERR" | grep -q "make portal-build" \
    || fail "error does not tell the developer to run 'make portal-build': $(echo "$ERR" | head -1)"
# The JS-bundle guard runs first: the committed portal shell
# (splash.html et al.) is present on a clean checkout but the built
# bundles are not, and the guest SPA deliberately has no index.html —
# the bundle set is its only staged-content canary.
echo "$ERR" | grep -q "JS bundles" \
    || fail "error does not name the unbuilt portal bundles: $(echo "$ERR" | head -1)"

# The guard must fire before any Go build output — the failure has to be
# fast and toolchain-independent.
echo "$ERR" | grep -q "Building Go binaries" \
    && fail "guard fired after the build started — it must refuse before toolchain work"

echo "passed=1 failed=0 (clean checkout refused: $(echo "$ERR" | head -1 | cut -c1-72)...)"
