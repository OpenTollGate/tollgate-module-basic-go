#!/usr/bin/env bash
# Publish a ngit-ci Manual Trigger (kind 9840) for this repository's Nostr
# mirror, signed by the repository maintainer.
#
# WHY THIS EXISTS
#   GitHub Actions is disabled org-wide, so `.github/workflows/` never runs and
#   there is no `workflow_dispatch` button for the release pipeline. The
#   ngit-ci coordinator starts a normal push-triggered workflow by itself, but
#   the second stage of the release pipeline (`.ngit/act/workflows/build-package.yml`)
#   is only declared for `main` and `v*`, so on any other ref it has to be
#   started by hand. That is this script: the hand-runnable publish path.
#
#   It is also the documented replacement for the GitHub twin's
#   `peter-evans/repository-dispatch` step, which dispatched into
#   OpenTollGate/tollgate-os with a cross-repo token that no longer exists.
#
# NIP-C1 shape (ngit-ci NIP.md, "Manual Trigger"):
#   kind 9840, empty content, exactly one `p` tag naming the coordinator,
#   `a` the repository announcement, `c` the commit to run, `w` the workflow
#   path plus the SHA-256 of that file's content, optional `r` for the ref.
#
# nak quirk: a multi-value tag is written with ';' as the separator, not a
# space (verified 2026-09-12) -- so the `w` tag is "<path>;<sha256>".
#
# Usage: scripts/ngit-ci-trigger.sh <workflow-path> <commit> [ref]
#
# Environment overrides (used by tests/ngit-ci-trigger_test.sh):
#   WT         repository root (default: the git root of this script)
#   KEYFILE    file containing the maintainer nsec (default below)
#   COORD_HEX  coordinator pubkey to name in the `p` tag
#   MAINT_HEX  maintainer pubkey the signing key must resolve to
#   RELAYS     relays to publish to
#
# The signing key is never printed, echoed or passed on a command line that
# this script logs; it is read from KEYFILE and handed to `nak` via --sec.

set -euo pipefail

WT=${WT:-$(git -C "$(dirname -- "$0")" rev-parse --show-toplevel)}
KEYFILE=${KEYFILE:-${HOME}/.hermes/.ngit-new-key}
COORD_HEX=${COORD_HEX:-765cd47badcbbc4a38c7d0c57d5607663b484c20cd59773f9f7064487f9431e8}
MAINT_HEX=${MAINT_HEX:-36bdeb239fbebe0ef4f9d50e92ae1583cda2a9c81df015ba91a1fde726dc74e0}
REPO_ID=${REPO_ID:-tollgate-module-basic-go}
RELAYS=${RELAYS:-"wss://relay.ngit.dev wss://gitnostr.com"}

WORKFLOW=${1:?usage: ngit-ci-trigger.sh <workflow-path> <commit> [ref]}
COMMIT=${2:?usage: ngit-ci-trigger.sh <workflow-path> <commit> [ref]}
REF=${3:-}

cd "$WT"

case "$WORKFLOW" in
  *.ngit/act/workflows/*.yml|.ngit/act/workflows/*.yml) ;;
  *) echo "ERROR: workflow must be a .ngit/act/workflows/*.yml path (got: $WORKFLOW)" >&2
     exit 1 ;;
esac

[ -f "$WORKFLOW" ] || { echo "ERROR: $WORKFLOW not found in $WT" >&2; exit 1; }

command -v nak >/dev/null 2>&1 || { echo "ERROR: nak not on PATH" >&2; exit 1; }
[ -f "$KEYFILE" ] || { echo "ERROR: key file not found: $KEYFILE" >&2; exit 1; }

# The declared commit must carry this workflow file, and the hash must be the
# file's content at that commit -- the coordinator rejects a mismatch.
command -v git >/dev/null 2>&1 || { echo "ERROR: git not on PATH" >&2; exit 1; }
git rev-parse --verify --quiet "${COMMIT}^{commit}" >/dev/null || {
  echo "ERROR: $COMMIT is not a commit in $WT" >&2; exit 1; }
git cat-file -e "${COMMIT}:${WORKFLOW}" 2>/dev/null || {
  echo "ERROR: $WORKFLOW does not exist at commit $COMMIT" >&2; exit 1; }
FILE_SHA=$(git show "${COMMIT}:${WORKFLOW}" | sha256sum | cut -d' ' -f1)

echo "workflow : $WORKFLOW"
echo "commit   : $COMMIT"
echo "sha256   : $FILE_SHA"

# grep exits 1 when there is no match; without this `|| true` the script would
# die inside the command substitution (set -e + pipefail) before it can explain
# why the key file is unusable.
NSEC=$(grep -o 'nsec1[0-9a-z]*' "$KEYFILE" 2>/dev/null | head -1 || true)
[ -n "$NSEC" ] || { echo "ERROR: no nsec1... key found in $KEYFILE" >&2; exit 1; }

# Refuse to sign with anything that is not the maintainer key. `nak key public`
# prints the public half only; never run `nak decode` on a private key.
SIGNER=$(nak key public "$NSEC" 2>/dev/null || true)
if [ "$SIGNER" != "$MAINT_HEX" ]; then
  echo "ERROR: $KEYFILE is not the maintainer key (signer ${SIGNER:0:16}..., expected ${MAINT_HEX:0:16}...)" >&2
  exit 1
fi

args=(
  --sec "$NSEC" -k 9840 -c ""
  --tag "p=$COORD_HEX"
  --tag "a=30617:$MAINT_HEX:$REPO_ID"
  --tag "c=$COMMIT"
  --tag "w=$WORKFLOW;$FILE_SHA"
)
[ -n "$REF" ] && args+=(--tag "r=$REF")

out=$(nak event "${args[@]}" $RELAYS < /dev/null 2>&1) || true
# Same pipefail trap as above: no match must reach the error branch below, not
# abort the script.
ev=$(printf '%s' "$out" | grep -o '"id":"[0-9a-f]\{64\}"' | head -1 | cut -d'"' -f4 || true)
if [ -z "$ev" ]; then
  echo "ERROR: no event id in the relay response:" >&2
  printf '%s\n' "$out" | tail -5 >&2
  exit 1
fi
echo "9840 request event id: $ev"
