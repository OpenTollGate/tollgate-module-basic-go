#!/usr/bin/env bash
# run-lnurlcash-lane.sh — the LNURLcash opt-in integration lane.
#
# Runs the gate with the method ENABLED, a real mint, and a fake issuer, then
# asserts the boundary that unit tests can only stub: an issuer that accepts but
# pays nothing must not produce an access grant.
#
# Bench discipline: `hw-bench-lease exec` acquires the machine-global mutex, runs
# the lane, and releases it on exit. Use `exec`, never `hold` — `hold` blocks
# until interrupted by design (it is the interactive-window form), so a script
# that calls it deadlocks while appearing to work. A leaked lease blocks the next
# worker, which has cost more time on this project than any flake.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROFILE=lnurlcash
COMPOSE="${COMPOSE:-podman-compose}"
LEASE="$HOME/prta/scripts/hw-bench-lease"

if [ -x "$LEASE" ]; then
    echo "--- lane under the bench mutex ---"
    set +e
    "$LEASE" exec \
        --purpose "LNURLcash opt-in integration lane (PR #1268)" \
        --task "lnurlcash-lane" \
        --resource "shared bench (MT3000 + ai-legion lab)" \
        -- bash "$HERE/lnurlcash-lane-body.sh"
    rc=$?
    set -e
    "$COMPOSE" -f "$HERE/docker-compose.yml" --profile "$PROFILE" down -v >/dev/null 2>&1 || true
    exit "$rc"
else
    echo "WARN: $LEASE not found — running the lane WITHOUT the bench mutex" >&2
    exec bash "$HERE/lnurlcash-lane-body.sh"
fi
