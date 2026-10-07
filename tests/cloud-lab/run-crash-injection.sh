#!/usr/bin/env bash
# run-crash-injection.sh — the #497 acceptance harness at the TollGate
# layer: SIGKILL the tollgate between the mint's swap acceptance and the
# wallet's proof save, restart, and require the value to be recovered.
#
# The kill is placed deterministically: the tollgate's mint traffic goes
# through killerproxy.py, which swallows the FIRST successful /v1/swap
# response (the mint HAS signed), writes a marker, and hangs; this
# driver SIGKILLs the tollgate container the moment the marker appears.
#
# Requires a tollgate built with the gonuts swap-intent machinery
# (gonuts-tollgate #31/#33 era) and the boot-resume wiring; against
# pre-intent binaries the recovery assertion correctly fails — that is
# the point of the harness.
#
# Usage (from tests/cloud-lab/):
#   ./run-crash-injection.sh
#
# Isolation knobs (same conventions as lab.sh):
#   COMPOSE_PROJECT_NAME     project isolation (default: cl-crash)
#   CLOUD_LAB_EXTRA_COMPOSE  extra -f override
set -euo pipefail

SELF="$(cd "$(dirname -- "$0")" && pwd)/$(basename -- "$0")"
SCRIPT_DIR="$(dirname "$SELF")"
cd "$SCRIPT_DIR"

PROJECT="${COMPOSE_PROJECT_NAME:-cl-crash}"
RUN_DIR="${CLOUD_LAB_STATE:-/tmp/cloud-lab-runs/$PROJECT}"
SUBNET="${CLOUD_LAB_CRASH_SUBNET:-172.31.77}"
MINT_IP="$SUBNET.2"

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

# --- per-run override: isolated names/subnet, no host ports, and the
# tollgate's mint pointed at the killer proxy (config mount rewrite).
cat > "$RUN_DIR/override.yml" <<EOF
services:
  mint:
    container_name: $PROJECT-mint
    ports: !reset []
    networks:
      tollgate-lab:
        ipv4_address: $MINT_IP
  mint-fees:
    container_name: $PROJECT-mint-fees
    ports: !reset []
    networks:
      tollgate-lab:
        ipv4_address: $SUBNET.3
  mint-rotate:
    container_name: $PROJECT-mint-rotate
    ports: !reset []
    networks:
      tollgate-lab:
        ipv4_address: $SUBNET.4
  killer:
    build:
      context: .
      dockerfile: Dockerfile.client
    container_name: $PROJECT-killer
    entrypoint: ["python3", "/killer/killerproxy.py", "8085", "http://$MINT_IP:8085", "/v1/swap", "/marker/kill.marker"]
    volumes:
      - ./killerproxy.py:/killer/killerproxy.py:ro
      - $RUN_DIR:/marker
    networks:
      tollgate-lab:
        ipv4_address: $SUBNET.30
  upstream:
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
    container_name: $PROJECT-reseller
    ports: !reset []
    networks:
      tollgate-lab:
        ipv4_address: $SUBNET.11
  client:
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

# the config rewrite: mint URL -> the in-network killer service
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
# killer answers on the network: verify from a short-lived client
"${COMPOSE[@]}" -f "$RUN_DIR/override.yml" run --rm --no-deps --entrypoint python3 client -c \
    'import urllib.request,sys; sys.exit(0 if urllib.request.urlopen("http://killer:8085/v1/keys", timeout=10).status==200 else 1)' \
    || { echo "FAIL: killer proxy not answering in-network"; exit 1; }
rm -f "$RUN_DIR/kill.marker" "$RUN_DIR/kill.marker.release"

# the tollgate boots LAST: its mint probe must find the killer answering,
# or it starts degraded and no payment ever reaches a swap
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
import sys, time, requests
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
    echo "--- killer log:"; docker logs "$PROJECT-killer" 2>&1 | tail -5
    echo "--- tollgate (payment path):"; docker logs "$PROJECT-upstream" 2>&1 | grep -iE 'PurchaseSession|swap|Accept|degrad|probe' | tail -8
    exit 1
fi
echo "   marker: $(cat "$RUN_DIR/kill.marker")"

echo "== SIGKILL the tollgate (mid-wait: mint signed, wallet has NOT processed)"
docker kill "$PROJECT-upstream" >/dev/null
touch "$RUN_DIR/kill.marker.release"   # killer passes through from now on
echo "   killed at $(date +%T)"

echo "== restarting the tollgate; boot resume must replay the intent"
"${COMPOSE[@]}" -f "$RUN_DIR/override.yml" up -d upstream >/dev/null
sleep 5

RECOVERED=""
# Two recovery vocabularies, both a pass: the wallet layer may log
# "pending swap/op ... recovered" (gonuts swap-intent resume), and the
# #502/#700 merchant layer logs "Owed grant applied" — the receive-intent
# resumed, NUT-07 proved the proofs SPENT, and the owed session landed.
RECOVERED_RE='pending (swap|op) .* recovered|Owed grant applied'
for i in $(seq 1 40); do
    if docker logs "$PROJECT-upstream" 2>&1 | grep -qE "$RECOVERED_RE"; then
        RECOVERED="$(docker logs "$PROJECT-upstream" 2>&1 | grep -E "$RECOVERED_RE" | tail -1)"
        break
    fi
    sleep 3
done

if [ -z "$RECOVERED" ]; then
    echo "FAIL: no recovery after restart — the crash window destroyed value:"
    docker logs "$PROJECT-upstream" 2>&1 | grep -iE 'resume|recovered|pending|swap' | tail -5
    exit 1
fi
echo "RECOVERED: $RECOVERED"

echo "== health check: a fresh payment must still succeed post-recovery"
"${COMPOSE[@]}" -f "$RUN_DIR/override.yml" run --rm --no-deps --entrypoint python3 client -c '
import sys, tempfile, requests
sys.path.insert(0, "/tests")
from conftest import create_cashu_token, MINT_URL, UPSTREAM_URL, run_cmd, generate_nostr_keypair, build_payment_event
wd = tempfile.mkdtemp()
run_cmd(["cdk-cli", "-w", wd, "mint", "http://killer:8085", "10000"])
token = create_cashu_token(wd, 30, mint_url="http://killer:8085")
sec, pub = generate_nostr_keypair()
upk = requests.get(UPSTREAM_URL, timeout=10).json()["pubkey"]
ev = build_payment_event(sec, pub, upk, "02:af:7e:40:00:ff", token)
r = requests.post(UPSTREAM_URL + "?mac=02:af:7e:40:00:ff", json=ev, timeout=60)
print("post-recovery payment:", r.status_code)
exit(0 if r.status_code == 200 else 1)
'
echo "== crash-injection run complete: value recovered, wallet healthy"
