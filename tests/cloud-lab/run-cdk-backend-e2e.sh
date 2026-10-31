#!/usr/bin/env bash
# run-cdk-backend-e2e.sh — P4 driver: the module on the CDK wallet backend,
# including the daemon-kill boundary legs that need host-side docker.
#
# Usage:
#   ./run-cdk-backend-e2e.sh            # full run (happy + kill legs)
#   ./run-cdk-backend-e2e.sh happy      # happy-path pytest only
#
# Composition: docker-compose.yml + docker-compose.cdk-backend.yml
# (services: mint, cdk-walletd, upstream-cdk; client runs pytest and the
# helper scripts under its /tests mount).
#
# The kill legs assert the money boundary the campaign is about:
#   A. Payment during a sidecar outage → the module refuses (no session
#      without a confirmed Receive), stays alive, and recovers when the
#      sidecar returns.
#   B. Sidecar killed mid-payment → whatever the outcome, the same token
#      replayed after sidecar restart+recovery must never grant twice.
set -euo pipefail
cd "$(dirname "$0")"

CDK_WALLETD_BIN="${CDK_WALLETD_BIN:-/root/.cargo-target/x86_64-unknown-linux-musl/release/cdk-walletd}"
[ -x "$CDK_WALLETD_BIN" ] || { echo "cdk-walletd binary not executable: $CDK_WALLETD_BIN (set CDK_WALLETD_BIN)"; exit 1; }
export CDK_WALLETD_BIN

MODE="${1:-full}"
COMPOSE="docker compose -f docker-compose.yml -f docker-compose.cdk-backend.yml"

