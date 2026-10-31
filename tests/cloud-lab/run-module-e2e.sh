#!/usr/bin/env bash
# run-module-e2e.sh — the module-level e2e conformance matrix driver.
#
# Usage:
#   BACKEND=cdk    ./run-module-e2e.sh [full|happy]   # sidecar backend (default)
#   BACKEND=gonuts ./run-module-e2e.sh [full|happy]   # default in-process backend
#
# One driver, two backends, the SAME leg logic — the conformance matrix:
# whatever leg each backend can express runs against it, and the verdicts
# are identical (value-based, never response-code-shaped).
#
#   LEG A  payment during the money-dependency outage → refused, module
#          survives, recovers. cdk: the sidecar is stopped (expect the
#          payment-processing-unavailable class — the decode RPC dies with
#          the daemon). gonuts: the MINT is stopped (decode is local; the
#          refusal comes from Receive's unreachable classification).
#   LEG B  money-dependency killed mid-payment → value-based verdict: at
#          most one grant's worth of allotment across first+replay, never
#          both granting.
#   LEG C  a Lightning quote survives the money-dependency's restart
#          (cdk: sidecar restart; gonuts: mint restart — the quote record
#          lives mint-side either way).
#   LEG D  deterministic ambiguity (cdk only): a unix-socket proxy drops
#          the daemon's answer to receive — executed-but-unanswered with
#          no kill race. The gonuts ambiguity class is covered by the
#          wallet-layer parity lane (three-backend) and the
#          purchasesession_outcome_unknown unit tests; there is no socket
#          to proxy on the in-process backend.
set -euo pipefail
cd "$(dirname "$0")"

BACKEND="${BACKEND:-cdk}"
MODE="${1:-full}"

case "$BACKEND" in
  cdk)
    OVERRIDE=docker-compose.cdk-backend.yml
    SVC=upstream-cdk;      CONT=tg-upstream-cdk
    URL=http://upstream-cdk:2121
    DEP=cdk-walletd;       DEPC=tg-cdk-walletd
    UPSET="mint cdk-walletd upstream-cdk"
    BUILDSET="client mint cdk-walletd upstream-cdk"
    ;;
  gonuts)
    OVERRIDE=docker-compose.gonuts-backend.yml
    SVC=upstream;          CONT=tg-upstream
    URL=http://upstream:2121
    DEP=mint;              DEPC=tg-mint
    UPSET="mint upstream"
    BUILDSET="client mint upstream"
    ;;
  *) echo "BACKEND must be cdk or gonuts (got: $BACKEND)"; exit 1 ;;
esac

CDK_WALLETD_BIN="${CDK_WALLETD_BIN:-/root/.cargo-target/x86_64-unknown-linux-musl/release/cdk-walletd}"
if [ "$BACKEND" = cdk ]; then
  [ -x "$CDK_WALLETD_BIN" ] || { echo "cdk-walletd binary not executable: $CDK_WALLETD_BIN (set CDK_WALLETD_BIN)"; exit 1; }
  export CDK_WALLETD_BIN
fi

COMPOSE="docker compose -f docker-compose.yml -f $OVERRIDE"

# Pre-build the client explicitly: `compose run client` builds its
# dependencies implicitly WITHOUT honoring build.network on this compose
# version, and a cold dependency build dies on the bench firewall.
build() { $COMPOSE build $BUILDSET; }
up()     { $COMPOSE up -d --build $UPSET "$@"; }
down()   { $COMPOSE down -v --remove-orphans >/dev/null 2>&1 || true; }
depmode(){ $COMPOSE up -d "$DEP" >/dev/null; }
pytest() { $COMPOSE run --rm -e UPSTREAM_URL=$URL --entrypoint pytest client "$@"; }
# --no-deps is LOAD-BEARING in the kill legs: a plain `compose run` starts
# the client's dependencies — including a money dependency we deliberately
# stopped — resurrecting it before the payment fires and silently turning
# every "outage" leg into a happy-path run (measured: allotment kept
# growing "during" the outage).
pay()    { $COMPOSE run --rm --no-deps -e UPSTREAM_URL=$URL --entrypoint python3 client /tests/cdk_e2e_pay.py "$@"; }
replay() { $COMPOSE run --rm --no-deps -e UPSTREAM_URL=$URL --entrypoint python3 client /tests/cdk_e2e_replay.py "$@"; }
quote()  { $COMPOSE run --rm --no-deps -e UPSTREAM_URL=$URL --entrypoint python3 client /tests/cdk_e2e_quote.py "$@"; }

