#!/usr/bin/env bash
# Contract test: tests/contract/check-changelog-fragments.py verdicts.
#
# The assemble tool (scripts/changelog-assemble.py check) validates a
# fragment's shape; this pins the IDENTITY fence that sits next to it:
# every fragment must name its own change — a real PR/issue number in the
# filename, that same number referenced in the body, no links to foreign
# PRs, no placeholder text. Each rule exists because a shape-valid
# fragment was still untraceable (vm-campaign.added.md, PR #704's entry,
# carried no number at all).
#
# A fence that cannot fail is decoration, so every case here plants the
# drift it is named for and requires the checker to refuse it — and a
# clean tree must pass. Offline only: the --online leg is CI's.
#
# Run from anywhere:  bash tests/contract/changelog-fragment-numbers_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CHECK="$ROOT/tests/contract/check-changelog-fragments.py"

cases=0
fails=0
pass() { cases=$((cases + 1)); printf 'ok   %s\n' "$1"; }
fail() { cases=$((cases + 1)); fails=$((fails + 1)); printf 'FAIL %s\n' "$1" >&2; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

new_tree() { # new_tree <dir>
    mkdir -p "$1/changelog.d"
}

frag() { # frag <dir> <filename> <body>
    printf '%s\n' "$3" >"$1/changelog.d/$2"
}

# check <dir> -> exit status of the checker
check() { python3 "$CHECK" --root "$1" >/dev/null 2>&1; }

# require_refusal <dir> <label> <needle>: checker must exit non-zero AND name it
require_refusal() {
    local d="$1" label="$2" needle="$3" out
    out="$(python3 "$CHECK" --root "$d" 2>&1)"
    if [ "$?" -ne 0 ]; then
        if printf '%s' "$out" | grep -qF -- "$needle"; then
            pass "$label"
        else
            fail "$label (refused, but the report does not name '$needle': $(printf '%s' "$out" | head -1))"
        fi
    else
        fail "$label (accepted — the fence is not holding)"
    fi
}

# --- the live shapes must pass ----------------------------------------------

d="$TMP/good-link"; new_tree "$d"
frag "$d" 739-brand.fixed.md '- **Fixed.** A body ending in the PR link
  ([#739](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/739)).'
check "$d" && pass "a fragment whose body links its own PR" \
    || fail "a fragment whose body links its own PR"

d="$TMP/good-issue"; new_tree "$d"
frag "$d" 502-journal.fixed.md '- **Fixed.** The tracking issue named bare (#502,
  two red rows), which is the identity when there is no PR link yet.'
check "$d" && pass "a fragment referencing its own ISSUE number bare" \
    || fail "a fragment referencing its own ISSUE number bare"

d="$TMP/good-foreign-bare"; new_tree "$d"
frag "$d" 612-fee.fixed.md '- **Fixed.** Builds on the earlier swap-fee work
  (#403) and lands the precheck. ([#612](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/612)).'
check "$d" && pass "bare mentions of other numbers stay free prose" \
    || fail "bare mentions of other numbers stay free prose"

d="$TMP/readme-only"; new_tree "$d"
printf 'instructions\n' >"$d/changelog.d/README.md"
check "$d" && pass "an empty directory (README only) passes" \
    || fail "an empty directory (README only) passes"

# --- the drift shapes must be refused, and named ----------------------------

d="$TMP/no-number"; new_tree "$d"
frag "$d" vm-campaign.added.md '- **Added.** A lane exists, says no number at all.'
require_refusal "$d" "a filename without a number" "vm-campaign.added.md"

d="$TMP/body-silent"; new_tree "$d"
frag "$d" 704-vm-campaign.added.md '- **Added.** The lane exists and the file is
  numbered, but the body never says which change it is.'
require_refusal "$d" "a body that never references its own number" "#704"

d="$TMP/foreign-link"; new_tree "$d"
frag "$d" 705-thing.fixed.md '- **Fixed.** Something that cites the research PR
  as if it were this change
  ([#631](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/631)).'
require_refusal "$d" "a link to a foreign PR (the #631 class)" "#631"

d="$TMP/label-mismatch"; new_tree "$d"
frag "$d" 708-thing.fixed.md '- **Fixed.** A hand-edited link whose label and
  target disagree ([#708](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/709)).'
require_refusal "$d" "a link whose label and target disagree" "709"

d="$TMP/placeholder"; new_tree "$d"
frag "$d" 711-thing.added.md '- **Added.** Number to be filled in later, TBD.'
require_refusal "$d" "placeholder text (TBD)" "TBD"

d="$TMP/placeholder-template"; new_tree "$d"
frag "$d" 712-thing.added.md '- **Added.** See <pr> for the discussion.'
require_refusal "$d" "placeholder text (a template token)" "<pr>"

d="$TMP/zero"; new_tree "$d"
frag "$d" 0-wip.internal.md '- **Internal.** A zero-numbered template file.'
require_refusal "$d" "a 0- placeholder number" "0-wip"

# --- verdict -----------------------------------------------------------------

printf '\n%s cases, %s failed\n' "$cases" "$fails"
[ "$fails" = 0 ] || exit 1
exit 0
