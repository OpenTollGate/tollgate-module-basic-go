#!/usr/bin/env bash
# pr-verify.sh — the LOCAL CI gate for a PR ref.
#
# WHY THIS EXISTS (owner directive: local CI)
#   GitHub Actions is org-blocked (decommissioned 2026-08-27), and the live
#   ngit lane (.ngit/act/workflows/) fires only on maintainer-signed mirror
#   pushes — a PR branch never reaches the mirror, so a PR ref otherwise
#   gets ZERO CI runs. This script is the local replacement: it runs, on a
#   checkout of the PR head, the same offline suites the fleet verifies
#   with, and posts the results table as a PR comment so the review thread
#   carries the evidence.
#
# WHAT IT RUNS (the per-PR shape — never a weaker set)
#   hygiene                conflict-marker grep over the tracked tree
#   contract-fences        the tests/contract checks test.yml runs offline
#   packaging-sequential   tests/packaging/*_test.sh, one by one
#   shell-suites           tests/uci-defaults-*_test.sh + the two ngit
#                         pipeline suites (release-check.sh's shell set)
#   packaging-concurrent   the packaging suites ALL AT ONCE — the lane the
#                         shared-LOGFILE flake (#948) only shows under, and
#                         the reason suites must be per-run isolated
#   go-battery             make go-battery (gofmt/vet/build/-race in every
#                         Go module — the canonical pre-PR gate)
#
#   NOT covered here, on purpose (say so, never imply otherwise): the
#   spec-quote drift check (greatspectations install + pinned nuts fetch),
#   reproducibility and conformance (release-check territory), and every
#   router-visible behaviour (PRTA / bench lanes).
#
# Usage:
#   scripts/pr-verify.sh 933                    # PR mode: verify + comment
#   scripts/pr-verify.sh 933 --no-post          # verify, print table only
#   scripts/pr-verify.sh origin/main            # ref mode: verify + print
#   scripts/pr-verify.sh 933 --only packaging-sequential,shell-suites
#
# Options:
#   --no-post           do not post the PR comment (table still printed)
#   --only LEG[,LEG…]   run a subset of the six legs (re-running one red
#                       leg; a narrowed run says so in its table header)
#   --remove-logs       delete the log dir on exit (default: keep — the
#                       posted comment references it)
#
# PR mode pins the head SHA reported by gh at start-of-run, so a force-push
# mid-run cannot change what is verified. No secrets are read or embedded;
# PR mode uses the invoking account's gh auth for view/comment only.
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# ---------------------------------------------------------------------------
# Leg registry (the order the table shows). --only validates against this.
# ---------------------------------------------------------------------------
LEGS=(hygiene contract-fences packaging-sequential shell-suites packaging-concurrent go-battery)

usage() {
    echo "usage: $0 <PR-number | git-ref> [--no-post] [--only LEG[,LEG...]] [--remove-logs]" >&2
    echo "legs: ${LEGS[*]}" >&2
    exit 2
}

die() { echo "pr-verify: $*" >&2; exit 2; }

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------
TARGET=""
NO_POST=0
REMOVE_LOGS=0
ONLY=""

while [ $# -gt 0 ]; do
    case "$1" in
        --no-post)      NO_POST=1 ;;
        --remove-logs)  REMOVE_LOGS=1 ;;
        --only)
            [ $# -ge 2 ] || die "--only needs a comma-separated leg list"
            ONLY="$2"; shift ;;
        --only=*)       ONLY="${1#--only=}" ;;
        -h|--help)      usage ;;
        -*)             die "unknown option: $1" ;;
        *)
            [ -z "$TARGET" ] || die "one target only (got '$TARGET' and '$1')"
            TARGET="$1" ;;
    esac
    shift
done
[ -n "$TARGET" ] || usage

# Validate --only against the registry (a typo must refuse, not skip).
if [ -n "$ONLY" ]; then
    IFS=',' read -r -a wanted <<<"$ONLY"
    for w in "${wanted[@]}"; do
        ok=0
        for l in "${LEGS[@]}"; do [ "$w" = "$l" ] && ok=1; done
        [ "$ok" = 1 ] || die "unknown leg '$w' (legs: ${LEGS[*]})"
    done
