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
set +e
"$COMPOSE" -f docker-compose.yml --profile "$PROFILE" run --rm client-lnurlcash
rc=$?
set -e

if [ "$rc" -ne 0 ]; then
    echo "--- gate log (for the failure) ---"
    "$COMPOSE" -f docker-compose.yml --profile "$PROFILE" logs --tail=200 upstream-lnurlcash || true
fi
exit "$rc"