# Pre-build the client explicitly: `compose run client` builds its
# dependencies implicitly WITHOUT honoring build.network on this compose
# version, and a cold dependency build dies on the bench firewall.
build() { $COMPOSE build client mint cdk-walletd upstream-cdk; }
up()     { $COMPOSE up -d --build mint cdk-walletd upstream-cdk "$@"; }
down()   { $COMPOSE down -v --remove-orphans >/dev/null 2>&1 || true; }
pytest() { $COMPOSE run --rm -e UPSTREAM_URL=http://upstream-cdk:2121 --entrypoint pytest client "$@"; }
# --no-deps is LOAD-BEARING in the kill legs: a plain `compose run` starts
# the client's dependencies — including a cdk-walletd we deliberately
# stopped — resurrecting the sidecar before the payment fires and silently
# turning every "outage" leg into a happy-path run (measured: allotment
# kept growing "during" the outage).
pay()    { $COMPOSE run --rm --no-deps -e UPSTREAM_URL=http://upstream-cdk:2121 --entrypoint python3 client /tests/cdk_e2e_pay.py "$@"; }
replay() { $COMPOSE run --rm --no-deps -e UPSTREAM_URL=http://upstream-cdk:2121 --entrypoint python3 client /tests/cdk_e2e_replay.py "$@"; }

wait_healthy() {  # wait_healthy <container> <service>
  local c="$1" svc="$2" i=0
  until [ "$(docker inspect -f '{{.State.Health.Status}}' "$c" 2>/dev/null || echo starting)" = healthy ]; do
    i=$((i+1)); [ $i -gt 90 ] && { echo "$c never became healthy"; $COMPOSE logs "$svc" 2>&1 | tail -40; return 1; }
    sleep 1
  done
}

dump_logs() {
  mkdir -p /tmp/cdk-e2e-logs
  $COMPOSE logs upstream-cdk > /tmp/cdk-e2e-logs/module.log 2>&1 || true
  $COMPOSE logs cdk-walletd   > /tmp/cdk-e2e-logs/sidecar.log 2>&1 || true
  docker logs walletd-legd    > /tmp/cdk-e2e-logs/legd.log 2>&1 || true
  cp /tmp/cdk-legd-proxy.log  /tmp/cdk-e2e-logs/proxy.log 2>/dev/null || true
}
trap 'dump_logs; down' EXIT

echo "== build + bring up (mint, cdk-walletd, upstream-cdk) =="
build >/dev/null
up >/dev/null
wait_healthy tg-cdk-walletd cdk-walletd
wait_healthy tg-upstream-cdk upstream-cdk

echo "== happy path: full payment on the cdk backend (in-container pytest) =="
pytest -v test_cdk_backend_e2e.py

echo "== service log: backend selection must say cdk =="
# Capture to a file first, then grep: `logs | grep -m1` under pipefail
# SIGPIPEs when grep exits early (same class as the mutation-rig lesson).
$COMPOSE logs upstream-cdk > /tmp/cdk-e2e-service.log 2>&1 || true
grep "Wallet backend selected" /tmp/cdk-e2e-service.log | head -1 | tee /tmp/cdk-e2e-selection.log
grep -q "cdk" /tmp/cdk-e2e-selection.log

echo "== sidecar log: money ops reached the daemon =="
$COMPOSE logs cdk-walletd > /tmp/cdk-e2e-sidecar.log 2>&1 || true
N=$(grep -ciE "receive|mint" /tmp/cdk-e2e-sidecar.log || true)
echo "  (sidecar money-op log lines: $N)"
[ "$N" -gt 0 ]

if [ "$MODE" != "full" ]; then echo "== happy-only mode: done =="; exit 0; fi

quote()  { $COMPOSE run --rm --no-deps -e UPSTREAM_URL=http://upstream-cdk:2121 --entrypoint python3 client /tests/cdk_e2e_quote.py "$@"; }

echo "== LEG C: Lightning quote survives a sidecar restart =="
Q=$(quote create || true)
echo "  quote created → $Q"
QID=$(echo "$Q" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('quote',''))" 2>/dev/null || true)
[ -n "$QID" ] || { echo "FAIL: no quote id from /ln-invoice: $Q"; exit 1; }
docker restart tg-cdk-walletd >/dev/null
wait_healthy tg-cdk-walletd cdk-walletd
sleep 3  # startup recovery pass
P=$(quote poll "$QID" || true)
echo "  post-restart poll → $P"
echo "$P" | grep -qE '"quote".*not|unknown quote|no such quote' && { echo "FAIL: quote lost across sidecar restart"; exit 1; }
echo "$P" | python3 -c "
import json,sys
d=json.load(sys.stdin)
assert d.get('http')==200, f'poll HTTP {d.get("http")}: {d}'
assert d.get('quote')=='$QID' or d.get('quote','')=='', f'poll answered a different quote: {d}'
print('  quote state survived the restart ✓')" || exit 1

echo "== LEG A: payment during sidecar outage =="
docker stop tg-cdk-walletd >/dev/null
A=$(pay --verbose || true)
echo "  outage payment → $A"
echo "$A" | grep -q '"kind": 1022' && { echo "FAIL: session granted while sidecar was DOWN"; exit 1; }
# The refusal must be the wallet-outage class (note NOT spent, safe to
# retry) — never invalid-token, which tells a paying customer their money
# is fake (the decode RPC dies with the daemon; caught 2026-11-01).
echo "$A" | grep -q '"payment-processing-unavailable"' || { echo "FAIL: outage refusal is not payment-processing-unavailable: $A"; exit 1; }
docker inspect -f '{{.State.Status}}' tg-upstream-cdk | grep -q running || { echo "FAIL: module died during sidecar outage"; $COMPOSE logs upstream-cdk | tail -20; exit 1; }
echo "  module stayed alive through the outage ✓"
$COMPOSE up -d cdk-walletd >/dev/null; sleep 2; wait_healthy tg-cdk-walletd cdk-walletd
A2=$(pay --verbose || true)
echo "  post-recovery payment → $A2"
echo "$A2" | grep -q '"kind": 1022' || { echo "FAIL: no session after sidecar recovery"; exit 1; }
echo "  module recovered on sidecar return ✓"

echo "== LEG B: kill sidecar mid-payment → replay must not double-grant =="
EVENT_FILE=".cdk-e2b-event.json"
rm -f "$EVENT_FILE"
pay --event-out "/tests/$EVENT_FILE" >/dev/null 2>&1 || true &
PAYPID=$!
sleep 0.4
docker kill tg-cdk-walletd >/dev/null 2>&1 || true
wait $PAYPID 2>/dev/null || true
[ -f "$EVENT_FILE" ] || { echo "FAIL: leg B never staged its event"; exit 1; }
echo "  sidecar killed mid-payment; event staged ✓"
FIRST_ALL=$(tail -1 /tmp/cdk-e2b-first.out 2>/dev/null || true)
$COMPOSE up -d cdk-walletd >/dev/null; sleep 2; wait_healthy tg-cdk-walletd cdk-walletd
sleep 4  # sidecar startup recovery pass

REPLAY=$(replay --verbose "/tests/$EVENT_FILE" || true)
echo "  first (killed mid-flight) → $(cat /tmp/cdk-e2b-first.out 2>/dev/null | tail -1)"
echo "  replay after restart+recovery → $REPLAY"

# VALUE-BASED verdict. The killed first attempt is genuinely ambiguous:
# if its swap never reached the mint, the proofs are unspent and the
# replay is a legitimate NEW payment (one grant). If the first attempt
# DID complete, the replay must be refused. What can never happen is
# BOTH granting — one token, one swap at the mint. Assert on allotment:
# at most one grant's worth (step_size * 100) of new session across the
# two attempts.
python3 - "$A2" "$FIRST_ALL" "$REPLAY" <<'PY'
import json, sys
STEP = 6_000_000  # step_size 60000 ms * 100 sats, from upstream-cdk-config.json
def parse(x):
    try:
        return json.loads(x)
    except Exception:
        return {}
def allot(d):
    try:
        return int(d["tags"]["allotment"][0]) if d.get("kind") == 1022 else 0
    except Exception:
        return 0
base, first, replay = parse(sys.argv[1]), parse(sys.argv[2]), parse(sys.argv[3])
base_all, first_all, replay_all = allot(base), allot(first), allot(replay)
first_granted, replay_granted = first_all > 0, replay_all > 0
if first_granted and replay_granted:
    raise SystemExit(f"FAIL: DOUBLE GRANT — both attempts granted (allotments {first_all}, {replay_all})")
# Allotment is the session's CUMULATIVE total: a legitimate single grant
# can lift it by at most one payment's worth above the LEG A baseline.
if replay_all > base_all + STEP:
    raise SystemExit(f"FAIL: allotment jumped {base_all} -> {replay_all} (baseline+{STEP} exceeded — more than one grant's worth)")
if first_granted:
    print(f"  first attempt granted before the kill (allotment {first_all}); replay refused ✓")
elif replay_granted:
    print(f"  first attempt settled nowhere; replay was the single legitimate grant (allotment {base_all} -> {replay_all}, bound {base_all + STEP}) ✓")
else:
    print(f"  neither attempt granted (token settled nowhere yet) — acceptable")
PY
rm -f "$EVENT_FILE"

echo "== LEG D: deterministic ambiguity — receive answered by silence =="
# The module's receive must surface as outcome-unknown (do-not-resubmit),
# never a generic "payment failed". A sock proxy at w.sock forwards the
# receive to the daemon, reads the answer, and DROPS it: the swap
# provably executed, the module provably unanswered — no kill race.
DOCKER_NET=$(docker inspect -f '{{range $k,$v := .NetworkSettings.Networks}}{{$k}}{{end}}' tg-cdk-walletd)
docker rm -f walletd-legd >/dev/null 2>&1 || true
docker stop tg-cdk-walletd >/dev/null
rm -f walletd-socket/real.sock
docker run -d --name walletd-legd --network "$DOCKER_NET" \
  -v "$CDK_WALLETD_BIN":/walletd/cdk-walletd:ro \
  -v "$(pwd)/walletd-socket":/run/walletd -v cloud-lab_walletd-data:/data \
  alpine:3.20 /bin/sh -c \
  "mkdir -p /run/walletd /data && exec /walletd/cdk-walletd --socket /run/walletd/real.sock --work-dir /data --mint http://mint:8085 --mnemonic-file /data/seed.txt" >/dev/null
for i in $(seq 1 20); do [ -S walletd-socket/real.sock ] && break; sleep 0.5; done
[ -S walletd-socket/real.sock ] || { echo "FAIL: leg D daemon never bound real.sock"; exit 1; }
python3 "$(pwd)/cdk_e2e_sockproxy.py" walletd-socket/w.sock walletd-socket/real.sock --swallow receive > /tmp/cdk-legd-proxy.log 2>&1 &
PROXY_PID=$!
for i in $(seq 1 20); do [ -S walletd-socket/w.sock ] && break; sleep 0.5; done
EVENT_D=.cdk-e2d-event.json; rm -f "$EVENT_D"
D=$(pay --verbose --event-out "/tests/$EVENT_D" || true)
echo "  swallowed receive payment → $D"
echo "$D" | grep -q '"kind": 21023' || { echo "FAIL: expected a notice, got: $D"; kill $PROXY_PID 2>/dev/null; exit 1; }
echo "$D" | grep -q '"payment-outcome-unknown"' && echo "  notice code = payment-outcome-unknown (do-not-resubmit) ✓" || { echo "FAIL: notice is NOT the outcome-unknown class — check code: $D"; kill $PROXY_PID 2>/dev/null; exit 1; }
grep -q SWALLOWED /tmp/cdk-legd-proxy.log || { echo "FAIL: proxy never swallowed a receive (leg did not exercise the path)"; kill $PROXY_PID 2>/dev/null; exit 1; }
# The daemon DID execute the swap (deterministic): the proofs are spent.
# The replay must be refused — and must never grant.
sleep 1
DR=$(replay --verbose "/tests/$EVENT_D" || true)
echo "  replay of the deterministically-spent token → $DR"
echo "$DR" | grep -q '"kind": 1022' && { echo "FAIL: DOUBLE GRANT after a swallowed receive"; kill $PROXY_PID 2>/dev/null; exit 1; }
echo "  spent-token replay refused, no double grant ✓"
rm -f "$EVENT_D"
kill $PROXY_PID 2>/dev/null || true
docker rm -f walletd-legd >/dev/null 2>&1 || true
$COMPOSE up -d cdk-walletd >/dev/null; sleep 2; wait_healthy tg-cdk-walletd cdk-walletd
D2=$(pay || true)
echo "  post-leg module recovered on the direct socket → $D2"
echo "$D2" | grep -q '"kind": 1022' || { echo "FAIL: module did not recover after LEG D"; exit 1; }

echo ""
echo "ALL P4 E2E LEGS PASSED"
