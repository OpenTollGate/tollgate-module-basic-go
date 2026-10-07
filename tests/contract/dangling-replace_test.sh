#!/usr/bin/env bash
# Contract test: scripts/check-dangling-replace.sh (#550 guard).
#
# A replace directive whose target directory does not exist turns every
# go build/test in that module into a module-resolution failure that looks
# exactly like a code regression — the false-negative verification class
# #550 was filed over. The guard must catch every directory-path shape
# (single-line, grouped, ./ ../ and absolute targets) while leaving
# module-path targets and commented-out directives alone, and its failure
# output must point at go.work, the sanctioned per-developer override.
#
# This test pins both directions with fixture trees so a passing guard is
# never vacuous: planted dangling directives must fail (the guard has
# teeth), and legitimate module layouts must pass (no false positives —
# a guard that cries wolf gets disabled).
#
# Run from anywhere:  bash tests/contract/dangling-replace_test.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CHECK="$ROOT/scripts/check-dangling-replace.sh"

cases=0
fails=0
pass() { cases=$((cases + 1)); printf 'ok   %s\n' "$1"; }
fail() { cases=$((cases + 1)); fails=$((fails + 1)); printf 'FAIL %s\n' "$1"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# fixture_tree <name>: a minimal multi-module tree; echoes its root. Both
# src/realdep and realdep exist so ./ and ../ targets from different
# depths resolve to real directories.
fixture_tree() { # fixture_tree <name>
    local d="$TMP/$1"
    mkdir -p "$d/src/cli" "$d/src/realdep" "$d/realdep" "$d/src/abs-target"
    cat >"$d/src/go.mod" <<'EOF'
module example.com/root

go 1.25

require example.com/realdep v0.0.0

replace example.com/realdep => ./realdep

replace (
	example.com/realdep => ../realdep
	github.com/external/fork => github.com/mirror/fork v1.2.3
)
EOF
    cat >"$d/src/cli/go.mod" <<'EOF'
module example.com/cli

go 1.25

// replace example.com/gone => ../third_party/gonuts-fork
replace example.com/realdep => ../realdep
EOF
    printf 'module example.com/realdep\n\ngo 1.25\n' >"$d/src/realdep/go.mod"
    printf 'module example.com/realdep\n\ngo 1.25\n' >"$d/realdep/go.mod"
    printf '%s\n' "$d"
}

# --- the guard passes a legitimate tree ------------------------------------
clean_tree="$(fixture_tree clean)"
if bash "$CHECK" "$clean_tree" >"$TMP/clean.out" 2>&1; then
    pass "a tree of existing targets (single, grouped, ./, ../, module-path) passes"
else
    fail "clean tree flagged — false positive:"
    sed 's/^/       /' "$TMP/clean.out"
fi

# --- the guard catches each dangling shape ---------------------------------
expect_dangling() { # expect_dangling <label> <go.mod-content-append>
    local label="$1" append="$2" d out
    d="$(fixture_tree "dangling-$label")"
    out="$TMP/$label.out"
    printf '%s\n' "$append" >>"$d/src/cli/go.mod"
    if bash "$CHECK" "$d" >"$out" 2>&1; then
        fail "dangling $label target NOT caught — the guard lost its teeth"
    else
        if grep -q 'cli/go.mod' "$out" && grep -q 'go.work' "$out"; then
            pass "dangling $label target fails, naming the go.mod and go.work"
        else
            fail "dangling $label caught but output lacks the go.mod name or go.work guidance"
            sed 's/^/       /' "$out"
        fi
    fi
}

expect_dangling relative 'replace example.com/gonuts-fork => ../third_party/gonuts-fork'
expect_dangling dotrel   'replace example.com/gonuts-fork => ./third_party/gonuts-fork'
expect_dangling absolute "replace example.com/gonuts-fork => $TMP/no-such-abs-dir"
expect_dangling grouped 'replace (
	example.com/gonuts-fork => ../third_party/gonuts-fork
	example.com/realdep => ../realdep
)'

# --- controls: what the guard must NOT flag --------------------------------
controls_tree="$(fixture_tree controls)"
# A commented-out directive is prose, not config (the comment-stripping
# control — the tokenizer reads code, not comments).
if bash "$CHECK" "$TMP/controls" >"$TMP/controls.out" 2>&1; then
    pass "commented-out and module-path replaces stay invisible"
else
    fail "controls tree flagged — the guard reads prose or module paths:"
    sed 's/^/       /' "$TMP/controls.out"
fi

echo
printf 'tests: %d passed, %d failed\n' "$((cases - fails))" "$fails"
[ "$fails" = 0 ] || exit 1
exit 0
