#!/usr/bin/env bash
# ngit-ci-release.sh — drive the sharded ngit release pipeline in order.
#
# WHAT THIS IS
#   Stage 2 of the ngit release pipeline is sharded across one workflow file per
#   group of legs (packaging/ngit-release-matrix.json). ngit-ci has no cross-file
#   `needs:`, so something has to trigger the shards in an order and decide, from
#   their published results, whether the release may be announced at all. That is
#   this script -- the orchestration half of the "announce only a complete
#   release" guarantee; the other half is the relay-side gate in
#   scripts/ngit-release-announce.sh, which re-checks everything and is what
#   actually refuses a partial release.
#
#   Nothing here is trusted by the gate: if this script wrongly started the
#   announce after a failed shard, the announce would still refuse.
#
# THE ORDER OF OPERATIONS
#   1. every release ref (one per shard, plus the announce ref) is pushed to the
#      ngit mirror FIRST, because a manual trigger names a commit that the
#      coordinator resolves from the mirror -- triggering before the ref exists
#      is accepted and then silently does nothing;
#   2. each shard is triggered and its kind-9842 workflow result awaited;
#   3. the first non-`success` result STOPS the chain. No announce is started,
#      so no kind-1063 is published and the release does not look complete;
#   4. only after every selected shard succeeded is the announce file started.
#
# IDEMPOTENCY
#   The release run id keys every record, so re-running is safe and cheap: the
#   shards are addressed by ref, so re-triggering one rewrites its ref, and each
#   package job republishes its own kind-30078 record (kind-30078 is addressed
#   by its `d` tag, so the newest wins rather than accumulating). Use
#   `--release-run` to resume the SAME release run after fixing one shard:
#
#     scripts/ngit-ci-release.sh v0.6.0-alpha3 alpha "$SHA" --shards ipk-none
#     scripts/ngit-ci-release.sh v0.6.0-alpha3 alpha "$SHA" \
#         --release-run "$RUN" --shards ipk-upx-ultrabrute-mips-24kc --then-announce
#
# Usage:
#   scripts/ngit-ci-release.sh <version> <channel> <commit> [options]
#
#   --shards a,b,c        run only these shards (default: every shard, in plan
#                         order, then the announce)
#   --release-run ID      reuse a release run id instead of minting one
#   --then-announce       run ONLY the announce (for resume; the shards named by
#                         --shards must already have succeeded)
#   --poll-timeout N      seconds to wait for one shard's result (default 2100)
#   --no-push             assume the refs are already on the mirror
#   --dry-run             print what would happen and exit
#
# Env:
#   KEYFILE    maintainer nsec file (default ~/.hermes/.ngit-new-key)
#   EVIDENCE   directory for the per-shard TSV (default ./.ngit-release-evidence)
#   NAK_RELAY  relay the kind-9842 results are read from
set -euo pipefail

ROOT=${WT:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}
cd "$ROOT"

VERSION=${1:-}; CHANNEL=${2:-}; COMMIT=${3:-}
if [ -z "$VERSION" ] || [ -z "$CHANNEL" ] || [ -z "$COMMIT" ]; then
  sed -n '28,48p' "$0" | sed 's/^# \{0,1\}//' >&2
  exit 2
fi
shift 3

SHARDS_SPEC=""; RELEASE_RUN=""; THEN_ANNOUNCE=0; POLL_TIMEOUT=2100; NO_PUSH=0; DRY_RUN=0
while [ $# -gt 0 ]; do
  case "$1" in
    --shards)       SHARDS_SPEC=${2:?}; shift 2 ;;
    --release-run)  RELEASE_RUN=${2:?}; shift 2 ;;
    --then-announce) THEN_ANNOUNCE=1; shift ;;
    --poll-timeout) POLL_TIMEOUT=${2:?}; shift 2 ;;
    --no-push)      NO_PUSH=1; shift ;;
    --dry-run)      DRY_RUN=1; shift ;;
    *) echo "ERROR: unknown option: $1" >&2; exit 2 ;;
  esac
done

