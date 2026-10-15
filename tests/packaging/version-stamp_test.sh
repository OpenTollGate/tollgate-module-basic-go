#!/usr/bin/env bash
# version-stamp_test.sh — #941: one override knob (TG_VERSION) resolved in
# one place (packaging/build-env.sh tg_version: TG_VERSION > VERSION file),
# consumed by every packaging lane, and a post-build fence
# (tg_assert_version_consistency) that warns by default and fails under
# TG_REQUIRE_VERSION_CONSISTENCY=1. The rc5 feed package shipped a binary
# self-reporting rc4 — the stamp derived from a lagged VERSION file while
# the tag and the package said rc5. This suite pins the three properties
# that keep that defect class out: resolution precedence, fence behavior
# (pass / warn / fail), and the wiring of both packaging scripts and the
# release workflow.
#
# Offline (no SDK, no router, no go build): the fence is exercised on
# stand-in binaries carrying the embedded string, the way strings(1) sees
# it in a real cross-compiled artifact.
#
# Usage: bash tests/packaging/version-stamp_test.sh   (repo root)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

fail() { echo "FAIL: $1"; exit 1; }

# --- 1. tg_version: TG_VERSION wins, the VERSION file is the fallback -------

# resolve <tg-version-or-empty>: prints tg_version's verdict. An empty
# TG_VERSION must behave like an unset one.
resolve() {
    ( set +e
      TG_ROOT="$REPO_ROOT" TG_VERSION="${1:-}" SOURCE_DATE_EPOCH=0 \
          bash -c '. "$TG_ROOT/packaging/build-env.sh"; tg_version' 2>/dev/null
    )
}

OVERRIDDEN="$(resolve v0.6.0-teststamp)"
[ "$OVERRIDDEN" = "v0.6.0-teststamp" ] \
    || fail "TG_VERSION=v0.6.0-teststamp resolved to '$OVERRIDDEN'; the override must win everywhere"

FILE_VERSION="$(tr -d '[:space:]' < "$REPO_ROOT/VERSION")"
[ -n "$FILE_VERSION" ] || fail "repo carries no VERSION content (test setup)"
FALLBACK="$(resolve "")"
[ "$FALLBACK" = "$FILE_VERSION" ] \
    || fail "empty TG_VERSION resolved to '$FALLBACK', expected the VERSION file's '$FILE_VERSION'"

# --- 2. the fence: pass / warn loudly / fail under TG_REQUIRE...=1 ----------

# fence_run <require 0|1> <binary> <expected>: runs the fence in a fresh
# shell (build-env.sh sources cleanly there) and captures RC + combined
# output. The rc5 incident shape — binary stamped rc4, package rc5 — is
# the planted-drift case that must refuse under require=1. The inner
# shell runs `set -euo pipefail` because EVERY caller lane does
# (local-build-ipk.sh, build-sdk-package.sh, both release workflows):
# a fence that only behaves without pipefail passed this suite while
# denying real builds (SIGPIPE from `grep -q` exiting early over a
# multi-megabyte `strings` stream — rehearsal-verified on a real build).
fence_run() {
    # set +e in the OUTER shell: the substitution's status is the fence's
    # verdict, and set -e would turn the failing case into a silent exit
    # before the RC assert below can name it.
    set +e
    FENCE_OUT="$(
        TG_ROOT="$REPO_ROOT" SOURCE_DATE_EPOCH=0 TG_REQUIRE_VERSION_CONSISTENCY="$1" \
            bash -c 'set -euo pipefail; . "$TG_ROOT/packaging/build-env.sh"; tg_assert_version_consistency "$1" "$2"' \
            fence-run "$2" "$3" 2>&1
    )"
    FENCE_RC=$?
    set -e
}

printf 'go.build.junk\nv0.6.0-teststamp\ntrailing.data\n' > "$TMP/stamped.bin"
printf 'v0.6.0-rc4\n' > "$TMP/lagged.bin"
# Realistic-mass stand-in: the match lands in the first kilobyte and
# megabytes follow, the shape a 12 MB cross-compiled binary presents to
# `strings`. Under pipefail this is what turned `grep -q`'s early exit
# into a 141 denial of a CORRECT stamp — the fixture must stay large
# enough (pipe buffer + grep's first read) for that class to reproduce.
{ printf 'v0.6.0-teststamp\n'; head -c 4194304 /dev/zero | tr '\0' 'j' | fold -w 64; } > "$TMP/big-stamped.bin"

fence_run 0 "$TMP/stamped.bin" v0.6.0-teststamp
[ "$FENCE_RC" -eq 0 ] || fail "fence failed a binary that embeds the expected version"
case "$FENCE_OUT" in *embeds*) ;; *) fail "fence passed silently; it must report the verified stamp";; esac

# Realistic mass under pipefail: the match early, megabytes behind it —
# the exact shape that made an early-exiting grep deny a correct stamp.
fence_run 0 "$TMP/big-stamped.bin" v0.6.0-teststamp
[ "$FENCE_RC" -eq 0 ] || fail "fence failed a realistic-mass binary that embeds the expected version (pipefail/SIGPIPE regression)"
case "$FENCE_OUT" in *embeds*) ;; *) fail "fence passed silently on the realistic-mass binary";; esac
fence_run 1 "$TMP/big-stamped.bin" v0.6.0-teststamp
[ "$FENCE_RC" -eq 0 ] || fail "require mode denied a correctly-stamped realistic-mass binary (pipefail/SIGPIPE regression)"

