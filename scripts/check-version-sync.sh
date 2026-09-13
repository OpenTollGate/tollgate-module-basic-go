#!/bin/sh
# Enforce the single source of truth for the release version.
#
# VERSION, at the repository root, is the one place the release version is
# written down. Everything else derives from it:
#
#   * CI (.github/workflows/build-package.yml) refuses to build a tag that
#     disagrees with VERSION, passes VERSION's value to -ldflags through the
#     go_ldflags helper in packaging/build-env.sh (the same helper
#     scripts/repro-test.sh calls), and version-stamps the .ipk control file
#     and the OpenWrt SDK Makefile with the tag / branch-derived
#     package_version;
#   * scripts/build-sdk-package.sh takes PACKAGE_VERSION from its caller (CI
#     passes the tag) and otherwise derives it from VERSION;
#   * the .ipk payload staging and the SDK Makefile substitute the
#     __TOLLGATE_VERSION__ placeholder that packaging/files/etc/uci-defaults/
#     99-tollgate-setup carries;
#   * packaging/local-build-ipk.sh falls back to reading VERSION itself.
#
# This script refuses to let a hand-written version literal creep back in, and
# optionally checks the tag against VERSION. Usage:
#
#   scripts/check-version-sync.sh            # internal consistency
#   scripts/check-version-sync.sh v0.6.0-alpha2   # ... and tag == VERSION
#
# It is wired into hooks/pre-commit and is a precondition in
# docs/release-process.md.
set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

EXPECTED="${1:-}"
fails=0

pass() { printf '  ok    %s\n' "$1"; }
fail() { printf '  FAIL  %s\n' "$1" >&2; fails=$((fails + 1)); }

printf 'check-version-sync: root %s\n' "$ROOT"

# --- 1. VERSION exists and is a supported release string --------------------
if [ ! -f VERSION ]; then
    fail "VERSION file is missing at the repository root"
    printf '\n1 check failed.\n' >&2
    exit 1
fi
VERSION="$(tr -d '[:space:]' < VERSION)"
if printf '%s' "$VERSION" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+(-(alpha|beta|rc|pre)[0-9]*)?$'; then
    pass "VERSION = $VERSION (a supported release string)"
else
    fail "VERSION '$VERSION' is not vMAJOR.MINOR.PATCH with an optional -alphaN/-betaN/-rcN/-preN suffix; packaging/normalize-apk-version.sh rejects anything else"
fi

# --- 2. the tag being released must equal VERSION ---------------------------
if [ -n "$EXPECTED" ]; then
    if [ "$EXPECTED" = "$VERSION" ]; then
        pass "tag $EXPECTED matches VERSION"
    else
        fail "tag $EXPECTED does not match VERSION ($VERSION) — bump VERSION and tag that commit"
    fi
fi

# --- 3. no version literal in the Go source ---------------------------------
# A release version in version.go would silently disagree with the tag, which is
# exactly how v0.0.0 shipped in an earlier build. The sentinel must stay.
if grep -qE '^[[:space:]]*Version[[:space:]]*=[[:space:]]*"v?[0-9]' src/cli/version.go; then
    fail "src/cli/version.go hardcodes a version literal; it must stay the \"dev\" sentinel and be injected with -ldflags"
else
    pass "src/cli/version.go carries no version literal"
fi
if grep -qE '^[[:space:]]*Version[[:space:]]*=[[:space:]]*"dev"' src/cli/version.go; then
    pass "src/cli/version.go keeps the \"dev\" sentinel"
else
    fail "src/cli/version.go no longer defines Version = \"dev\"; the -ldflags fallback must remain a non-release sentinel"
fi

# --- 4. the local .ipk builder derives from VERSION -------------------------
if grep -qE 'PKG_VERSION="?v[0-9]' packaging/local-build-ipk.sh; then
    fail "packaging/local-build-ipk.sh hardcodes a version literal; it must read VERSION"
else
    pass "packaging/local-build-ipk.sh hardcodes no version"
fi
if grep -q 'cat "$REPO_ROOT/VERSION"' packaging/local-build-ipk.sh; then
    pass "packaging/local-build-ipk.sh reads VERSION"
else
    fail "packaging/local-build-ipk.sh no longer reads VERSION"
fi
if grep -q '__TOLLGATE_VERSION__' packaging/local-build-ipk.sh; then
    pass "packaging/local-build-ipk.sh substitutes the setup-script placeholder"
else
    fail "packaging/local-build-ipk.sh does not substitute __TOLLGATE_VERSION__ into 99-tollgate-setup"
fi

# --- 4b. no PKG_VERSION literal default in ANY build script ------------------
# ${PKG_VERSION:-vX.Y.Z} (env-override with a literal fallback) hides a stale
# version from the plain `PKG_VERSION="vX.Y.Z"` greps above while still
# shipping it in every build that does not export PKG_VERSION. Every build
# script must fall back to the VERSION file instead.
BAD_PKG_DEFAULTS="$(grep -rnE 'PKG_VERSION[:-][^ ]*=?-?\"?v[0-9]+\.[0-9]+\.[0-9]+' \
    packaging/*.sh scripts/*.sh 2>/dev/null | grep -v 'check-version-sync.sh' || true)"
