#!/usr/bin/env bash
# ngit-release-announce.sh — resolve, gate and (optionally) announce a release.
#
# THE PROBLEM THIS SOLVES
#   Before the release matrix was sharded, each package job published its own
#   kind-1063 announcement as soon as its own artifact was uploaded. A run that
#   was cut off at the coordinator's 1800 s ceiling therefore left a *partial*
#   release that looked finished: five announcements existed, eleven legs never
#   ran, and nothing anywhere said so (measured 2026-09-12, commit b25d8a28).
#
# THE RULE
#   Nothing is announced until EVERY shard of the same (version, channel,
#   release run) has reported success AND every leg in the shard plan has a
#   build record naming that same release run. Both halves are needed:
#
#   * the per-shard records catch a shard that never finished;
#   * the per-leg records catch a shard that finished a subset of its legs;
#   * the release-run comparison catches the staleness that would otherwise
#     make "the record exists" meaningless -- kind-30078 is addressed by its
#     `d` tag, so a record from an earlier release at the same commit is still
#     there to be found. A record that does not name THIS release run is not
#     evidence about this release.
#
#   Because the release run id is what all of that is keyed on, it must be
#   fresh per release. A release run equal to the build id would let a previous
#   successful run at the same commit satisfy the gate, so it is refused.
#
# Usage: scripts/ngit-release-announce.sh <version> <channel> <release_run> <build_id>
#          (--dry-run | --publish)
#
#   --dry-run   resolve and gate only. Prints what would be announced and exits
#               non-zero if the release is incomplete. This is what the
#               `release-guard` job runs, and what a failed-shard run is
#               expected to fail on.
#   --publish   gate, then publish one kind-1063 per leg from its build record.
#               Requires NSEC_HEX.
#
# Env:
#   PLAN                 shard plan (default packaging/ngit-release-matrix.json)
#   COORDINATION_RELAYS  relays carrying the kind-30078 records
#   RELAYS               relays the kind-1063 announcements are published to
#
# Exit codes:
#   0  the release is complete (and, with --publish, announced)
#   1  the release is INCOMPLETE or a record disagrees with this release run;
#      the failing shard/leg is named on stderr
#   2  the call itself is unusable (bad arguments, missing tool, unusable plan,
#      or a release run that cannot be trusted) -- never reported as a pass
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage: scripts/ngit-release-announce.sh <version> <channel> <release_run> <build_id>
         (--dry-run | --publish)

  --dry-run   gate only; exit non-zero when the release is incomplete
  --publish   gate, then publish a kind-1063 per leg (needs NSEC_HEX)
EOF
}

VERSION=${1:-}; CHANNEL=${2:-}; RELEASE_RUN=${3:-}; BUILD_ID=${4:-}
MODE=${5:-}
case "$MODE" in
  --dry-run|--publish) ;;
  *) usage; exit 2 ;;
esac
[ -n "$VERSION" ] && [ -n "$CHANNEL" ] && [ -n "$RELEASE_RUN" ] && [ -n "$BUILD_ID" ] || { usage; exit 2; }

PLAN=${PLAN:-packaging/ngit-release-matrix.json}
COORDINATION_RELAYS=${COORDINATION_RELAYS:-"wss://relay.damus.io wss://nos.lol wss://nostr.mom wss://relay1.orangesync.tech wss://relay2.orangesync.tech"}
RELAYS=${RELAYS:-"$COORDINATION_RELAYS"}
PACKAGE_NAME=${PACKAGE_NAME:-tollgate-wrt}
ROOT=${WT:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}
cd "$ROOT"

command -v nak >/dev/null 2>&1 || { echo "ERROR: nak not on PATH" >&2; exit 2; }
command -v jq  >/dev/null 2>&1 || { echo "ERROR: jq not on PATH" >&2; exit 2; }
[ -f "$PLAN" ] || { echo "ERROR: no shard plan at $PLAN" >&2; exit 2; }

