#!/bin/sh
# Enforce the single source of truth for the release version.
#
# VERSION, at the repository root, is the one place the release version is
# written down. Everything else derives from it — or overrides it in exactly
# one way: TG_VERSION (packaging/build-env.sh tg_version) is the single
# documented override knob, wired through every consumer (#941):
#
#   * CI (.github/workflows/build-package.yml) refuses to build a tag that
#     disagrees with VERSION, derives the ldflags stamp from the tag on tag
#     builds (from TG_VERSION on a dispatch override, else VERSION) through
#     the go_ldflags helper in packaging/build-env.sh (the same helper
#     scripts/repro-test.sh calls), and version-stamps the .ipk control file
#     and the OpenWrt SDK Makefile with the tag / branch-derived
#     package_version;
#   * scripts/build-sdk-package.sh takes PACKAGE_VERSION from its caller (CI
#     passes the tag) and otherwise resolves it with tg_version;
#   * the .ipk payload staging and the SDK Makefile substitute the
#     __TOLLGATE_VERSION__ placeholder that packaging/files/etc/uci-defaults/
#     99-tollgate-setup carries;
#   * packaging/local-build-ipk.sh resolves through the same tg_version
#     helper;
#   * every build lane asserts, post-build, that the binary's embedded
#     version equals the version it packaged (tg_assert_version_consistency;
#     TG_REQUIRE_VERSION_CONSISTENCY=1 makes it fail — #941, the rc5
#     package carried a binary stamped from a lagged VERSION file).
#
# This script refuses to let a hand-written version literal creep back in,
# refuses a workflow Go pin that disagrees with packaging/build-inputs.json
# (the toolchain single source of truth), and optionally checks the tag
# against VERSION. Usage:
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
    fail "packaging/local-build-ipk.sh hardcodes a version literal; it must resolve via tg_version"
else
    pass "packaging/local-build-ipk.sh hardcodes no version"
fi
if grep -q 'tg_version' packaging/local-build-ipk.sh; then
    pass "packaging/local-build-ipk.sh resolves the version through tg_version (TG_VERSION > VERSION)"
else
    fail "packaging/local-build-ipk.sh does not call tg_version; it must not re-derive the version itself (#941)"
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

# --- 5b. the placeholder appears exactly once in code (the assignment) ------
# Packaging substitutes __TOLLGATE_VERSION__ globally. A second occurrence in
# actual code — the #459 bug was a literal sentinel inside the case pattern —
# gets rewritten to the real version, matches the substituted assignment, and
# makes the fallback fire on every shipped build. Mentions inside comments are
# harmless (and #463's fix carries one), so only non-comment lines count.
PLACEHOLDER_CODE_LINES=$(grep -v '^[[:space:]]*#' "$SETUP_SCRIPT" | grep -c '__TOLLGATE_VERSION__' || true)
if [ "$PLACEHOLDER_CODE_LINES" -eq 1 ]; then
    pass "$SETUP_SCRIPT uses the placeholder exactly once in code (assignment only)"
else
    fail "$SETUP_SCRIPT uses __TOLLGATE_VERSION__ on $PLACEHOLDER_CODE_LINES non-comment lines; global substitution rewrites every occurrence, so it must appear only in the SETUP_VERSION assignment (#459)"
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
    fail "scripts/build-sdk-package.sh hardcodes a version literal; it must resolve via tg_version"
else
    pass "scripts/build-sdk-package.sh hardcodes no version"
fi
if grep -q 'tg_version' scripts/build-sdk-package.sh; then
    pass "scripts/build-sdk-package.sh resolves the version through tg_version (TG_VERSION > VERSION)"
else
    fail "scripts/build-sdk-package.sh does not call tg_version; it must not re-derive the version itself (#941)"
fi

# --- 8b. one override knob, one resolver, one fence (#941) -------------------
# TG_VERSION must be declared exactly once (build-env.sh's tg_version:
# TG_VERSION > VERSION file) and consumed everywhere; the tag-derived stamp
# and the post-build consistency fence are what keep a lagged VERSION file
# from shipping a mis-stamped binary again (rc5 carried a rc4 stamp).
if grep -q 'tg_version()' packaging/build-env.sh && grep -q 'TG_VERSION' packaging/build-env.sh; then
    pass "packaging/build-env.sh owns the tg_version resolver (TG_VERSION > VERSION)"
else
    fail "packaging/build-env.sh no longer defines tg_version with the TG_VERSION override"
fi
for script in packaging/local-build-ipk.sh scripts/build-sdk-package.sh; do
    if grep -q 'tg_assert_version_consistency' "$script"; then
        pass "$script asserts the built binary's embedded version"
    else
        fail "$script lost the post-build version-consistency fence (#941)"
    fi
done
if grep -q 'TG_REQUIRE_VERSION_CONSISTENCY: "1"' .github/workflows/build-package.yml \
    && grep -q 'tg_assert_version_consistency' .github/workflows/build-package.yml; then
    pass "build-package.yml runs the consistency fence in failing mode"
