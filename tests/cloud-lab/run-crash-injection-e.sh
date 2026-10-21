#!/usr/bin/env bash
# run-crash-injection-e.sh — SCRATCH-ADDITION rc1-793 (disclosed).
#
# Scenario (e): stale intent beyond the NUT-19 window. cdk-mintd's
# default response cache is in-memory with a 60 s TTL (cdk-axum
# DEFAULT_TTL_SECS), so a crash whose replay arrives LATER than the TTL
# finds a mint that already processed the swap: inputs are spent, the
# cached answer is gone, and the replay is refused — the intent must
# stay journaled (visible, not lost), the service healthy, across
# repeated loads, with no busy-loop.
#
# Sequence: stock crash window (killer swallows the mint's first swap
# answer, driver SIGKILLs the tollgate, releases) → the tollgate stays
# DOWN for TTL_WAIT seconds (default 95 > 60 s TTL) → restart. The
# boot-resume replay is refused; the driver then:
#   - captures the refusal (upstream log carries the mint's HTTP code
#     and body via gonuts' truncateForLog; the killer's hash log pins
#     the replay as byte-identical to the crashed POST);
#   - proves the intent is still journaled (OpID bytes present in
#     /etc/tollgate/wallet.db inside the container);
#   - repeats the load REPEAT_LOADS times, requiring EXACTLY one
#     recovery attempt per load (busy-loop guard) and intent retention;
#   - finishes with the stock health payment (full session event).
#
# Driver deltas vs run-crash-injection-var.sh (all lane-local,
# disclosed): the post-crash down-time, the load-repeat loop, and the
# journal/attempt assertions replace the recovery-wait; the killer is
# killerproxy-hash.py (stock control flow, hash logging only).
#
# Usage (from tests/cloud-lab/):
#   COMPOSE_PROJECT_NAME=cl-crash-793e CLOUD_LAB_CRASH_SUBNET=172.31.85 \
#   CLOUD_LAB_STATE=/tmp/cloud-lab-runs/cl-crash-793e KEEP=1 \
#   TTL_WAIT=95 REPEAT_LOADS=3 ./run-crash-injection-e.sh
set -euo pipefail

SELF="$(cd "$(dirname -- "$0")" && pwd)/$(basename -- "$0")"
SCRIPT_DIR="$(dirname "$SELF")"
cd "$SCRIPT_DIR"

PROJECT="${COMPOSE_PROJECT_NAME:-cl-crash-793e}"
RUN_DIR="${CLOUD_LAB_STATE:-/tmp/cloud-lab-runs/$PROJECT}"
SUBNET="${CLOUD_LAB_CRASH_SUBNET:-172.31.85}"
MINT_IP="$SUBNET.2"
TTL_WAIT="${TTL_WAIT:-95}"
REPEAT_LOADS="${REPEAT_LOADS:-3}"

COMPOSE=(docker compose -f docker-compose.yml)
if [ -n "${CLOUD_LAB_EXTRA_COMPOSE:-}" ]; then
    COMPOSE+=(-f "$CLOUD_LAB_EXTRA_COMPOSE")
fi
COMPOSE+=(-p "$PROJECT")

cleanup() {
    if [ "${KEEP:-0}" = "1" ]; then
        echo "KEEP=1: lab left up for inspection (project $PROJECT, state $RUN_DIR)"
        return
    fi
    "${COMPOSE[@]}" down -v >/dev/null 2>&1 || true
    rm -rf "$RUN_DIR"
}
trap cleanup EXIT

mkdir -p "$RUN_DIR"

# --- per-run override: isolated names/subnet, no host ports, killer = hash proxy.
# SCRATCH-PATCH rc1-771 carried forward: build.network host (build-bridge DNS workaround).
cat > "$RUN_DIR/override.yml" <<EOF
services:
  mint:
    build:
      network: host
    container_name: $PROJECT-mint
    ports: !reset []
    networks:
      tollgate-lab:
        ipv4_address: $MINT_IP
  mint-fees:
    build:
      network: host
    container_name: $PROJECT-mint-fees
    ports: !reset []
    networks:
      tollgate-lab:
        ipv4_address: $SUBNET.3
  mint-rotate:
    build:
      network: host
    container_name: $PROJECT-mint-rotate
    ports: !reset []
    networks:
      tollgate-lab:
        ipv4_address: $SUBNET.4
  killer:
    build:
      context: .
      dockerfile: Dockerfile.client
      network: host
    container_name: $PROJECT-killer
    entrypoint: ["python3", "/killer/lanekiller.py", "8085", "http://$MINT_IP:8085", "/v1/swap", "/marker/kill.marker"]
    volumes:
      - ./killerproxy-hash.py:/killer/lanekiller.py:ro
      - $RUN_DIR:/marker
    networks:
      tollgate-lab:
        ipv4_address: $SUBNET.30
  upstream:
    build:
      network: host
    container_name: $PROJECT-upstream
    ports: !reset []
    volumes:
      - $RUN_DIR/upstream-config.json:/etc/tollgate/config.json
      - ./configs/install.json:/etc/tollgate/install.json
      - ./configs/dhcp.leases:/tmp/dhcp.leases
    networks:
      tollgate-lab:
        ipv4_address: $SUBNET.10
  reseller:
    build:
      network: host
    container_name: $PROJECT-reseller
    ports: !reset []
    networks:
      tollgate-lab:
        ipv4_address: $SUBNET.11
  client:
    build:
      network: host
    container_name: $PROJECT-client
    ports: !reset []
    networks:
      tollgate-lab:
        ipv4_address: $SUBNET.20