if [ -n "$BAD_PKG_DEFAULTS" ]; then
    printf '%s\n' "$BAD_PKG_DEFAULTS" >&2
    fail "a build script defaults PKG_VERSION to a version literal; default to \$(cat VERSION) instead"
else
    pass "no build script defaults PKG_VERSION to a version literal"
fi

# --- 5. the shipped setup script carries the placeholder, not a literal -----
SETUP_SCRIPT=packaging/files/etc/uci-defaults/99-tollgate-setup
if grep -qE '^SETUP_VERSION="v[0-9]' "$SETUP_SCRIPT"; then
    fail "$SETUP_SCRIPT hardcodes a version literal; it must ship the __TOLLGATE_VERSION__ placeholder"
else
    pass "$SETUP_SCRIPT carries no version literal"
fi
if grep -q '^SETUP_VERSION="__TOLLGATE_VERSION__"$' "$SETUP_SCRIPT"; then
    pass "$SETUP_SCRIPT ships the __TOLLGATE_VERSION__ placeholder"
else
    fail "$SETUP_SCRIPT does not define SETUP_VERSION=\"__TOLLGATE_VERSION__\""
fi

# --- 6. every packaging path substitutes the placeholder --------------------
# .ipk through CI's payload staging:
if grep -q 's|__TOLLGATE_VERSION__|${{ needs.determine-versioning.outputs.package_version }}|g' .github/workflows/build-package.yml; then
    pass "build-package.yml substitutes the placeholder into the .ipk payload"
else
    fail "build-package.yml does not substitute __TOLLGATE_VERSION__ into the .ipk payload"
fi
# The tag/version guard that keeps a release from being published under a tag
# that disagrees with VERSION:
if grep -q 'does not match VERSION' .github/workflows/build-package.yml; then
    pass "build-package.yml rejects a tag that disagrees with VERSION"
else
    fail "build-package.yml has no tag-vs-VERSION guard"
fi
# .apk through the OpenWrt SDK Makefile:
if grep -q 's|__TOLLGATE_VERSION__|$(TOLLGATE_DISPLAY_VERSION)|g' packaging/Makefile; then
    pass "packaging/Makefile substitutes the placeholder into the .apk tree"
else
    fail "packaging/Makefile does not substitute __TOLLGATE_VERSION__"
fi

# --- 7. the docs still say where the version lives --------------------------
# AGENTS.md is the first choice, but it is a protected agent-instruction file
# that is not writable in every workflow; CONTRIBUTING.md is the
# contributor-facing copy and docs/release-process.md the maintainer one.
if grep -q 'Version single source of truth' AGENTS.md 2>/dev/null; then
    pass "AGENTS.md documents the single source of truth"
elif grep -q 'Version single source of truth' CONTRIBUTING.md 2>/dev/null; then
    pass "CONTRIBUTING.md documents the single source of truth"
elif grep -q 'Version single source of truth' docs/release-process.md 2>/dev/null; then
    pass "docs/release-process.md documents the single source of truth"
else
    fail "no document states the version single source of truth (AGENTS.md, CONTRIBUTING.md, docs/release-process.md)"
fi

# --- 8. the SDK build helper derives from VERSION too -----------------------
if grep -nE 'PACKAGE_VERSION="v?[0-9]' scripts/build-sdk-package.sh | grep -v '0\.0\.0-r0' | grep -q .; then
    fail "scripts/build-sdk-package.sh hardcodes a version literal; it must default to VERSION"
else
    pass "scripts/build-sdk-package.sh hardcodes no version"
fi
if grep -q 'cat.*VERSION\|< "\$REPO_ROOT/VERSION"' scripts/build-sdk-package.sh; then
    pass "scripts/build-sdk-package.sh falls back to VERSION"
else
    fail "scripts/build-sdk-package.sh does not read VERSION"
fi

# --- 9. the release documents carry the version being released --------------
# A stale RELEASE-NOTES.md shipped once already (the v0.5.0 document was still
# in the tree two minors later); make the docs line up with VERSION or fail.
if grep -q "^## \[$VERSION\]" CHANGELOG.md; then
    pass "CHANGELOG.md has a [$VERSION] section"
else
    fail "CHANGELOG.md has no '## [$VERSION]' section; finalize [Unreleased] into the release heading"
fi
if head -n 1 RELEASE-NOTES.md | grep -qF "$VERSION"; then
    pass "RELEASE-NOTES.md documents $VERSION"
else
    fail "RELEASE-NOTES.md does not mention $VERSION on its title line; rewrite it for this release"
fi
if [ -f docs/release-process.md ] \
    && grep -q 'push upstream refs/tags/' docs/release-process.md \
    && grep -q 'never the fork' docs/release-process.md; then
    pass "docs/release-process.md carries the upstream tag runbook"
else
    fail "docs/release-process.md is missing or does not document tagging on upstream (never the fork)"
fi

printf '\n'
if [ "$fails" -ne 0 ]; then
    printf '%d version-consistency check(s) FAILED.\n' "$fails" >&2
    exit 1
fi
printf 'Version %s is consistent across every packaging path.\n' "$VERSION"
