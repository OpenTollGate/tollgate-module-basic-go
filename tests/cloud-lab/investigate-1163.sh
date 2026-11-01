#!/usr/bin/env bash
# investigate-1163.sh — catch a failing gonuts LEG D in the act and discriminate:
# after a grant-despite-swallow failure, re-POST the SAME staged event against
# the LIVE stack. If the replay is refused as SPENT, the mint processed the
# module's swap (it bypassed the armed proxy — a rig/timing question). If the
# replay GRANTS, the proofs were never spent and the module credited a token
# with zero mint involvement (a product finding).
set -uo pipefail
cd "$(dirname "$0")"

C() { docker compose -f docker-compose.yml -f docker-compose.gonuts-backend.yml run --rm --no-deps -e UPSTREAM_URL=http://upstream:2121 -e MINT_URL=http://kp:33390 --entrypoint python3 client "$@"; }

for ATTEMPT in $(seq 1 6); do
  echo "=== attempt $ATTEMPT ==="
  KEEP_STACK=1 BACKEND=gonuts ./run-module-e2e.sh full > /tmp/inv1163-run.log 2>&1
  RC=$?
  echo "driver EXIT=$RC legd_ok=$(grep -c 'do-not-resubmit' /tmp/inv1163-run.log)"
  if [ $RC -eq 0 ]; then
    echo "green run; continuing"
    docker compose -f docker-compose.yml -f docker-compose.gonuts-backend.yml down -v --remove-orphans >/dev/null 2>&1
    sleep 12
    continue
  fi
  if grep -q "expected a notice" /tmp/inv1163-run.log; then
    echo ">>> LEG D failure reproduced — stack is still UP (driver trap only dumps logs)"
    break
  fi
  echo "different failure:"
  grep -E "FAIL" /tmp/inv1163-run.log | head -2
  docker compose -f docker-compose.yml -f docker-compose.gonuts-backend.yml down -v --remove-orphans >/dev/null 2>&1
  sleep 12
done

if ! grep -q "expected a notice" /tmp/inv1163-run.log; then
  echo "no LEG D failure caught in 6 attempts"; exit 0
fi

EV=.module-e2d-event.json
if [ ! -f "$EV" ]; then echo "no staged event left behind"; exit 1; fi
echo "-- the failing payment's response was:"
grep "swallowed swap payment" /tmp/inv1163-run.log | tail -1
echo "-- re-POSTing the SAME event against the live stack --"
C /tests/cdk_e2e_replay.py --verbose "/tests/$EV" 2>&1 | tail -1
echo "-- interpretation: refused/spent => mint saw the swap (proxy bypass/timing);"
echo "   granted => proofs never spent, module credited with zero mint contact --"
