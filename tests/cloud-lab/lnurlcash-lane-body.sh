#!/usr/bin/env bash
# lnurlcash-lane-body.sh — the work, with no mutex logic.
#
# Split from run-lnurlcash-lane.sh on purpose: the mutex wrapper must hand a
# FILE to hw-bench-lease exec, not a function serialised from its own shell. A
# `declare -f` copy loses the wrapper's variables, which produced a lane that
# printed its banners and ran nothing — the worst kind of green.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROFILE=lnurlcash
COMPOSE="${COMPOSE:-podman-compose}"

cd "$HERE"

teardown() {
    "$COMPOSE" -f docker-compose.yml --profile "$PROFILE" down -v >/dev/null 2>&1 || true
}
trap teardown EXIT INT TERM

# Build first so a compile error is reported as a compile error, not as a
# container that never became healthy.
echo "--- build ---"
"$COMPOSE" -f docker-compose.yml --profile "$PROFILE" build

echo "--- up (mint + fake issuer + gate) ---"
"$COMPOSE" -f docker-compose.yml --profile "$PROFILE" up -d \
    mint fake-issuer upstream-lnurlcash

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