else
    wanted=("${LEGS[@]}")
fi
want_leg() {
    local L; for L in "${wanted[@]}"; do [ "$L" = "$1" ] && return 0; done
    return 1
}

# ---------------------------------------------------------------------------
# Preflight
# ---------------------------------------------------------------------------
for c in git make go node python3; do
    command -v "$c" >/dev/null 2>&1 || die "$c is required but not on PATH"
done

SLUG="$(git -C "$ROOT" remote get-url origin 2>/dev/null \
    | sed -E 's#.*github\.com[:/]##; s#\.git$##')"
[ -n "$SLUG" ] || die "cannot derive the GitHub slug from the origin remote"

PR_NUMBER=""
PR_TITLE=""
PR_STATE=""
HEAD_REF=""
BASE_SHA=""

if [[ "$TARGET" =~ ^[0-9]+$ ]]; then
    command -v gh >/dev/null 2>&1 || die "PR mode needs gh (ref: $SLUG)"
    PR_NUMBER="$TARGET"
    # One gh call, TSV via gh's built-in jq — no separate jq dependency.
    # (No baseRefOid: not every gh version offers it; the base commit is
    # derived locally as the merge-base with origin/main instead.)
    meta="$(gh pr view "$PR_NUMBER" -R "$SLUG" \
        --json title,state,headRefName,headRefOid \
        --jq='[.title,.state,.headRefName,.headRefOid]|@tsv' \
        2>/dev/null)" || die "gh pr view $PR_NUMBER failed"
    IFS=$'\t' read -r PR_TITLE PR_STATE HEAD_REF HEAD_OID <<<"$meta"
    [ "$PR_STATE" = "OPEN" ] || die "PR #$PR_NUMBER is $PR_STATE — verify open PRs"
    git -C "$ROOT" fetch --quiet origin main 2>/dev/null || true
    BASE_SHA="$(git -C "$ROOT" merge-base "$HEAD_OID" origin/main 2>/dev/null || echo '?')"
    echo "== PR #$PR_NUMBER: $PR_TITLE"
    echo "== head $HEAD_REF @ ${HEAD_OID:0:10} (base ${BASE_SHA:0:10})"
else
    HEAD_REF="$TARGET"
    # A remote-tracking ref or local ref resolves without a fetch; anything
    # else is fetched from origin. FETCH_HEAD is captured immediately.
    if HEAD_OID="$(git -C "$ROOT" rev-parse --verify --quiet "$TARGET^{commit}")"; then
        echo "== ref $TARGET @ ${HEAD_OID:0:10} (already local)"
    else
        git -C "$ROOT" fetch --quiet origin "$TARGET" \
            || die "cannot fetch '$TARGET' from origin"
        HEAD_OID="$(git -C "$ROOT" rev-parse --verify FETCH_HEAD^{commit})" \
            || die "FETCH_HEAD did not resolve to a commit"
    fi
    BASE_SHA="$(git -C "$ROOT" rev-parse origin/main 2>/dev/null || echo "?")"
    echo "== ref $TARGET @ ${HEAD_OID:0:10}"
fi

# ---------------------------------------------------------------------------
# Worktree + logs
# ---------------------------------------------------------------------------
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
LOGDIR="/tmp/tollgate-pr-verify/${TARGET//-/_}-$STAMP"
mkdir -p "$LOGDIR"
WT="$LOGDIR/worktree"

cleanup() {
    git -C "$ROOT" worktree remove --force "$WT" >/dev/null 2>&1 || true
    git -C "$ROOT" worktree prune >/dev/null 2>&1 || true
    if [ "$REMOVE_LOGS" = 1 ]; then rm -rf "$LOGDIR"; else
        rm -rf "$WT" 2>/dev/null || true   # keep logs, drop the checkout
        echo "== logs kept in $LOGDIR"
    fi
}
trap cleanup EXIT

