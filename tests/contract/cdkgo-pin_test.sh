#!/usr/bin/env bash
# Contract test: tests/contract/check-cdkgo-pin.py verdicts.
#
# The cdk-go pin fence exists because main split the pin across carriers
# (v0.17.3 ×3 + v0.18.1 ×1) while #1190 converged every carrier on v0.18.3 —
# the #791 drift class with a different module. A fence that cannot fail is
# decoration, so every drift shape is planted in a fixture tree and the
# checker must refuse it BY NAME, then a clean tree must pass. Both go.mod
# require shapes are exercised, and so are both REPLACE shapes: a replace on
# the fenced module voids the require-line pin (the Amperstrand fork class,
# #1149), which is why the cdk fence refuses replaces outright — the gonuts
# fence deliberately does not, and its harness pins that difference too.
#
# Offline only: the --latest leg needs the network and is CI/release-check's.
#
# Run from anywhere:  bash tests/contract/cdkgo-pin_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CHECK="$ROOT/tests/contract/check-cdkgo-pin.py"

cases=0
fails=0
pass() { cases=$((cases + 1)); printf 'ok   %s\n' "$1"; }
fail() { cases=$((cases + 1)); fails=$((fails + 1)); printf 'FAIL %s\n' "$1" >&2; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

MANIFEST='{"cdk_go": {"repo": "https://github.com/cashubtc/cdk-go", "version": "%s"}}'
seed_tree() { # seed_tree <dir> <version> — a root manifest pinning <version>
    mkdir -p "$1/packaging"
    printf "$MANIFEST" "$2" >"$1/packaging/build-inputs.json"
}

carrier_block() { # carrier_block <go.mod-path> <version> — block-form require
    mkdir -p "$(dirname "$1")"
    printf 'module example.com/carrier\n\ngo 1.26.0\n\nrequire (\n\tgithub.com/cashubtc/cdk-go %s\n)\n' "$2" >"$1"
    printf 'github.com/cashubtc/cdk-go %s h1:fixturehash=\ngithub.com/cashubtc/cdk-go %s/go.mod h1:fixturehash=\n' "$2" "$2" >"${1%go.mod}go.sum"
}

carrier_single() { # carrier_single <go.mod-path> <version> — single-line require
    mkdir -p "$(dirname "$1")"
    printf 'module example.com/tool\n\ngo 1.26.0\n\nrequire github.com/cashubtc/cdk-go %s\n' "$2" >"$1"
    printf 'github.com/cashubtc/cdk-go %s h1:fixturehash=\n' "$2" >"${1%go.mod}go.sum"
}

require_verdict() { # require_verdict <dir> <want:accept|refuse> <label> [needle]
    local d="$1" want="$2" label="$3" needle="${4:-}" out rc
    out="$(python3 "$CHECK" --root "$d" 2>&1)"; rc=$?
    if [ "$want" = accept ] && [ "$rc" -eq 0 ]; then pass "$label"; return; fi
    if [ "$want" = refuse ] && [ "$rc" -ne 0 ]; then
        if [ -z "$needle" ] || printf '%s' "$out" | grep -qF -- "$needle"; then
            pass "$label"
        else
            fail "$label (refused, but the report does not name '$needle')"
        fi
        return
    fi
    fail "$label (rc=$rc, want $want; output: $(printf '%s' "$out" | tail -2 | tr '\n' ' '))"
}

# --- a clean tree passes, with BOTH require shapes ---------------------------

d="$TMP/aligned"; seed_tree "$d" v0.18.3
carrier_block  "$d/src/tollwallet/go.mod" v0.18.3
carrier_single "$d/scripts/tool/go.mod"    v0.18.3
require_verdict "$d" accept "an aligned tree (block + single-line carriers) passes"

# --- drift shapes are refused, and named --------------------------------------

d="$TMP/block-drift"; seed_tree "$d" v0.18.3
carrier_block "$d/src/merchant/go.mod" v0.18.2
require_verdict "$d" refuse "a block-form carrier on a different version" "pins github.com/cashubtc/cdk-go v0.18.2, manifest says v0.18.3"

d="$TMP/single-drift"; seed_tree "$d" v0.18.3
carrier_block  "$d/src/tollwallet/go.mod" v0.18.3
carrier_single "$d/src/cli/go.mod" v0.17.3
require_verdict "$d" refuse "a single-line carrier left behind (the main split's v0.17.3 shape)" "src/cli/go.mod: pins github.com/cashubtc/cdk-go v0.17.3"

d="$TMP/sum-lag"; seed_tree "$d" v0.18.3
carrier_block "$d/src/go.mod" v0.18.3
printf 'github.com/cashubtc/cdk-go v0.18.1 h1:fixturehash=\n' >"$d/src/go.sum"
require_verdict "$d" refuse "a go.sum that never recorded the truth version (half-update)" "does not record github.com/cashubtc/cdk-go v0.18.3"

d="$TMP/pseudo"; seed_tree "$d" "v0.19.0-0.20261101120000-abcdef012345"
carrier_block "$d/src/tollwallet/go.mod" "v0.19.0-0.20261101120000-abcdef012345"
require_verdict "$d" accept "a self-consistent pseudo-version integration pin passes OFFLINE (the --latest leg is the release gate for it)"

d="$TMP/no-manifest-key"; mkdir -p "$d/packaging"
printf '{"gonuts": {"version": "v0.13.0"}}\n' >"$d/packaging/build-inputs.json"
carrier_block "$d/src/tollwallet/go.mod" v0.18.3
require_verdict "$d" refuse "a manifest without the .cdk_go key" "no .cdk_go.version"

d="$TMP/carrierless"; seed_tree "$d" v0.18.3
mkdir -p "$d/src/other"
printf 'module example.com/other\n\ngo 1.26.0\n' >"$d/src/other/go.mod"
require_verdict "$d" refuse "a tree whose manifest names a pin no carrier has" "no go.mod in the tree references"

# --- replace shapes are refused, and named (the #1149 class) ------------------
# Identity holds in each of these trees — the require lines all match the
# manifest — so these cases prove the replace leg catches a drift the
# identity leg is structurally blind to.

d="$TMP/replace-single"; seed_tree "$d" v0.18.3
mkdir -p "$d/src/tollwallet"
printf 'module example.com/carrier\n\ngo 1.26.0\n\nrequire github.com/cashubtc/cdk-go v0.18.3\n\nreplace github.com/cashubtc/cdk-go => github.com/Amperstrand/cdk-go v0.18.3-ffi\n' >"$d/src/tollwallet/go.mod"
printf 'github.com/cashubtc/cdk-go v0.18.3 h1:fixturehash=\n' >"$d/src/tollwallet/go.sum"
require_verdict "$d" refuse "a single-line fork replace voids the pin (the #1149 shape)" "replaces github.com/cashubtc/cdk-go with github.com/Amperstrand/cdk-go"

d="$TMP/replace-block"; seed_tree "$d" v0.18.3
carrier_block "$d/src/tollwallet/go.mod" v0.18.3
printf '\nreplace (\n\tgithub.com/cashubtc/cdk-go => github.com/Amperstrand/cdk-go v0.18.3-ffi\n)\n' >>"$d/src/tollwallet/go.mod"
require_verdict "$d" refuse "a block-form fork replace voids the pin" "replaces github.com/cashubtc/cdk-go with github.com/Amperstrand/cdk-go"

d="$TMP/replace-pinned"; seed_tree "$d" v0.18.3
carrier_block "$d/src/tollwallet/go.mod" v0.18.3
printf '\nreplace github.com/cashubtc/cdk-go v0.18.3 => github.com/Amperstrand/cdk-go v0.18.3-ffi\n' >>"$d/src/tollwallet/go.mod"
require_verdict "$d" refuse "a version-pinned fork replace voids the pin" "replaces github.com/cashubtc/cdk-go with github.com/Amperstrand/cdk-go"

# --- verdict ------------------------------------------------------------------

printf '\n%s cases, %s failed\n' "$cases" "$fails"
[ "$fails" = 0 ] || exit 1
exit 0