# --- 0. the release run id has to be one a stale record cannot satisfy.
[ "$RELEASE_RUN" != "$BUILD_ID" ] || {
  echo "ERROR: release_run ($RELEASE_RUN) equals build_id ($BUILD_ID)." >&2
  echo "A release run id must be fresh per release, or a record from an earlier" >&2
  echo "successful run at this commit would satisfy the gate. scripts/ngit-ci-release.sh" >&2
  echo "generates one of the form <build_id>-<utc timestamp>." >&2
  exit 2
}

# --- 1. the shard plan must still describe what the workflows run.
if [ -x scripts/ngit-gen-shards.py ] || [ -f scripts/ngit-gen-shards.py ]; then
  python3 scripts/ngit-gen-shards.py --check >/dev/null || {
    echo "ERROR: the committed shard workflows do not match $PLAN." >&2
    echo "Refusing to announce a release whose plan and workflows disagree." >&2
    exit 2
  }
fi

mapfile -t SHARDS < <(bash scripts/ngit-shards.sh list)
[ "${#SHARDS[@]}" -gt 0 ] || { echo "ERROR: $PLAN yielded no shards" >&2; exit 2; }
mapfile -t LEGS < <(bash scripts/ngit-shards.sh all-legs)
[ "${#LEGS[@]}" -gt 0 ] || { echo "ERROR: $PLAN yielded no legs" >&2; exit 2; }

echo "== release gate: $VERSION channel=$CHANNEL release_run=$RELEASE_RUN build_id=$BUILD_ID"
echo "   plan: $PLAN (${#SHARDS[@]} shards, ${#LEGS[@]} legs)"

fetch_record() { # <d-tag>
  local d="$1" out
  out=$(timeout 60 nak req -k 30078 --tag "d=$d" --limit 1 $COORDINATION_RELAYS \
        < /dev/null 2>/dev/null | grep -m1 '^{' || true)
  printf '%s' "$out"
}

# content is a JSON document encoded as a string: parse twice.
record_content() { printf '%s' "$1" | jq -r '.content' | jq -c . 2>/dev/null || echo 'null'; }

FAIL=0
bad() { printf 'FAIL: %s\n' "$*" >&2; FAIL=1; }

# --- 2. every shard of this release run must have reported success.
echo "-- shards"
for shard in "${SHARDS[@]}"; do
  want_legs=$(bash scripts/ngit-shards.sh legs "$shard" | grep -c . || true)
  ev=$(fetch_record "tollgate-build/${RELEASE_RUN}/shard/${shard}")
  if [ -z "$ev" ]; then
    bad "shard '$shard' published no completion record for release run $RELEASE_RUN (it failed, timed out, or never ran)"
    continue
  fi
  content=$(record_content "$ev")
  if printf '%s' "$content" | jq -e --arg run "$RELEASE_RUN" --argjson n "$want_legs" \
       '.release_run == $run and .status == "success" and .legs == $n' >/dev/null 2>&1; then
    echo "   ok   $shard ($want_legs leg(s))"
  else
    bad "shard '$shard' record is not a success for this release run: $content"
  fi
done

