#!/usr/bin/env bash
# ngit-matrix-expectations.sh — read the expected (arch, format) pairs out of a
# workflow file's static build matrix.
#
# Why this exists: ngit-ci does not support a dynamic `strategy.matrix` built
# from `needs.<job>.outputs`, so the ngit release pipeline writes its matrix out
# as static YAML. That means no run-time `matrix` output is available for the
# publication gate to read, and hand-maintaining a second copy of the matrix in
# the workflow's env would drift silently — the failure this gate exists to
# catch. Extracting the pairs from the workflow file itself keeps the gate's
# expectations and the build matrix the same single source of truth.
#
# Usage: scripts/ngit-matrix-expectations.sh <workflow-file> [options]
#   --compression none|all   only rows whose `compression` is/with any value
#                            (default: none — the gate verifies the
#                            compression=none announcements, like the GitHub
#                            twin's verify-publication job)
#   --formats ipk,apk        formats to emit (default: ipk,apk)
#
# Prints one `arch/format` per line, sorted and unique, on stdout.
# Everything else (counts, what was filtered, what it could not parse) goes to
# stderr.
#
# Exit codes:
#   0  pairs extracted
#   2  unreadable file, bad option, or ZERO rows extracted — a parser that
#      silently returns nothing would make the gate verify nothing and pass

set -euo pipefail

usage() { echo "usage: scripts/ngit-matrix-expectations.sh <workflow-file> [--compression none|all] [--formats ipk,apk]" >&2; }

FILE="${1:-}"
[ -n "$FILE" ] && [ "$FILE" != "--help" ] || { usage; exit 2; }
shift

COMPRESSION=none
FORMATS=ipk,apk
while [ $# -gt 0 ]; do
  case "$1" in
    --compression) COMPRESSION="${2:?--compression needs a value}"; shift 2 ;;
    --formats)     FORMATS="${2:?--formats needs a value}"; shift 2 ;;
    --help)        usage; exit 0 ;;
    *) echo "ERROR: unknown option: $1" >&2; usage; exit 2 ;;
  esac
done

[ -f "$FILE" ] || { echo "ERROR: no such workflow file: $FILE" >&2; exit 2; }

rows=$(awk '
  /^[ \t]*-[ \t]*\{/ && /architecture:/ {
    line = $0
    arch = ""; fmt = ""; comp = "none"
    if (match(line, /architecture:[ \t]*[^,}]+/)) {
      arch = substr(line, RSTART, RLENGTH); sub(/architecture:[ \t]*/, "", arch); gsub(/[ \t]/, "", arch)
    }
    if (match(line, /format:[ \t]*[^,}]+/)) {
      fmt = substr(line, RSTART, RLENGTH); sub(/format:[ \t]*/, "", fmt); gsub(/[ \t]/, "", fmt)
    }
    if (match(line, /compression:[ \t]*[^,}]+/)) {
      comp = substr(line, RSTART, RLENGTH); sub(/compression:[ \t]*/, "", comp); gsub(/[ \t]/, "", comp)
    }
    if (arch != "" && fmt != "") printf "%s/%s\t%s\n", arch, fmt, comp
  }
' "$FILE")

total=$(printf '%s\n' "$rows" | grep -c . || true)
[ "$total" -gt 0 ] || {
  echo "ERROR: no matrix rows with architecture+format found in $FILE" >&2
  echo "       (refusing to emit an empty expectation set — the gate would verify nothing)" >&2
  exit 2
}

pairs=$(printf '%s\n' "$rows" \
  | awk -F'\t' -v want="$COMPRESSION" -v formats=",$FORMATS," '
      {
        fmt = $1; sub(/.*\//, "", fmt)
        if (($2 == want || want == "all") && index(formats, "," fmt ",") && $1 ~ /(^|\/)(ipk|apk)$/) print $1
      }' \
  | grep -v '^$' | sort -u || true)

kept=$(printf '%s\n' "$pairs" | grep -c . || true)
[ "$kept" -gt 0 ] || {
  echo "ERROR: $total matrix row(s) in $FILE, but none matched compression=$COMPRESSION formats=$FORMATS" >&2
  exit 2
}
echo "matrix rows: $total, emitting $kept (arch/format, compression=$COMPRESSION, formats=$FORMATS)" >&2
printf '%s\n' "$pairs"