else
    fail "build-package.yml must run tg_assert_version_consistency with TG_REQUIRE_VERSION_CONSISTENCY=1 (#941)"
fi
# The ngit mirror is the build of record (README): its stage-1 compile
# shard must fence the same way — a committed "every build lane asserts"
# claim that skips the lane releases actually come from is the drift-doc
# class this repo fences.
if grep -q 'TG_REQUIRE_VERSION_CONSISTENCY: "1"' .ngit/act/workflows/build-package-binaries.yml \
    && grep -q 'tg_assert_version_consistency' .ngit/act/workflows/build-package-binaries.yml; then
    pass "the ngit build-of-record lane runs the consistency fence in failing mode"
else
    fail ".ngit/act/workflows/build-package-binaries.yml must run tg_assert_version_consistency with TG_REQUIRE_VERSION_CONSISTENCY=1 (#941)"
fi
# Fence subjects derive from TARGETS, never an enumerated key copy: an
# enumerated fence silently skips a target added later (#791 class).
for lane in .github/workflows/build-package.yml .ngit/act/workflows/build-package-binaries.yml; do
    if grep -qE 'for key in (arm64|armv7|mips|mipsle|amd64|riscv)' "$lane"; then
        fail "$lane enumerates the compile keys in its fence loop; derive them from TARGETS (#791 class)"
    else
        pass "$lane derives its fence subjects from TARGETS"
    fi
done

# --- 8c. tg_version is the only VERSION reader among build scripts ----------
# Discovery by glob, never enumeration: any script under scripts/ or
# packaging/ that still reads the VERSION file directly re-declares the
# resolver — the per-script re-derivation is exactly what let rc5's stamp
# lag the tag (#941). The resolver itself (packaging/build-env.sh) and this
# fence are the only sanctioned readers.
DIRECT_VERSION_READERS="$(grep -rln 'REPO_ROOT/VERSION' scripts/*.sh packaging/*.sh 2>/dev/null \
    | grep -v 'scripts/check-version-sync.sh' || true)"
if [ -z "$DIRECT_VERSION_READERS" ]; then
    pass "no build script reads VERSION directly; build-env.sh tg_version is the only resolver"
else
    printf '%s\n' "$DIRECT_VERSION_READERS" >&2
    fail "a build script reads the VERSION file directly; resolve through build-env.sh tg_version instead (#941)"
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

# --- 10. no lane-local Go literals: the manifest is the only source --------
# packaging/build-inputs.json is the single source of truth for the toolchain
# that builds the shipped binaries, and the lanes derive from it at run time
# (the go_pin step resolves .go.version via jq). A lane-local literal is the
# exact defect class that built 1.25.0 binaries against a 1.25.8 pin, so none
# may exist — not even one that happens to match today (it would go stale on
# the next pin bump). go-version-file users (test.yml resolves src/go.mod's
# module minimum) are deliberately exempt: that is a different invariant,
# checked below only for uniformity among the modules.
if command -v python3 >/dev/null 2>&1 && [ -f packaging/build-inputs.json ]; then
    PIN_GO="$(python3 -c 'import json; print(json.load(open("packaging/build-inputs.json"))["go"]["version"])' 2>/dev/null || true)"
    if [ -z "$PIN_GO" ]; then
        fail "cannot read go.version from packaging/build-inputs.json (the lanes derive their toolchain from it — a lane would resolve nothing)"
    else
        pin_drift=0
        for wf in .ngit/act/workflows/*.yml .github/workflows/*.yml; do
            [ -f "$wf" ] || continue
            for lit in $(sed -n 's/.*\(GO_VERSION\|go-version\): *"\([0-9][^"]*\)".*/\2/p' "$wf"); do
                fail "$wf carries a lane-local Go literal ($lit); derive from packaging/build-inputs.json ($PIN_GO) via the go_pin step instead"
                pin_drift=1
            done
        done
        if [ "$pin_drift" -eq 0 ]; then
            pass "no lane-local Go literals; every lane derives from build-inputs.json ($PIN_GO)"
        fi
    fi
else
    printf '  SKIP  python3 or packaging/build-inputs.json unavailable; Go-literal check not run\n' >&2
fi

# The 16 module go.mod directives must agree with each other. The module
# minimum is a separate invariant from the build pin (it may trail it) — only
# internal consistency is enforced.
gomod_first=""
gomod_drift=0
for gm in $(find src -name go.mod | sort); do
    gomod_go="$(sed -n 's/^go //p' "$gm" | head -1)"
    if [ -z "$gomod_first" ]; then
        gomod_first="$gomod_go"
    elif [ "$gomod_go" != "$gomod_first" ]; then
        fail "$gm declares go $gomod_go; the other modules declare $gomod_first"
        gomod_drift=1
    fi
done
if [ "$gomod_drift" -eq 0 ]; then
    pass "all module go.mod directives agree (go $gomod_first)"
fi

printf '\n'
if [ "$fails" -ne 0 ]; then
    printf '%d version-consistency check(s) FAILED.\n' "$fails" >&2
    exit 1
fi
printf 'Version %s is consistent across every packaging path.\n' "$VERSION"
