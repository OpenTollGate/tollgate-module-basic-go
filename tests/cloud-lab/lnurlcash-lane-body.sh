#!/usr/bin/env bash
# lnurlcash-lane-body.sh — the work, with no mutex logic.
#
# Split from run-lnurlcash-lane.sh on purpose: the mutex wrapper must hand a
# FILE to hw-bench-lease exec, not a function serialised from its own shell. A
# `declare -f` copy loses the wrapper's variables, which produced a lane that
# printed its banners and ran nothing — the worst kind of green.
#
# Readiness is POLLED here rather than expressed as `condition: service_healthy`
# in the compose file: podman-compose drives healthchecks through systemd user
# units, which a rootless session does not have, so a health condition never
# resolves and the lane sits in "starting" indefinitely. That is how an earlier
# run of this lane burned 48 minutes and executed no assertions at all.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROFILE=lnurlcash
COMPOSE="${COMPOSE:-podman-compose}"

cd "$HERE"

teardown() {
    "$COMPOSE" -f docker-compose.yml --profile "$PROFILE" down -v >/dev/null 2>&1 || true
}
trap teardown EXIT INT TERM

READY_TIMEOUT="${READY_TIMEOUT:-120}"

# wait_http URL NAME — poll until the URL answers at all, or fail with the
# container's own log tail. A lane that hangs is worse than one that fails.
wait_http() {
    local url="$1" name="$2" deadline
    deadline=$(( $(date +%s) + READY_TIMEOUT ))
    while [ "$(date +%s)" -lt "$deadline" ]; do
        if "$COMPOSE" -f docker-compose.yml --profile "$PROFILE" \
               exec -T "$name" curl -sf --max-time 2 "$url" >/dev/null 2>&1; then
            echo "  ready: $name ($url)"
            return 0
        fi
        sleep 2
    done
    echo "ERROR: $name did not answer $url within ${READY_TIMEOUT}s" >&2
    "$COMPOSE" -f docker-compose.yml --profile "$PROFILE" logs --tail=60 "$name" >&2 || true
    return 1
}

echo "--- build ---"
"$COMPOSE" -f docker-compose.yml --profile "$PROFILE" build

echo "--- up (mint + fake issuer + gate) ---"
"$COMPOSE" -f docker-compose.yml --profile "$PROFILE" up -d \
    mint fake-issuer upstream-lnurlcash

echo "--- wait for readiness ---"
wait_http "http://127.0.0.1:8085/v1/keys" "mint"
wait_http "http://127.0.0.1:8099/_state" "fake-issuer"
wait_http "http://127.0.0.1:2121/" "upstream-lnurlcash"

echo "--- run the lane's assertions ---"
# The log-leak assertion reads the gate's log from the HOST side: `podman logs`
# does not exist inside the test-runner container, and shelling out to it there
# failed in a way that looked like a leak. Dump it to a file the runner mounts.
GATE_LOG="$HERE/.lane-gate.log"
"$COMPOSE" -f docker-compose.yml --profile "$PROFILE" logs --no-color upstream-lnurlcash > "$GATE_LOG" 2>&1 || true

set +e
LNURLCASH_GATE_LOG=/tests/.lane-gate.log \
"$COMPOSE" -f docker-compose.yml --profile "$PROFILE" run --rm client-lnurlcash | tee "$HERE/.lane-result.log"
rc=$?
set -e

# A skipped security assertion is not a passing one. The log-leak check is the
# only thing standing between a bearer secret and an operator's bug report, so a
# SKIP here fails the lane.
if grep -q 'SKIPPED' "$HERE/.lane-result.log"; then
    echo "ERROR: the lane SKIPPED an assertion — a skipped check is not a passing one" >&2
    grep 'SKIPPED' "$HERE/.lane-result.log" >&2
    rc=1
fi

if [ "$rc" -ne 0 ]; then
    echo "--- gate log (for the failure) ---"
    "$COMPOSE" -f docker-compose.yml --profile "$PROFILE" logs --tail=200 upstream-lnurlcash || true
fi
exit "$rc"
