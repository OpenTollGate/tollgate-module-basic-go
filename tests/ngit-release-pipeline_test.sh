#!/usr/bin/env bash
# Offline test for the sharded release pipeline: the plan reader
# (scripts/ngit-shards.sh), the shard renderer (scripts/ngit-gen-shards.py) and
# the announce gate (scripts/ngit-release-announce.sh).
#
# The point of the gate is that it REFUSES: a release whose shards did not all
# succeed must not be announced. So most of this file is negative controls --
# a missing shard record, a missing leg record, and a leg record left over from
# a previous release run at the same commit must all end in "nothing published",
# and that is asserted by counting the publication calls, not by trusting the
# exit code alone.
#
# `nak` is replaced by a record/replay shim on PATH; no relay is contacted and
# no event is published anywhere.
#
# Usage: bash tests/ngit-release-pipeline_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

PASS=0
FAIL=0
ok()   { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad()  { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }
check() { # <description> <expected-rc> <actual-rc>
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (expected rc=$2, got rc=$3)"; fi
}

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# --------------------------------------------------------------- the nak shim
mkdir -p "$TMP/bin"
cat > "$TMP/bin/nak" <<'SHIM'
#!/usr/bin/env bash
# Replay kind-30078 records from $FIXTURES/<d-tag>.json, and log every
# `nak event` publication attempt to $PUBLOG.
set -uo pipefail
cmd=${1:-}
shift || true
case "$cmd" in
  req)
    d=""
    while [ $# -gt 0 ]; do
      case "$1" in
        --tag) case "$2" in d=*) d="${2#d=}" ;; esac; shift 2 ;;
        *) shift ;;
      esac
    done
    f="$FIXTURES/$(printf '%s' "$d" | tr '/' '_').json"
    [ -f "$f" ] && cat "$f"
    exit 0
    ;;
  event)
    printf '%s\n' "$*" >> "$PUBLOG"
    echo '{"id":"'"$(printf '%064d' 1)"'"}'
    exit 0
    ;;
  key)
    echo "0000000000000000000000000000000000000000000000000000000000000000"
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
SHIM
chmod +x "$TMP/bin/nak"
export PATH="$TMP/bin:$PATH"
export FIXTURES="$TMP/fixtures"
export PUBLOG="$TMP/published.log"
mkdir -p "$FIXTURES"
: > "$PUBLOG"

# ------------------------------------------------------------ fixture helpers
# emit <d-tag> <content-json>   -> writes the replay file for that record
emit() {
  local d="$1" content="$2"
  jq -cn --arg d "$d" --argjson c "$content" \
    '{kind:30078, id:("a" * 64), pubkey:("b" * 64), created_at:0,
      tags:[["d",$d]], content:($c | tojson), sig:("c" * 128)}' \
    > "$FIXTURES/$(printf '%s' "$d" | tr '/' '_').json"
}

RUN="a1b2c3d4-20260920T000000Z"
BUILD="a1b2c3d4"
VERSION="v0.0.1-test"
CHANNEL="dev"

