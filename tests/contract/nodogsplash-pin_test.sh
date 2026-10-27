#!/usr/bin/env bash
# Contract test: tests/contract/check-nodogsplash-pin.py verdicts.
#
# The nodogsplash pin exists because the daemon was an UNVERSIONED runtime
# dependency: nothing in the tree named which one we ship, so a feed bump
# silently changed what an operator's router runs. Per AGENTS.md rule 4 a fence
# that cannot fail is decoration — so every drift shape is PLANTED in a fixture
# tree and the checker must REFUSE it BY NAME, and a clean tree must pass.
#
# The drift shapes are the ones this repo has actually produced or can:
#
#   * a doc naming a version no era pins (the README bench note's shape);
#   * a carrier naming the WRONG PACKAGING RELEASE — the half a single-value
#     pin loses, and the exact difference between the two OpenWrt eras
#     (apk 5.0.2-r2 vs ipk 5.0.2-r1). This one must be refused even though
#     the upstream version matches, or the fence collapses the eras into a
#     false equality;
#   * a package-filename claim (nodogsplash-5.0.2-r2.apk), which is how the
#     installed artifact is named in prose;
#   * a manifest that dropped the pin, dropped an era, or dropped the -rN
#     release — each must be refused as a MISSING PIN, not tolerated;
#   * a tree where nothing names a version at all.
#
# Also planted: the artifact leg, with a REAL .ipk built by this repo's own
# packager, declaring a nodogsplash the manifest does not name.
#
# Offline only: the --feed leg needs the network and is CI/release-check's.
#
# Run from anywhere:  bash tests/contract/nodogsplash-pin_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CHECK="$ROOT/tests/contract/check-nodogsplash-pin.py"

