#!/usr/bin/env bash
# Contract test: scripts/assemble-ci.py verdicts (#920).
#
# The Test workflow twins (.github/workflows/test.yml and its .ngit/act
# copy) are rendered from ci.d/ job fragments by one generator. A fence
# that cannot fail is decoration, so every drift shape is planted in a
# fixture tree and the generator must refuse it BY NAME, then a clean
# tree must pass:
#
#   - a hand-edit to a rendered copy              -> --check refuses
#   - a fragment changed without re-rendering     -> --check refuses
#   - a duplicate job id in one lane              -> refused, both files named
#   - a typo'd lane directory (ci.d/bothh/)       -> refused, not silently
#                                                   dropped from a lane
#   - a fragment that is not a job mapping        -> refused
#   - per-lane variants of one job whose
#     strategy.matrix diverges                    -> refused, both named
#   - two fragments sharing an order prefix       -> refused
#
# Offline only. Run from anywhere:  bash tests/contract/ci-fragments_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
GEN="$ROOT/scripts/assemble-ci.py"

cases=0
fails=0
pass() { cases=$((cases + 1)); printf 'ok   %s\n' "$1"; }
fail() { cases=$((cases + 1)); fails=$((fails + 1)); printf 'FAIL %s\n' "$1" >&2; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

verdict() { # verdict <root> <want:accept|refuse> <label> [needle]
    local r="$1" want="$2" label="$3" needle="${4:-}" out rc
    out="$(python3 "$GEN" --root "$r" --check 2>&1)"; rc=$?
    if [ "$want" = accept ] && [ "$rc" -eq 0 ]; then pass "$label"; return; fi
    if [ "$want" = refuse ] && [ "$rc" -ne 0 ]; then
        if [ -z "$needle" ] || printf '%s' "$out" | grep -qF -- "$needle"; then
            pass "$label"
        else
            fail "$label (refused, but the report does not name '$needle')"
        fi
        return
    fi
    fail "$label (rc=$rc, want $want; output: $(printf '%s' "$out" | tail -3 | tr '\n' ' '))"
}

ONE_JOB='  fixture-job:
    runs-on: ubuntu-latest
    steps:
      - run: echo ok
'

seed() { # seed <root>: a minimal clean fragment tree, rendered
    mkdir -p "$1/ci.d/both"
    printf '%s' "$ONE_JOB" >"$1/ci.d/both/10-fixture-job.yml"
    python3 "$GEN" --root "$1" >/dev/null 2>&1
}

# --- the real tree is clean and lists both targets ----------------------------

if python3 "$GEN" --check; then
    pass "the repo's rendered twins match ci.d/ fragments"
else
    fail "the repo's rendered twins have drifted from ci.d/ fragments"
fi
python3 "$GEN" --list | grep -q '^github -> \.github/workflows/test\.yml$' \
    && python3 "$GEN" --list | grep -q '^ngit -> \.ngit/act/workflows/test\.yml$' \
    && pass "--list names both render targets" \
    || fail "--list does not name both render targets"

# --- 1. clean fixture passes ----------------------------------------------------

d="$TMP/clean"; seed "$d"
verdict "$d" accept "a clean rendered fixture passes --check"

# --- 2. a hand-edit to a rendered copy is refused and named ---------------------

d="$TMP/hand-edit"; seed "$d"
printf '      - run: echo sneaky\n' >>"$d/.github/workflows/test.yml"
verdict "$d" refuse "a hand-edit to .github/workflows/test.yml" \
    ".github/workflows/test.yml: DRIFTED"

d="$TMP/hand-edit-ng"; seed "$d"
printf '      - run: echo sneaky\n' >>"$d/.ngit/act/workflows/test.yml"
verdict "$d" refuse "a hand-edit to the ngit twin" \
    ".ngit/act/workflows/test.yml: DRIFTED"

# --- 3. a fragment changed without re-rendering is refused ---------------------

d="$TMP/stale-fragment"; seed "$d"
printf '      - run: echo added\n' >>"$d/ci.d/both/10-fixture-job.yml"
verdict "$d" refuse "a fragment changed without re-rendering" "DRIFTED"

# --- 4. a duplicate job id in one lane names both files ------------------------

d="$TMP/dup-job"; seed "$d"
mkdir -p "$d/ci.d/github"
printf '%s' "$ONE_JOB" >"$d/ci.d/github/20-fixture-job.yml"
verdict "$d" refuse "a duplicate job id in one lane" "already declared by"

# --- 5. a typo'd lane directory is refused, not silently ignored ----------------

d="$TMP/typo-dir"; seed "$d"
mkdir -p "$d/ci.d/bothh"
printf '%s' "$ONE_JOB" >"$d/ci.d/bothh/30-fixture-job.yml"
verdict "$d" refuse "a typo'd lane directory" "not a lane directory"

# --- 6. a fragment that is not a job mapping is refused ------------------------

d="$TMP/not-a-job"; seed "$d"
mkdir -p "$d/ci.d/github"
printf 'runs-on: ubuntu-latest\n' >"$d/ci.d/github/20-not-a-job.yml"
verdict "$d" refuse "a fragment whose body is not a job mapping" "first non-comment line"

# --- 7. per-lane variants with diverging matrices are refused -------------------

MATRIX_JOB='  m:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        module:
          - src/a
          - src/b
    steps:
      - run: echo hi
'
d="$TMP/matrix-drift"; mkdir -p "$d/ci.d/github" "$d/ci.d/ngit"
printf '%s' "$MATRIX_JOB" >"$d/ci.d/github/10-m.yml"
printf '%s' "$MATRIX_JOB" | sed 's|- src/b$|- src/c|' >"$d/ci.d/ngit/10-m.yml"
python3 "$GEN" --root "$d" >/dev/null 2>&1
verdict "$d" refuse "per-lane variants of one job with a diverging matrix" "diverging"

# --- 8. two fragments sharing an order prefix are refused ----------------------

d="$TMP/prefix-clash"; seed "$d"
mkdir -p "$d/ci.d/github"
printf '%s' "$ONE_JOB" | sed 's/fixture-job/another-job/' >"$d/ci.d/github/10-another.yml"
verdict "$d" refuse "two fragments sharing an order prefix" "order prefix"

printf '\n%d case(s), %d failure(s)\n' "$cases" "$fails"
[ "$fails" -eq 0 ]
