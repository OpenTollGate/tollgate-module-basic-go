#!/usr/bin/env bash
# run-crash-injection-d.sh — SCRATCH-ADDITION rc1-793 (disclosed).
#
# Scenario (d): Receive-during-resume. Derived verbatim from
# run-crash-injection-var.sh (which is itself the stock driver + two
# declared differences) with the receive-during-resume phase replacing
# the plain restart:
#   1. crash window exactly as stock (killer swallows the mint's first
#      swap answer, driver SIGKILLs the tollgate, releases the killer);
#   2. restart; the boot-resume replay POST arrives at the killer and is
#      HELD there (killerproxy-hold.py) for KILLER_HOLD seconds — the
#      mint has answered, the wallet is waiting: resume is IN FLIGHT;
#   3. the moment the hold marker appears, a fresh purchase (30 sats,
#      different customer MAC) is submitted to the tollgate's API —
#      the background-every-load design's unique concurrency window;
#   4. assertions: replay bytes byte-identical to the crashed POST
#      (sha256), the concurrent purchase gets a full verdict (200 +
#      kind-1022 + allotment), recovery still completes, no
#      10002/duplicate-output at the mint, and the ordering evidence
#      (purchase swap passed while the replay was still held, or — if
#      the wallet serializes — completed after it) is printed.
# Then the stock health payment (full session event) as the final gate.
#
# Driver deltas vs run-crash-injection-var.sh (all lane-local, disclosed):
#   - killer = killerproxy-hold.py with a hold-seconds arg (KILLER_HOLD);
#   - the concurrent purchase run stays ATTACHED (no -d) so pay2.log
#     captures the verdict and `wait` propagates its exit code;
#   - post-run assertions: replay byte-identity, fresh-derivation isolation,
#     mint 10002/duplicate-output sweep.
#
# Usage (from tests/cloud-lab/):
#   COMPOSE_PROJECT_NAME=cl-crash-793d CLOUD_LAB_CRASH_SUBNET=172.31.84 \
#   CLOUD_LAB_STATE=/tmp/cloud-lab-runs/cl-crash-793d KEEP=1 \
#   KILLER_HOLD=14 ./run-crash-injection-d.sh
set -euo pipefail

SELF="$(cd "$(dirname -- "$0")" && pwd)/$(basename -- "$0")"
SCRIPT_DIR="$(dirname "$SELF")"
cd "$SCRIPT_DIR"

PROJECT="${COMPOSE_PROJECT_NAME:-cl-crash-793d}"
RUN_DIR="${CLOUD_LAB_STATE:-/tmp/cloud-lab-runs/$PROJECT}"
SUBNET="${CLOUD_LAB_CRASH_SUBNET:-172.31.84}"
MINT_IP="$SUBNET.2"
KILLER_HOLD="${KILLER_HOLD:-14}"
MAC1="02:c4:55:01:00:ff"   # crash-window customer
MAC2="02:9d:aa:02:00:ff"   # receive-during-resume customer
MAC3="02:af:7e:40:00:ff"   # health payment customer

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

# --- per-run override: isolated names/subnet, no host ports, killer = hold proxy.
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
    entrypoint: ["python3", "/killer/lanekiller.py", "8085", "http://$MINT_IP:8085", "/v1/swap", "/marker/kill.marker", "$KILLER_HOLD"]
    volumes:
      - ./killerproxy-hold.py:/killer/lanekiller.py:ro
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
"${COMPOSE[@]}" -f "$RUN_DIR/override.yml" run --rm --no-deps --entrypoint python3 client -c \
    'import urllib.request,sys; sys.exit(0 if urllib.request.urlopen("http://killer:8085/v1/keys", timeout=10).status==200 else 1)' \
    || { echo "FAIL: killer proxy not answering in-network"; exit 1; }
rm -f "$RUN_DIR/kill.marker" "$RUN_DIR/kill.marker.release" "$RUN_DIR/kill.marker.hold"

"${COMPOSE[@]}" -f "$RUN_DIR/override.yml" up -d --build upstream >/dev/null
for i in $(seq 1 30); do
    [ "$("${COMPOSE[@]}" -f "$RUN_DIR/override.yml" ps --format '{{.Status}}' upstream 2>/dev/null)" = *"healthy"* ] && break
    sleep 2
done
"${COMPOSE[@]}" -f "$RUN_DIR/override.yml" ps --format '{{.Name}} {{.Status}}'
docker logs "$PROJECT-upstream" 2>&1 | grep 'Accepted mints' | tail -1

