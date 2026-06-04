#!/usr/bin/env bash
#
# Run `go test` and append a Markdown summary to the GitHub Actions job summary.
#
# Usage: go-test-summary.sh <label> [go test args...]
#
# The script mirrors test output to stdout (so the live job log is unchanged),
# captures it, and writes a compact pass/fail/skip table plus any failing test
# names to $GITHUB_STEP_SUMMARY. It exits with the underlying `go test` status
# so the job still fails when tests fail.
#
# Portable across GitHub-hosted runners and `act`: depends only on bash, grep
# and tee (no jq/python), and falls back to stdout when $GITHUB_STEP_SUMMARY is
# unset (e.g. when run locally).
set -uo pipefail

label="${1:?usage: go-test-summary.sh <label> [go test args...]}"
shift

out="$(mktemp)"
trap 'rm -f "$out"' EXIT

set -o pipefail
go test "$@" 2>&1 | tee "$out"
status=${PIPESTATUS[0]}

# grep -c exits non-zero when there are no matches; `|| true` keeps the count.
pass=$(grep -c '^[[:space:]]*--- PASS:' "$out" || true)
fail=$(grep -c '^[[:space:]]*--- FAIL:' "$out" || true)
skip=$(grep -c '^[[:space:]]*--- SKIP:' "$out" || true)
notest=$(grep -c '\[no test files\]' "$out" || true)

if [ "$status" -eq 0 ]; then icon="✅"; else icon="❌"; fi

summary="${GITHUB_STEP_SUMMARY:-/dev/stdout}"

{
  echo "### ${icon} ${label}"
  echo ""
  echo "| Passed | Failed | Skipped | Pkgs without tests |"
  echo "|-------:|-------:|--------:|-------------------:|"
  echo "| ${pass} | ${fail} | ${skip} | ${notest} |"
  echo ""
  if [ "$fail" -ne 0 ]; then
    echo "<details><summary>Failing tests</summary>"
    echo ""
    echo '```'
    grep '^[[:space:]]*--- FAIL:' "$out" || true
    echo '```'
    echo ""
    echo "</details>"
    echo ""
  fi
} >> "$summary"

exit "$status"