# Populate a COMPLETE release: every shard record and every leg record.
populate_complete() {
  rm -f "$FIXTURES"/*.json
  while IFS=$'\t' read -r shard arch fmt comp; do
    local n
    n=$(bash scripts/ngit-shards.sh legs "$shard" | grep -c .)
    emit "tollgate-build/$RUN/shard/$shard" \
      "{\"shard\":\"$shard\",\"release_run\":\"$RUN\",\"build_id\":\"$BUILD\",\"status\":\"success\",\"legs\":$n}"
    emit "tollgate-build/$BUILD/$arch/$fmt/$comp" \
      "{\"sha256\":\"$(printf '%064d' 7)\",\"filename\":\"$arch.$fmt\",\"urls\":[\"https://m1/$arch\",\"https://m2/$arch\"],\"architecture\":\"$arch\",\"format\":\"$fmt\",\"compression\":\"$comp\",\"release_run\":\"$RUN\",\"shard\":\"$shard\"}"
  done < <(bash scripts/ngit-shards.sh all-legs)
}

gate() { # <mode> ; prints rc
  bash scripts/ngit-release-announce.sh "$VERSION" "$CHANNEL" "$RUN" "$BUILD" "$1" >/dev/null 2>&1
  echo $?
}

publish_calls() { wc -l < "$PUBLOG" | tr -d ' '; }

# =============================================================== plan reader
echo "== scripts/ngit-shards.sh"
[ "$(bash scripts/ngit-shards.sh list | wc -l | tr -d ' ')" -ge 5 ] \
  && ok "plan lists shards" || bad "plan lists shards"
[ "$(bash scripts/ngit-shards.sh all-legs | wc -l | tr -d ' ')" -eq 17 ] \
  && ok "plan yields the full 14 ipk + 3 apk matrix (17 legs)" \
  || bad "plan yields 17 legs (got $(bash scripts/ngit-shards.sh all-legs | wc -l | tr -d ' '))"
[ "$(bash scripts/ngit-shards.sh expectations --compression all --formats ipk,apk | wc -l | tr -d ' ')" -eq 8 ] \
  && ok "expectations are the 8 (arch, format) pairs" \
  || bad "expectations are 8 pairs"
[ "$(bash scripts/ngit-shards.sh shard-of mips_24kc ipk upx-brute)" = "ipk-upx-brute-mips-24kc" ] \
  && ok "shard-of names the owning shard" || bad "shard-of names the owning shard"
bash scripts/ngit-shards.sh shard-of no-such-arch ipk none >/dev/null 2>&1
check "shard-of exits 1 for an unowned leg" 1 $?
# Fail closed: an empty plan must not become "no expectations, gate passes".
WF="$TMP/empty.json"; echo '{"shards":[]}' > "$WF"
PLAN="$WF" bash scripts/ngit-shards.sh expectations >/dev/null 2>&1
check "an empty plan is refused (exit 2), not treated as no expectations" 2 $?
PLAN="$ROOT/packaging/ngit-release-matrix.json" bash scripts/ngit-shards.sh expectations >/dev/null 2>&1
check "the committed plan is usable" 0 $?

# The gate can read its expectations two ways: from the plan JSON
# (scripts/ngit-shards.sh, used by the announce workflow) and from the shard
# workflow files themselves (scripts/ngit-matrix-expectations.sh, used by
# verify-publication.yml). They must agree, or one of the two would verify a
# different release than the other.
bash scripts/ngit-matrix-expectations.sh .ngit/act/workflows/build-package-*.yml \
  --compression all --formats ipk,apk > "$TMP/from_files.txt" 2>/dev/null
bash scripts/ngit-shards.sh expectations --compression all --formats ipk,apk > "$TMP/from_plan.txt"
if diff -q "$TMP/from_files.txt" "$TMP/from_plan.txt" >/dev/null; then
  ok "the two expectation sources agree (8 (arch, format) pairs)"
else
  bad "the two expectation sources agree"
  diff "$TMP/from_files.txt" "$TMP/from_plan.txt" | head
fi
if diff -q <(sort "$TMP/from_files.txt") <(sort "$TMP/from_plan.txt") >/dev/null \
   && [ "$(wc -l < "$TMP/from_files.txt" | tr -d ' ')" -eq 8 ]; then
  ok "the union of the shard files covers every architecture"
else
  bad "the union of the shard files covers every architecture"
fi
# A per-shard subset must NOT be able to stand in for the whole matrix.
bash scripts/ngit-matrix-expectations.sh .ngit/act/workflows/build-package-ipk-none.yml \
  --compression all --formats ipk,apk > "$TMP/one_shard.txt" 2>/dev/null
[ "$(wc -l < "$TMP/one_shard.txt" | tr -d ' ')" -lt 8 ] \
  && ok "one shard's rows are a strict subset of the full expectation set" \
  || bad "one shard's rows are a strict subset of the full expectation set"

# ========================================================== shard renderer
echo "== scripts/ngit-gen-shards.py"
python3 scripts/ngit-gen-shards.py --check >/dev/null 2>&1
check "committed shards match the plan" 0 $?
for id in $(bash scripts/ngit-shards.sh list); do
  f=".ngit/act/workflows/build-package-$id.yml"
  [ -f "$f" ] || { bad "shard $id has a workflow file"; continue; }
  python3 - "$f" <<'PY' || bad "shard $id is valid YAML with a workflow_dispatch trigger"
import sys, yaml
d = yaml.safe_load(open(sys.argv[1]))
if d is True or True not in d: sys.exit(1)
if "workflow_dispatch" not in d[True]: sys.exit(1)
if "package" not in d.get("jobs", {}): sys.exit(1)
PY
done
ok "every shard renders, and each declares workflow_dispatch + a package job"
# The package-job epoch env must render the ${{ }} expression form. The
# generator once emitted it from inside an f-string, collapsing {{ to {;
# the runner then passes '${ ... }' through literally and
# packaging/build-env.sh's epoch validation (non-integer => tg_die) fails
# every package job.
SHARD_COUNT=$(bash scripts/ngit-shards.sh list | wc -l | tr -d ' ')
GOOD_EPOCH=$(grep -rlF 'SOURCE_DATE_EPOCH: ${{ needs.resolve-inputs.outputs.source_date_epoch }}' \
  .ngit/act/workflows/ | wc -l | tr -d ' ')
[ "$GOOD_EPOCH" -eq "$SHARD_COUNT" ] \
  && ok "every shard carries the \${{ }} epoch expression ($GOOD_EPOCH/$SHARD_COUNT)" \
  || bad "every shard carries the \${{ }} epoch expression ($GOOD_EPOCH/$SHARD_COUNT)"
if grep -rFq 'SOURCE_DATE_EPOCH: ${ ' .ngit/act/workflows/; then
  bad "no shard carries a collapsed single-brace epoch env"
else
  ok "no shard carries a collapsed single-brace epoch env"
fi
# Negative control: perturb the plan and the check must fail.
cp packaging/ngit-release-matrix.json "$TMP/plan.bak"
python3 - <<'PY'
import json
p = json.load(open("packaging/ngit-release-matrix.json"))
p["shards"][0]["legs"].append({"architecture": "aarch64_cortex-a53", "compile_key": "arm64",
                               "format": "ipk", "compression": "upx-best"})
json.dump(p, open("packaging/ngit-release-matrix.json", "w"))
PY
python3 scripts/ngit-gen-shards.py --check >/dev/null 2>&1
check "check FAILS when the plan gains a leg (drift is caught)" 1 $?
cp "$TMP/plan.bak" packaging/ngit-release-matrix.json
python3 scripts/ngit-gen-shards.py --check >/dev/null 2>&1
check "check passes again once the plan is restored" 0 $?

# ============================================================ the announce gate
echo "== scripts/ngit-release-announce.sh"

# The shim ignores the value; the script only requires it to be present.
export NSEC_HEX=0000000000000000000000000000000000000000000000000000000000000000

populate_complete
: > "$PUBLOG"
check "a complete release passes the gate" 0 "$(gate --dry-run)"
check "a gate-only run publishes nothing" 0 "$(publish_calls)"
check "the same complete release passes --publish" 0 "$(gate --publish)"
[ "$(publish_calls)" -eq 17 ] \
  && ok "--publish announces every leg (17 calls)" \
  || bad "--publish announces 17 legs (got $(publish_calls))"

# Negative control 1: a shard that failed or timed out leaves no shard record.
populate_complete
rm -f "$FIXTURES/tollgate-build_${RUN}_shard_ipk-none.json"
: > "$PUBLOG"
check "a release with one failed shard is REFUSED" 1 "$(gate --dry-run)"
check "the refused release publishes nothing (gate)" 0 "$(publish_calls)"
check "--publish cannot publish a release with a failed shard" 1 "$(gate --publish)"
check "--publish published NOTHING for the failed-shard release" 0 "$(publish_calls)"

# Negative control 2: the shard finished but one of its legs did not.
populate_complete
rm -f "$FIXTURES/tollgate-build_${BUILD}_mips_24kc_ipk_upx-brute.json"
: > "$PUBLOG"
check "a release missing one leg record is REFUSED" 1 "$(gate --dry-run)"
check "the missing-leg release publishes nothing" 0 "$(publish_calls)"

# Negative control 3: staleness. The leg record exists, but from an EARLIER
# release run at the same commit -- the shape a re-run of one shard produces.
populate_complete
emit "tollgate-build/$BUILD/aarch64_cortex-a53/apk/none" \
  "{\"sha256\":\"$(printf '%064d' 9)\",\"filename\":\"stale.apk\",\"urls\":[\"https://m1/stale\"],\"architecture\":\"aarch64_cortex-a53\",\"format\":\"apk\",\"compression\":\"none\",\"release_run\":\"$BUILD\",\"shard\":\"apk-mediatek-filogic\"}"
: > "$PUBLOG"
check "a leg record from another release run is REFUSED (staleness)" 1 "$(gate --dry-run)"
check "the stale-record release publishes nothing" 0 "$(publish_calls)"

# Negative control 4: a shard that claims success for a different release run.
populate_complete
emit "tollgate-build/$RUN/shard/ipk-none" \
  "{\"shard\":\"ipk-none\",\"release_run\":\"other-run\",\"build_id\":\"$BUILD\",\"status\":\"success\",\"legs\":6}"
check "a shard record for another release run is REFUSED" 1 "$(gate --dry-run)"

# Negative control 5: a shard record that reports failure.
populate_complete
emit "tollgate-build/$RUN/shard/apk-x86-64" \
  "{\"shard\":\"apk-x86-64\",\"release_run\":\"$RUN\",\"build_id\":\"$BUILD\",\"status\":\"failure\",\"legs\":1}"
check "a shard record reporting failure is REFUSED" 1 "$(gate --dry-run)"

# Negative control 6: a shard that only did part of its own legs.
populate_complete
emit "tollgate-build/$RUN/shard/ipk-none" \
  "{\"shard\":\"ipk-none\",\"release_run\":\"$RUN\",\"build_id\":\"$BUILD\",\"status\":\"success\",\"legs\":3}"
check "a shard that reported fewer legs than the plan is REFUSED" 1 "$(gate --dry-run)"

# The release run must not be the build id: an earlier success at the same
# commit would otherwise satisfy the gate.
bash scripts/ngit-release-announce.sh "$VERSION" "$CHANNEL" "$BUILD" "$BUILD" --dry-run >/dev/null 2>&1
check "release_run == build_id is refused (exit 2)" 2 $?
bash scripts/ngit-release-announce.sh "$VERSION" "$CHANNEL" "$RUN" "$BUILD" >/dev/null 2>&1
check "an invocation without --dry-run/--publish is refused (exit 2)" 2 $?

# ======================================================== the trigger driver
echo "== scripts/ngit-commit-epoch.sh"
[ "$(bash scripts/ngit-commit-epoch.sh HEAD)" = "$(git log -1 --format=%ct HEAD)" ] \
  && ok "reads the commit time from local history" \
  || bad "reads the commit time from local history"
# No local history and an unreachable mirror: refuse, never guess. A wrong
# SOURCE_DATE_EPOCH silently produces a differently-built artifact.
bash scripts/ngit-commit-epoch.sh 0000000000000000000000000000000000000000 \
  ws://127.0.0.1:1/nothing.git >/dev/null 2>&1
check "refuses when the commit time cannot be determined (exit 1)" 1 $?

echo "== scripts/ngit-ci-release.sh"
bash scripts/ngit-ci-release.sh v0.0.1-test dev HEAD --dry-run >/dev/null 2>&1
check "the driver renders a plan in --dry-run" 0 $?
bash scripts/ngit-ci-release.sh v0.0.1-test dev HEAD --shards no-such-shard --dry-run >/dev/null 2>&1
check "the driver refuses an unknown shard id (exit 2)" 2 $?
HEAD_ID=$(git rev-parse HEAD | cut -c1-8)
bash scripts/ngit-ci-release.sh v0.0.1-test dev HEAD --release-run "$HEAD_ID" --dry-run >/dev/null 2>&1
check "the driver refuses a release run equal to the build id (exit 2)" 2 $?

echo
echo "$PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
