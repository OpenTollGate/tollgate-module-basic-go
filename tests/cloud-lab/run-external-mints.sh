#!/usr/bin/env bash
# run-external-mints.sh — live external-mint e2e lane.
#
# Boots the lab plus the profile-gated `upstream-ext` service (whose
# accepted_mints includes https://testnut.cashu.exchange — nutshell's
# main branch with a FakeWallet backend, so minting is free) and runs
# test_external_mints.py against it: real-mint keyset fees, the #409
# below-swap-fee pre-check, and a fee-deducted session credit — the
# full payment path against a mint this repo does not control.
#
# Requires outbound internet from the client container. Real-money
# mints are never touched; if testnut is unreachable every test skips
# with a reason (canary, not gate).
#
# Usage (from tests/cloud-lab/):
#   ./run-external-mints.sh          # full run, teardown at the end
#   KEEP=1 ./run-external-mints.sh   # leave the lab up for inspection
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

cleanup() {
    if [ "${KEEP:-0}" != "1" ]; then
        docker compose --profile external-mints down -v >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT

echo "== Bringing up mint + upstream-ext (accepted_mints includes testnut)"
docker compose --profile external-mints up -d --build mint upstream-ext >/dev/null

echo "== External-mint suite (testnut.cashu.exchange, nutshell main + FakeWallet)"
EXTERNAL_MINTS=1 UPSTREAM_URL=http://upstream-ext:2121 \
    docker compose --profile external-mints run --rm \
    -e EXTERNAL_MINTS -e UPSTREAM_URL \
    --entrypoint sh client \
    -c 'rm -rf /tests/__pycache__ && cd /tests && python3 -m pytest -sv test_external_mints.py'

echo "== External-mint run complete"
