#!/usr/bin/env bash
# pr-conflict-sweep: report every open PR that conflicts with main.
#
# The queue's silent rot is the conflicting PR nobody looks at (a blocker
# that cannot merge is worse than one waiting in line — #899 sat unnoticed
# through a whole session). This sweep makes the drift visible in one
# command; it changes nothing, pushes nothing, rebases nothing.
#
# Usage:
#   scripts/pr-conflict-sweep.sh                 # human-readable
#   scripts/pr-conflict-sweep.sh --markdown      # table for an issue comment
#   scripts/pr-conflict-sweep.sh --remote origin # use a different remote
#
# Requires: gh (authenticated), git, network for the PR list; the conflict
# test itself is local (merge-tree against <remote>/main — no worktrees, no
# checkouts, nothing written to the current branch).
#
# WHY THE REMOTE IS A KNOB, NOT A CONSTANT
#
# The PR head ref `pull/<N>/head` is served by the repo that HOSTS the PR, and
# only by that repo. In a common local layout `origin` is a personal FORK and
# `upstream` is OpenTollGate; fetching `origin pull/<N>/head` there fails for
# every PR, and a fork's `main` also trails upstream's. Hardcoding `origin`
# therefore made this sweep answer "all merge-clean" on a checkout where it
# could not read a single head — a false all-clear, which is precisely the
# blind spot the sweep exists to remove.
#
# So: pick the remote deliberately, and NEVER print a success sentence over
# PRs that were not actually examined. Unfetchable heads are counted, named,
# and make the run exit non-zero.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

MODE="text"
# Default to upstream: the PR-hosting repo. `origin` is a fork in the layout
# this script was written in, and a fork serves no pull/ refs at all.
REMOTE="${CONFLICT_SWEEP_REMOTE:-upstream}"

while [ $# -gt 0 ]; do
    case "$1" in
        --markdown) MODE="markdown"; shift ;;
        --remote) REMOTE="${2:?--remote needs a value}"; shift 2 ;;
        -h|--help)
            echo "usage: $0 [--markdown] [--remote <git-remote>]" >&2
            exit 0 ;;
        *) echo "usage: $0 [--markdown] [--remote <git-remote>]" >&2; exit 2 ;;
    esac
done

if ! git remote get-url "$REMOTE" >/dev/null 2>&1; then
    echo "pr-conflict-sweep: no such remote '$REMOTE' (try --remote origin, or --remote upstream)" >&2
    exit 2
fi

git fetch "$REMOTE" main --quiet
MAIN=$(git rev-parse "$REMOTE/main")

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# PR list: number, title, head sha
gh pr list -R OpenTollGate/tollgate-module-basic-go --state open --limit 100 \
    --json number,title,headRefOid --jq '.[] | [.number, .title, .headRefOid] | @tsv' > "$TMP/prs.tsv"

conflicting=0
total=0
examined=0
unfetchable=0
unenumerated=""

while IFS=$'\t' read -r num title sha; do
    total=$((total+1))
    # Fetch the PR head into a temp ref (never a local branch name space).
    # A failure here is NOT a "no conflict" answer — it is an unexamined PR.
    if ! git fetch --quiet "$REMOTE" "pull/$num/head" 2>/dev/null; then
        unfetchable=$((unfetchable+1))
        unenumerated="$unenumerated $num"
        continue
    fi
    examined=$((examined+1))
    head=$(git rev-parse FETCH_HEAD)
    # grep exits 1 when a PR is conflict-free — that is the common case, not a failure
    files=$(git merge-tree --write-tree "$MAIN" "$head" 2>/dev/null | grep '^CONFLICT' | sed 's/.*in //' | sort -u | paste -sd, - || true)
    if [ -n "$files" ]; then
        conflicting=$((conflicting+1))
        if [ "$MODE" = "markdown" ]; then
            rows+="| #$num | ${title:0:60} | \`$files\` |
"
        else
            printf 'CONFLICTS  #%-4s %s\n           files: %s\n' "$num" "${title:0:60}" "$files"
        fi
    fi
done < "$TMP/prs.tsv"

if [ "$MODE" = "markdown" ]; then
    echo "## PR conflict sweep — $(date -u +%FT%TZ)"
    echo
    echo "Open PRs: $total · examined: $examined · conflicting with \`$REMOTE/main\`: **$conflicting**"
    if [ "$unfetchable" -gt 0 ]; then
        echo
        echo "> **$unfetchable head(s) unfetchable from \`$REMOTE\`** — these were NOT examined and are"
        echo "> excluded from the count:$unenumerated"
        echo "> If that is every PR, \`$REMOTE\` is not the PR-hosting repo; retry with \`--remote upstream\`."
    fi
    echo
    if [ "$conflicting" -gt 0 ]; then
        echo "| PR | Title | Conflicting files |"
        echo "|---|---|---|"
        printf '%s' "$rows"
    elif [ "$examined" -eq 0 ]; then
        echo "_Nothing examined — no all-clear can be given._"
    else
        echo "_No conflicts — the examined queue is merge-clean._"
    fi
else
    if [ "$unfetchable" -gt 0 ]; then
        printf 'UNEXAMINED %s PR head(s) unfetchable from %s:%s\n' \
            "$unfetchable" "$REMOTE" "$unenumerated"
        echo "  (if that is every PR, '$REMOTE' is not the PR-hosting repo — retry --remote upstream)"
    fi
    if [ "$examined" -eq 0 ]; then
        echo "no PRs examined: 0 of $total heads could be read — refusing to report an all-clear"
        exit 1
    fi
    if [ "$conflicting" = 0 ]; then
        echo "no conflicts: $examined of $total open PRs examined, all merge-clean vs $REMOTE/main"
    else
        echo "conflicts: $conflicting of $examined examined open PRs conflict with $REMOTE/main"
    fi
fi

# A sweep that could not read part of the queue is not a clean sweep.
if [ "$unfetchable" -gt 0 ]; then
    exit 1
fi