wait_healthy() {  # wait_healthy <container> <service>
  local c="$1" svc="$2" i=0
  until [ "$(docker inspect -f '{{.State.Health.Status}}' "$c" 2>/dev/null || echo starting)" = healthy ]; do
    i=$((i+1)); [ $i -gt 90 ] && { echo "$c never became healthy"; $COMPOSE logs "$svc" 2>&1 | tail -40; return 1; }
    sleep 1
  done
}

dump_logs() {
  mkdir -p /tmp/module-e2e-logs
  $COMPOSE logs "$SVC" > /tmp/module-e2e-logs/module.log 2>&1 || true
  if [ "$BACKEND" = cdk ]; then
    $COMPOSE logs cdk-walletd > /tmp/module-e2e-logs/sidecar.log 2>&1 || true
    docker logs walletd-legd    > /tmp/module-e2e-logs/legd.log 2>&1 || true
    cp /tmp/cdk-legd-proxy.log  /tmp/module-e2e-logs/proxy.log 2>/dev/null || true
  fi
}
trap 'dump_logs; down' EXIT

echo "== BACKEND=$BACKEND: build + bring up ($UPSET) =="
build >/dev/null
up >/dev/null
[ "$BACKEND" = cdk ] && wait_healthy tg-cdk-walletd cdk-walletd
wait_healthy "$CONT" "$SVC"

echo "== happy path: full payment (in-container pytest) =="
pytest -v test_cdk_backend_e2e.py

if [ "$BACKEND" = cdk ]; then
  echo "== service log: backend selection must say cdk =="
  # Capture to a file first, then grep: `logs | grep -m1` under pipefail
  # SIGPIPEs when grep exits early (same class as the mutation-rig lesson).
  $COMPOSE logs "$SVC" > /tmp/module-e2e-service.log 2>&1 || true
  grep "Wallet backend selected" /tmp/module-e2e-service.log | head -1 | tee /tmp/module-e2e-selection.log
  grep -q "cdk" /tmp/module-e2e-selection.log
else
  echo "== service log: default backend must show the legacy wallet init =="
  $COMPOSE logs "$SVC" > /tmp/module-e2e-service.log 2>&1 || true
  grep "TollWallet.New" /tmp/module-e2e-service.log | head -1 | tee /tmp/module-e2e-selection.log
  grep -q "TollWallet.New" /tmp/module-e2e-selection.log
fi

if [ "$MODE" != "full" ]; then echo "== happy-only mode: done =="; exit 0; fi

echo "== LEG C: Lightning quote survives a $DEP restart =="
Q=$(quote create || true)
echo "  quote created → $Q"
QID=$(echo "$Q" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('quote',''))" 2>/dev/null || true)
[ -n "$QID" ] || { echo "FAIL: no quote id from /ln-invoice: $Q"; exit 1; }
docker restart "$DEPC" >/dev/null
wait_healthy "$DEPC" "$DEP"
sleep 3  # settle: sidecar recovery pass / mint re-probe
P=$(quote poll "$QID" || true)
echo "  post-restart poll → $P"
echo "$P" | grep -qE '"quote".*not|unknown quote|no such quote' && { echo "FAIL: quote lost across $DEP restart"; exit 1; }
echo "$P" | python3 -c "
import json,sys
d=json.load(sys.stdin)
assert d.get('http')==200, f'poll HTTP {d.get(\"http\")}: {d}'
assert d.get('quote')=='$QID' or d.get('quote','')=='', f'poll answered a different quote: {d}'
print('  quote state survived the restart ✓')" || exit 1

echo "== LEG A: payment during $DEP outage =="
docker stop "$DEPC" >/dev/null
A=$(pay --verbose || true)
echo "  outage payment → $A"
echo "$A" | grep -q '"kind": 1022' && { echo "FAIL: session granted while $DEP was DOWN"; exit 1; }
if [ "$BACKEND" = cdk ]; then
  # The refusal must be the wallet-outage class (note NOT spent, safe to
  # retry) — never invalid-token, which tells a paying customer their money
  # is fake (the decode RPC dies with the daemon; caught 2026-11-01).
  echo "$A" | grep -q '"payment-processing-unavailable"' || { echo "FAIL: outage refusal is not payment-processing-unavailable: $A"; exit 1; }
