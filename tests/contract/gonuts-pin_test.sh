#!/usr/bin/env bash
# Contract test: tests/contract/check-gonuts-pin.py verdicts.
#
# The gonuts pin fence (#791) exists because the pin drifted three ways in
# one release week; a fence that cannot fail is decoration, so every drift
# shape is planted in a fixture tree and the checker must refuse it BY NAME,
# then a clean tree must pass. Both go.mod require shapes are exercised —
# the single-line form is the one that hid a real stale pin (token-recovery,
# v0.10.0) from the first draft of the checker's regex.
#
# Offline only: the --latest leg needs the network and is CI/release-check's.
#
# Run from anywhere:  bash tests/contract/gonuts-pin_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CHECK="$ROOT/tests/contract/check-gonuts-pin.py"

cases=0
fails=0
pass() { cases=$((cases + 1)); printf 'ok   %s\n' "$1"; }
fail() { cases=$((cases + 1)); fails=$((fails + 1)); printf 'FAIL %s\n' "$1" >&2; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

MANIFEST='{"gonuts": {"repo": "https://github.com/OpenTollGate/gonuts-tollgate", "version": "%s"}}'
seed_tree() { # seed_tree <dir> <version> — a root manifest pinning <version>
    mkdir -p "$1/packaging"
    printf "$MANIFEST" "$2" >"$1/packaging/build-inputs.json"
}

carrier_block() { # carrier_block <go.mod-path> <version> — block-form require
    mkdir -p "$(dirname "$1")"
    printf 'module example.com/carrier\n\ngo 1.26.0\n\nrequire (\n\tgithub.com/OpenTollGate/gonuts-tollgate %s\n)\n' "$2" >"$1"
    printf 'github.com/OpenTollGate/gonuts-tollgate %s h1:fixturehash=\ngithub.com/OpenTollGate/gonuts-tollgate %s/go.mod h1:fixturehash=\n' "$2" "$2" >"${1%go.mod}go.sum"
}

carrier_single() { # carrier_single <go.mod-path> <version> — single-line require
    mkdir -p "$(dirname "$1")"
    printf 'module example.com/tool\n\ngo 1.26.0\n\nrequire github.com/OpenTollGate/gonuts-tollgate %s\n' "$2" >"$1"
    printf 'github.com/OpenTollGate/gonuts-tollgate %s h1:fixturehash=\n' "$2" >"${1%go.mod}go.sum"
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

d="$TMP/aligned"; seed_tree "$d" v0.13.0
carrier_block  "$d/src/merchant/go.mod" v0.13.0
carrier_single "$d/scripts/tool/go.mod"    v0.13.0
require_verdict "$d" accept "an aligned tree (block + single-line carriers) passes"

# --- drift shapes are refused, and named --------------------------------------

d="$TMP/block-drift"; seed_tree "$d" v0.13.0
carrier_block "$d/src/merchant/go.mod" v0.13.1
require_verdict "$d" refuse "a block-form carrier on a different version" "pins github.com/OpenTollGate/gonuts-tollgate v0.13.1, manifest says v0.13.0"

d="$TMP/single-drift"; seed_tree "$d" v0.13.0
carrier_block  "$d/src/tollwallet/go.mod" v0.13.0
carrier_single "$d/scripts/token-recovery/go.mod" v0.10.0
require_verdict "$d" refuse "a single-line carrier left behind (the token-recovery shape)" "scripts/token-recovery/go.mod: pins github.com/OpenTollGate/gonuts-tollgate v0.10.0"

d="$TMP/sum-lag"; seed_tree "$d" v0.13.0
carrier_block "$d/src/cli/go.mod" v0.13.0
printf 'github.com/OpenTollGate/gonuts-tollgate v0.12.0 h1:fixturehash=\n' >"$d/src/cli/go.sum"
require_verdict "$d" refuse "a go.sum that never recorded the truth version (half-update)" "does not record github.com/OpenTollGate/gonuts-tollgate v0.13.0"

d="$TMP/pseudo"; seed_tree "$d" "v0.14.0-0.20261006144824-d7591d2f3cb3"
carrier_block "$d/src/merchant/go.mod" "v0.14.0-0.20261006144824-d7591d2f3cb3"
require_verdict "$d" accept "a self-consistent pseudo-version integration pin passes OFFLINE (the --latest leg is the release gate for it)"

d="$TMP/no-manifest-key"; mkdir -p "$d/packaging"
printf '{"go": {"version": "1.26.8"}}\n' >"$d/packaging/build-inputs.json"
require_verdict "$d" refuse "a manifest without the .gonuts key" "no .gonuts.version"

d="$TMP/carrierless"; seed_tree "$d" v0.13.0
mkdir -p "$d/src/other"
printf 'module example.com/other\n\ngo 1.26.0\n' >"$d/src/other/go.mod"
require_verdict "$d" refuse "a tree whose manifest names a pin no carrier has" "no go.mod in the tree references"

# --- verdict ------------------------------------------------------------------

printf '\n%s cases, %s failed\n' "$cases" "$fails"
[ "$fails" = 0 ] || exit 1
exit 0