fence_run 0 "$TMP/lagged.bin" v0.6.0-rc5
[ "$FENCE_RC" -eq 0 ] || fail "warn mode must not fail the build (got RC=$FENCE_RC)"
case "$FENCE_OUT" in *WARNING*) ;; *) fail "warn mode was not loud: no WARNING in output";; esac

fence_run 1 "$TMP/lagged.bin" v0.6.0-rc5
[ "$FENCE_RC" -ne 0 ] \
    || fail "TG_REQUIRE_VERSION_CONSISTENCY=1 did not fail a lagged stamp (the rc5 shape) — the fence is decoration"

fence_run 0 "$TMP/absent.bin" v0.6.0-teststamp
[ "$FENCE_RC" -eq 0 ] || fail "warn mode must not fail on a missing binary path"
fence_run 1 "$TMP/absent.bin" v0.6.0-teststamp
[ "$FENCE_RC" -ne 0 ] || fail "TG_REQUIRE_VERSION_CONSISTENCY=1 must fail a missing binary path"

# --- 3. wiring: both scripts and the workflow consume the resolver ----------

grep -q 'PKG_VERSION="${PKG_VERSION:-$(tg_version)}"' "$REPO_ROOT/packaging/local-build-ipk.sh" \
    || fail "local-build-ipk.sh no longer resolves PKG_VERSION through tg_version"
grep -q 'PACKAGE_VERSION="${PACKAGE_VERSION:-$(tg_version)}"' "$REPO_ROOT/scripts/build-sdk-package.sh" \
    || fail "build-sdk-package.sh no longer resolves PACKAGE_VERSION through tg_version"
for script in packaging/local-build-ipk.sh scripts/build-sdk-package.sh; do
    grep -q 'tg_assert_version_consistency' "$REPO_ROOT/$script" \
        || fail "$script lost the post-build version-consistency fence"
done

WF="$REPO_ROOT/.github/workflows/build-package.yml"
grep -q 'TG_REQUIRE_VERSION_CONSISTENCY: "1"' "$WF" \
    || fail "build-package.yml does not set TG_REQUIRE_VERSION_CONSISTENCY=1 (the fence only warns there otherwise)"
grep -q 'TG_VERSION: ${{ github.event.inputs.version }}' "$WF" \
    || fail "build-package.yml does not wire the workflow_dispatch version input into TG_VERSION"
grep -q 'BUILD_VERSION="${GITHUB_REF_NAME}"' "$WF" \
    || fail "build-package.yml does not derive the stamp from the git tag on tag builds"
grep -q 'tg_assert_version_consistency' "$WF" \
    || fail "build-package.yml does not run the post-build consistency fence"

# --- 3b. the ngit build-of-record lane fences too; no enumerated keys ---------

# The ngit mirror is the build of record (README): its stage-1 compile
# shard must run the same fence in failing mode, and its CLI binary must
# carry main.version — an unstamped CLI embeds no version, so the fence
# would rightly refuse it.
NGIT="$REPO_ROOT/.ngit/act/workflows/build-package-binaries.yml"
grep -q 'TG_REQUIRE_VERSION_CONSISTENCY: "1"' "$NGIT" \
    || fail "the ngit stage-1 compile lane (build of record) does not run the fence in failing mode"
grep -q 'tg_assert_version_consistency' "$NGIT" \
    || fail "the ngit stage-1 compile lane lost the post-build version-consistency fence"
grep -q "main.version=\$PACKAGE_VERSION" "$NGIT" \
    || fail "the ngit stage-1 compile lane no longer stamps the CLI (main.version)"

# Both compile lanes must derive the fence's subjects from TARGETS, never
# an enumerated copy of the key list: an enumerated fence silently skips a
# target added to TARGETS later (the #791 enumeration-drift class,
# mutation-verified in review).
for wf in "$WF" "$NGIT"; do
    if grep -qE 'for key in (arm64|armv7|mips|mipsle|amd64|riscv)' "$wf"; then
        fail "$(basename "$wf") enumerates the compile keys in its fence loop; derive them from TARGETS"
    fi
done

# --- 4. harness lanes resolve (and pin) the same string ---------------------

# The reproducibility lanes were the remaining direct VERSION readers
# (#941 follow-up): each must resolve through tg_version and pin the
# resolved string into the build it drives — reprotest's hostile env
# variations and the remote VM must not re-derive the version.
for script in scripts/repro-test.sh scripts/repro-variance.sh scripts/shc-build-package.sh; do
    grep -q 'tg_version' "$REPO_ROOT/$script" \
        || fail "$script does not resolve its version through tg_version"
done
grep -q 'TG_VERSION=$PKG_VERSION' "$REPO_ROOT/scripts/repro-variance.sh" \
    || fail "repro-variance.sh does not pin the resolved version into the varied builds"
grep -q 'export PKG_VERSION=$PKG_VERSION' "$REPO_ROOT/scripts/shc-build-package.sh" \
    || fail "shc-build-package.sh does not pin the resolved version into the remote build"

echo "passed=1 failed=0 (override wins over VERSION; fence warns/fails; three lanes wired)"