else
  # gonuts VACUITY NOTE: the pay helper FUNDS its wallet from the mint, so
  # with the mint down the helper itself dies before any POST — this leg
  # only proves module survival here. The NON-vacuous mint-outage refusal
  # through the module is LEG B's killed-mid-flight first attempt (funded
  # before the kill, refused 21023 — asserted there).
  echo "  (gonuts: helper could not fund against the dead mint; module-survival leg only — see LEG B for the refusal)"
fi
docker inspect -f '{{.State.Status}}' "$CONT" | grep -q running || { echo "FAIL: module died during $DEP outage"; $COMPOSE logs "$SVC" | tail -20; exit 1; }
echo "  module stayed alive through the outage ✓"
depmode; sleep 2; wait_healthy "$DEPC" "$DEP"
[ "$BACKEND" = gonuts ] && sleep 8  # mint re-probe / recovery settle
A2=$(pay --verbose || true)
echo "  post-recovery payment → $A2"
echo "$A2" | grep -q '"kind": 1022' || { echo "FAIL: no session after $DEP recovery"; exit 1; }
echo "  module recovered on $DEP return ✓"

echo "== LEG B: kill $DEP mid-payment → replay must not double-grant =="
EVENT_FILE=".module-e2b-event.json"
LEG_B_OK=0
for ATTEMPT in 1 2; do
  rm -f "$EVENT_FILE"
  # gonuts: the kill target is the mint, which also serves the helper's
  # funding step — kill later than cdk so the POST (not the funding) is
  # the in-flight op; one retry if the race eats the staging.
  KILL_AT=0.4; [ "$BACKEND" = gonuts ] && KILL_AT=2.0
  ( pay --event-out "/tests/$EVENT_FILE" >/tmp/module-e2b-first.out 2>&1 || true ) &
  PAYPID=$!
  sleep "$KILL_AT"
  docker kill "$DEPC" >/dev/null 2>&1 || true
  wait $PAYPID 2>/dev/null || true
  # The event file alone is not enough: the first attempt must have
  # actually reached the module (non-empty verdict output) or the leg
  # proves nothing about the payment path.
  FIRST_RAW_NOW=$(tail -1 /tmp/module-e2b-first.out 2>/dev/null || true)
  if [ -f "$EVENT_FILE" ] && [ -n "$FIRST_RAW_NOW" ]; then LEG_B_OK=1; break; fi
  echo "  (staging raced the kill; retry $ATTEMPT)"
  depmode; sleep 2; wait_healthy "$DEPC" "$DEP"
  [ "$BACKEND" = gonuts ] && sleep 8
done
[ "$LEG_B_OK" = 1 ] || { echo "FAIL: leg B never staged its event"; exit 1; }
echo "  $DEP killed mid-payment; event staged ✓"
depmode; sleep 2; wait_healthy "$DEPC" "$DEP"
sleep 4  # recovery pass
[ "$BACKEND" = gonuts ] && sleep 8

REPLAY=$(replay --verbose "/tests/$EVENT_FILE" || true)
echo "  first (killed mid-flight) → $(cat /tmp/module-e2b-first.out 2>/dev/null | tail -1)"
echo "  replay after restart+recovery → $REPLAY"

# VALUE-BASED verdict. The killed first attempt is genuinely ambiguous:
# if its swap never reached the mint, the proofs are unspent and the
# replay is a legitimate NEW payment (one grant). If the first attempt
# DID complete, the replay must be refused. What can never happen is
# BOTH granting — one token, one swap at the mint. Assert on allotment:
# at most one grant's worth (step_size * 100) of new session across the
# two attempts.
FIRST_RAW=$(cat /tmp/module-e2b-first.out 2>/dev/null | tail -1 || true)
python3 - "$A2" "$FIRST_RAW" "$REPLAY" <<'PY'
import json, sys
STEP = 6_000_000  # step_size 60000 ms * 100 sats (both lane configs)
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

if [ "$BACKEND" = cdk ]; then
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
  EVENT_D=.module-e2d-event.json; rm -f "$EVENT_D"
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
else
  echo "== LEG D: skipped on gonuts (no proxyable socket; ambiguity class covered by the wallet-layer parity lane and unit tests) =="
fi

echo ""
echo "ALL MODULE E2E LEGS PASSED (backend=$BACKEND)"
