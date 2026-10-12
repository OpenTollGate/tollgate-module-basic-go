#!/usr/bin/env bash
# run-crash-injection-e2.sh — SCRATCH-ADDITION rc1-866 (disclosed).
#
# Scenario (e′): the (e) crash shape — stale intent beyond the NUT-19
# window (killer swallows the mint's swap answer, driver SIGKILLs the
# tollgate, stays down TTL_WAIT > the 60 s cache TTL) — run against the
# gonuts PR #40 restore fallback (src/go.mod replace, lane-local). The
# boot-resume replay is still REFUSED 11001 at the mint (byte-identical
# re-POST, non-200), but the wallet must now RECOVER the intent via
# NUT-09 /v1/restore: the recovery line names the fallback, the amount is
# the full crashed swap value, the intent is DELETED from the bbolt
# journal, repeated loads stay silent (nothing stuck, no busy-loop), the
# mint log carries the one expected 11001 refusal and no 10002/duplicate
# output, and the post-recovery health payment still converges.
#
# MINT_DOCKERFILE selects the mint image for the dual-mint matrix
# (tollgate #866/#867): Dockerfile.mint (cdk-mintd 0.17.6, the lane
# baseline) or Dockerfile.mint018 (cdk-mintd 0.18.1, DB-authoritative
# config — its entrypoint renders the settings document from the same
# env vars the 0.17.6 daemon consumed).
#
# Usage (from tests/cloud-lab/):
#   COMPOSE_PROJECT_NAME=cl-crash-866e2 CLOUD_LAB_CRASH_SUBNET=172.31.87 \
#   CLOUD_LAB_STATE=/tmp/cloud-lab-runs/cl-crash-866e2 KEEP=1 \
#   TTL_WAIT=95 REPEAT_LOADS=3 ./run-crash-injection-e2.sh
#   MINT_DOCKERFILE=Dockerfile.mint018 ./run-crash-injection-e2.sh   # 0.18.1 leg
set -euo pipefail

SELF="$(cd "$(dirname -- "$0")" && pwd)/$(basename -- "$0")"
SCRIPT_DIR="$(dirname "$SELF")"
cd "$SCRIPT_DIR"

PROJECT="${COMPOSE_PROJECT_NAME:-cl-crash-866e2}"
RUN_DIR="${CLOUD_LAB_STATE:-/tmp/cloud-lab-runs/$PROJECT}"
SUBNET="${CLOUD_LAB_CRASH_SUBNET:-172.31.87}"
MINT_IP="$SUBNET.2"
TTL_WAIT="${TTL_WAIT:-95}"
REPEAT_LOADS="${REPEAT_LOADS:-3}"
MINT_DOCKERFILE="${MINT_DOCKERFILE:-Dockerfile.mint}"
echo "== mint image: $MINT_DOCKERFILE (fork: gonuts PR #40, lane-local replace)"

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
# rc1-866: the mint service build selects MINT_DOCKERFILE (dual-mint matrix).
cat > "$RUN_DIR/override.yml" <<EOF
services:
  mint:
    build:
      network: host
      dockerfile: $MINT_DOCKERFILE
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

echo "== restarting the tollgate; replay must be REFUSED, recovery must come via NUT-09 restore"
"${COMPOSE[@]}" -f "$RUN_DIR/override.yml" up -d upstream >/dev/null

RECOVERY_LINE=""
OPID=""
AMOUNT=""
for i in $(seq 1 40); do
    LINE="$(docker logs "$PROJECT-upstream" 2>&1 | grep -E 'pending swap [0-9a-f]+ recovered [0-9]+ sats' | tail -1 || true)"
    if [ -n "$LINE" ]; then
        if ! printf '%s' "$LINE" | grep -q 'via NUT-09 restore'; then
            echo "FAIL: recovery came from the NUT-19 cache replay — TTL did not expire (rerun with larger TTL_WAIT): $LINE"
            exit 1
        fi
        RECOVERY_LINE="$LINE"
        OPID="$(printf '%s' "$LINE" | sed -E 's/.*pending swap ([0-9a-f]+) .*/\1/')"
        AMOUNT="$(printf '%s' "$LINE" | sed -E 's/.*recovered ([0-9]+) sats.*/\1/')"
        break
    fi
    if docker logs "$PROJECT-upstream" 2>&1 | grep -q 'not recoverable this pass'; then
        echo "FAIL: the restore fallback did not complete — refusal logged instead:"
        docker logs "$PROJECT-upstream" 2>&1 | grep -E 'not recoverable|restore|resume|pending' | tail -6
        exit 1
    fi
    sleep 3
