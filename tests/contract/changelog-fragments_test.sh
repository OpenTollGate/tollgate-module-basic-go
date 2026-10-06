#!/usr/bin/env bash
# Contract test: the changelog fragment tool (scripts/changelog-assemble.py).
#
# Fragments exist so that two pull requests never edit the same lines of
# CHANGELOG.md: each PR adds its own uniquely-named file under changelog.d/,
# and the maintainer folds them into the changelog in one step. Every merge
# therefore leaves the changelog untouched, and the conflicts that used to be
# resolved by hand (a dozen hunks on the 0.6.0 train) cannot be produced.
#
# This test pins the fold's shape — where entries land, in what order, what is
# deleted, what is never touched — and the verdicts of `check`, so a release
# never depends on the tool's mood.
#
# Run from anywhere:  bash tests/contract/changelog-fragments_test.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
TOOL="$ROOT/scripts/changelog-assemble.py"

cases=0
fails=0
pass() { cases=$((cases + 1)); printf 'ok   %s\n' "$1"; }
fail() { cases=$((cases + 1)); fails=$((fails + 1)); printf 'FAIL %s\n' "$1"; }
ok() { if [ "$1" = 0 ]; then pass "$2"; else fail "$2"; fi; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# --- fixtures -------------------------------------------------------------

fixture() { # fixture <dir>
    mkdir -p "$1/changelog.d"
    cat >"$1/CHANGELOG.md" <<'EOF'
# Changelog

All notable changes to this project are documented here.

## [Unreleased]

## [v0.9.0] - 2026-01-01
### Added

- **An older, released change.** It stays exactly where it is.
EOF
}

fragment() { # fragment <dir> <filename> <line>...
    local d="$1" name="$2"
    shift 2
    printf '%s\n' "$@" >"$d/changelog.d/$name"
}

# Line number of the first line containing the literal string, or 0.
line_of() { grep -n -F -- "$2" "$1" 2>/dev/null | head -1 | cut -d: -f1; }
has() { grep -q -F -- "$2" "$1"; }

tool() { python3 "$TOOL" "$@"; }

# --- 1. fold: where entries land, in what order ---------------------------

d="$TMP/fold"
fixture "$d"
fragment "$d" 7-beta.added.md '- **Beta feature.** One line.'
fragment "$d" 3-delta.added.md '- **Delta feature.** One line.'
fragment "$d" 12-alpha.fixed.md '- **Alpha fix.** One line.'
fragment "$d" 9-gamma.internal.md '- **Gamma internals.** One line.'

if tool fold --root "$d" >"$TMP/fold.out" 2>&1; then
    c="$d/CHANGELOG.md"
    a="$(line_of "$c" '### Added')"
    i="$(line_of "$c" '### Changed / Internal')"
    f="$(line_of "$c" '### Fixed')"
    u="$(line_of "$c" '## [Unreleased]')"
    rel="$(line_of "$c" '## [v0.9.0]')"
    if { [ -n "$a" ] && [ -n "$i" ] && [ -n "$f" ] && [ "$u" -lt "$a" ] \
        && [ "$a" -lt "$i" ] && [ "$i" -lt "$f" ] && [ "$f" -lt "$rel" ]; }; then
        pass "fold: sections sit in the release block, in canonical order"
    else
        fail "fold: sections sit in the release block, in canonical order (a=$a i=$i f=$f rel=$rel)"
    fi
    b="$(line_of "$c" '- **Beta feature.**')"
    dl="$(line_of "$c" '- **Delta feature.**')"
    if { [ -n "$b" ] && [ -n "$dl" ] && [ "$b" -lt "$dl" ]; }; then
        pass "fold: entries read newest first (descending PR number)"
    else
        fail "fold: entries read newest first (beta=$b delta=$dl)"
    fi
    if { has "$c" '- **Alpha fix.** One line.' && has "$c" '- **Gamma internals.** One line.'; }; then
        pass "fold: fragment bodies land verbatim"
    else
        fail "fold: fragment bodies land verbatim"
    fi
    left="$(find "$d/changelog.d" -name '*.md' | wc -l)"
    if [ "$left" = 0 ]; then
        pass "fold: folded fragments are deleted"
    else
        fail "fold: folded fragments are deleted ($left left)"
    fi
    tail_orig='- **An older, released change.** It stays exactly where it is.'
    if has "$c" "$tail_orig"; then
        pass "fold: the released sections are left alone"
    else
        fail "fold: the released sections are left alone"
    fi
    if grep -q '^# Changelog' "$c"; then
        pass "fold: the document header survives"
    else
        fail "fold: the document header survives"
    fi
else
    fail "fold: exits 0 (output: $(head -3 "$TMP/fold.out" | tr '\n' ' '))"
    for n in sections-order newest-first verbatim deleted released-untouched header; do
        fail "fold: $n (fold did not run)"
    done
fi

# --- 2. fold --dry-run writes nothing ------------------------------------

d="$TMP/dry"
fixture "$d"
fragment "$d" 7-beta.added.md '- **Beta feature.** One line.'
cp "$d/CHANGELOG.md" "$TMP/dry.before"
if tool fold --root "$d" --dry-run >"$TMP/dry.out" 2>&1; then
    if cmp -s "$TMP/dry.before" "$d/CHANGELOG.md"; then
        pass "dry-run: CHANGELOG.md is not written"
    else
        fail "dry-run: CHANGELOG.md is not written"
    fi
    if [ -f "$d/changelog.d/7-beta.added.md" ]; then
        pass "dry-run: fragments are kept"
    else
        fail "dry-run: fragments are kept"
    fi
    if grep -q -F -- 'Beta feature' "$TMP/dry.out"; then
        pass "dry-run: the pending entry is shown"
    else
        fail "dry-run: the pending entry is shown"
    fi
else
    fail "dry-run: exits 0"
    fail "dry-run: CHANGELOG.md is not written"
    fail "dry-run: fragments are kept"
    fail "dry-run: the pending entry is shown"
fi

# --- 3. check: names and bodies are validated ----------------------------

d="$TMP/bad"
fixture "$d"
fragment "$d" 7-beta.added.md '- **Beta feature.** One line.'
if tool check --root "$d" >"$TMP/bad.out" 2>&1; then
    pass "check: a well-formed fragment passes"
else
    fail "check: a well-formed fragment passes ($(head -2 "$TMP/bad.out" | tr '\n' ' '))"
fi

fragment "$d" plain-name.md '- **No type in the name.**'
if tool check --root "$d" >"$TMP/bad2.out" 2>&1; then
    fail "check: a fragment without a type is refused"
else
    if grep -q -F -- 'plain-name.md' "$TMP/bad2.out"; then
        pass "check: a fragment without a type is refused, and named"
    else
        fail "check: a fragment without a type is refused, and named"
    fi
fi
rm "$d/changelog.d/plain-name.md"

fragment "$d" 5-x.bogus.md '- **Unknown type.**'
if tool check --root "$d" >"$TMP/bad3.out" 2>&1; then
    fail "check: an unknown type is refused"
else
    if grep -q -F -- '5-x.bogus.md' "$TMP/bad3.out"; then
        pass "check: an unknown type is refused, and named"
    else
        fail "check: an unknown type is refused, and named"
    fi
fi
rm "$d/changelog.d/5-x.bogus.md"

fragment "$d" 6-y.fixed.md 'Not a bullet.'
if tool check --root "$d" >"$TMP/bad4.out" 2>&1; then
    fail "check: a body that is not a bullet is refused"
else
    if grep -q -F -- '6-y.fixed.md' "$TMP/bad4.out"; then
        pass "check: a body that is not a bullet is refused, and named"
    else
        fail "check: a body that is not a bullet is refused, and named"
    fi
fi

# --- 4. check: the directory's README is not a fragment ------------------

d="$TMP/readme"
fixture "$d"
printf 'How to add a fragment.\n' >"$d/changelog.d/README.md"
if tool check --root "$d" >"$TMP/readme.out" 2>&1; then
    pass "check: changelog.d/README.md is ignored"
else
    fail "check: changelog.d/README.md is ignored ($(head -2 "$TMP/readme.out" | tr '\n' ' '))"
fi
cp "$d/CHANGELOG.md" "$TMP/readme.before"
if tool fold --root "$d" >"$TMP/readme2.out" 2>&1; then
    if { cmp -s "$TMP/readme.before" "$d/CHANGELOG.md" && [ -f "$d/changelog.d/README.md" ]; }; then
        pass "fold: no fragments is a no-op and keeps the README"
    else
        fail "fold: no fragments is a no-op and keeps the README"
    fi
else
    fail "fold: no fragments is a no-op (exit $?)"
fi

# --- 5. fold: an unknown target is refused, not guessed ------------------

d="$TMP/target"
fixture "$d"
fragment "$d" 7-beta.added.md '- **Beta feature.** One line.'
cp "$d/CHANGELOG.md" "$TMP/target.before"
if tool fold --root "$d" --target v9.9.9 >"$TMP/target.out" 2>&1; then
    fail "fold --target: an unknown release heading is refused"
else
    if { grep -q -F -- 'v9.9.9' "$TMP/target.out" && cmp -s "$TMP/target.before" "$d/CHANGELOG.md"; }; then
        pass "fold --target: an unknown release heading is refused, and nothing is written"
    else
        fail "fold --target: an unknown release heading is refused, and nothing is written"
    fi
fi

# --- 6. fold: an existing block keeps its content ------------------------

d="$TMP/existing"
fixture "$d"
fragment "$d" 7-beta.added.md '- **Beta feature.** One line.'
if tool fold --root "$d" --target v0.9.0 >"$TMP/existing.out" 2>&1; then
    c="$d/CHANGELOG.md"
    rel="$(line_of "$c" '## [v0.9.0]')"
    new="$(line_of "$c" '- **Beta feature.**')"
    old="$(line_of "$c" 'An older, released change')"
    if { [ -n "$new" ] && [ -n "$old" ] && [ "$rel" -lt "$new" ] && [ "$new" -lt "$old" ]; }; then
        pass "fold: a new wave lands under the release heading, above what was there"
    else
        fail "fold: a new wave lands under the release heading, above what was there (rel=$rel new=$new old=$old)"
    fi
    if grep -c -F -- '### Added' "$c" | grep -q '^2$'; then
        pass "fold: the existing subsection is not rewritten"
    else
        fail "fold: the existing subsection is not rewritten"
    fi
else
    fail "fold: into a populated block exits 0 ($(head -3 "$TMP/existing.out" | tr '\n' ' '))"
    fail "fold: a new wave lands under the release heading, above what was there"
    fail "fold: the existing subsection is not rewritten"
fi

# --- verdict --------------------------------------------------------------

printf '\n%s cases, %s failed\n' "$cases" "$fails"
[ "$fails" = 0 ] || exit 1