networks:
  tollgate-lab:
    driver: bridge
    ipam:
      driver: default
      config:
        - subnet: $SUBNET.0/24
EOF

python3 - "$RUN_DIR" <<'PY'
import json, sys
run_dir = sys.argv[1]
cfg = json.load(open("configs/upstream-config.json"))
for m in cfg.get("accepted_mints", []):
    if "mint" in m["url"] and "fees" not in m["url"] and "rotate" not in m["url"]:
        m["url"] = "http://killer:8085"
json.dump(cfg, open(f"{run_dir}/upstream-config.json", "w"), indent=2)
PY

echo "== all mints + killer first, the tollgate last"
"${COMPOSE[@]}" -f "$RUN_DIR/override.yml" up -d --build mint mint-fees mint-rotate killer >/dev/null
for i in $(seq 1 40); do
    curl -sf "http://$MINT_IP:8085/v1/keys" >/dev/null 2>&1 && break
    sleep 1
done
"${COMPOSE[@]}" -f "$RUN_DIR/override.yml" run --rm --no-deps --entrypoint python3 client -c \
    'import urllib.request,sys; sys.exit(0 if urllib.request.urlopen("http://killer:8085/v1/keys", timeout=10).status==200 else 1)' \
    || { echo "FAIL: killer proxy not answering in-network"; exit 1; }
rm -f "$RUN_DIR/kill.marker" "$RUN_DIR/kill.marker.release"

"${COMPOSE[@]}" -f "$RUN_DIR/override.yml" up -d --build upstream >/dev/null
for i in $(seq 1 30); do
    [ "$("${COMPOSE[@]}" -f "$RUN_DIR/override.yml" ps --format '{{.Status}}' upstream 2>/dev/null)" = *"healthy"* ] && break
    sleep 2
done
"${COMPOSE[@]}" -f "$RUN_DIR/override.yml" ps --format '{{.Name}} {{.Status}}'
docker logs "$PROJECT-upstream" 2>&1 | grep 'Accepted mints' | tail -1

echo "== funding and paying through the proxy path"
TOKEN_JSON="$("${COMPOSE[@]}" -f "$RUN_DIR/override.yml" run --rm --no-deps --entrypoint python3 client -c '
import sys, tempfile, json
sys.path.insert(0, "/tests")
import os
os.environ["MINT_URL"] = "http://killer:8085"
from conftest import create_cashu_token, run_cmd
wd = tempfile.mkdtemp()
run_cmd(["cdk-cli", "-w", wd, "mint", "http://killer:8085", "10000"])
print(json.dumps({"token": create_cashu_token(wd, 50)}))
' 2>/dev/null | tail -1)"
TOKEN="$(python3 -c "import json,sys; print(json.loads(sys.argv[1])['token'])" "$TOKEN_JSON")"
echo "   token: ${TOKEN:0:32}…"

echo "== paying in the background (the swap response will be swallowed)"
"${COMPOSE[@]}" -f "$RUN_DIR/override.yml" run --rm --no-deps -d --entrypoint python3 client -c '
import sys, requests
sys.path.insert(0, "/tests")
from conftest import UPSTREAM_URL, build_payment_event, generate_nostr_keypair
token = sys.argv[1]
sec, pub = generate_nostr_keypair()
upk = requests.get(UPSTREAM_URL, timeout=10).json()["pubkey"]
ev = build_payment_event(sec, pub, upk, "02:c4:55:01:00:ff", token)
try:
    r = requests.post(UPSTREAM_URL + "?mac=02:c4:55:01:00:ff", json=ev, timeout=90)
    print("pay:", r.status_code)
except Exception as e:
    print("pay died:", type(e).__name__)
' "$TOKEN" > "$RUN_DIR/pay.log" 2>&1 &

echo "== waiting for the swap response marker (mint has signed)"
for i in $(seq 1 60); do
    [ -f "$RUN_DIR/kill.marker" ] && break
    sleep 1