git -C "$ROOT" fetch --quiet origin "$HEAD_REF" 2>/dev/null || true
if ! git -C "$ROOT" rev-parse --verify --quiet "${HEAD_OID}^{commit}" >/dev/null; then
    # The PR head moved between view and fetch (force-push) — one retry.
    if [ -n "$PR_NUMBER" ]; then
        HEAD_OID="$(gh pr view "$PR_NUMBER" -R "$SLUG" --json headRefOid --jq=.headRefOid)"
        git -C "$ROOT" fetch --quiet origin "$HEAD_REF" || true
    fi
    git -C "$ROOT" rev-parse --verify --quiet "${HEAD_OID}^{commit}" >/dev/null \
        || die "head commit $HEAD_OID is not local after fetch — rerun"
fi
git -C "$ROOT" worktree add --quiet --detach "$WT" "$HEAD_OID" \
    || die "worktree add failed for $HEAD_OID"
echo "== worktree: $WT"

SCRIPT_ID="$(sha256sum "$ROOT/scripts/pr-verify.sh" 2>/dev/null | cut -c1-12)"
HOST="$(hostname -s 2>/dev/null || echo unknown)"

# ---------------------------------------------------------------------------
# Result recording
# ---------------------------------------------------------------------------
leg_names=() leg_status=() leg_detail=() leg_secs=()
FAILED_LEGS=()

record() { # record <leg> <PASS|FAIL|SKIP> <detail> <seconds>
    leg_names+=("$1"); leg_status+=("$2"); leg_detail+=("$3"); leg_secs+=("$4")
    [ "$2" = "FAIL" ] && FAILED_LEGS+=("$1")
}

# ---------------------------------------------------------------------------
# Legs (all run from the worktree root)
# ---------------------------------------------------------------------------
cd "$WT" || die "cannot cd into worktree"

run_hygiene() {
    echo "== leg: hygiene"
    local t=$SECONDS log="$LOGDIR/hygiene.log"
    # Match of the marker-plus-space shape is a failure (test.yml's hygiene
    # job; git grep exits 0 when it FINDS one).
    if git grep -nE '^(<{7} |>{7} )' -- . >"$log" 2>&1; then
        record hygiene FAIL "committed conflict markers — see hygiene.log" $((SECONDS - t))
    else
        record hygiene PASS "no conflict markers in the tracked tree" $((SECONDS - t))
    fi
}

run_contract_fences() {
    echo "== leg: contract-fences"
    local t=$SECONDS log="$LOGDIR/contract-fences.log"
    # name | owning file | command. A check whose owning file is absent from
    # the verified tree is SKIPped, not failed — fences land in PRs, and a
    # branch point before the fence cannot carry it (the entry-UI marker
    # fence arrives with #933, so main and earlier branches do not have it).
    local names=(
        "changelog fragments (assemble check)"
        "changelog fragments contract test"
        "schema lint (js-schema-lint)"
        "entry-UI marker path fence"
        "doc-facts contract"
        "build purity"
        "SSID format"
        "SSID naming contract"
        "dependency sync"
        "gonuts pin fence (offline identity)"
        "gonuts pin carriers"
        "mirror/relay sync"
        "stale import paths"
    )
    local files=(
        "scripts/changelog-assemble.py"
        "tests/contract/changelog-fragments_test.sh"
        "tests/contract/js-schema-lint.mjs"
        "tests/contract/check-entry-ui-marker-path.sh"
        "tests/contract/check-docs-facts.py"
        "tests/contract/build-purity.sh"
        "tests/contract/check-ssid-format.sh"
        "tests/contract/check-ssid-naming.sh"
        "tests/contract/check-deps-sync.py"
        "tests/contract/gonuts-pin_test.sh"
        "tests/contract/check-gonuts-pin.py"
        "tests/contract/check-mirror-sync.py"
        "tests/contract/check-import-paths.py"
    )
    local cmds=(
        "python3 scripts/changelog-assemble.py check"
        "bash tests/contract/changelog-fragments_test.sh"
        "node tests/contract/js-schema-lint.mjs"
        "bash tests/contract/check-entry-ui-marker-path.sh"
        "python3 tests/contract/check-docs-facts.py"
        "bash tests/contract/build-purity.sh"
        "bash tests/contract/check-ssid-format.sh"
        "bash tests/contract/check-ssid-naming.sh"
        "python3 tests/contract/check-deps-sync.py"
        "bash tests/contract/gonuts-pin_test.sh"
        "python3 tests/contract/check-gonuts-pin.py"
        "python3 tests/contract/check-mirror-sync.py"
        "python3 tests/contract/check-import-paths.py"
    )
    : >"$log"
    local red=() skipped=() ran=0 i
    for i in "${!names[@]}"; do
        if [ ! -f "${files[$i]}" ]; then
            skipped+=("${names[$i]}")
            echo "== ${names[$i]}: SKIP (absent from this tree)" >>"$log"
            continue
        fi
        ran=$((ran + 1))
        echo "== ${names[$i]}" >>"$log"
        if ! bash -c "${cmds[$i]}" >>"$log" 2>&1; then
            red+=("${names[$i]}")
        fi
    done
    local detail="${ran} checks green"
    [ "${#skipped[@]}" = 0 ] || detail+="; ${#skipped[@]} skipped (absent from tree: ${skipped[*]})"
    if [ "${#red[@]}" = 0 ]; then
        record contract-fences PASS "$detail" $((SECONDS - t))
    else
        record contract-fences FAIL "red: ${red[*]} — see contract-fences.log; $detail" $((SECONDS - t))
    fi
}

