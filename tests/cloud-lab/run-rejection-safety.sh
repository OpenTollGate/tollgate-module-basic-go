#!/usr/bin/env bash
# run-rejection-safety.sh — three-phase outage-safety e2e: a payment refused
# while the mint is down must not burn the token.
#
# A1 (mint up) mints and stashes the token; A2 (mint stopped, client run
# with --no-deps so compose cannot restart the mint through depends_on)
# pays and expects a refusal without a crash; B (mint back) proves every
# proof UNSPENT at the mint (NUT-07) and that the same token still buys a
# session. The token is handed between phases via .rejection-state.json —
# the same shape as run-keyset-rotation.sh.
#
# Usage (from tests/cloud-lab/):
#   ./run-rejection-safety.sh          # full run, teardown at the end
#   KEEP=1 ./run-rejection-safety.sh   # leave the lab up for inspection
#
# Isolation knobs (see README "Per-checkout project isolation"):
#   COMPOSE_PROJECT_NAME     project isolation
#   CLOUD_LAB_EXTRA_COMPOSE  extra -f override (e.g. strip host ports)
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

COMPOSE=(docker compose -f docker-compose.yml)
if [ -n "${CLOUD_LAB_EXTRA_COMPOSE:-}" ]; then
    COMPOSE+=(-f "$CLOUD_LAB_EXTRA_COMPOSE")
fi

cleanup() {
    # The phases stash a live bearer token into the working tree; an
    # aborted run must not leave ecash sitting in a checkout.
    rm -f "$SCRIPT_DIR/.rejection-state.json"
    if [ "${KEEP:-0}" != "1" ]; then
        "${COMPOSE[@]}" down -v >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT

echo "== Phase A1: mint up — fund and stash the token"
"${COMPOSE[@]}" up -d --build mint mint-fees upstream >/dev/null
REJECTION_LANE=1 "${COMPOSE[@]}" run --rm -e REJECTION_LANE --entrypoint sh client \
    -c 'rm -rf /tests/__pycache__ && cd /tests && python3 -m pytest -q test_rejection_safety.py::TestOutageDoesNotBurnToken -k phase_a1'

echo "== Phase A2: stop the mint — the payment must be refused, not a crash"
"${COMPOSE[@]}" stop mint >/dev/null
REJECTION_LANE=1 "${COMPOSE[@]}" run --rm --no-deps -e REJECTION_LANE --entrypoint sh client \
    -c 'rm -rf /tests/__pycache__ && cd /tests && python3 -m pytest -q test_rejection_safety.py::TestOutageDoesNotBurnToken -k phase_a2'

echo "== Phase B: mint back — the same token must still spend"
"${COMPOSE[@]}" start mint >/dev/null
REJECTION_LANE=1 "${COMPOSE[@]}" run --rm -e REJECTION_LANE --entrypoint sh client \
    -c 'rm -rf /tests/__pycache__ && cd /tests && python3 -m pytest -q test_rejection_safety.py::TestOutageDoesNotBurnToken -k phase_b'

echo "== Rejection-safety run complete"