done
if [ ! -f "$RUN_DIR/kill.marker" ]; then
    echo "FAIL: swap response never arrived at the proxy"
    docker logs "$PROJECT-killer" 2>&1 | tail -5
    docker logs "$PROJECT-upstream" 2>&1 | grep -iE 'PurchaseSession|swap|Accept|degrad|probe' | tail -8
    exit 1
fi
echo "   marker: $(cat "$RUN_DIR/kill.marker")"

echo "== SIGKILL the tollgate (mid-wait: mint signed, wallet has NOT processed)"
docker kill "$PROJECT-upstream" >/dev/null
touch "$RUN_DIR/kill.marker.release"
echo "   killed at $(date +%T)"

PRE_HASH="$(docker logs "$PROJECT-killer" 2>&1 | awk '
/hash: .*body_sha256=/ { if (match($0, /body_sha256=[0-9a-f]+/)) last_hash = substr($0, RSTART+13, 64) }
/SWALLOWED first\(marker\)/ { print last_hash; exit }')"
echo "   pre-crash swap sha256: $PRE_HASH"

echo "== staying down ${TTL_WAIT}s — beyond the cdk-mintd in-memory NUT-19 TTL (60 s)"
sleep "$TTL_WAIT"

echo "== restarting the tollgate; the boot-resume replay must be REFUSED (already spent)"
"${COMPOSE[@]}" -f "$RUN_DIR/override.yml" up -d upstream >/dev/null

REFUSAL=""
OPID=""
for i in $(seq 1 40); do
    LINE="$(docker logs "$PROJECT-upstream" 2>&1 | grep 'not recoverable this pass' | tail -1 || true)"
    if [ -n "$LINE" ]; then
        REFUSAL="$LINE"
        OPID="$(echo "$LINE" | sed -E 's/.*pending swap ([0-9a-f]+) .*/\1/')"
        break
    fi
    if docker logs "$PROJECT-upstream" 2>&1 | grep -qE 'pending (swap|op) .* recovered'; then
        echo "FAIL: replay SUCCEEDED after ${TTL_WAIT}s down — NUT-19 TTL did not expire (rerun with larger TTL_WAIT)"
        exit 1
    fi
    sleep 3
done
if [ -z "$REFUSAL" ]; then
    echo "FAIL: neither recovery nor refusal logged — recovery pass silent:"
    docker logs "$PROJECT-upstream" 2>&1 | grep -iE 'resume|pending|swap|recover' | tail -8
    exit 1
fi
echo "REFUSED: $REFUSAL"
echo "OPID:    $OPID"

echo "== replay byte-identity + refusal code at the killer"
REPLAY_HASH="$(docker logs "$PROJECT-killer" 2>&1 | awk -v pre="$PRE_HASH" '
/hash: .*body_sha256=/ { if (match($0, /body_sha256=[0-9a-f]+/)) last_hash = substr($0, RSTART+13, 64) }
/SWALLOWED first\(marker\)/ { sw = 1; next }
sw == 1 && /hash: .*body_sha256=/ { rep_hash = last_hash }
sw == 1 && /PASSED code=/ { print rep_hash; exit }')"
REPLAY_CODE="$(docker logs "$PROJECT-killer" 2>&1 | awk '
/SWALLOWED first\(marker\)/ { sw = 1 }
sw == 1 && /PASSED code=/ { if (match($0, /code=[0-9]+/)) { print substr($0, RSTART+5, RLENGTH-5); exit } }')"
echo "   replay sha256: $REPLAY_HASH (pre-crash: $PRE_HASH)"
echo "   replay HTTP status at mint: $REPLAY_CODE"
[ -n "$REPLAY_HASH" ] && [ "$REPLAY_HASH" = "$PRE_HASH" ] \
    || { echo "FAIL: replay bytes differ from the crashed POST"; exit 1; }
case "$REPLAY_CODE" in
    200) echo "FAIL: mint answered 200 — cache still warm, scenario not exercised"; exit 1 ;;
    4*|5*) echo "   refusal is a non-200 ($REPLAY_CODE): OK" ;;
    *) echo "WARN: unexpected replay status $REPLAY_CODE" ;;
esac

attempt_count() { docker logs "$PROJECT-upstream" 2>&1 | grep -c "pending swap $OPID.*not recoverable this pass" || true; }
intent_in_db() { docker exec "$PROJECT-upstream" sh -c "grep -ac '$OPID' /etc/tollgate/wallet.db" 2>/dev/null || echo 0; }