# --- 3. every leg in the plan must have a build record for this release run.
echo "-- legs"
LEGS_JSONL=$(mktemp)
trap 'rm -f "$LEGS_JSONL"' EXIT
while IFS=$'\t' read -r shard arch fmt comp; do
  d="tollgate-build/${BUILD_ID}/${arch}/${fmt}/${comp}"
  ev=$(fetch_record "$d")
  if [ -z "$ev" ]; then
    bad "leg $arch/$fmt/$comp (shard $shard) has no build record at $d"
    continue
  fi
  content=$(record_content "$ev")
  run_ok=$(printf '%s' "$content" | jq -r --arg run "$RELEASE_RUN" '.release_run == $run' 2>/dev/null || echo false)
  if [ "$run_ok" != "true" ]; then
    got=$(printf '%s' "$content" | jq -r '.release_run // "unset"' 2>/dev/null || echo unparseable)
    bad "leg $arch/$fmt/$comp (shard $shard) records release_run=$got, not $RELEASE_RUN — it was built by another run"
    continue
  fi
  urls=$(printf '%s' "$content" | jq -r '.urls | length' 2>/dev/null || echo 0)
  hash=$(printf '%s' "$content" | jq -r '.sha256 // empty')
  if [ -z "$hash" ]; then
    bad "leg $arch/$fmt/$comp (shard $shard) record has no sha256: $content"
    continue
  fi
  if [ "$urls" -lt 1 ]; then
    bad "leg $arch/$fmt/$comp (shard $shard) record carries no mirror URL"
    continue
  fi
  printf '%s\n' "$content" >> "$LEGS_JSONL"
  printf '   ok   %-22s %-4s %-16s %s (%s mirror(s), shard %s)\n' \
    "$arch" "$fmt" "$comp" "${hash:0:16}…" "$urls" "$shard"
done < <(printf '%s\n' "${LEGS[@]}")

if [ "$FAIL" -ne 0 ]; then
  echo >&2
  echo "RELEASE INCOMPLETE: nothing has been announced and nothing will be." >&2
  echo "Fix or re-run the failing shard(s), then run the announce again with the" >&2
  echo "same release run id (scripts/ngit-ci-release.sh --resume $RELEASE_RUN)." >&2
  exit 1
fi

legs_found=$(grep -c . "$LEGS_JSONL" || true)
echo "-- complete: ${#SHARDS[@]}/${#SHARDS[@]} shards, $legs_found/${#LEGS[@]} legs"

if [ "$MODE" = "--dry-run" ]; then
  echo "dry run: the release is complete; announcements would be published now."
  exit 0
fi

# --- 4. announce. Only reachable with a complete release.
: "${NSEC_HEX:?--publish needs NSEC_HEX in the environment}"
echo "-- announcing"
published=0
while IFS= read -r content; do
  arch=$(printf '%s' "$content" | jq -r '.architecture')
  fmt=$(printf '%s' "$content" | jq -r '.format')
  comp=$(printf '%s' "$content" | jq -r '.compression')
  filename=$(printf '%s' "$content" | jq -r '.filename')
  hash=$(printf '%s' "$content" | jq -r '.sha256')
  url_tags=()
  while IFS= read -r u; do url_tags+=(--tag "url=$u"); done < <(printf '%s' "$content" | jq -r '.urls[]')
  out=$(nak event --sec "$NSEC_HEX" -k 1063 \
    -c "TollGate Package: ${PACKAGE_NAME} for ${arch}" \
    "${url_tags[@]}" \
    --tag m="application/octet-stream" \
    --tag x="$hash" --tag ox="$hash" \
    --tag filename="$filename" \
    --tag A="$arch" --tag v="$VERSION" --tag c="$CHANNEL" \
    --tag n="$PACKAGE_NAME" --tag compression="$comp" --tag format="$fmt" \
    $RELAYS < /dev/null 2>&1) || true
  ev=$(printf '%s' "$out" | grep -o '"id":"[0-9a-f]\{64\}"' | head -1 | cut -d'"' -f4)
  if [ -n "$ev" ]; then
    echo "   1063 $arch/$fmt/$comp -> $ev"
    published=$((published + 1))
  else
    echo "   FAILED to publish $arch/$fmt/$comp: $(printf '%s' "$out" | tail -1)" >&2
    FAIL=1
  fi
done < "$LEGS_JSONL"

echo "published $published/${#LEGS[@]} announcement(s)"
[ "$FAIL" -eq 0 ] || exit 1
[ "$published" -eq "${#LEGS[@]}" ] || { echo "ERROR: only $published of ${#LEGS[@]} announced" >&2; exit 1; }