packaging_suites=()
shell_suites=()

discover_suites() {
    local shopt_state
    shopt_state="$(shopt -p nullglob)" || true
    shopt -s nullglob
    packaging_suites=(tests/packaging/*_test.sh)
    shell_suites=(tests/uci-defaults-*_test.sh tests/ngit-ci-trigger_test.sh tests/ngit-release-pipeline_test.sh)
    eval "$shopt_state"
}

run_packaging_sequential() {
    echo "== leg: packaging-sequential"
    local t=$SECONDS dir="$LOGDIR/packaging-sequential"
    mkdir -p "$dir"
    local red=() s n=0
    for s in "${packaging_suites[@]}"; do
        n=$((n + 1))
        if ! bash "$s" >"$dir/$(basename "$s").log" 2>&1; then
            red+=("$(basename "$s")")
            touch "$dir/$(basename "$s").failed"
        fi
    done
    if [ "${#red[@]}" = 0 ]; then
        record packaging-sequential PASS "$n suites" $((SECONDS - t))
    else
        record packaging-sequential FAIL "red (${#red[@]}/$n): ${red[*]} — logs in $dir" $((SECONDS - t))
    fi
}

run_shell_suites() {
    echo "== leg: shell-suites"
    local t=$SECONDS dir="$LOGDIR/shell-suites"
    mkdir -p "$dir"
    local red=() s n=0
    for s in "${shell_suites[@]}"; do
        n=$((n + 1))
        if ! bash "$s" >"$dir/$(basename "$s").log" 2>&1; then
            red+=("$(basename "$s")")
            touch "$dir/$(basename "$s").failed"
        fi
    done
    if [ "${#red[@]}" = 0 ]; then
        record shell-suites PASS "$n suites (uci-defaults + ngit pipeline)" $((SECONDS - t))
    else
        record shell-suites FAIL "red (${#red[@]}/$n): ${red[*]} — logs in $dir" $((SECONDS - t))
    fi
}

run_packaging_concurrent() {
    echo "== leg: packaging-concurrent"
    local t=$SECONDS dir="$LOGDIR/packaging-concurrent"
    mkdir -p "$dir"
    local pids=() files=() s
    for s in "${packaging_suites[@]}"; do
        files+=("$s")
        bash "$s" >"$dir/$(basename "$s").log" 2>&1 &
        pids+=($!)
    done
    local red=() i rc
    for i in "${!pids[@]}"; do
        wait "${pids[$i]}"; rc=$?
        if [ "$rc" != 0 ]; then
            red+=("$(basename "${files[$i]}")")
            touch "$dir/$(basename "${files[$i]}").failed"
        fi
    done
    if [ "${#red[@]}" = 0 ]; then
        record packaging-concurrent PASS "${#pids[@]} suites, all at once" $((SECONDS - t))
    else
        record packaging-concurrent FAIL "red (${#red[@]}/${#pids[@]}): ${red[*]} — logs in $dir" $((SECONDS - t))
    fi
}

run_go_battery() {
    echo "== leg: go-battery"
    local t=$SECONDS log="$LOGDIR/go-battery.log"
    local mods
    mods="$(find src -name go.mod | wc -l | tr -d ' ')"
    if make go-battery >"$log" 2>&1; then
        record go-battery PASS "$mods modules, gofmt/vet/build/-race" $((SECONDS - t))
    else
        record go-battery FAIL "$mods modules — see go-battery.log" $((SECONDS - t))
    fi
}

discover_suites
STARTED=$SECONDS
want_leg hygiene               && run_hygiene
want_leg contract-fences       && run_contract_fences
want_leg packaging-sequential  && run_packaging_sequential
want_leg shell-suites          && run_shell_suites
want_leg packaging-concurrent  && run_packaging_concurrent
want_leg go-battery            && run_go_battery
WALL=$((SECONDS - STARTED))

# ---------------------------------------------------------------------------
# Table + verdict
# ---------------------------------------------------------------------------
green=0; red=0
for st in "${leg_status[@]}"; do
    case "$st" in PASS) green=$((green + 1)) ;; FAIL) red=$((red + 1)) ;; esac
done
VERDICT="$green legs green"
[ "$red" = 0 ] || VERDICT="$red leg(s) RED — $green green"

echo
echo "=================================================="
printf '%-24s %-5s %-10s %s\n' "LEG" "RES" "SECS" "DETAIL"
printf '%-24s %-5s %-10s %s\n' "---" "---" "----" "------"
for i in "${!leg_names[@]}"; do
    printf '%-24s %-5s %-10s %s\n' \
        "${leg_names[$i]}" "${leg_status[$i]}" "${leg_secs[$i]}" "${leg_detail[$i]}"
done
echo "=================================================="
echo "VERDICT: $VERDICT (${WALL}s)"

# ---------------------------------------------------------------------------
# PR comment
# ---------------------------------------------------------------------------
compose_comment() {
    local body="$LOGDIR/comment.md"
    {
        if [ -n "$PR_NUMBER" ]; then
            echo "### Local CI gate — PR #${PR_NUMBER}: ${PR_TITLE}"
            echo
        else
            echo "### Local CI gate — ref \`${HEAD_REF}\`"
            echo
        fi
        echo "Run of \`scripts/pr-verify.sh\` (the local CI gate: GitHub Actions is"
        echo "org-blocked and the ngit lane fires only on maintainer mirror pushes,"
        echo "so a PR ref gets no CI runs — this comment is the offline verification"
        echo "evidence, produced on the pinned head SHA)."
        echo
        echo "- **Head:** \`${HEAD_REF}\` @ \`${HEAD_OID:0:10}\`"
        [ -n "$PR_NUMBER" ] && echo "- **Base:** \`main\` @ \`${BASE_SHA:0:10}\`"
        echo "- **Gate:** pr-verify \`$SCRIPT_ID\` on \`${HOST}\`, started $(date -u '+%Y-%m-%d %H:%M UTC'), wall ${WALL}s"
        echo "- **Logs:** \`${LOGDIR}\` (on the verifying host)"
        echo
        echo "| Leg | Result | Detail |"
        echo "| --- | --- | --- |"
        for i in "${!leg_names[@]}"; do
            echo "| ${leg_names[$i]} | **${leg_status[$i]}** | ${leg_detail[$i]} (${leg_secs[$i]}s) |"
        done
        echo
        echo "**Verdict: ${VERDICT}.**"
        echo
        # Concurrent-only reds with a green sequential run are the shared-
        # LOGFILE flake shape (#948) on trees that predate its merge — hint,
        # not verdict.
        local conc_red=0 seq_green=0 i
        for i in "${!leg_names[@]}"; do
            [ "${leg_names[$i]}" = "packaging-concurrent" ] && [ "${leg_status[$i]}" = "FAIL" ] && conc_red=1
            [ "${leg_names[$i]}" = "packaging-sequential" ] && [ "${leg_status[$i]}" = "PASS" ] && seq_green=1
        done
        if [ "$conc_red" = 1 ] && [ "$seq_green" = 1 ]; then
            echo "> Note: the concurrent packaging leg is red while the same suites"
            echo "> passed sequentially — the exact shape of the shared-LOGFILE flake"
            echo "> root-caused in #948 (present on trees that predate its merge)."
            echo "> Re-verify on a #948-based tree before treating it as PR-caused."
            echo
        fi
        if [ "${#wanted[@]}" != "${#LEGS[@]}" ]; then
            echo "> Note: narrowed run (--only) — legs not listed here did not run."
            echo
        fi
        echo "<details><summary>What this gate does <b>not</b> run (no vacuous green)</summary>"
        echo
        echo "- spec-quote drift check (greatspectations tool + pinned nuts fetch)"
        echo "- reproducibility / conformance lanes (\`make release-check\` territory)"
        echo "- router-visible behaviour (PRTA / bench — unit green ≠ router works)"
        echo
        echo "</details>"
        # Trees predating the NGIT_TRUST generalization carry the old trigger
        # suite, whose nsec-arg pubkey derivation fails on nak v0.16.2 — an
        # environmental red on this seat, not a PR-caused one. Detect the
        # signature so a re-run on an old-tree PR cannot be misread.
        if [ -f "$LOGDIR/shell-suites/ngit-ci-trigger_test.sh.log" ] \
            && grep -q 'invalid hex key' "$LOGDIR/shell-suites/ngit-ci-trigger_test.sh.log"; then
            echo
            echo "> Note: the ngit trigger suite red carries the nak \`key public\`"
            echo "> nsec-arg signature — the portability break the NGIT_TRUST"
            echo "> generalization fixed. Trees predating that change reproduce it"
            echo "> on this seat; it is environmental, not PR-caused."
        fi
        # Failing-leg tails for the thread, capped so the comment stays
        # readable. Suite dirs carry one .failed marker per red suite.
        local shown=0
        for i in "${!leg_names[@]}"; do
            [ "${leg_status[$i]}" = "FAIL" ] || continue
            local leg="${leg_names[$i]}" taillog=""
            case "$leg" in
                hygiene)              taillog="$LOGDIR/hygiene.log" ;;
                contract-fences)      taillog="$LOGDIR/contract-fences.log" ;;
                go-battery)           taillog="$LOGDIR/go-battery.log" ;;
                packaging-sequential) taillog="$LOGDIR/packaging-sequential" ;;
                shell-suites)         taillog="$LOGDIR/shell-suites" ;;
                packaging-concurrent) taillog="$LOGDIR/packaging-concurrent" ;;
            esac
            [ -n "$taillog" ] || continue
            echo
            if [ "$shown" = 0 ]; then
                echo "<details><summary>failing-leg log tails</summary>"
                shown=1
            fi
            echo
            echo "##### $leg — tail"
            echo '````'
            if [ -d "$taillog" ]; then
                local m
                for m in "$taillog"/*.failed; do
                    [ -e "$m" ] || continue
                    local lg="${m%.failed}.log"
                    echo "--- $(basename "$lg")"
                    tail -n 15 "$lg" 2>/dev/null || echo "(log missing)"
                done
            else
                tail -n 30 "$taillog"
            fi
            echo '````'
        done
        [ "$shown" = 1 ] && { echo; echo "</details>"; }
    } >"$body"
    echo "$body"
}

if [ -n "$PR_NUMBER" ] && [ "$NO_POST" = 0 ]; then
    BODY="$(compose_comment)"
    if URL="$(gh pr comment "$PR_NUMBER" -R "$SLUG" --body-file "$BODY")"; then
        echo "== posted: $URL"
    else
        echo "== FAILED to post comment (body kept at $BODY)" >&2
        red=$((red + 1))
    fi
elif [ -n "$PR_NUMBER" ]; then
    echo "== --no-post: comment body kept at $(compose_comment)"
fi

[ "$red" = 0 ] && exit 0 || exit 1