ATTEMPTS="$(attempt_count)"
DBHITS="$(intent_in_db)"
echo "== after first refused load: attempts=$ATTEMPTS intent-in-wallet.db=$DBHITS"
[ "$ATTEMPTS" = "1" ] || { echo "FAIL: expected exactly 1 recovery attempt, saw $ATTEMPTS"; exit 1; }
[ "$DBHITS" -ge 1 ] || { echo "SEV: intent $OPID vanished from the journal (wallet.db) after a refused replay"; exit 1; }
docker logs "$PROJECT-upstream" 2>&1 | grep -qE 'pending swap .* recovered' \
    && { echo "FAIL: a recovery line exists — value was recovered where refusal was expected"; exit 1; } || true
echo "   intent retained: OK; no recovery: OK"

for LOAD in $(seq 1 "$REPEAT_LOADS"); do
    echo "== repeated load $LOAD/$REPEAT_LOADS (kill + restart, fresh wallet load)"
    docker kill "$PROJECT-upstream" >/dev/null
    "${COMPOSE[@]}" -f "$RUN_DIR/override.yml" up -d upstream >/dev/null
    for i in $(seq 1 20); do
        NOW="$(attempt_count)"
        [ "$NOW" -gt "$ATTEMPTS" ] && break
        sleep 3
    done
    sleep 8
    NOW="$(attempt_count)"
    EXPECTED=$((ATTEMPTS + 1))
    if [ "$NOW" != "$EXPECTED" ]; then
        echo "FAIL: load $LOAD produced $NOW total attempts (expected exactly $EXPECTED) — busy-loop or missing retry"
        exit 1
    fi
    DBHITS="$(intent_in_db)"
    [ "$DBHITS" -ge 1 ] || { echo "SEV: intent $OPID vanished from the journal on load $LOAD"; exit 1; }
    ATTEMPTS="$NOW"
    echo "   load $LOAD: attempts now $ATTEMPTS (exactly one new), intent still journaled ($DBHITS byte-hits)"
done

echo "== no busy-loop: total attempts $ATTEMPTS across $((REPEAT_LOADS + 1)) loads"
docker stats --no-stream --format '   {{.Name}}: CPU {{.CPUPerc}}' "$PROJECT-upstream"

echo "== fund-safety sweep: no 10002 / duplicate-output at the mint"
MINT_ERRS="$(docker logs "$PROJECT-mint" 2>&1 | grep -ciE '10002|duplicate' || true)"
[ "$MINT_ERRS" = "0" ] || { echo "SEV: mint log shows duplicate-output errors:"; docker logs "$PROJECT-mint" 2>&1 | grep -iE '10002|duplicate' | head; exit 1; }
echo "   mint log clean of 10002/duplicate (0 hits)"

echo "== health check: a fresh payment must still succeed (full session event)"
"${COMPOSE[@]}" -f "$RUN_DIR/override.yml" run --rm --no-deps --entrypoint python3 client -c '
import sys, tempfile, requests, json
sys.path.insert(0, "/tests")
from conftest import create_cashu_token, UPSTREAM_URL, run_cmd, generate_nostr_keypair, build_payment_event
wd = tempfile.mkdtemp()
run_cmd(["cdk-cli", "-w", wd, "mint", "http://killer:8085", "10000"])
token = create_cashu_token(wd, 30, mint_url="http://killer:8085")
sec, pub = generate_nostr_keypair()
upk = requests.get(UPSTREAM_URL, timeout=10).json()["pubkey"]
ev = build_payment_event(sec, pub, upk, "02:af:7e:40:00:ff", token)
r = requests.post(UPSTREAM_URL + "?mac=02:af:7e:40:00:ff", json=ev, timeout=60)
print("post-refusal health payment:", r.status_code)
try:
    body = r.json()
    print("session event kind:", body.get("kind"))
    tags = [t[0] for t in body.get("tags", [])]
    print("session event tags:", tags)
    ok = r.status_code == 200 and body.get("kind") == 1022 and ("allotment" in tags or "amount" in tags)
except Exception:
    print("non-JSON body:", r.text[:400]); ok = False
exit(0 if ok else 1)
'

echo "== merchant journals (customer-side state after the refused replay)"
docker exec "$PROJECT-upstream" sh -c 'cat /etc/tollgate/receive-intents.json 2>/dev/null' | python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    print("(no receive-intents.json)"); sys.exit(0)
for ref, it in d.items():
    print(f"   receive-intent {ref[:16]}: amount={it.get(\"amount_sats\")} state={it.get(\"state\")} note={str(it.get(\"resolution_note\",\"\"))[:70]}")' || true
docker exec "$PROJECT-upstream" sh -c 'cat /etc/tollgate/owed-grants.json 2>/dev/null' | head -c 600 | sed 's/^/   /' || true
echo
echo "== scenario (e) complete: stale intent retained across $((REPEAT_LOADS + 1)) loads, service healthy, no busy-loop"
