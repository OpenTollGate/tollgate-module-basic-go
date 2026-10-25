#!/usr/bin/env bash
# Contract test: the fences.d/ dispatcher contract (#920).
#
# hooks/pre-commit used to be one script every fence-adding lane
# concatenated into, and a merge on main ate an entire `fi` between two
# fences — bash -n failed on main ("unexpected end of file", found
# 2026-10-14), so every commit through the hook was refused since. The
# dispatcher + one-complete-if/fi-block-per-file shape makes that
# unrepresentable, and a fence that cannot fail is decoration, so every
# drift shape is planted in a fixture repository and a real
# `git commit` through the hook must be REFUSED, with the offending
# fragment named; a clean fixture must commit. This drives the real
# surface (git + the hook), not a reimplementation of it.
#
# Offline only. Run from anywhere:  bash tests/contract/fences-dispatch_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
HOOK="$ROOT/hooks/pre-commit"
FENCES_DIR="$ROOT/fences.d"

cases=0
fails=0
pass() { cases=$((cases + 1)); printf 'ok   %s\n' "$1"; }
fail() { cases=$((cases + 1)); fails=$((fails + 1)); printf 'FAIL %s\n' "$1" >&2; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# --- the real tree: dispatcher and every fragment parse, names are ordered --

bash -n "$HOOK" \
    && pass "hooks/pre-commit parses" \
    || fail "hooks/pre-commit does not parse (bash -n)"

[ -d "$FENCES_DIR" ] && [ -n "$(ls "$FENCES_DIR"/*.sh 2>/dev/null)" ] \
    && pass "fences.d/ carries fences" \
    || fail "fences.d/ is missing or carries no *.sh fences"

name_bad=0
for f in "$FENCES_DIR"/*.sh; do
    fname="$(basename "$f")"
    if ! printf '%s' "$fname" | grep -qE '^[0-9]{2,3}-[a-z0-9-]+\.sh$'; then
        fail "$fname: fences.d/ names are NN-<slug>.sh (numeric prefix = run order)"
        name_bad=1
    fi
    if ! bash -n "$f"; then
        fail "$fname: does not parse standalone (bash -n) — the exact shape that broke main"
        name_bad=1
    fi
done
[ "$name_bad" -eq 0 ] && pass "every fences.d/*.sh is named NN-<slug>.sh and parses standalone"

# --- fixture repos: a real git commit through the real hook ------------------
#
# commit_in <dir> <path-to-stage>: stage a file, commit through the hook,
# return git's exit status and leave the combined output in $OUT.
commit_in() { # commit_in <dir> <staged-path>
    local d="$1" staged="$2"
    mkdir -p "$d/$(dirname "$staged")"
    echo x >"$d/$staged"
    git -C "$d" add "$staged"
    OUT="$(git -C "$d" \
        -c user.name=fixture -c user.email=fixture@example.invalid \
        -c commit.gpgsign=false -c core.hooksPath=hooks \
        commit -m fixture 2>&1)"
    return $?
}

seed_repo() { # seed_repo <dir>: a git repo with the REAL dispatcher installed
    mkdir -p "$1"
    git -C "$1" init -q
    git -C "$1" -c user.name=fixture -c user.email=fixture@example.invalid \
        -c commit.gpgsign=false commit -q --allow-empty -m init
    mkdir -p "$1/hooks" "$1/fences.d"
    cp "$HOOK" "$1/hooks/pre-commit"
}

CLEAN_FENCE='if git diff --cached --name-only | grep -q "^src/"; then
    echo "fixture fence ran"
fi
'

# 1. Positive control: a clean fence that runs must not block the commit.
d="$TMP/clean"; seed_repo "$d"
printf '%s' "$CLEAN_FENCE" >"$d/fences.d/10-clean.sh"
if commit_in "$d" src/file.go && printf '%s' "$OUT" | grep -q 'fixture fence ran'; then
    pass "a clean fence runs and the commit lands"
else
    fail "a clean fence blocked the commit (rc=$?; output: $OUT)"
fi

# 2. The main bug, planted: a fragment missing its `fi` (the exact hunk a
#    merge ate on main). The commit must be refused AND the fragment named.
d="$TMP/eaten-fi"; seed_repo "$d"
printf '%s' 'if git diff --cached --name-only | grep -q "^src/"; then
    echo "never closed"
' >"$d/fences.d/10-eaten-fi.sh"
if commit_in "$d" src/file.go; then
    fail "a fragment missing its fi let the commit through (main's exact bug)"
elif printf '%s' "$OUT" | grep -q 'fences.d/10-eaten-fi.sh'; then
    pass "a fragment missing its fi refuses the commit and names the file"
else
    fail "the commit was refused but the report does not name the fragment (output: $OUT)"
fi

# 3. A fence whose check fails must block the commit (trigger matched).
d="$TMP/red-fence"; seed_repo "$d"
printf '%s' 'if git diff --cached --name-only | grep -q "^src/"; then
    echo "boom" >&2
    exit 1
fi
' >"$d/fences.d/10-red.sh"
if commit_in "$d" src/file.go; then
    fail "a failing fence let the commit through"
else
    pass "a failing fence refuses the commit"
fi

# 4. A fence whose trigger does not match must not block the commit.
d="$TMP/no-trigger"; seed_repo "$d"
printf '%s' 'if git diff --cached --name-only | grep -q "^src/"; then
    echo "boom" >&2
    exit 1
fi
' >"$d/fences.d/10-skip.sh"
if commit_in "$d" docs/file.md; then
    pass "an untriggered fence is skipped and the commit lands"
else
    fail "an untriggered fence blocked the commit (output: $OUT)"
fi

# 5. A lost fences.d/ (empty directory) must be loud, not silently green.
d="$TMP/no-fences"; seed_repo "$d"
if commit_in "$d" src/file.go; then
    fail "an empty fences.d/ let the commit through"
else
    pass "an empty fences.d/ refuses the commit"
fi

printf '\n%d case(s), %d failure(s)\n' "$cases" "$fails"
[ "$fails" -eq 0 ]