echo "== funding: crash-window token (50) + concurrent-purchase token (30)"
TOKENS_JSON="$("${COMPOSE[@]}" -f "$RUN_DIR/override.yml" run --rm --no-deps --entrypoint python3 client -c '
import sys, tempfile, json
sys.path.insert(0, "/tests")
import os
os.environ["MINT_URL"] = "http://killer:8085"
from conftest import create_cashu_token, run_cmd
wd1, wd2 = tempfile.mkdtemp(), tempfile.mkdtemp()
run_cmd(["cdk-cli", "-w", wd1, "mint", "http://killer:8085", "10000"])
run_cmd(["cdk-cli", "-w", wd2, "mint", "http://killer:8085", "10000"])
print(json.dumps({"crash": create_cashu_token(wd1, 50), "concurrent": create_cashu_token(wd2, 30)}))
' 2>/dev/null | tail -1)"
CRASH_TOKEN="$(python3 -c "import json,sys; print(json.loads(sys.argv[1])['crash'])" "$TOKENS_JSON")"
CONC_TOKEN="$(python3 -c "import json,sys; print(json.loads(sys.argv[1])['concurrent'])" "$TOKENS_JSON")"
echo "   crash token: ${CRASH_TOKEN:0:32}…  concurrent token: ${CONC_TOKEN:0:32}…"

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
' "$CRASH_TOKEN" > "$RUN_DIR/pay.log" 2>&1 &

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
touch "$RUN_DIR/kill.marker.release"   # killer passes through from now on
echo "   killed at $(date +%T)"

echo "== restarting the tollgate; the boot-resume replay will be HELD at the killer"
"${COMPOSE[@]}" -f "$RUN_DIR/override.yml" up -d upstream >/dev/null

echo "== waiting for the resume replay to arrive at the killer (hold marker)"
HOLD_AT=""
for i in $(seq 1 120); do
    if [ -f "$RUN_DIR/kill.marker.hold" ]; then
        HOLD_AT="$(cat "$RUN_DIR/kill.marker.hold")"
        break
    fi
    if docker logs "$PROJECT-upstream" 2>&1 | grep -qE 'pending (swap|op) .* recovered'; then
        echo "FAIL: resume replay completed BEFORE the hold engaged (window missed)"
        docker logs "$PROJECT-killer" 2>&1 | tail -8
        exit 1
    fi
    sleep 0.5
done
if [ -z "$HOLD_AT" ]; then
    echo "FAIL: resume replay never arrived at the killer within 60s"
    docker logs "$PROJECT-upstream" 2>&1 | grep -iE 'resume|pending|swap' | tail -8
    docker logs "$PROJECT-killer" 2>&1 | tail -8
    exit 1
fi
echo "   resume is IN FLIGHT: $HOLD_AT"

echo "== firing the concurrent fresh purchase against the serving tollgate (resume still held)"
"${COMPOSE[@]}" -f "$RUN_DIR/override.yml" run --rm --no-deps --entrypoint python3 client -c '
import sys, time, requests
sys.path.insert(0, "/tests")
from conftest import UPSTREAM_URL, build_payment_event, generate_nostr_keypair
token = sys.argv[1]
deadline = time.time() + 25
ev = None
while time.time() < deadline:
    try:
        sec, pub = generate_nostr_keypair()
        upk = requests.get(UPSTREAM_URL, timeout=5).json()["pubkey"]
        ev = build_payment_event(sec, pub, upk, "02:9d:aa:02:00:ff", token)
        break
    except Exception as e:
        print("setup retry:", type(e).__name__, flush=True)
        time.sleep(0.5)
if ev is None:
    print("pay2: FAILED to reach tollgate API within window"); sys.exit(1)
deadline = time.time() + 30
while time.time() < deadline:
    try:
        r = requests.post(UPSTREAM_URL + "?mac=02:9d:aa:02:00:ff", json=ev, timeout=30)
        try:
            body = r.json()
            print("pay2:", r.status_code, "kind:", body.get("kind"))
            tags = [t[0] for t in body.get("tags", [])]
            print("pay2 tags:", tags)
            ok = r.status_code == 200 and body.get("kind") == 1022 and "allotment" in tags
        except Exception:
            print("pay2 non-JSON:", r.text[:300]); ok = False
        print("PAY2_RESULT:", "OK" if ok else "BAD")
        sys.exit(0 if ok else 1)
    except requests.exceptions.ConnectionError:
        print("pay2 conn retry", flush=True)
        time.sleep(0.5)
    except Exception as e:
        print("pay2 error:", type(e).__name__, e); sys.exit(1)
print("pay2: no verdict within window"); sys.exit(1)
' "$CONC_TOKEN" > "$RUN_DIR/pay2.log" 2>&1 &
PAY2_PID=$!

