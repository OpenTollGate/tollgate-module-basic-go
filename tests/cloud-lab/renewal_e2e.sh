#!/usr/bin/env bash
# renewal_e2e.sh — host-side end-to-end driver for the #430 bytes-renewal fix.
#
# Runs the bytes-metered two-router lab (docker-compose.bytes.yml) against
# the CURRENT WORKTREE's source and asserts the renewal behavior:
#
#   P1  fund the reseller (client pays reseller)
#   P2  trigger upstream discovery (default route + address event in the
#       reseller container; no WiFi in Docker)
#   P3  initial autopay purchase: allotment = 5 x 22,020,096 = 110,100,480
#   P4  THE #430 CHECK: at near-zero usage for 60 s, NO renewal may fire
#       (unfixed builds renew within ~15 s and double the allotment)
#   P5  drive usage past half the allotment: exactly ONE renewal fires,
#       allotment doubles to 220,200,960
#   P6  drive usage further: exactly one more renewal, allotment
#       330,301,440; reseller still healthy
#
# The clamp announcement ("clamped" log lines) is counted per phase: fixed
# builds announce once per distinct effective offset (3 total across P4-P6),
# the pre-fix build announces nothing, and a per-poll announcement would
# show up as dozens (the log-spam regression).
#
# Usage: bash renewal_e2e.sh <label>    (from tests/cloud-lab)
# Env:   E2E_QUICK_OBSERVE=30   shorten P4 (only for the baseline repro)

set -u

LABEL=${1:?usage: renewal_e2e.sh <label>}
OBSERVE_SECONDS=${E2E_QUICK_OBSERVE:-60}

BASE_COMPOSE_FILE="docker-compose.yml"
BYTES_COMPOSE_FILE="docker-compose.bytes.yml"
BASE="docker compose -f $BASE_COMPOSE_FILE"
FULL="docker compose -f $BASE_COMPOSE_FILE -f $BYTES_COMPOSE_FILE"
# Host-port override support: -f disables auto-loading of
# docker-compose.override.yml, so append it explicitly when present.
[ -f docker-compose.override.yml ] && FULL="$FULL -f docker-compose.override.yml"

RESELLER="tg-reseller"
UPSTREAM="tg-upstream"
RESELLER_WAN=172.29.0.5

STEP=22020096
ALLOT1=$((5 * STEP))            # 110100480
ALLOT2=$((10 * STEP))           # 220200960
ALLOT3=$((15 * STEP))           # 330301440
HALF=$((ALLOT1 / 2))
# fake-ndsctl reports downloaded+uploaded in KB; baseline is 1024+512 KB.
# usage = (down+up)*1024 - (1024+512)*1024; target usage = HALF + ~4 MiB
TARGET_USAGE=$((HALF + 4 * 1024 * 1024))
DRIVE_KB=$(( (TARGET_USAGE + 1536 * 1024) / 1024 ))

PASS=0
FAIL=0
RESLOG="renewal_e2e-$LABEL.log"

note() { printf '\n== [%s] %s\n' "$LABEL" "$*"; }
ok()   { PASS=$((PASS + 1)); printf 'PASS: %s\n' "$*"; }
bad()  { FAIL=$((FAIL + 1)); printf 'FAIL: %s\n' "$*"; }

reseller_logs() { docker logs "$RESELLER" 2>&1; }
count_since() {  # count_since <pattern> <baseline-count>
    local now
    now=$(reseller_logs | grep -c "$1" || true)
    echo $((now - $2))
}

usage_of_reseller() {  # query upstream /usage for the reseller MAC
    docker exec "$UPSTREAM" curl -sf -H "X-Real-Ip: $RESELLER_WAN" \
        "http://localhost:2121/usage" || echo "query-failed"
}

wait_allotment() {  # wait_allotment <expected> <timeout_s> <what>
    local expected=$1 timeout=$2 what=$3 out=""
    local deadline=$((SECONDS + timeout))
    while [ $SECONDS -lt $deadline ]; do
        out=$(usage_of_reseller)
        if [ "$out" = "$expected/$expected" ] || [ "$out" = "0/$expected" ] \
           || [ "${out#*/}" = "$expected" ]; then
            ok "$what: allotment=$expected (usage endpoint: $out)"
            return 0
        fi
        sleep 2
    done
    bad "$what: never reached allotment=$expected (last: $out)"
    return 1
}

note "P0 build + start (mint, upstream, reseller — bytes lab)"
$FULL down -v --remove-orphans >/dev/null 2>&1 || true
if ! $FULL --profile two-router up -d --build >>"$RESLOG" 2>&1; then
    bad "compose up failed (see $RESLOG)"
    exit 1
