#!/usr/bin/env bash
# release-check: the one-command pre-release gate (docs/release-process.md).
#
# It orchestrates the existing gates — it must never replace or relax one:
# every check below is the same command CI or the runbook runs, and a failure
# here is a failure of the underlying gate, not of the wrapper.
#
# Usage: make release-check VERSION=v0.6.0-rc1
#        scripts/release-check.sh v0.6.0-rc1
#
# Environment:
#   TOLLGATE_RELEASE_CHECK_CONFORMANCE=1  require the docker conformance
#     subset instead of skipping it when docker/PRTA is unavailable (the
#     release manager sets this on the machine that owns the lane).
#   TOLLGATE_RELEASE_CHECK_REPRO=none     skip the reproducibility build
#   TOLLGATE_RELEASE_CHECK_SKIP_CONFORMANCE=1  skip the conformance lane
#     (the make release-check-fast profile sets both: go-battery, deps,
#     contract, packaging, the invariant groups and version consistency in
#     one pass — the per-push shape of the gate).
#     (default: binaries x86_64 — the cheap leg; run `make
#     reproducibility-test T=... ARCH=...` for the full matrix).
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

VERSION="${1:-}"
if [ -z "$VERSION" ]; then
    if [ -f "$ROOT/VERSION" ]; then
        VERSION="$(cat "$ROOT/VERSION")"
    else
        echo "usage: $0 <version>   (or run from a tree with a VERSION file)" >&2
        exit 2
    fi
fi

results=()
overall=0

record() { # record <label> <status: PASS|FAIL|SKIP> <detail>
    results+=("$1: $2${3:+ ($3)}")
    if [ "$2" = "FAIL" ]; then overall=1; fi
}

run_go_battery() {
    if make go-battery >"$tmpdir/go-battery.log" 2>&1; then
        record "Go battery" PASS "16 modules, gofmt/vet/build/-race"
    else
        record "Go battery" FAIL "see $tmpdir/go-battery.log"
    fi
}

run_deps_imports() {
    local ok=0
    python3 tests/contract/check-deps-sync.py >"$tmpdir/deps.log" 2>&1 || ok=1
    python3 tests/contract/check-mirror-sync.py >>"$tmpdir/deps.log" 2>&1 || ok=1
    python3 tests/contract/check-import-paths.py >>"$tmpdir/deps.log" 2>&1 || ok=1
    if [ "$ok" = 0 ]; then
        record "Dependency/import consistency" PASS
    else
        record "Dependency/import consistency" FAIL "see $tmpdir/deps.log"
    fi
}

run_contract() {
    local ok=0
    node tests/contract/js-schema-lint.mjs >"$tmpdir/contract.log" 2>&1 || ok=1
    bash tests/contract/build-purity.sh >>"$tmpdir/contract.log" 2>&1 || ok=1
    python3 tests/contract/check-docs-facts.py >>"$tmpdir/contract.log" 2>&1 || ok=1
    if [ "$ok" = 0 ]; then
        record "Contract" PASS "schema lint + build purity + doc facts"
    else
        record "Contract" FAIL "see $tmpdir/contract.log"
    fi
}