EVIDENCE=${EVIDENCE:-.ngit-release-evidence}
NAK_RELAY=${NAK_RELAY:-wss://relay.ngit.dev}
ANNOUNCE_WORKFLOW=.ngit/act/workflows/build-package-announce.yml
COORD_HEX=${COORD_HEX:-765cd47badcbbc4a38c7d0c57d5607663b484c20cd59773f9f7064487f9431e8}

command -v nak >/dev/null 2>&1 || { echo "ERROR: nak not on PATH" >&2; exit 2; }
git rev-parse --verify --quiet "$COMMIT^{commit}" >/dev/null || { echo "ERROR: commit $COMMIT is not in this repository" >&2; exit 2; }
SHA=$(git rev-parse "$COMMIT^{commit}")
BUILD_ID=${SHA:0:8}

ALL_SHARDS=$(bash scripts/ngit-shards.sh list)
[ -n "$ALL_SHARDS" ] || { echo "ERROR: the shard plan yielded no shards" >&2; exit 2; }

if [ -n "$SHARDS_SPEC" ]; then
  WANT=$(printf '%s' "$SHARDS_SPEC" | tr ',' '\n' | grep -v '^$')
else
  WANT=$(printf '%s\n' "$ALL_SHARDS")
fi
# Refuse an unknown shard id rather than silently running nothing: a typo that
# turns "run the release" into "announce nothing built" is the failure shape
# this whole pipeline exists to avoid.
for s in $WANT; do
  printf '%s\n' "$ALL_SHARDS" | grep -qx "$s" || { echo "ERROR: '$s' is not a shard in the plan" >&2; exit 2; }
done

RELEASE_RUN=${RELEASE_RUN:-"${BUILD_ID}-$(date -u +%Y%m%dT%H%M%SZ)"}
[ "$RELEASE_RUN" != "$BUILD_ID" ] || { echo "ERROR: release run must not equal the build id" >&2; exit 2; }

REF_PREFIX="refs/heads/release/${VERSION}/${CHANNEL}/${RELEASE_RUN}"

echo "== ngit release run"
echo "   version    : $VERSION"
echo "   channel    : $CHANNEL"
echo "   commit     : $SHA (build id $BUILD_ID)"
echo "   release run: $RELEASE_RUN"
echo "   ref prefix : $REF_PREFIX"
echo "   shards     : $(printf '%s' "$WANT" | tr '\n' ' ')"
echo "   evidence   : $EVIDENCE"

if [ "$DRY_RUN" -eq 1 ]; then
  echo "-- dry run: nothing triggered."
  for s in $WANT; do echo "   would run shard $s -> $REF_PREFIX/$s"; done
  [ "$THEN_ANNOUNCE" -eq 1 ] && echo "   would run the announce -> $REF_PREFIX/announce"
  exit 0
fi

mkdir -p "$EVIDENCE"
RESULTS="$EVIDENCE/shard-results.tsv"
if [ ! -f "$RESULTS" ]; then
  printf 'release_run\tshard\tworkflow\tref\tresult_event_id\tconclusion\tqueued_at\tstarted_at\tfinished_at\tduration_s\n' > "$RESULTS"
fi

# ---------------------------------------------------------------- push the refs
push_ref() { # <ref>
  local ref="$1" nsec got
  nsec=$(grep -oE 'nsec1[0-9a-z]+' "${KEYFILE:-$HOME/.hermes/.ngit-new-key}" | head -1)
  [ -n "$nsec" ] || { echo "ERROR: no nsec in ${KEYFILE:-$HOME/.hermes/.ngit-new-key}" >&2; return 1; }
  # git-remote-nostr resolves its signer from the repository's OWN config, so
  # the key goes in there for the duration of the push and no longer.
  git config --local nostr.nsec "$nsec"
  git push --quiet ngit "$SHA:$ref" >/dev/null 2>&1 || true
  git config --local --unset nostr.nsec || true
  # The nostr transport's exit status is untrustworthy in both directions, so
  # success is decided by reading the ref back.
  got=$(timeout 60 git ls-remote ngit "$ref" 2>/dev/null | awk '{print $1}' | head -1)
  if [ "$got" != "$SHA" ]; then
    echo "ERROR: ngit mirror does not have $ref at $SHA (got '${got:-<absent>}')" >&2
    return 1
  fi
}

# --------------------------------------------------------------- trigger + wait
trigger() { # <workflow> <ref>
  bash scripts/ngit-ci-trigger.sh "$1" "$SHA" "$2"
}

# The kind-9842 workflow result for one workflow path at one ref+commit.
fetch_result() { # <workflow> <ref>
  timeout 90 nak req -k 9842 -a "$COORD_HEX" -l 200 "$NAK_RELAY" < /dev/null 2>/dev/null \
    | grep '^{' \
    | jq -c --arg w "$1" --arg r "$2" --arg c "$SHA" \
        'select(any(.tags[]; .[0] == "w" and .[1] == $w)
                and any(.tags[]; .[0] == "r" and .[1] == $r)
                and any(.tags[]; .[0] == "c" and .[1] == $c))' 2>/dev/null \
    | tail -1
}

tag_of() { # <event-json> <tag-name>
  printf '%s' "$1" | jq -r --arg t "$2" '[.tags[] | select(.[0] == $t) | .[1]] | last // ""'
}

