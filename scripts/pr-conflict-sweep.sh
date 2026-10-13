#!/usr/bin/env bash
# pr-conflict-sweep: report every open PR that conflicts with main.
#
# The queue's silent rot is the conflicting PR nobody looks at (a blocker
# that cannot merge is worse than one waiting in line — #899 sat unnoticed
# through a whole session). This sweep makes the drift visible in one
# command; it changes nothing, pushes nothing, rebases nothing.
#
# Usage:
#   scripts/pr-conflict-sweep.sh              # human-readable
#   scripts/pr-conflict-sweep.sh --markdown   # table for an issue comment
#
# Requires: gh (authenticated), git, network for the PR list; the conflict
# test itself is local (merge-tree against origin/main — no worktrees, no
# checkouts, nothing written to the current branch).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

MODE="text"
[ "${1:-}" = "--markdown" ] && MODE="markdown"

git fetch origin main --quiet
MAIN=$(git rev-parse origin/main)

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# PR list: number, title, head sha
gh pr list -R OpenTollGate/tollgate-module-basic-go --state open --limit 100 \
    --json number,title,headRefOid --jq '.[] | [.number, .title, .headRefOid] | @tsv' > "$TMP/prs.tsv"

conflicting=0
total=0
rows=""

while IFS=$'\t' read -r num title sha; do
    total=$((total+1))
    # Fetch the PR head into a temp ref (never a local branch name space)
    if ! git fetch --quiet origin "pull/$num/head" 2>/dev/null; then
        continue
    fi
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
    echo "Open PRs: $total · conflicting with \`origin/main\`: **$conflicting**"
    echo
    if [ "$conflicting" -gt 0 ]; then
        echo "| PR | Title | Conflicting files |"
        echo "|---|---|---|"
        printf '%s' "$rows"
    else
        echo "_No conflicts — the queue is merge-clean._"
    fi
else
    [ "$conflicting" = 0 ] && echo "no conflicts: $total open PRs are all merge-clean vs origin/main"
fi
