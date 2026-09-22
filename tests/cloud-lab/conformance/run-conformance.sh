#!/usr/bin/env bash
# run-conformance.sh — Go-side fast-subset conformance lane (#503) against
# the shared matrix in PRTA (physical-router-test-automation), driven
# through the PRTA mint fault proxy running as a lab service.
#
# Phases (in-container pytest, orchestrated here because the client
# container has no docker control):
#   duplicates      duplicate-post-sequential + duplicate-post-concurrent
#   swap-timeout    swap-timeout-retry (drop_response on the first swap)
#   kill            pay-kill-post-receive-pre-session: the proxy's
#                   notify_on:response webhook targets an unroutable TEST-NET
#                   address, so it blocks ~5s AFTER the mint processed the
#                   swap; this runner polls the proxy state from the host,
#                   docker-kills the daemon inside that window, restarts it,
#                   and the aftermath phase measures the retry.
#   alias           mint-alias-spellings (config vs token spelling; the
#                   wallet mint-count is read host-side via the CLI socket)
#
# Each phase writes a verdict part; this script merges them into a PRTA
# verdict table (.conformance/verdicts-go.json) and prints it.
#
# Usage (from tests/cloud-lab/):
#   ./conformance/run-conformance.sh         # full lane, teardown at the end
#   KEEP=1 ./conformance/run-conformance.sh  # leave the lab up
#
# Skips cleanly (exit 0) when docker is missing or the PRTA checkout is
# absent. Override the PRTA location with PRTA_CONFORMANCE_DIR. Isolation
# knobs: COMPOSE_PROJECT_NAME, CLOUD_LAB_EXTRA_COMPOSE (see README).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR/.."

PROXY_HOST_URL="${PROXY_HOST_URL:-http://127.0.0.1:9090}"
PROXY_MINT_URL="${PROXY_MINT_URL:-http://faultproxy:9090}"

if ! command -v docker >/dev/null 2>&1; then
    echo "conformance: docker not available — skipping (lane requires the cloud lab)"
    exit 0
fi

PRTA_CONFORMANCE_DIR="${PRTA_CONFORMANCE_DIR:-$SCRIPT_DIR/../../../physical-router-test-automation/tests/conformance}"
if [ ! -f "$PRTA_CONFORMANCE_DIR/matrix.yaml" ] || [ ! -f "$PRTA_CONFORMANCE_DIR/faultproxy.py" ]; then
    echo "conformance: PRTA conformance dir not found at $PRTA_CONFORMANCE_DIR — skipping."
    echo "conformance: clone OpenTollGate/physical-router-test-automation next to this repo"
    echo "conformance: or set PRTA_CONFORMANCE_DIR. The matrix is co-owned; this lane"
    echo "conformance: never vendors a fork of it (#503)."
    exit 0
fi

