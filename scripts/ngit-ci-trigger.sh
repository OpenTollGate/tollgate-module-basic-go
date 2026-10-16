#!/usr/bin/env bash
# Publish a ngit-ci Manual Trigger (kind 9840) for this repository's Nostr
# mirror, signed by an identity this operator has declared they trust.
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
#   KEYFILE    file containing the signing nsec (default below)
#   COORD_HEX  coordinator pubkey to name in the `p` tag
#   RELAYS     relays to publish to
#
# TRUST MODEL — there is no single source of authority for a Nostr identity,
# so none ships in this file. The operator DECLARES which identities this run
# may sign with, in NGIT_TRUST (space- or comma-separated entries):
#   <64-hex pubkey>   trust an identity by its public key
#   npub1...          the same, bech32 form
#   nsec1...          trust an identity whose private half you hold; only its
#                     public half is derived (never printed)
# MAINT_HEX (a single hex pubkey) still works as the legacy one-entry form.
# With neither declared the script FAILS CLOSED. Rotation is a declaration
# change, not a code change: carry old and new in NGIT_TRUST across the cut,
# drop the old one after.
#
# The matched identity also names the `a=30617:<hex>:<repo>` coordinate, so it
# must be an identity the coordinator still serves for this repository. When a
# trigger is refused or ignored, check the live announcement:
#   nak req -k 30617 -d tollgate-module-basic-go wss://relay.ngit.dev
#
# The signing key is never printed, echoed or logged; it is read from KEYFILE
# and handed to `nak` via --sec. (The key does appear in nak's argv for the
# duration of a call — the same exposure the publish step has always had; a
# hardening to an fd-passing scheme would have to change nak first.)

set -euo pipefail

WT=${WT:-$(git -C "$(dirname -- "$0")" rev-parse --show-toplevel)}
KEYFILE=${KEYFILE:-${HOME}/.hermes/.ngit-new-key}
COORD_HEX=${COORD_HEX:-765cd47badcbbc4a38c7d0c57d5607663b484c20cd59773f9f7064487f9431e8}
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

# --------------------------------------------------------------------------
# Trust declaration -> a set of 64-hex pubkeys.
# --------------------------------------------------------------------------
if [ -n "${NGIT_TRUST:-}" ]; then
  TRUST_SRC="$NGIT_TRUST"
elif [ -n "${MAINT_HEX:-}" ]; then
  echo "WARNING: MAINT_HEX is the legacy single-entry trust form; rename it to NGIT_TRUST (a set)." >&2
  TRUST_SRC="$MAINT_HEX"
else
  echo "ERROR: no trusted identities declared. Set NGIT_TRUST to the identities" >&2
  echo "this run may sign with (64-hex pubkeys, npub1... or nsec1... entries," >&2
  echo "space- or comma-separated). No built-in authority ships in this file:" >&2
  echo "you define which identities you trust." >&2
  exit 1
fi

# pubkey_of <nsec> -> the 64-hex public half, or empty. Older nak builds take
# the nsec as `nak key public`'s argument; nak v0.16.2 takes hex only there
# (verified: `invalid hex key` on a valid nsec), so the fallback renders a
# throwaway kind-1 event with --sec -- the surface the publish step already
# depends on, stable across versions -- and reads the pubkey out of the JSON.
# The private half never reaches the output.
pubkey_of() { # pubkey_of <nsec>
    local nsec="$1" pub
    pub=$(nak key public "$nsec" 2>/dev/null || true)
    if ! printf '%s' "$pub" | grep -qE '^[0-9a-f]{64}$'; then
        pub=$(nak event --sec "$nsec" -k 1 -c "tollgate-pubkey-probe" < /dev/null 2>/dev/null \
              | grep -o '"pubkey":"[0-9a-f]\{64\}"' | head -1 | cut -d'"' -f4 || true)
    fi
    printf '%s' "$pub"
}

TRUST_HEXES=""
entry_no=0
for entry in ${TRUST_SRC//,/ }; do
  entry_no=$((entry_no + 1))
  hex=""
  case "$entry" in
    npub1*) hex=$(nak decode "$entry" 2>/dev/null || true) ;;
    nsec1*) hex=$(pubkey_of "$entry") ;;
    *) if printf '%s' "$entry" | grep -qE '^[0-9a-fA-F]{64}$'; then
         hex=$(printf '%s' "$entry" | tr 'A-F' 'a-f')
       fi ;;
  esac
  if ! printf '%s' "$hex" | grep -qE '^[0-9a-f]{64}$'; then
    echo "ERROR: NGIT_TRUST entry $entry_no is not a usable 64-hex pubkey, npub1... or nsec1... value" >&2
    echo "       (its public half could not be derived; the entry itself is never printed)" >&2
    exit 1
  fi
  TRUST_HEXES+="$hex"$'\n'
done

# --------------------------------------------------------------------------
# The signing key must resolve into the declared trust set.
# --------------------------------------------------------------------------
# grep exits 1 when there is no match; without this `|| true` the script would
# die inside the command substitution (set -e + pipefail) before it can explain
# why the key file is unusable.
NSEC=$(grep -o 'nsec1[0-9a-z]*' "$KEYFILE" 2>/dev/null | head -1 || true)
[ -n "$NSEC" ] || { echo "ERROR: no nsec1... key found in $KEYFILE" >&2; exit 1; }

SIGNER=$(pubkey_of "$NSEC")
if [ -z "$SIGNER" ]; then
  echo "ERROR: could not derive the public half of the key in $KEYFILE (nak on PATH?)" >&2
  exit 1
fi
if ! printf '%s\n' "$TRUST_HEXES" | grep -qxF "$SIGNER"; then
  echo "ERROR: the key in $KEYFILE (signer ${SIGNER:0:16}...) is not in the declared trust set (NGIT_TRUST)" >&2
  exit 1
fi

args=(
  --sec "$NSEC" -k 9840 -c ""
  --tag "p=$COORD_HEX"
  --tag "a=30617:$SIGNER:$REPO_ID"
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
