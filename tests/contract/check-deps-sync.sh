#!/usr/bin/env bash
# Wrapper for check-deps-sync.py — works as pre-commit hook or CI step.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$SCRIPT_DIR/check-deps-sync.py" "$@"