done
if [ -z "$RECOVERY_LINE" ]; then
    echo "FAIL: no recovery line within timeout — upstream log tail:"
    docker logs "$PROJECT-upstream" 2>&1 | grep -iE 'resume|pending|swap|recover|restore' | tail -8
    exit 1
fi
echo "RECOVERED: $RECOVERY_LINE"
echo "OPID: $OPID  AMOUNT: ${AMOUNT} sats"
[ "$AMOUNT" = "50" ] || { echo "FAIL: expected the full 50-sat swap value, got $AMOUNT"; exit 1; }

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
    4*|5*) echo "   refusal is a non-200 ($REPLAY_CODE): the mint still refuses the replay — recovery came from /v1/restore" ;;
    *) echo "WARN: unexpected replay status $REPLAY_CODE" ;;
esac

echo "== intent retirement: byte-grep is presence-only evidence — bbolt keeps residue
   in freed pages, so 0 hits can never be proven after a real delete (rel-793's (e)
   used the same grep to prove PRESENCE of a journaled intent). Deletion is proven
   behaviorally below: the recovery line only prints after DeletePendingSwap
   succeeded, and every repeated load must stay silent — a journaled intent emits
   exactly one recovery attempt per load (the (e) scenario's own baseline)."
sleep 3
DBHITS="$(docker exec "$PROJECT-upstream" sh -c "grep -ac '$OPID' /etc/tollgate/wallet.db" 2>/dev/null || echo 0)"
echo "   wallet.db byte-hits for $OPID: $DBHITS (informational)"

recovery_count() { docker logs "$PROJECT-upstream" 2>&1 | grep -c 'via NUT-09 restore' || true; }
refusal_count()  { docker logs "$PROJECT-upstream" 2>&1 | grep -c 'not recoverable this pass' || true; }

for LOAD in $(seq 1 "$REPEAT_LOADS"); do
    echo "== repeated load $LOAD/$REPEAT_LOADS (kill + restart; post-recovery loads must stay silent)"
    docker kill "$PROJECT-upstream" >/dev/null
    "${COMPOSE[@]}" -f "$RUN_DIR/override.yml" up -d upstream >/dev/null
    sleep 20
    RC="$(recovery_count)"
    FC="$(refusal_count)"
    [ "$RC" = "1" ] || { echo "FAIL: load $LOAD changed the recovery count to $RC (expected 1 forever)"; exit 1; }
    [ "$FC" = "0" ] || { echo "FAIL: load $LOAD produced refusal lines ($FC) — something re-stuck"; exit 1; }
    echo "   load $LOAD: silent — no new recovery, no refusals (nothing left journaled)"
done

echo "== no busy-loop at rest"
docker stats --no-stream --format '   {{.Name}}: CPU {{.CPUPerc}}' "$PROJECT-upstream"

echo "== fund-safety sweep: no 10002 / duplicate-output at the mint"
MINT_ERRS="$(docker logs "$PROJECT-mint" 2>&1 | grep -ciE '10002|duplicate' || true)"
[ "$MINT_ERRS" = "0" ] || { echo "SEV: mint log shows duplicate-output errors:"; docker logs "$PROJECT-mint" 2>&1 | grep -iE '10002|duplicate' | head; exit 1; }
REFUSALS="$(docker logs "$PROJECT-mint" 2>&1 | grep -c 'Token Already Spent' || true)"
echo "   mint log: 0 hits 10002/duplicate; 11001 refusals: $REFUSALS (expected >= 1 — the beyond-window replay)"
[ "$REFUSALS" -ge 1 ] || { echo "FAIL: the mint never refused the replay — scenario not exercised"; exit 1; }

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
print("post-recovery health payment:", r.status_code)
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

echo "== merchant journals (customer-side state after the restore recovery)"
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
echo "== scenario (e') complete on $MINT_DOCKERFILE: beyond-window intent RECOVERED via NUT-09 restore, retired from the journal, loads silent, service healthy"