run_packaging() {
    local ok=0 log="$tmpdir/packaging.log"
    : >"$log"
    local t
    for t in tests/packaging/*_test.sh; do
        echo "== $t" >>"$log"
        bash "$t" >>"$log" 2>&1 || ok=1
    done
    for t in tests/uci-defaults-*_test.sh tests/ngit-ci-trigger_test.sh tests/ngit-release-pipeline_test.sh; do
        echo "== $t" >>"$log"
        bash "$t" >>"$log" 2>&1 || ok=1
    done
    if [ "$ok" = 0 ]; then
        record "Packaging" PASS "shell suites + uci-defaults + release pipeline"
    else
        record "Packaging" FAIL "see $log"
    fi
}

run_invariant() { # run_invariant <label> <dir> <go test args...>
    local label="$1" dir="$2"; shift 2
    local safe="${label//\//-}"
    if (cd "$dir" && go test -race -count=1 -tags testenv "$@" . >"$tmpdir/invariant-$safe.log" 2>&1); then
        record "$label" PASS
    else
        record "$label" FAIL "see $tmpdir/invariant-$safe.log"
    fi
}

run_conformance() {
    if [ "${TOLLGATE_RELEASE_CHECK_SKIP_CONFORMANCE:-0}" = 1 ]; then
        record "Conformance (fast subset)" SKIP "disabled by env (fast mode)"
        return
    fi
    if ! command -v docker >/dev/null 2>&1; then
        if [ "${TOLLGATE_RELEASE_CHECK_CONFORMANCE:-0}" = 1 ]; then
            record "Conformance (fast subset)" FAIL "docker unavailable but required"
        else
            record "Conformance (fast subset)" SKIP "docker unavailable"
        fi
        return
    fi
    bash tests/cloud-lab/conformance/run-conformance.sh >"$tmpdir/conformance.log" 2>&1
    local lane_rc=$?
    # The lane skips cleanly (exit 0) when its prerequisites are missing —
    # no PRTA checkout, no daemon image — so the skip must be detected from
    # the log on EITHER exit path, never read as a pass.
    if grep -qi "skipping" "$tmpdir/conformance.log"; then
        if [ "${TOLLGATE_RELEASE_CHECK_CONFORMANCE:-0}" = 1 ]; then
            record "Conformance (fast subset)" FAIL "lane skipped but required"
        else
            record "Conformance (fast subset)" SKIP "lane prerequisites missing"
        fi
        return
    fi
    if [ "$lane_rc" != 0 ]; then
        record "Conformance (fast subset)" FAIL "see $tmpdir/conformance.log"
    elif grep -q "=fail" "$tmpdir/conformance.log"; then
        record "Conformance (fast subset)" FAIL "invariant verdicts failed"
    else
        record "Conformance (fast subset)" PASS
    fi
}

run_matrix() {
    local expect="$tmpdir/matrix-expect.txt"
    if VERIFY_EXPECT="$(bash scripts/ngit-matrix-expectations.sh .ngit/act/workflows/build-package-*.yml 2>"$tmpdir/matrix.log")" \
        python3 - "$expect" <<'PY'
import json, os, sys
expect = os.environ["VERIFY_EXPECT"]
matrix = json.load(open("packaging/ngit-release-matrix.json"))
plan = set()
for shard in matrix.get("shards", []):
    for leg in shard.get("legs", []):
        plan.add((leg["architecture"], leg.get("format", "ipk")))
want = set()
for line in expect.splitlines():
    want.add(tuple(line.strip().split("/")))
open(sys.argv[1], "w").write(f"expect={len(want)} plan={len(plan)} missing={sorted(want - plan)}")
PY
    then
        if grep -q "missing=\[\]" "$expect"; then
            record "Release matrix" PASS "workflow shards match the plan"
        else
            record "Release matrix" FAIL "$(cat "$expect")"
        fi
    else
        record "Release matrix" FAIL "see $tmpdir/matrix.log"
    fi
}

run_repro() {
    if [ "${TOLLGATE_RELEASE_CHECK_REPRO:-binaries}" = "none" ]; then
        record "Reproducibility" SKIP "disabled by env"
        return
    fi
    if make reproducibility-test T="${TOLLGATE_RELEASE_CHECK_REPRO:-binaries}" ARCH="${TOLLGATE_RELEASE_CHECK_REPRO_ARCH:-x86_64}" \
        >"$tmpdir/repro.log" 2>&1; then
        record "Reproducibility" PASS "${TOLLGATE_RELEASE_CHECK_REPRO:-binaries}/${TOLLGATE_RELEASE_CHECK_REPRO_ARCH:-x86_64} byte-identical"
    else
        record "Reproducibility" FAIL "see $tmpdir/repro.log"
    fi
}

run_version() {
    if sh scripts/check-version-sync.sh "$VERSION" >"$tmpdir/version.log" 2>&1; then
        record "Version consistency" PASS "$VERSION"
    else
        record "Version consistency" FAIL "VERSION/CHANGELOG/RELEASE-NOTES/tag disagree — see $tmpdir/version.log"
    fi
}

tmpdir="$(mktemp -d /tmp/tollgate-release-check.XXXXXX)"

echo "== tollgate-wrt release check: $VERSION"
echo "== logs in $tmpdir"
echo

run_go_battery
run_deps_imports
run_contract
run_packaging
run_invariant "Concurrent duplicate invariant" src/merchant \
    -run 'TestPurchaseSessionRefusesConcurrentDuplicateBeforeTheMint|TestPurchaseSessionGuardHoldsThroughTheOutcomeUnknownWindow'
run_invariant "Ambiguous swap output-reuse invariant" src/tollwallet \
    -run 'TestReceive_DroppedSwapResponseNeverReExposesOutputs|TestReceive_WalletUsableAfterDroppedSwapResponse'
run_invariant "Payment/service-or-recovery invariant" src/merchant \
    -run 'TestPaidPurchaseWhoseGateFailsIsOwedThenGrantedExactlyOnce|TestOwedGrantSurvivesRestartAndConverges|TestOwedTimeGrantExpiresPastItsWindow'
run_conformance
run_matrix
run_repro
run_version

echo
echo "=================================="
for line in "${results[@]}"; do
    echo "$line"
done
echo "=================================="
if [ "$overall" = 0 ]; then
    echo "READY FOR HARDWARE: YES ($VERSION)"
else
    echo "READY FOR HARDWARE: NO ($VERSION)"
fi
exit "$overall"