record_result() { # <shard> <workflow> <ref> <event-json>
  local shard="$1" wf="$2" ref="$3" ev="$4"
  local id conclusion queued started finished duration
  id=$(printf '%s' "$ev" | jq -r '.id')
  conclusion=$(tag_of "$ev" conclusion)
  queued=$(tag_of "$ev" queued_at)
  started=$(tag_of "$ev" started_at)
  finished=$(printf '%s' "$ev" | jq -r '.created_at')
  duration=""
  [ -n "$started" ] && [ -n "$finished" ] && duration=$((finished - started))
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$RELEASE_RUN" "$shard" "$wf" "$ref" "$id" "$conclusion" \
    "$queued" "$started" "$finished" "${duration:-}" >> "$RESULTS"
  printf '%s' "$conclusion"
}

wait_for_result() { # <shard> <workflow> <ref>
  local shard="$1" wf="$2" ref="$3" waited=0 ev conclusion
  while [ "$waited" -lt "$POLL_TIMEOUT" ]; do
    ev=$(fetch_result "$wf" "$ref")
    if [ -n "$ev" ]; then
      conclusion=$(record_result "$shard" "$wf" "$ref" "$ev")
      echo "   [$shard] conclusion=$conclusion d=$(($(printf '%s' "$ev" | jq -r '.created_at') - $(tag_of "$ev" started_at)))s"
      printf '%s' "$conclusion"
      return 0
    fi
    sleep 20
    waited=$((waited + 20))
    if [ $((waited % 120)) -eq 0 ]; then
      echo "   [$shard] still running (${waited}s / ${POLL_TIMEOUT}s)"
    fi
  done
  echo "ERROR: no kind-9842 result for $shard after ${POLL_TIMEOUT}s" >&2
  printf 'no-result'
}

declare -a RAN=()
ABORTED=""

if [ "$THEN_ANNOUNCE" -eq 0 ]; then
  if [ "$NO_PUSH" -eq 0 ]; then
    echo "-- pushing $(printf '%s' "$WANT" | wc -w) release ref(s) to the ngit mirror"
    for s in $WANT; do
      push_ref "$REF_PREFIX/$s" || exit 1
    done
    push_ref "$REF_PREFIX/announce" || exit 1
    echo "   refs present on the mirror"
  fi

  for s in $WANT; do
    # The generator names each shard file after its plan id; validate that here
    # so a plan/workflow mismatch is a loud error, not a trigger of a file that
    # does not exist.
    wf=".ngit/act/workflows/build-package-$s.yml"
    [ -f "$wf" ] || { echo "ERROR: no workflow file $wf for shard '$s'" >&2; exit 1; }
    echo "-- shard $s"
    trigger "$wf" "$REF_PREFIX/$s"
    conclusion=$(wait_for_result "$s" "$wf" "$REF_PREFIX/$s")
    RAN+=("$s")
    if [ "$conclusion" != "success" ]; then
      ABORTED="$s"
      ABORTED_CONCLUSION="$conclusion"
      break
    fi
  done
fi

if [ -n "$ABORTED" ]; then
  echo >&2
  echo "RELEASE NOT ANNOUNCED: shard '$ABORTED' returned '$ABORTED_CONCLUSION'." >&2
  echo "No kind-1063 was published, so the release does not look complete." >&2
  echo "Fix the shard, then resume the SAME release run:" >&2
  echo "  scripts/ngit-ci-release.sh $VERSION $CHANNEL $SHA \\" >&2
  echo "      --release-run $RELEASE_RUN --shards $ABORTED --then-announce" >&2
  echo "and finally run the announce:" >&2
  echo "  scripts/ngit-ci-release.sh $VERSION $CHANNEL $SHA \\" >&2
  echo "      --release-run $RELEASE_RUN --then-announce" >&2
  exit 1
fi

echo "-- announce"
if [ "$NO_PUSH" -eq 0 ]; then
  push_ref "$REF_PREFIX/announce" || exit 1
fi
trigger "$ANNOUNCE_WORKFLOW" "$REF_PREFIX/announce"
announce_conclusion=$(wait_for_result announce "$ANNOUNCE_WORKFLOW" "$REF_PREFIX/announce")

echo
echo "== summary (release run $RELEASE_RUN)"
awk -F'\t' 'NR > 1 && $1 == "'"$RELEASE_RUN"'" {
  printf "   %-42s %-10s %ss\n", $2, $6, ($10 == "" ? "-" : $10); total += $10
} END { printf "   %-42s %-10s %ss\n", "TOTAL", "", total }' "$RESULTS"
echo "   evidence: $RESULTS"

if [ "$announce_conclusion" != "success" ]; then
  echo >&2
  echo "RELEASE ANNOUNCE FAILED (conclusion=$announce_conclusion). Read the 9841 job logs:" >&2
  echo "  nak req -k 9841 -a $COORD_HEX -l 20 $NAK_RELAY" >&2
  exit 1
fi
echo "release $VERSION (channel $CHANNEL) announced from release run $RELEASE_RUN"
