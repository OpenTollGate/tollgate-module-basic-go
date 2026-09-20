#!/usr/bin/env bash
# ngit-shards.sh — query the ngit release shard plan.
#
# The plan lives in packaging/ngit-release-matrix.json (single source of truth
# for both the build matrix and the shard assignment). Everything that needs to
# know "which legs exist" and "which shard owns this leg" asks this script, so
# the guard, the publication gate and the trigger driver cannot drift from the
# matrix.
#
# Usage: scripts/ngit-shards.sh <command> [args]      (run from the repo root)
#
#   list                                  shard ids, one per line, in plan order
#   legs <shard-id>                       arch<TAB>format<TAB>compression<TAB>compile_key<TAB>sdk
#   all-legs                              shard-id<TAB>arch<TAB>format<TAB>compression
#   expectations [--formats ipk,apk] [--compression none|all]
#                                         arch/format pairs, sorted+unique
#                                         (the shape verify_publication.sh wants)
#   shard-of <arch> <format> <compression>
#                                         the shard id that owns that leg; exit 1
#                                         when no shard does
#   budget <shard-id>                     the shard's budget_secs
#   ceiling                               the coordinator ceiling this plan targets
#   total-budget                          sum of every shard's budget_secs
#
# Exit codes:
#   0  answered
#   1  answered "no" (shard-of found no owner)
#   2  the plan is unusable (missing file, jq absent, malformed, or EMPTY where
#      emptiness would make a caller verify nothing) — never a silent pass

set -euo pipefail

PLAN=${PLAN:-packaging/ngit-release-matrix.json}

usage() {
  sed -n '3,30p' "$0" | sed 's/^# \{0,1\}//' >&2
}

die() { printf 'ERROR: %s\n' "$*" >&2; exit 2; }

[ -f "$PLAN" ] || die "no shard plan at $PLAN (run from the repository root)"
command -v jq >/dev/null 2>&1 || die "jq not on PATH"

# Fail closed on a plan that parses to nothing.
jq -e '.shards | type == "array" and length > 0' "$PLAN" >/dev/null \
  || die "$PLAN declares no shards"

# Fail closed on a leg whose keys are missing — a partially-parsed row would
# silently reduce the matrix the guard and the gate believe in.
bad=$(jq -r '
  .shards[] as $s | $s.legs[]?
  | select((.architecture // "") == "" or (.format // "") == ""
           or (.compression // "") == "" or (.compile_key // "") == ""
           or (($s.id // "") == ""))
  | "\($s.id)/\(.architecture // "?"): incomplete leg"
' "$PLAN")
[ -z "$bad" ] || die "malformed leg(s) in $PLAN:
$bad"

CMD=${1:-}
[ -n "$CMD" ] || { usage; exit 2; }
shift || true

case "$CMD" in
  list)
    jq -r '.shards[].id' "$PLAN"
    ;;

  legs)
    id=${1:?usage: ngit-shards.sh legs <shard-id>}
    out=$(jq -r --arg id "$id" '
      .shards[] | select(.id == $id) | .legs[]
      | [.architecture, .format, .compression, .compile_key, (.sdk // "-")]
      | @tsv' "$PLAN")
    [ -n "$out" ] || die "no shard with id '$id' in $PLAN (or it has no legs)"
    printf '%s\n' "$out"
    ;;

  all-legs)
    out=$(jq -r '
      .shards[] as $s | $s.legs[]
      | [$s.id, .architecture, .format, .compression] | @tsv' "$PLAN")
    [ -n "$out" ] || die "$PLAN yields no legs at all"
    printf '%s\n' "$out"
    ;;

  expectations)
    formats=ipk,apk
    compression=none
    while [ $# -gt 0 ]; do
      case "$1" in
        --formats)     formats=${2:?--formats needs a value}; shift 2 ;;
        --compression) compression=${2:?--compression needs a value}; shift 2 ;;
        *) die "unknown option: $1" ;;
      esac
    done
    out=$(jq -r --arg comp "$compression" --arg fmts ",$formats," '
      [ .shards[].legs[]
        | . as $leg
        | select(($leg.compression == $comp) or ($comp == "all"))
        | select($fmts | index("," + $leg.format + ","))
        | "\($leg.architecture)/\($leg.format)" ] | unique | .[]' "$PLAN")
    [ -n "$out" ] || die "$PLAN has no leg matching compression=$compression formats=$formats — refusing to emit an empty expectation set"
    printf '%s\n' "$out"
    ;;

  shard-of)
    arch=${1:?usage: ngit-shards.sh shard-of <arch> <format> <compression>}
    fmt=${2:?usage: ngit-shards.sh shard-of <arch> <format> <compression>}
    comp=${3:?usage: ngit-shards.sh shard-of <arch> <format> <compression>}
    out=$(jq -r --arg a "$arch" --arg f "$fmt" --arg c "$comp" '
      .shards[] | select(any(.legs[]; .architecture == $a and .format == $f and .compression == $c)) | .id' "$PLAN" | head -1)
    if [ -z "$out" ]; then
      printf 'no shard owns %s/%s/%s\n' "$arch" "$fmt" "$comp" >&2
      exit 1
    fi
    printf '%s\n' "$out"
    ;;

  budget)
    id=${1:?usage: ngit-shards.sh budget <shard-id>}
    jq -r --arg id "$id" '.shards[] | select(.id == $id) | .budget_secs' "$PLAN" \
      | grep -v '^null$'
    ;;

  ceiling)
    jq -r '.ceiling_secs' "$PLAN"
    ;;

  total-budget)
    jq -r '[.shards[].budget_secs] | add' "$PLAN"
    ;;

  -h|--help|help)
    usage
    ;;

  *)
    die "unknown command: $CMD"
    ;;
esac
