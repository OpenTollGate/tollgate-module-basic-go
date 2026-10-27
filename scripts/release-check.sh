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
#   TOLLGATE_RELEASE_CHECK_INSTALLER_{ARTIFACT,SSH_KEY,HOST,PORT,SERIAL}
#     run the installer-contract lane (pristine 25.12 apk VM, #892) as part
#     of the gate; without ARTIFACT+SSH_KEY it SKIPs — set
#     TOLLGATE_RELEASE_CHECK_INSTALLER=1 to make that skip a failure on the
#     machine that owns the lane.
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
        record "Go battery" PASS "18 modules, gofmt/vet/build/-race"
    else
        record "Go battery" FAIL "see $tmpdir/go-battery.log"
    fi
}

# The cold-sums leg (the #1036 class): go.sum gaps only surface when the
# module graph resolves without a warm build cache — every green lane that
# reused built packages missed the v0.6.0-era main-branch gap for exactly
# that reason. A scratch GOMODCACHE+GOCACHE rebuild of every module is the
# only lane-shaped way to prove the sums complete; `go mod tidy -diff` in
# the pre-commit hook is the cheap commit-time shadow of the same fence.
run_cold_sums() {
    local scratch
    scratch="$(mktemp -d -t tg-coldsums.XXXXXX)" || { record "Cold sums" FAIL "mktemp"; return; }
    if env GOMODCACHE="$scratch/mod" GOCACHE="$scratch/cache" make go-battery \
        >"$tmpdir/coldsums.log" 2>&1; then
        record "Cold sums" PASS "18-module battery, scratch module+build caches"
    else
        record "Cold sums" FAIL "see $tmpdir/coldsums.log (cold go.sum gap — run 'go mod tidy' in the named module)"
    fi
    rm -rf "$scratch"
}

run_deps_imports() {
    local ok=0
    python3 tests/contract/check-deps-sync.py >"$tmpdir/deps.log" 2>&1 || ok=1
    python3 tests/contract/check-mirror-sync.py >>"$tmpdir/deps.log" 2>&1 || ok=1
    python3 tests/contract/check-import-paths.py >>"$tmpdir/deps.log" 2>&1 || ok=1
    # The go.sum fence (#976): a merge can leave importers' go.sum
    # incomplete (#975 class) and every other gate stays green — the
    # per-module `go build` probe is the only thing that catches it.
    bash tests/contract/check-gosum-completeness.sh >>"$tmpdir/deps.log" 2>&1 || ok=1
    # The gonuts pin: identity offline, plus the --latest release-time verdict
    # (#791) — a release is not cut while the wallet fork has a newer stable
    # tag than the one every carrier pins (the #705-in-v0.13.2 near-miss).
    python3 tests/contract/check-gonuts-pin.py >>"$tmpdir/deps.log" 2>&1 || ok=1
    python3 tests/contract/check-gonuts-pin.py --latest >>"$tmpdir/deps.log" 2>&1 || ok=1
    # The captive-portal daemon: identity offline + the live-feed leg (the
    # release-time verdict that the manifest still names what each era's feed
    # builds). Same shape as the gonuts pair above, same reason: a pin that
    # names a package the feed no longer publishes is a release blocker, and
    # a fence that cannot look must not pass.
    python3 tests/contract/check-nodogsplash-pin.py >>"$tmpdir/deps.log" 2>&1 || ok=1
    python3 tests/contract/check-nodogsplash-pin.py --feed >>"$tmpdir/deps.log" 2>&1 || ok=1
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

run_installer_contract() {
    # The pristine apk-era install contract on a real VM (#892) — the same
    # skip/require shape as the conformance lane: it SKIPs when its
    # prerequisites (a built .apk + VM access) are absent unless
    # TOLLGATE_RELEASE_CHECK_INSTALLER=1 makes the skip a failure.
    # Known-reds are kept visible on purpose (a permanently green lane that
    # hides known failures stops being read): the lane's ICCHECK/ICFAILED
    # output names them — see tests/installer-contract/README.md.
    if [ "${TOLLGATE_RELEASE_CHECK_SKIP_INSTALLER:-0}" = 1 ]; then
        record "Installer contract (pristine apk VM)" SKIP "disabled by env"
        return
    fi
    local artifact="${TOLLGATE_RELEASE_CHECK_INSTALLER_ARTIFACT:-}"
    local key="${TOLLGATE_RELEASE_CHECK_INSTALLER_SSH_KEY:-}"
    if [ -z "$artifact" ] || [ ! -f "$artifact" ] || [ -z "$key" ]; then
        if [ "${TOLLGATE_RELEASE_CHECK_INSTALLER:-0}" = 1 ]; then
            record "Installer contract (pristine apk VM)" FAIL "required but prerequisites missing: set TOLLGATE_RELEASE_CHECK_INSTALLER_ARTIFACT (built .apk) and TOLLGATE_RELEASE_CHECK_INSTALLER_SSH_KEY — see tests/installer-contract/README.md"
        else
            record "Installer contract (pristine apk VM)" SKIP "lane prerequisites missing (built .apk + VM key) — set TOLLGATE_RELEASE_CHECK_INSTALLER=1 to require it; see tests/installer-contract/README.md"
        fi
        return
    fi
    local args=(--artifact "$artifact" --host "${TOLLGATE_RELEASE_CHECK_INSTALLER_HOST:-127.0.0.1}"
        --port "${TOLLGATE_RELEASE_CHECK_INSTALLER_PORT:-2222}" --ssh-key "$key"
        --source-sha "$(git -C "$ROOT" rev-parse HEAD)" --out "$tmpdir/installer-contract")
    [ -n "${TOLLGATE_RELEASE_CHECK_INSTALLER_SERIAL:-}" ] && args+=(--serial "$TOLLGATE_RELEASE_CHECK_INSTALLER_SERIAL")
    if bash "$ROOT/tests/installer-contract/run.sh" "${args[@]}" >"$tmpdir/installer-contract.log" 2>&1; then
        record "Installer contract (pristine apk VM)" PASS
    else
        record "Installer contract (pristine apk VM)" FAIL "see $tmpdir/installer-contract.log (ICFAILED names the reds — the documented known-reds stay red by design)"
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

run_fold_state() {
    # The release-fold fence (docs/release-process.md): a FINAL version must
    # be folded (own section, drained fragments, current notes); pre-releases
    # warn-only. Same command CI/hooks can run: check-release-fold-state.py.
    local out
    if out=$(python3 tests/contract/check-release-fold-state.py "$VERSION" 2>&1); then
        case "$out" in
            *WARN*) record "Changelog fold state" PASS "pre-release: $(echo "$out" | head -1 | sed 's/WARN: //')" ;;
            *) record "Changelog fold state" PASS "folded" ;;
        esac
    else
        record "Changelog fold state" FAIL "$out"
    fi
}

run_fold_state
run_go_battery
run_cold_sums
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
run_installer_contract
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
