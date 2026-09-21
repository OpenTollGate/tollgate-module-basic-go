#!/usr/bin/env bash
# run-keyset-rotation.sh — two-phase keyset-rotation e2e for the swap-fee path.
#
# Phase A boots mint-rotate with ONE active keyset at input_fee_ppk=100;
# the client mints proofs on it. Phase B recreates the container with that
# keyset EXPIRED (same mnemonic-derived ID) plus a fresh zero-fee active
# keyset; the client then pays with the phase-A proofs — cdk-mintd 0.17.6
# refuses swaps on expired keysets outright, and the test pins that this
# surfaces as a clean classified payment error.
#
# Usage (from tests/cloud-lab/):
#   ./run-keyset-rotation.sh          # full run, teardown at the end
#   KEEP=1 ./run-keyset-rotation.sh   # leave the lab up for inspection
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

PHASE_A_ROTATIONS='[{"unit":"sat","version":"v1","input_fee_ppk":100,"expired":false}]'
PHASE_B_ROTATIONS='[{"unit":"sat","version":"v1","input_fee_ppk":100,"expired":true},{"unit":"sat","version":"v1","input_fee_ppk":0,"expired":false}]'

cleanup() {
    # Phase A stashes a live bearer token into the working tree
    # (.rotation-state.json); an aborted run must not leave ecash — and
    # the expired keyset's id — sitting in a checkout.
    rm -f "$SCRIPT_DIR/.rotation-state.json"
    if [ "${KEEP:-0}" != "1" ]; then
        docker compose down -v >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT

# A container passing its /v1/keys healthcheck is not proof of health: a
# wedged cdk-mintd accepts quotes but never settles them (PRTA #95), which
# would hang every payment test in the phase. The rotation lane restarts
# mint-rotate between phases — exactly the lab-churn pattern that wedged
# PRTA's mint — so probe the real quote->PAID path before each phase.
# Exit 0 settled; 1 wedge (abort — a wedge mid-rotation is a finding, not
# something to retry silently); 2 unreachable.
settle_probe() {
    python3 "$SCRIPT_DIR/probe_mint_settlement.py" "http://localhost:8087"
}

echo "== Phase A: active keyset @ 100 ppk, mint proofs on it"
export MINT_ROTATE_ROTATIONS="$PHASE_A_ROTATIONS"
docker compose up -d --build mint upstream mint-rotate mint-fees >/dev/null
settle_probe
ROTATION_LANE=1 docker compose run --rm -e ROTATION_LANE --entrypoint sh client \
    -c 'rm -rf /tests/__pycache__ && cd /tests && python3 -m pytest -sv test_keyset_rotation.py -k phase_a'

echo "== Phase B: rotate — old keyset expired @ 100 ppk, new active @ 0"
unset MINT_ROTATE_ROTATIONS
export MINT_ROTATE_ROTATIONS="$PHASE_B_ROTATIONS"
docker compose stop mint-rotate >/dev/null
docker compose up -d mint-rotate >/dev/null
settle_probe
ROTATION_LANE=1 docker compose run --rm -e ROTATION_LANE --entrypoint sh client \
    -c 'rm -rf /tests/__pycache__ && cd /tests && python3 -m pytest -sv test_keyset_rotation.py -k "phase_b or Boundaries"'

echo "== Keyset-rotation run complete"
