#!/usr/bin/env bash
# negative-control.sh — drift detection for the contract suites themselves.
#
# A contract suite is only as good as its failure mode: a suite that still
# PASSES against the broken tree it claims to guard has decorative
# assertions (AGENTS.md, "Where each class of verification lives"). This
# runner proves each suite bites:
#
#   1. create a throwaway worktree at a BROKEN git ref (default: the
#      v0.6.0-rc1 tag — the bench-verified-broken stop-ship cluster),
#   2. overlay the CURRENT suites onto it,
#   3. run every suite against the broken tree,
#   4. expect FAILURES. A suite that passes on the broken ref is reported
#      as DECORATIVE — that is the drift this runner exists to catch.
#
# Usage:
#   scripts/negative-control.sh                       # broken = v0.6.0-rc1, suites = ./tests/contract
#   scripts/negative-control.sh --broken <git-ref>    # any tag/branch/SHA
#   scripts/negative-control.sh --suites <dir>        # e.g. a PR worktree's tests/contract
#
# Example (proving the stop-ship train's suites bite on rc1):
#   git fetch origin pull/786/head:train && git worktree add /tmp/train train
#   scripts/negative-control.sh --suites /tmp/train/tests/contract
#
# Exits nonzero if any suite is decorative or errors; a summary table ends
# the run. Run per release, and whenever a suite or its guarded file changes.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BROKEN="v0.6.0-rc1"
SUITES="$ROOT/tests/contract"
KEEP=0

while [ $# -gt 0 ]; do
  case "$1" in
    --broken) BROKEN="$2"; shift 2 ;;
    --suites) SUITES="$2"; shift 2 ;;
    --keep)   KEEP=1; shift ;;
    *) echo "usage: $0 [--broken <ref>] [--suites <dir>] [--keep]" >&2; exit 2 ;;
  esac
done

if ! git -C "$ROOT" rev-parse --verify --quiet "$BROKEN^{commit}" >/dev/null; then
  echo "negative-control: broken ref '$BROKEN' not found in $ROOT" >&2
  exit 2
fi
[ -d "$SUITES" ] || { echo "negative-control: suites dir '$SUITES' not found" >&2; exit 2; }

TMPROOT="$(mktemp -d /tmp/negative-control.XXXXXX)"
git -C "$ROOT" worktree prune 2>/dev/null || true
trap '[ "$KEEP" = 1 ] || rm -rf "$TMPROOT"; git -C "$ROOT" worktree list --porcelain >/dev/null' EXIT
WT="$TMPROOT/broken"
git -C "$ROOT" worktree add --quiet --detach "$WT" "$BROKEN" || {
  echo "negative-control: cannot create worktree at $BROKEN" >&2; exit 2; }

declare -a NAME RESULT DETAIL
n=0
total=0; bite=0; decorative=0; errored=0

for suite in "$SUITES"/*.sh; do
  name="$(basename "$suite")"
  [ -x "$suite" ] || continue
  total=$((total + 1))
  # fresh broken tree per suite: a suite may write into its tree
  git -C "$WT" checkout --quiet -- . 2>/dev/null || true
  git -C "$WT" clean --quiet -fd 2>/dev/null || true
  mkdir -p "$WT/tests/contract"
  cp "$suite" "$WT/tests/contract/$name"
  out="$(cd "$WT" && timeout 120 bash "tests/contract/$name" 2>&1)"
  rc=$?
  if [ $rc -eq 0 ]; then
    RESULT[$n]="PASS-ON-BROKEN"; decorative=$((decorative + 1))
    DETAIL[$n]="passes on the broken ref — either decorative, or its fix predates the broken ref (verify which)"
  elif [ $rc -eq 124 ]; then
    RESULT[$n]="TIMEOUT"; errored=$((errored + 1))
    DETAIL[$n]="runner timed out on the broken tree — inspect manually"
  else
    RESULT[$n]="BITES"; bite=$((bite + 1))
    failline="$(echo "$out" | grep -m1 -E 'FAIL' | head -c 100)"
    DETAIL[$n]="fails on the broken ref as designed${failline:+ — $failline}"
  fi
  NAME[$n]="$name"
  n=$((n + 1))
done

git -C "$ROOT" worktree remove --force "$WT" >/dev/null 2>&1

echo
echo "negative-control: broken ref $BROKEN, suites from $SUITES"
echo "------------------------------------------------------------------"
for i in $(seq 0 $((n - 1))); do
  printf '%-10s %s\n           %s\n' "${RESULT[$i]}" "${NAME[$i]}" "${DETAIL[$i]}"
done
echo "------------------------------------------------------------------"
echo "$bite/$total suites bite, $decorative pass-on-broken, $errored errored"

if [ "$KEEP" = 1 ]; then echo "kept work tree: $TMPROOT (remove by hand)"; fi
[ "$decorative" -eq 0 ] && [ "$errored" -eq 0 ]
