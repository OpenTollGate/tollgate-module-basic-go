#!/usr/bin/env bash
# run-seal-vetting.sh — reproduce the seal-vetting findings against upstream.
#
# WHAT THIS DOES
#
# Clones the upstream lnurlcash wallet at the exact commit we reviewed, installs
# its dependencies, drops our adversarial test into its own test tree, and runs it
# with the project's own vitest. The findings are therefore reproduced against the
# real modules with the real harness -- not against a reimplementation in a
# scratch directory.
#
# WHY IT MATTERS
#
# This is the difference between "we read the code and think X" and "here is X
# failing in the author's own test framework". The seal's central claim is that a
# consignment can be validated offline; the tests below show what that validation
# does and does not actually check.
#
# WHAT IT DOES NOT DO
#
# It does not modify upstream, does not push anywhere, and writes only inside its
# own scratch directory. It is a read-only evaluation of a pinned revision.
#
# USAGE
#   bash run-seal-vetting.sh [workdir]      # default: ~/seal-vetting-work
#
# EXIT: 0 if all tests pass as expected (i.e. the findings reproduce).

set -euo pipefail

UPSTREAM="https://github.com/lnurlcash/lnurl-wallet"
# Pinned: the revision this analysis was performed against. Pin it, because a
# conclusion about "the seal" is only meaningful for a specific revision -- and
# the author may fix these findings, which would make an unpinned re-run disagree
# for the right reason.
PIN="f1cdb9e59ca573f228fae7e63bf509c9a2afd5eb"

HERE="$(cd "$(dirname "$0")" && pwd)"
WORK="${1:-$HOME/seal-vetting-work}"
SRC="$WORK/lnurl-wallet"

echo "=== seal vetting: reproduce adversarial findings ==="
echo "  upstream : $UPSTREAM"
echo "  pinned   : $PIN"
echo "  workdir  : $WORK"
echo

mkdir -p "$WORK"

if [ ! -d "$SRC/.git" ]; then
  echo "--- cloning ---"
  git clone "$UPSTREAM" "$SRC"
fi

echo "--- checking out the pinned revision ---"
git -C "$SRC" fetch --quiet origin "$PIN" 2>/dev/null || true
git -C "$SRC" checkout --quiet "$PIN"

ACTUAL="$(git -C "$SRC" rev-parse HEAD)"
if [ "$ACTUAL" != "$PIN" ]; then
  echo "FATAL: could not check out $PIN (at $ACTUAL)" >&2
  exit 1
fi
echo "  at $ACTUAL"

echo "--- installing dependencies (may take a minute) ---"
if [ ! -d "$SRC/node_modules" ]; then
  ( cd "$SRC" && npm ci --no-audit --no-fund )
else
  echo "  node_modules present, skipping"
fi

# Install our test into the project's own test tree so it runs under the same
# vitest config, aliases and transform pipeline as the project's own tests.
echo "--- installing the adversarial test ---"
cp "$HERE/seal_adversarial.test.ts" "$SRC/src/__seal_adversarial.test.ts"

echo
echo "--- running (upstream's own vitest) ---"
# Capture to a log next to this script, so a run leaves evidence behind rather
# than only a terminal scrollback.
LOG="$HERE/out/run-$(date +%Y%m%d-%H%M%S).txt"
mkdir -p "$HERE/out"
set +e
( cd "$SRC" && npx vitest run src/__seal_adversarial.test.ts --reporter=verbose ) \
  2>&1 | tee "$LOG"
STATUS="${PIPESTATUS[0]}"
set -e

echo
echo "=== done (vitest exit $STATUS) ==="
echo "    evidence: $LOG"
echo "    expected: $HERE/out/GREEN.txt"
echo "    A differing result means either the pin moved or upstream fixed a finding."

# Exit non-zero if the test run itself failed, so this is usable in CI or a lane.
exit "$STATUS"
