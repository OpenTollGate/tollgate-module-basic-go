#!/usr/bin/env bash
#
# Run the module suite as a HOST-STATE gate — the engine of test.yml's
# clean-container-test job.
#
# Usage: clean-container-gate.sh   (from anywhere; resolves the repo root)
#
# The job's body runs in a pristine golang:<manifest pin>-bookworm container,
# so a test that is green on the ubuntu-latest runner image and red here is
# coupled to host state (the /etc/tollgate class, $HOME leftovers, runner
# packages on PATH, warm caches) — it passes because of where it runs, not
# because of what it tests.
#
# This gate fails closed:
#   * any failing test NOT listed in
#     tests/contract/clean-container-known-issues.txt is fatal;
#   * a listed failure is printed as TOLERATED with its issue, and the list
#     is the complete, meant-to-shrink tolerance (same contract as the
#     happy-path gate): when the issue closes, the line is deleted and the
#     failure is fatal again by itself;
#   * a module that fails to build (a FAIL with no failed test name) is
#     always fatal — a build cannot be a known issue;
#   * a non-zero go test exit that reports no failed test is fatal too
#     (a harness failure is never tolerated).
#
# The module list mirrors the go-test matrix in test.yml (same nine nested
# modules) plus the main package's testenv run. Intentionally omitted, same
# as the matrix: src/cli, src/upstream_detector and src/upstream_session_manager
# — their go.mods transitively pull the lnd -> ltcsuite/ltcd chain, which only
# builds standalone after an invasive rewrite (the NOTE in test.yml's go-test
# job); they still build under make go-battery's root-module build. Adding
# them here is tracked where the matrix tracks it.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
KNOWN="$ROOT/tests/contract/clean-container-known-issues.txt"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

MODULES=(
  src/config_manager
  src/utils
  src/cmd/tollgate-cli
  src/lightning
  src/merchant
  src/tollgate_protocol
  src/tollwallet
  src/valve
  src/wireless_gateway_manager
)

fatal=0
tolerated=0
# Every "<test> <module>" that failed, one per line.
: >"$WORK/failures.txt"

run_lane() { # run_lane <label> <go test args...>
    local label="$1"
    shift
    echo ""
    echo "== $label"
    if ! go test "$@" >"$WORK/lane.log" 2>&1; then
        local names
        names="$(grep -E '^--- FAIL: ' "$WORK/lane.log" | sed -E 's/^--- FAIL: ([^ ]+).*/\1/' | sort -u || true)"
        if [ -z "$names" ] && [ -s "$WORK/lane.log" ]; then
            sed 's/^/        /' "$WORK/lane.log" | tail -20 >&2
        fi
        if [ -z "$names" ]; then
            echo "🔴 $label: go test exited non-zero with no failed test name (build or harness failure) — always fatal"
            fatal=$((fatal + 1))
            return
        fi
        while IFS= read -r name; do
            # Subtest failures key on their top-level test for tolerance.
            local top="${name%%/*}"
            printf '%s\t%s\n' "$top" "$label" >>"$WORK/failures.txt"
        done <<<"$names"
    else
        echo "✅ $label: green"
    fi
}

echo "clean-container gate: root $ROOT"
echo "toolchain: $(go version)"

for module in "${MODULES[@]}"; do
    run_lane "$module" -C "$ROOT/$module" -count=1 -race ./...
done
run_lane "src (main package, testenv)" -C "$ROOT/src" -count=1 -race -tags testenv .

# --- reconcile against the known-issues list ---------------------------------

# Known-issue entries promise an OPEN issue; enforce it wherever a token
# exists (the GitHub lane). A closed issue whose test still fails keeps
# tolerating silently otherwise — the exact gap the #775 review flagged.
if [ -n "${GH_TOKEN:-}${GITHUB_TOKEN:-}" ] && command -v gh >/dev/null 2>&1; then
    while IFS= read -r line; do
        case "$line" in ''|'#'*) continue ;; esac
        entry_issue="$(printf '%s' "$line" | grep -oE '#[0-9]+' | head -1 | tr -d '#')"
        [ -n "$entry_issue" ] || { echo "🔴 FATAL: known-issue line lacks an issue reference: $line"; fatal=$((fatal + 1)); continue; }
        state="$(gh issue view "$entry_issue" --repo OpenTollGate/tollgate-module-basic-go --json state --jq .state 2>/dev/null || true)"
        if [ "$state" = "CLOSED" ]; then
            echo "🔴 FATAL: #$entry_issue is CLOSED but its test is still tolerated — delete the entry so the failure is fatal again"
            fatal=$((fatal + 1))
        fi
    done <"$KNOWN"
fi

echo ""
echo "== reconciliation"
while IFS=$'\t' read -r test module; do
    [ -n "$test" ] || continue
    if grep -Eq "^${test}\b" "$KNOWN"; then
        issue="$(grep -E "^${test}\b" "$KNOWN" | head -1 | sed -E 's/^[^\t]*\t[^\t]*\t[[:space:]]*//')"
        echo "🟡 TOLERATED (known issue): $test in $module — $issue"
        tolerated=$((tolerated + 1))
    else
        echo "🔴 FATAL: $test failed in $module and is not a known issue — host-state coupling or a regression; file the issue, do not widen the gate"
        fatal=$((fatal + 1))
    fi
done <"$WORK/failures.txt"

# A tolerance entry whose test did NOT fail is stale — the list is meant to
# shrink, so say so loudly (a warning: flaky tests may pass sometimes).
while IFS= read -r line; do
    case "$line" in ''|'#'*) continue ;; esac
    entry_test="$(printf '%s' "$line" | cut -f1)"
    if ! grep -qF "$entry_test" "$WORK/failures.txt"; then
        echo "⚠️  stale tolerance: $entry_test did not fail — delete its line so the failure becomes fatal again"
    fi
done <"$KNOWN"

summary="${GITHUB_STEP_SUMMARY:-/dev/stdout}"
{
    echo "### Clean-container gate"
    echo ""
    echo "| Verdict | Count |"
    echo "|---------|------:|"
    echo "| Fatal failures | $fatal |"
    echo "| Tolerated (known issues) | $tolerated |"
} >>"$summary"

echo ""
if [ "$fatal" -ne 0 ]; then
    echo "❌ clean-container gate: $fatal unlisted failure(s), $tolerated tolerated."
    exit 1
fi
echo "✅ clean-container gate: no unlisted failures ($tolerated tolerated known issue(s))."
exit 0