cases=0
fails=0
pass() { cases=$((cases + 1)); printf 'ok   %s\n' "$1"; }
fail() { cases=$((cases + 1)); fails=$((fails + 1)); printf 'FAIL %s\n' "$1" >&2; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# A manifest carrying the same per-era shape the real one does. Values are
# parameters so a case can drop a key or move a release.
manifest() { # manifest <dir> [apk-release] [ipk-release]
    local d="$1" apk_r="${2:-2}" ipk_r="${3:-1}"
    mkdir -p "$d/packaging"
    cat >"$d/packaging/build-inputs.json" <<EOF
{
  "schema": 1,
  "nodogsplash": {
    "upstream": {"repo": "https://github.com/nodogsplash/nodogsplash", "tag": "v5.0.2"},
    "per_era": {
      "apk": {
        "version": "5.0.2", "release": "$apk_r",
        "feed_repo": "openwrt/routing", "feed_package": "nodogsplash",
        "feed_branch": "openwrt-25.12",
        "source_hash": "908d3674e93726fdcefb4c3b6705c745753435df9d46425781a57e3f6b417797"
      },
      "ipk": {
        "version": "5.0.2", "release": "$ipk_r",
        "feed_repo": "openwrt/routing", "feed_package": "nodogsplash",
        "feed_branch": "openwrt-24.10",
        "source_hash": "908d3674e93726fdcefb4c3b6705c745753435df9d46425781a57e3f6b417797"
      }
    },
    "era_divergence": "apk 5.0.2-r2, ipk 5.0.2-r1"
  }
}
EOF
}

# A clean tree: one carrier naming the upstream version bare (matches BOTH
# eras, the shape most of this repo's prose uses).
clean_tree() { # clean_tree <dir> [apk-release] [ipk-release]
    local d="$1"
    manifest "$d" "${2:-2}" "${3:-1}"
    mkdir -p "$d/docs"
    printf '# Bench note\n\nThe stack runs nodogsplash 5.0.2 with a keepalive contract.\n' \
        >"$d/docs/bench.md"
}

require_verdict() { # require_verdict <dir> <accept|refuse> <label> [needle]
    local d="$1" want="$2" label="$3" needle="${4:-}" out rc
    out="$(python3 "$CHECK" --root "$d" 2>&1)"; rc=$?
    if [ "$want" = accept ] && [ "$rc" -eq 0 ]; then pass "$label"; return; fi
    if [ "$want" = refuse ] && [ "$rc" -ne 0 ]; then
        if [ -z "$needle" ] || printf '%s' "$out" | grep -qF -- "$needle"; then
            pass "$label"
        else
            fail "$label (refused, but the report does not name '$needle'; got: $(printf '%s' "$out" | tail -3 | tr '\n' ' '))"
        fi
        return
    fi
    fail "$label (rc=$rc, want $want; output: $(printf '%s' "$out" | tail -3 | tr '\n' ' '))"
}

# --- a clean tree passes -----------------------------------------------------

d="$TMP/clean"; clean_tree "$d"
require_verdict "$d" accept "a tree naming the pinned upstream version passes"

d="$TMP/clean-same-release"; manifest "$d" 2 2
mkdir -p "$d/docs"; printf 'nodogsplash 5.0.2\n' >"$d/docs/x.md"
require_verdict "$d" accept "identical era releases are allowed (the pin need not diverge forever)"

# --- drift shapes are refused, and named --------------------------------------

d="$TMP/wrong-version"; clean_tree "$d"
printf 'The daemon (nodogsplash 5.0.3) gates the interface.\n' >"$d/docs/version.md"
require_verdict "$d" refuse "a carrier naming a version no era pins" \
    "names nodogsplash 5.0.3, which neither era pins"

d="$TMP/wrong-release"; clean_tree "$d"
printf 'Shipped as nodogsplash-5.0.2-r3.apk on the 25.12 lane.\n' >"$d/docs/release.md"
require_verdict "$d" refuse "a carrier naming the wrong PACKAGING release (the half a single-value pin loses)" \
    "names nodogsplash 5.0.2-r3"

# The load-bearing one: 5.0.2-r2 is CORRECT for the apk era and WRONG for the
# ipk era. A checker that collapsed the eras into one string would pass this.
d="$TMP/era-false-equality"; clean_tree "$d"
printf 'Installed as nodogsplash_5.0.2-r2_x86_64.ipk from the 24.10 feed.\n' >"$d/docs/artifacts.md"
require_verdict "$d" refuse "the apk era's -r2 named for the ipk lane is refused (no false equality)" \
    "names nodogsplash 5.0.2-r2"

d="$TMP/nothing-names-it"; manifest "$d"
mkdir -p "$d/docs"; printf 'The portal is gated by a captive-portal daemon.\n' >"$d/docs/x.md"
require_verdict "$d" refuse "a tree that names no version at all" \
    "no file in the tree names a nodogsplash version"

# --- a broken pin is refused as a missing PIN ---------------------------------

d="$TMP/no-pin"; mkdir -p "$d/packaging"
printf '{"go": {"version": "1.26.10"}}\n' >"$d/packaging/build-inputs.json"
mkdir -p "$d/docs"; printf 'nodogsplash 5.0.2\n' >"$d/docs/x.md"
require_verdict "$d" refuse "a manifest with no .nodogsplash block" "no .nodogsplash block"

d="$TMP/no-per-era"; mkdir -p "$d/packaging"
printf '{"nodogsplash": {"version": "5.0.2"}}\n' >"$d/packaging/build-inputs.json"
mkdir -p "$d/docs"; printf 'nodogsplash 5.0.2\n' >"$d/docs/x.md"
require_verdict "$d" refuse "a single-value pin (no .per_era map)" "no .per_era map"

d="$TMP/missing-era"; mkdir -p "$d/packaging"
printf '{"nodogsplash": {"per_era": {"apk": {"version": "5.0.2", "release": "2"}}}}\n' \
    >"$d/packaging/build-inputs.json"
mkdir -p "$d/docs"; printf 'nodogsplash 5.0.2\n' >"$d/docs/x.md"
require_verdict "$d" refuse "a pin that names only one era" ".nodogsplash.per_era.ipk is missing"

d="$TMP/bare-version"; mkdir -p "$d/packaging"
printf '{"nodogsplash": {"per_era": {"apk": {"version": "5.0.2"}, "ipk": {"version": "5.0.2", "release": "1"}}}}\n' \
    >"$d/packaging/build-inputs.json"
mkdir -p "$d/docs"; printf 'nodogsplash 5.0.2\n' >"$d/docs/x.md"
require_verdict "$d" refuse "a pin without the packaging release (version only)" \
    "must pin version AND packaging release"

# --- the artifact leg reads what the package manager reads --------------------
#
# Build a real .ipk with this repo's own packager and plant a dependency on a
# daemon version the manifest does not name. The artifact is the thing apk/opkg
# consumes, so this leg must refuse on metadata no recipe edit can hide.

d="$TMP/artifact"; clean_tree "$d"
if [ -f "$ROOT/packaging/build-ipk.sh" ]; then
    work="$TMP/artifact-build"
    mkdir -p "$work/payload/usr/bin"
    : >"$work/payload/usr/bin/tollgate-wrt"
    build_ipk() { # build_ipk <out-name> <depends>
        env PKG_NAME="tollgate-wrt" PKG_VERSION="0.0.0-test" ARCH="x86_64" \
            MAINTAINER="TollGate <tollgate@tollgate.me>" LICENSE="GPL-3.0-only" \
            DEPENDS="$2" PROVIDES="nodogsplash-files" REPLACES="base-files" \
            DESCRIPTION="TollGate Basic Module for OpenWrt" \
            sh "$ROOT/packaging/build-ipk.sh" "$work/payload" "$work/$1" >/dev/null 2>&1
    }

    if build_ipk unversioned.ipk "libc, nodogsplash, jq, ca-bundle"; then
        out="$(python3 "$CHECK" --root "$d" --artifact "$work/unversioned.ipk" 2>&1)"; rc=$?
        if [ "$rc" -eq 0 ]; then
            pass "an artifact depending on nodogsplash without a version passes (the recipes' shape)"
        else
            fail "an unversioned nodogsplash dependency should pass as the recipes' shape (got rc=$rc: $(printf '%s' "$out" | tail -2 | tr '\n' ' '))"
        fi
    else
        fail "could not build the unversioned test .ipk with packaging/build-ipk.sh"
    fi

    if build_ipk foreign.ipk "libc, nodogsplash (>= 5.0.9), jq"; then
        out="$(python3 "$CHECK" --root "$d" --artifact "$work/foreign.ipk" 2>&1)"; rc=$?
        if [ "$rc" -ne 0 ] && printf '%s' "$out" | grep -qF "5.0.9"; then
            pass "an artifact resolving a daemon the pin does not name is refused, by name"
        else
            fail "the artifact leg did not refuse a foreign nodogsplash version (rc=$rc: $(printf '%s' "$out" | tail -2 | tr '\n' ' '))"
        fi
    else
        fail "could not build the versioned test .ipk with packaging/build-ipk.sh"
    fi

    # The pin NAMES 5.0.2-r2 for apk; an artifact that states the pinned
    # version must pass, or the leg would refuse correctness as drift.
    if build_ipk pinned.ipk "libc, nodogsplash (>= 5.0.2), jq"; then
        out="$(python3 "$CHECK" --root "$d" --artifact "$work/pinned.ipk" 2>&1)"; rc=$?
        if [ "$rc" -eq 0 ]; then
            pass "an artifact stating the pinned daemon version passes"
        else
            fail "an artifact stating the pinned version was refused (rc=$rc: $(printf '%s' "$out" | tail -2 | tr '\n' ' '))"
        fi
    fi
else
    fail "packaging/build-ipk.sh is missing — cannot exercise the artifact leg"
fi

# --- the real tree is clean ---------------------------------------------------

out="$(python3 "$CHECK" --root "$ROOT" 2>&1)"; rc=$?
if [ "$rc" -eq 0 ]; then
    pass "this checkout's own tree passes the fence"
else
    fail "this checkout's own tree does not pass: $(printf '%s' "$out" | tail -3 | tr '\n' ' ')"
fi

# --- verdict ------------------------------------------------------------------

printf '\n%s cases, %s failed\n' "$cases" "$fails"
[ "$fails" = 0 ] || exit 1
exit 0
