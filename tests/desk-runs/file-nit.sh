#!/usr/bin/env bash
# Open an issue from a desk-run nit, carrying the run's environment
# declaration so every filed defect names where it was seen.
# Usage: file-nit.sh <run-record.md> <issue-title> <evidence-file>
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="${TG_REPO:-OpenTollGate/tollgate-module-basic-go}"
RUN="${1:?usage: file-nit.sh <run-record.md> <title> <evidence-file>}"
TITLE="${2:?missing title}"
EVID="${3:?missing evidence file}"

ENV_BLOCK="$(sed -n '/^## 1\. Environment/,/^## 2\./p' "$RUN" | sed '$d')"
TMP="$(mktemp)"
{
  echo "## Where seen (desk run)"
  echo
  echo "$ENV_BLOCK"
  echo
  echo "---"
  echo
  echo "## Evidence"
  echo
  echo '```'
  cat "$EVID"
  echo '```'
  echo
  echo "---"
  echo
  echo "*Filed by tests/desk-runs/file-nit.sh from \`$(basename "$RUN")\`. The evidence block is verbatim from the run and pre-sanitized per that directory's rules.*"
} > "$TMP"
gh issue create -R "$REPO" --title "$TITLE" --body-file "$TMP"