CONF_DIR=".conformance"
rm -f "$CONF_DIR"/verdict-parts/*.json "$CONF_DIR"/evidence/*.log "$CONF_DIR"/state.json "$CONF_DIR"/kill.json 2>/dev/null || true
mkdir -p "$CONF_DIR/verdict-parts" "$CONF_DIR/evidence"

python3 - "$PRTA_CONFORMANCE_DIR/matrix.yaml" <<'EOF'
import sys, yaml
fast = {"swap-timeout-retry", "duplicate-post-sequential", "duplicate-post-concurrent",
        "mint-alias-spellings", "pay-kill-post-receive-pre-session"}
m = yaml.safe_load(open(sys.argv[1]))
have = {s["id"] for s in m["scenarios"] if s.get("subset") == "fast"}
missing = fast - have
assert not missing, f"matrix.yaml fast subset is missing {missing}; lane and matrix drifted"
print(f"conformance: matrix OK ({len(m['scenarios'])} scenarios, fast subset matches this lane)")
EOF

python3 - "$PROXY_MINT_URL" "$CONF_DIR/runtime-config.json" <<'EOF'
import json, sys
proxy_url = sys.argv[1]
cfg = json.load(open("configs/upstream-config.json"))
cfg["accepted_mints"] = [dict(cfg["accepted_mints"][0], url=proxy_url)]
json.dump(cfg, open(sys.argv[2], "w"), indent=2)
EOF

# The proxy image builds from PRTA's faultproxy.py copied verbatim plus a
# trivial Dockerfile — generated at run time, never committed.
mkdir -p "$CONF_DIR/proxy-build"
cp "$PRTA_CONFORMANCE_DIR/faultproxy.py" "$CONF_DIR/proxy-build/faultproxy.py"
cat >"$CONF_DIR/proxy-build/Dockerfile" <<'EOF'
FROM python:3.12-slim-bookworm
COPY faultproxy.py /app/faultproxy.py
ENTRYPOINT ["python3", "/app/faultproxy.py"]
EOF

COMPOSE=(docker compose -f docker-compose.yml -f docker-compose.conformance.yml)
if [ -n "${CLOUD_LAB_EXTRA_COMPOSE:-}" ]; then
    COMPOSE+=(-f "$CLOUD_LAB_EXTRA_COMPOSE")
fi

cleanup() {
    docker logs tg-upstream >"$CONF_DIR/evidence/tg-upstream.log" 2>&1 || true
    docker logs tg-faultproxy >"$CONF_DIR/evidence/faultproxy.log" 2>&1 || true
    rm -f "$CONF_DIR/state.json" "$CONF_DIR/kill.json"
    if [ "${KEEP:-0}" != "1" ]; then
        "${COMPOSE[@]}" down -v >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT

echo "== conformance: build + start mint, fault proxy and upstream (config points at the proxy)"
"${COMPOSE[@]}" up -d --build mint faultproxy upstream >/dev/null

for _ in $(seq 1 30); do
    curl -sf "$PROXY_HOST_URL/__fault/state" >/dev/null && break
    sleep 1
done
curl -sf "$PROXY_HOST_URL/__fault/state" >/dev/null || {
    echo "conformance: fault proxy did not come up (see $CONF_DIR/evidence/faultproxy.log)"
    exit 1
}

run_phase() {
    local selector="$1"
    local log="$CONF_DIR/evidence/$selector.log"
    # A failing phase is a measured verdict, not a lane error: the runner
    # must complete every phase and let the verdict table speak.
    "${COMPOSE[@]}" run --rm --no-deps \
        -e CONFORMANCE_LANE=1 -e PROXY_MINT_URL="$PROXY_MINT_URL" \
        --entrypoint sh client \
        -c "cd /tests && python3 -m pytest -q conformance/test_conformance.py -k '$selector' --tb=short" \
        2>&1 | tee "$log" || true
}

echo "== phase: duplicates"
run_phase "duplicate_post"

echo "== phase: swap timeout (drop_response on the first swap; ~1 min)"
run_phase "swap_timeout_retry"

echo "== phase: kill at post-receive/pre-session"
run_phase "kill_boundary_setup"

# The notify webhook targets an unroutable TEST-NET address and blocks ~5s
# after the mint processed the swap; the rule's hit counter flips the moment
# the swap response is ready. Poll for it and kill inside the hold window.
KILLED=0
for _ in $(seq 1 120); do
    HITS=$(curl -sf "$PROXY_HOST_URL/__fault/state" 2>/dev/null | python3 -c '
import json, sys
state = json.load(sys.stdin)
hits = [r.get("hits", 0) for r in state.get("rules", []) if r.get("id") == "kill-on-swap-response"]
print(hits[0] if hits else 0)
' 2>/dev/null || echo 0)
    if [ "${HITS:-0}" -ge 1 ]; then
        docker kill tg-upstream >/dev/null
        echo "{\"container\": \"tg-upstream\", \"trigger\": \"kill-on-swap-response hit\", \"killed_at\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}" \
            >"$CONF_DIR/kill.json"
        KILLED=1
        break
    fi
    sleep 0.25
done
[ "$KILLED" = 1 ] || { echo "conformance: kill trigger never fired"; exit 1; }
cat "$CONF_DIR/kill.json"

docker start tg-upstream >/dev/null
for _ in $(seq 1 30); do curl -sf http://127.0.0.1:2121/ >/dev/null && break; sleep 1; done
curl -sf http://127.0.0.1:2121/ >/dev/null || { echo "conformance: upstream did not come back"; exit 1; }

run_phase "kill_boundary_aftermath"

echo "== phase: mint alias spellings"
run_phase "mint_alias_spellings"

echo "== host-side: wallet mint count via the CLI socket (alias fold)"
docker exec tg-upstream sh -c \
    "printf '%s\n' '{\"command\":\"wallet\",\"args\":[\"info\"]}' | socat - UNIX-CONNECT:/etc/tollgate/tollgate.sock" \
    >"$CONF_DIR/evidence/wallet-info.json" 2>"$CONF_DIR/evidence/wallet-info.err" || true

python3 - "$CONF_DIR" <<'EOF'
import glob, json, sys, time
conf = sys.argv[1]
rows = [json.load(open(p)) for p in sorted(glob.glob(f"{conf}/verdict-parts/*.json"))]

expected = {"swap-timeout-retry", "duplicate-post-sequential", "duplicate-post-concurrent",
            "mint-alias-spellings", "pay-kill-post-receive-pre-session"}
seen = {r["scenario"] for r in rows}
for missing in sorted(expected - seen):
    rows.append({"scenario": missing, "invariants": {},
                 "verdict": "error",
                 "evidence": [f".conformance/evidence/"],
                 "detail": "phase produced no verdict part (see phase logs)"})

try:
    info = json.load(open(f"{conf}/evidence/wallet-info.json"))
    mint_count = info.get("data", {}).get("mint_count")
    mints = list(info.get("data", {}).get("mint_balances", {}).keys())
except (ValueError, OSError):
    mint_count, mints = None, []
for row in rows:
    if row["scenario"] == "mint-alias-spellings":
        if mint_count is None:
            row["invariants"]["no-fund-loss"] = {"status": "error",
                                                 "detail": "wallet-info not readable (socat/CLI socket)"}
            row["verdict"] = "error"
        else:
            ok = mint_count == 1
            row["invariants"]["no-fund-loss"] = {
                "status": "pass" if ok else "fail",
                "detail": f"mint_count={mint_count} mints={mints} (must be exactly one canonical entry)",
            }
            if not ok:
                row["verdict"] = "fail"

table = {
    "backend": "go",
    "matrix_version": 1,
    "generated": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    "rows": rows,
}
with open(f"{conf}/verdicts-go.json", "w") as fh:
    json.dump(table, fh, indent=2)

print("\nscenario".ljust(38), "verdict".ljust(8), "invariants")
for row in rows:
    inv = " ".join(f"{k}={v['status']}" for k, v in sorted(row["invariants"].items()))
    print(row["scenario"].ljust(38), row["verdict"].ljust(8), inv)
fails = [r for r in rows if r["verdict"] == "fail"]
errors = [r for r in rows if r["verdict"] == "error"]
print(f"\nconformance: {len(rows)} scenarios, {len(fails)} FAIL, {len(errors)} ERROR"
      + (" — see .conformance/verdicts-go.json and .conformance/evidence/" if fails or errors else ""))
sys.exit(1 if (fails or errors) else 0)
EOF
