#!/usr/bin/env bash
# ngit-commit-epoch.sh — the committer time of a commit, as a unix epoch.
#
# WHY THIS IS NOT `git log -1 --format=%ct`
#   ngit-ci checks a commit out into an act job WITHOUT git metadata: inside the
#   job `git rev-parse` reports "not a git repository". packaging/build-env.sh
#   requires SOURCE_DATE_EPOCH for a reproducible package build and derives it
#   from `git log -1 --format=%ct HEAD` when the environment does not supply it,
#   so the ngit release lane needs another way to state it. That gap is what
#   turned build-portal red once the reproducible-build requirement (#383)
#   landed: the ngit lane is not exercised by GitHub CI, so nothing noticed.
#
#   A commit's time is a property of the commit, not of the runner, so the
#   answer is fetched from the git transport the runner is already building
#   from — a depth-1 fetch of exactly that commit. No clock is read, so two runs
#   at the same commit get the same epoch, which is the whole point.
#
# Usage: scripts/ngit-commit-epoch.sh <commit-ish> [<git-url>]
#
# Env:
#   NGIT_MIRROR_URL  git URL to fall back to (default: this repository's ngit
#                    mirror over https). The GitHub twin is not used as a
#                    default because a branch that has not merged yet is not
#                    fetchable from there.
#
# Exit codes:
#   0  printed the epoch
#   1  the epoch could not be determined (no local history, unreachable mirror,
#      or the commit is not served) — never a guessed timestamp, because a wrong
#      SOURCE_DATE_EPOCH silently produces a differently-built artifact

set -euo pipefail

SHA=${1:?usage: ngit-commit-epoch.sh <commit-ish> [<git-url>]}
DEFAULT_URL="https://relay.ngit.dev/npub1nng5mxkdh2mu593twukfr7j3fk5wxfy0v8ujf0e5g8nwwtzlphhqksqpew/tollgate-module-basic-go.git"
URL=${2:-${NGIT_MIRROR_URL:-$DEFAULT_URL}}

ROOT=${WT:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}

# 1. A normal checkout — the local history is the cheapest and most exact answer.
if epoch=$(git -C "$ROOT" log -1 --format=%ct "$SHA" 2>/dev/null) && [ -n "$epoch" ]; then
  printf '%s\n' "$epoch"
  exit 0
fi

# 2. An act job: no history. Fetch that one commit from the mirror the release
#    is being built from.
command -v git >/dev/null 2>&1 || { echo "ERROR: git is not available" >&2; exit 1; }
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
git init -q "$TMP"
if git -C "$TMP" fetch -q --depth=1 "$URL" "$SHA" 2>/dev/null; then
  epoch=$(git -C "$TMP" log -1 --format=%ct FETCH_HEAD)
  printf '%s\n' "$epoch"
  exit 0
fi

echo "ERROR: cannot determine the commit time of '$SHA'." >&2
echo "       No local history in $ROOT, and $URL did not serve that commit." >&2
echo "       Set NGIT_MIRROR_URL to a remote that has it, or export SOURCE_DATE_EPOCH." >&2
exit 1