echo "== waiting for the resume recovery to complete (held response must still land)"
RECOVERED=""
for i in $(seq 1 40); do
    if docker logs "$PROJECT-upstream" 2>&1 | grep -qE 'pending (swap|op) .* recovered'; then
        RECOVERED="$(docker logs "$PROJECT-upstream" 2>&1 | grep -E 'pending (swap|op) .* recovered' | tail -1)"
        break
    fi
    sleep 3
done
if [ -z "$RECOVERED" ]; then
    echo "FAIL: no recovery after the held replay — value destroyed:"
    docker logs "$PROJECT-upstream" 2>&1 | grep -iE 'resume|recovered|pending|swap' | tail -6
    exit 1
fi
echo "RECOVERED: $RECOVERED"

echo "== waiting for the concurrent purchase verdict"
wait $PAY2_PID || { echo "FAIL: concurrent purchase did not get a valid verdict:"; cat "$RUN_DIR/pay2.log"; exit 1; }
cat "$RUN_DIR/pay2.log"

echo "== ordering / isolation evidence (killer log)"
docker logs "$PROJECT-killer" 2>&1 | grep -E 'hash:|decision:' | sed "s/^/   /"

echo "== byte-identity + isolation assertions (hash paired to its decision line)"
read -r PRE_HASH REPLAY_HASH CONC_HASH < <(docker logs "$PROJECT-killer" 2>&1 | awk '
/hash: .*body_sha256=/ { if (match($0, /body_sha256=[0-9a-f]+/)) last_hash = substr($0, RSTART+13, 64); last_ts = $1 }
/SWALLOWED first\(marker\)/ && pre == "" { pre = last_hash; pre_ts = last_ts }
/HELD code=/ && rep == "" { rep = last_hash; rep_ts = last_ts }
/HOLD-RELEASED/ { rel_ts = $1 }
/PASSED code=.*released=True/ && conc == "" && rep != "" { conc = last_hash; conc_ts = last_ts }
END { print pre, rep, conc }
')
OVERLAP_VERDICT="$(docker logs "$PROJECT-killer" 2>&1 | awk '
/hash: .*body_sha256=/ { if (match($0, /body_sha256=[0-9a-f]+/)) last_hash = substr($0, RSTART+13, 64) }
/HELD code=/ && rep == "" { rep = last_hash }
/HOLD-RELEASED/ { rel = $1 }
/PASSED code=.*released=True/ && conc == "" && rep != "" { conc = $1 }
END {
  if (conc != "" && rel != "" && conc < rel) print "OVERLAP: concurrent swap passed the proxy (" conc ") while the resume replay was still held (released " rel ")";
  else if (conc != "" && rel != "") print "SERIALIZED: concurrent swap passed (" conc ") only after the held replay was released (" rel ") — wallet deferred the Receive";
  else print "ORDERING-UNKNOWN";
}')"
echo "   pre-crash swap sha256:  $PRE_HASH"
echo "   resume replay sha256:   $REPLAY_HASH"
echo "   concurrent swap sha256: $CONC_HASH"
echo "   ordering: $OVERLAP_VERDICT"
[ -n "$PRE_HASH" ] && [ "$PRE_HASH" = "$REPLAY_HASH" ] \
    || { echo "FAIL: replay bytes differ from the crashed POST (not byte-identical)"; exit 1; }
[ -n "$CONC_HASH" ] && [ "$CONC_HASH" != "$REPLAY_HASH" ] \
    || { echo "FAIL: concurrent purchase re-used the replay derivation (interleaving!)"; exit 1; }
echo "   byte-identical replay: OK; fresh derivation for the concurrent purchase: OK"

echo "== fund-safety sweep: mint must show no 10002 / duplicate-output / already-spent errors"
MINT_ERRS="$(docker logs "$PROJECT-mint" 2>&1 | grep -ciE '10002|duplicate|already.?spent' || true)"
[ "$MINT_ERRS" = "0" ] || { echo "SEV: mint log shows $MINT_ERRS duplicate/spent error lines:"; docker logs "$PROJECT-mint" 2>&1 | grep -iE '10002|duplicate|already.?spent' | head -10; exit 1; }
echo "   mint log clean (0 hits)"

echo "== health check: a fresh payment must still succeed post-recovery (full session event)"
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
print("post-recovery payment:", r.status_code)
try:
    body = r.json()
    print("session event kind:", body.get("kind"))
    tags = [t[0] for t in body.get("tags", [])]
    print("session event tags:", tags)
    ok = r.status_code == 200 and body.get("kind") == 1022 and ("allotment" in tags or "amount" in tags)
except Exception as e:
    print("non-JSON body:", r.text[:400]); ok = False
exit(0 if ok else 1)
'
echo "== scenario (d) crash-injection run complete: resume survived a concurrent Receive"