fi
for i in $(seq 1 60); do
    m=$(curl -sf "http://127.0.0.1:${E2E_MINT_PORT:-8085}/v1/keys" >/dev/null 2>&1 && echo ok || true)
    u=$(docker exec "$UPSTREAM" curl -sf http://localhost:2121/ >/dev/null 2>&1 && echo ok || true)
    r=$(docker exec "$RESELLER" curl -sf http://localhost:2121/ >/dev/null 2>&1 && echo ok || true)
    [ -n "$m" ] && [ -n "$u" ] && [ -n "$r" ] && break
    sleep 5
done
if [ -z "${m:-}" ] || [ -z "${u:-}" ] || [ -z "${r:-}" ]; then
    bad "services did not become healthy (mint=${m:-no} upstream=${u:-no} reseller=${r:-no})"
    exit 1
fi
ok "services healthy (mint, upstream, reseller)"

note "P1 fund the reseller (client pays 200 sats)"
FUND_OUT="fund-$LABEL.out"
# $FULL, never $BASE: a base-only `compose run` recreates the dependencies
# from the milliseconds config and reverts the bytes lab (see the header
# of docker-compose.bytes.yml).
if $FULL run --rm --entrypoint python3 client fund_reseller.py \
        >"$FUND_OUT" 2>&1 && grep -q "FUND-OK" "$FUND_OUT"; then
    ok "reseller funded"
    cat "$FUND_OUT" >>"$RESLOG"
else
    bad "funding failed (see $FUND_OUT / $RESLOG)"
    cat "$FUND_OUT" >>"$RESLOG" 2>/dev/null || true
    exit 1
fi
BAL=$(docker exec "$RESELLER" curl -s http://localhost:2121/balance || true)
echo "reseller balance: $BAL" >>"$RESLOG"

note "P2 nudge discovery (the periodic cycle retries after funding)"
# Session creation failed at startup (no funds yet); the detector retries
# via polling. An address event accelerates the next attempt.
docker exec "$RESELLER" ip addr add 172.29.0.6/24 dev eth1 >>"$RESLOG" 2>&1 || true
ok "discovery nudge applied"

note "P3 wait for initial autopay purchase"
B_SEND=$(reseller_logs | grep -c "Sending payment to upstream" || true)
B_TRIG=$(reseller_logs | grep -c "Renewal threshold reached" || true)
B_CLAMP=$(reseller_logs | grep -c "clamped" || true)
if wait_allotment "$ALLOT1" 120 "P3 initial purchase"; then
    SENDS0=$(count_since "Sending payment to upstream" "$B_SEND")
    [ "$SENDS0" -eq 1 ] && ok "P3 exactly one payment sent ($SENDS0)" \
                         || bad "P3 expected 1 payment, saw $SENDS0"
else
    note "P3 FAILED — dumping reseller log tail for diagnosis"
    reseller_logs | tail -80 >>"$RESLOG"
    printf '\nRESULTS %s: PASS=%d FAIL=%d\n' "$LABEL" "$PASS" "$FAIL"
    exit 1
fi

note "P4 #430 core: observe ${OBSERVE_SECONDS}s at near-zero usage"
sleep "$OBSERVE_SECONDS"
sleep 3  # let in-flight log lines and payments settle before counting
SENDS_P4=$(count_since "Sending payment to upstream" "$B_SEND")
TRIG_P4=$(count_since "Renewal threshold reached" "$B_TRIG")
NOSESS_P4=$(count_since "No session exists" "$B_TRIG")  # same baseline is fine: count must stay 0
CLAMP_P4=$(count_since "clamped" "$B_CLAMP")
ALLOT_END_P4=$(usage_of_reseller)
echo "P4 sends=$SENDS_P4 triggers=$TRIG_P4 nossess=$NOSESS_P4 clamps=$CLAMP_P4 usage=$ALLOT_END_P4" >>"$RESLOG"
# The race-free invariant: at near-zero usage the allotment must not grow.
if [ "${ALLOT_END_P4#*/}" = "$ALLOT1" ]; then
    ok "P4 allotment unchanged through the near-zero window ($ALLOT_END_P4)"
elif [ "${ALLOT_END_P4#*/}" -gt "$ALLOT1" ] 2>/dev/null; then
    bad "P4 allotment grew at near-zero usage: $ALLOT_END_P4 — #430 reproduced"
else
    bad "P4 allotment query failed: $ALLOT_END_P4"
fi
if [ "$TRIG_P4" -eq 0 ]; then
    ok "P4 no renewal trigger at near-zero usage (triggers=$TRIG_P4 sends=$SENDS_P4 nossess=$NOSESS_P4)"
elif [ "$TRIG_P4" -ge 1 ]; then
    bad "P4 renewal(s) fired at near-zero usage: triggers=$TRIG_P4 sends=$SENDS_P4 — #430 reproduced"
else
    bad "P4 unexpected state: triggers=$TRIG_P4 sends=$SENDS_P4"
fi
if [ "$CLAMP_P4" -ge 1 ] && [ "$CLAMP_P4" -le 2 ]; then
    ok "P4 clamp announced sparingly ($CLAMP_P4)"
elif [ "$CLAMP_P4" -gt 2 ]; then
    bad "P4 clamp announcement spam: $CLAMP_P4 in ${OBSERVE_SECONDS}s"
else
    note "P4 no clamp announcement (pre-fix build)"
fi

B_SEND=$(reseller_logs | grep -c "Sending payment to upstream" || true)
B_TRIG=$(reseller_logs | grep -c "Renewal threshold reached" || true)
B_CLAMP=$(reseller_logs | grep -c "clamped" || true)

note "P5 drive usage past half the allotment (${DRIVE_KB} KB reported)"
docker exec "$UPSTREAM" sh -c "echo '$DRIVE_KB 512' > /tmp/fake-nds-usage"
P4_CLEAN=1
if [ "$TRIG_P4" -ne 0 ] || [ "$SENDS_P4" -ne 0 ]; then
    P4_CLEAN=0
    note "P5 renewal expectation relaxed: P4 already reproduced #430 (allotment pre-doubled)"
fi
DEADLINE=$((SECONDS + 90))
FIRED=0
while [ $SECONDS -lt $DEADLINE ]; do
    TRIG=$(count_since "Renewal threshold reached" "$B_TRIG")
    if [ "$TRIG" -ge 1 ]; then FIRED=1; break; fi
    sleep 2
done
sleep 10  # let the purchase complete and settle
SENDS_P5=$(count_since "Sending payment to upstream" "$B_SEND")
TRIG_P5=$(count_since "Renewal threshold reached" "$B_TRIG")
CLAMP_P5=$(count_since "clamped" "$B_CLAMP")
echo "P5 sends=$SENDS_P5 triggers=$TRIG_P5 clamps=$CLAMP_P5" >>"$RESLOG"
if [ "$P4_CLEAN" -eq 1 ]; then
    if [ "$FIRED" -eq 1 ] && [ "$SENDS_P5" -eq 1 ]; then
        ok "P5 exactly one renewal near the limit (triggers=$TRIG_P5 sends=$SENDS_P5)"
    else
        bad "P5 renewal near limit: triggers=$TRIG_P5 sends=$SENDS_P5 FIRED=$FIRED"
    fi
elif [ "$FIRED" -eq 1 ]; then
    ok "P5 renewal still fires near limit even after P4's spurious double (buggy-build relaxation)"
else
    note "P5 no renewal on buggy build (drive did not cross the pre-doubled threshold) — informational"
fi
wait_allotment "$ALLOT2" 60 "P5 allotment doubled" || true
[ "$CLAMP_P5" -le 2 ] && ok "P5 clamp announcements bounded ($CLAMP_P5)" \
                     || bad "P5 clamp spam ($CLAMP_P5)"

B_SEND=$(reseller_logs | grep -c "Sending payment to upstream" || true)
B_TRIG=$(reseller_logs | grep -c "Renewal threshold reached" || true)

note "P6 drive usage further — one more controlled renewal"
# target usage ~ ALLOT2 - 0.6*ALLOT1 (past half of ALLOT2, within its clamp)
TARGET2=$((ALLOT2 - 6 * STEP / 10))
DRIVE2_KB=$(( (TARGET2 + 1536 * 1024) / 1024 ))
docker exec "$UPSTREAM" sh -c "echo '$DRIVE2_KB 512' > /tmp/fake-nds-usage"
DEADLINE=$((SECONDS + 90))
while [ $SECONDS -lt $DEADLINE ]; do
    TRIG=$(count_since "Renewal threshold reached" "$B_TRIG")
    [ "$TRIG" -ge 1 ] && break
    sleep 2
done
sleep 10
SENDS_P6=$(count_since "Sending payment to upstream" "$B_SEND")
echo "P6 sends=$SENDS_P6" >>"$RESLOG"
[ "$SENDS_P6" -eq 1 ] && ok "P6 exactly one further renewal ($SENDS_P6)" \
                     || bad "P6 expected 1 renewal, saw $SENDS_P6"
wait_allotment "$ALLOT3" 60 "P6 allotment grew to 15 steps" || true

if docker exec "$RESELLER" curl -sf http://localhost:2121/ >/dev/null 2>&1; then
    ok "reseller still healthy at the end"
else
    bad "reseller unhealthy at the end"
fi

note "capturing full container logs for evidence"
reseller_logs >"reseller-$LABEL.full.log" 2>/dev/null || true
docker logs "$UPSTREAM" >"upstream-$LABEL.full.log" 2>&1 || true

$FULL --profile two-router down -v --remove-orphans >>"$RESLOG" 2>&1 || true

printf '\nRESULTS %s: PASS=%d FAIL=%d\n' "$LABEL" "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
