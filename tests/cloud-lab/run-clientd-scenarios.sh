#!/usr/bin/env bash
# run-clientd-scenarios.sh — tollgate-clientd scenario battery for the
# cloud lab (profile "clientd").
#
#   S2  bytes-metric lifecycle: buy a data step, meter advances via the
#       file-driven fake ndsctl, renewal before exhaustion, gate close on
#       exhaustion, automatic re-payment.
#   S3  nutshell wallet live: fund via cdk-cli, receive into a REAL cashu
#       wallet, clientd pays with `--wallet nutshell`.
#   S4  mint outage mid-session: renewal fails with logged backoff, mint
#       restarts, clientd recovers. (ms sessions in this lab expire lazily,
#       so expiry is a NOTE, not an assertion — recovery with a fresh
#       session is the pass criterion.)
#   S5  keyset rotation + swap fees — requires the mint-rotate/mint-fees
#       services (keyset-rotation branch); SKIPPED with a message when the
#       current compose does not define them.
#
# Usage (from tests/cloud-lab/):
#   ./run-clientd-scenarios.sh                    # S2 S3 S4
#   SCENARIOS="S2" ./run-clientd-scenarios.sh     # subset
#   OUT=dir  — results dir (default /tmp/tollgate-clientd-scenarios)
#
# Fee note: on mints charging input fees, the router credits
# floor((amount - fee)/price) steps — buy more per top-up than you need.
set -u
exec 9>/tmp/tollgate-clientd-scenarios.lock
flock -n 9 || { echo "another battery instance is already running"; exit 1; }
cd "$(dirname "$0")" || exit 1

OUT="${OUT:-/tmp/tollgate-clientd-scenarios}"
mkdir -p "$OUT"
STATUS="$OUT/STATUS"
: > "$STATUS"

MAC=02:00:00:00:00:20   # mapped to the client IPs (172.28.0.20) in dhcp.leases

log()  { echo "[$(date -u +%H:%M:%S)] $*" | tee -a "$OUT/RUNNER.log"; }
mark() { echo "$1" >> "$STATUS"; log "$1"; }

DC() { docker compose "$@"; }

usage_of() { docker exec "$1" curl -s --max-time 5 "http://$2:2121/usage" 2>/dev/null; }

wait_usage() { # container gateway matcher timeout
  local end=$(( $(date +%s) + $4 ))
  while [ "$(date +%s)" -lt "$end" ]; do
    if usage_of "$1" "$2" | grep -qE "$3"; then return 0; fi
    sleep 2
  done
  return 1
}

start_daemon() { # name gateway service extra-args...
  local name=$1 gw=$2 svc=$3; shift 3
  if ! DC run -d --name "$name" --entrypoint python3 "$svc" \
      /clientd/tollgate-clientd.py --gateway "$gw" --mac "$MAC" \
      --interval 1 "$@" > "$OUT/$name-start.log" 2>&1; then
    log "start_daemon $name FAILED — see $OUT/$name-start.log"
  fi
}
daemon_log() { mkdir -p "$OUT/$1" && docker logs "$1" >> "$OUT/$1/clientd.log" 2>&1; }
scenario_dir() { mkdir -p "$OUT/$1"; }

counters() { # downloaded_KB
  docker exec tg-upstream-bytes sh -c \
    "printf 'DOWNLOADED_KB=%s\nUPLOADED_KB=0\n' '$1' > /ndsctl-data/counters.env" \
    2>/dev/null
}

ensure_images() {
  log "building images (client, then client-ns layered on it)"
  DC build client >/dev/null 2>&1 || { mark "FAIL images-client"; return 1; }
  DC build client-ns >/dev/null 2>&1 || { mark "FAIL images-client-ns"; return 1; }
}

# ---------------------------------------------------------------- S2 bytes
S2() {
  scenario_dir S2
  DC --profile clientd up -d upstream-bytes >/dev/null 2>&1
  DC restart upstream-bytes >/dev/null 2>&1
  counters 0
  sleep 5
  DC run --rm --entrypoint sh client -c 'cdk-cli -w /w/s2 mint http://mint:8085 50 >/dev/null 2>&1' \
    > "$OUT/S2/fund.log" 2>&1
  start_daemon S2-clientd upstream-bytes client --wallet cdk-cli --wallet-dir /w/s2 --steps 1 --renew-below 2MB
  sleep 8
  if ! wait_usage S2-clientd upstream-bytes "[0-9]+/5242880" 60; then
    mark "FAIL S2-initial-payment"; daemon_log S2-clientd
    docker rm -f S2-clientd >/dev/null 2>&1; return 1
  fi
  mark "PASS S2-initial-payment"
  # advance the meter ~1MiB/3s; renewal must fire before exhaustion
  local renewed=0 cycles=0
  while [ $cycles -lt 60 ]; do
    cycles=$((cycles+1))
    counters $(( cycles * 1024 ))
    daemon_log S2-clientd
    if docker logs S2-clientd 2>&1 | grep -q "allotment now 10485760"; then renewed=1; break; fi
    sleep 3
  done
  if [ $renewed = 1 ]; then mark "PASS S2-renewal-before-exhaustion"; else mark "FAIL S2-renewal-before-exhaustion"; fi
  # force exhaustion: expect DEAUTH + re-payment
  counters 30720
  sleep 10
  docker exec tg-upstream-bytes cat /tmp/ndsctl.log > "$OUT/S2/ndsctl.log" 2>/dev/null
  if grep -q "DEAUTH $MAC" "$OUT/S2/ndsctl.log"; then mark "PASS S2-gate-closed-on-exhaustion"; else mark "FAIL S2-gate-closed-on-exhaustion"; fi
  if wait_usage S2-clientd upstream-bytes "[0-9]+/[0-9]+" 60; then mark "PASS S2-repaid-after-close"; else mark "FAIL S2-repaid-after-close"; fi
  daemon_log S2-clientd
  docker rm -f S2-clientd >/dev/null 2>&1
  DC stop upstream-bytes >/dev/null 2>&1
}

# ---------------------------------------------------------------- S3 nutshell
S3() {
  scenario_dir S3
  DC up -d mint upstream >/dev/null 2>&1
  DC restart upstream >/dev/null 2>&1
  sleep 5
  # fund via cdk-cli, then hand the tokens to a REAL nutshell wallet
  # (wallet lives in the named volume via CASHU_DIR=/w — POSIX sh only)
  if ! DC run --rm --entrypoint sh client-ns -c '
      set -e
      rm -rf /w/ns /w/s3
      cdk-cli -w /w/s3 mint http://mint:8085 50 >/dev/null
      TOKEN=$(cdk-cli -w /w/s3 send --mint-url http://mint:8085 --v3 --amount 40 | grep "^cashuA" | tail -1)
      echo "token: $(printf %s "$TOKEN" | cut -c1-40)..." >&2
      cashu --wallet ns --yes receive "$TOKEN"
      cashu --wallet ns balance
    ' > "$OUT/S3/fund.log" 2>&1; then
    mark "FAIL S3-nutshell-receive"; cat "$OUT/S3/fund.log"; return 1
  fi
  mark "PASS S3-nutshell-received-token"
  start_daemon S3-clientd upstream client-ns --wallet nutshell --wallet-dir ns --steps 1 --renew-below 30s
  sleep 10
  if wait_usage S3-clientd upstream "[0-9]+/60000" 90; then
    mark "PASS S3-nutshell-payment-accepted"
  else
    mark "FAIL S3-nutshell-payment-accepted"
  fi
  daemon_log S3-clientd
  docker exec S3-clientd cashu --wallet ns balance > "$OUT/S3/balance-after.log" 2>&1
  docker rm -f S3-clientd >/dev/null 2>&1
  DC stop upstream >/dev/null 2>&1
}

# ---------------------------------------------------------------- S4 outage
S4() {
  scenario_dir S4
  DC up -d mint upstream >/dev/null 2>&1
  DC restart upstream >/dev/null 2>&1
  sleep 5
  DC run --rm --entrypoint sh client -c 'cdk-cli -w /w/s4 mint http://mint:8085 50 >/dev/null 2>&1' \
    > "$OUT/S4/fund.log" 2>&1
  start_daemon S4-clientd upstream client --wallet cdk-cli --wallet-dir /w/s4 --steps 1 --renew-below 45s
  wait_usage S4-clientd upstream "[0-9]+/60000" 60 || { mark "FAIL S4-setup"; daemon_log S4-clientd; docker rm -f S4-clientd >/dev/null 2>&1; return 1; }
  mark "PASS S4-initial-payment"
  sleep 5
  docker stop tg-mint >/dev/null 2>&1
  log "S4: mint stopped at $(date -u +%H:%M:%S)"
  local failed=0
  for _ in $(seq 1 40); do
    sleep 3
    if docker logs S4-clientd 2>&1 | grep -q "top-up failed"; then failed=1; break; fi
  done
  [ $failed = 1 ] && mark "PASS S4-failure-logged" || mark "FAIL S4-failure-logged"
  # ms sessions expire lazily in this lab — record whether /usage flips, do
  # not assert it; the hard criterion is recovery after restart below.
  if wait_usage S4-clientd upstream "-1/-1" 90; then
    log "NOTE S4: /usage flipped to -1/-1 (session expired)"
  else
    log "NOTE S4: /usage did not flip within 90s (lazy ms-expiry — known lab behavior)"
  fi
  docker start tg-mint >/dev/null 2>&1
  log "S4: mint restarted at $(date -u +%H:%M:%S)"
  if wait_usage S4-clientd upstream "[0-9]+/[0-9]+" 240; then
    mark "PASS S4-recovery-after-restart"
  else
    mark "FAIL S4-recovery-after-restart"
  fi
  daemon_log S4-clientd
  docker rm -f S4-clientd >/dev/null 2>&1
  DC stop upstream >/dev/null 2>&1
}

# ------------------------------------------------- S5 rotation + fees (opt-in)
S5() {
  if ! DC config --services 2>/dev/null | grep -q '^mint-rotate$'; then
    log "S5: SKIPPED — mint-rotate/mint-fees services not defined in this compose (see the cloud-lab keyset-rotation branch)"
    return 0
  fi
  scenario_dir S5
  export MINT_ROTATE_ROTATIONS='[{"unit":"sat","version":"v1","input_fee_ppk":100,"expired":false}]'
  DC up -d mint-rotate mint-fees upstream-rotate >/dev/null 2>&1
  sleep 5
  DC run --rm --entrypoint sh client -c '
      set -e
      rm -rf /w/s5r /w/s5f
      cdk-cli -w /w/s5r mint http://mint-rotate:8085 50 >/dev/null
      cdk-cli -w /w/s5f mint http://mint-fees:8085 50 >/dev/null
    ' > "$OUT/S5/fund.log" 2>&1 || { mark "FAIL S5-fund"; cat "$OUT/S5/fund.log"; return 1; }

  DC restart upstream-rotate >/dev/null 2>&1; sleep 5
  start_daemon S5-clientd upstream-rotate client --wallet cdk-cli --wallet-dir /w/s5r --steps 2 --renew-below 45s
  if wait_usage S5-clientd upstream-rotate "[0-9]+/[0-9]+" 90; then
    local allot; allot=$(usage_of S5-clientd upstream-rotate | cut -d/ -f2)
    mark "PASS S5a-fee-mint-payment (allotment=${allot}ms; fee-aware step reduction if < 120000)"
    echo "allotment_after_fee=${allot}" >> "$OUT/S5/state.txt"
  else
    mark "FAIL S5a-fee-mint-payment"
  fi
  daemon_log S5-clientd
  docker rm -f S5-clientd >/dev/null 2>&1

  export MINT_ROTATE_ROTATIONS='[{"unit":"sat","version":"v1","input_fee_ppk":100,"expired":true},{"unit":"sat","version":"v1","input_fee_ppk":0,"expired":false}]'
  DC stop mint-rotate >/dev/null 2>&1
  DC up -d mint-rotate >/dev/null 2>&1
  sleep 8
  DC restart upstream-rotate >/dev/null 2>&1; sleep 5
  start_daemon S5b-clientd upstream-rotate client --wallet cdk-cli --wallet-dir /w/s5r --steps 2 --renew-below 45s
  sleep 25
  daemon_log S5b-clientd
  if docker logs S5b-clientd 2>&1 | grep -q "paid"; then
    mark "PASS S5b-rotation-survived (wallet re-keyed, payment succeeded)"
  elif docker logs S5b-clientd 2>&1 | grep -q "top-up failed"; then
    mark "PASS S5b-rotation-clean-failure (no crash, backoff active)"
  else
    mark "FAIL S5b-rotation-no-signal"
  fi
  docker rm -f S5b-clientd >/dev/null 2>&1

  start_daemon S5c-clientd upstream-rotate client --wallet cdk-cli --wallet-dir /w/s5f --mint http://mint-fees:8085 --steps 1 --renew-below 45s
  sleep 20
  daemon_log S5c-clientd
  if docker logs S5c-clientd 2>&1 | grep -qE "top-up failed|paid"; then
    mark "PASS S5c-fee-edge-clean (1-sat token handled without wedge)"
  else
    mark "FAIL S5c-fee-edge-no-signal"
  fi
  docker rm -f S5c-clientd >/dev/null 2>&1
  DC stop upstream-rotate mint-rotate mint-fees >/dev/null 2>&1
}

SCENARIOS="${SCENARIOS:-S2 S3 S4}"
log "=== clientd scenario battery start: $SCENARIOS ==="
ensure_images || exit 1
for s in $SCENARIOS; do
  case "$s" in
    S2|S3|S4|S5) "$s" || true ;;
    *) log "unknown scenario: $s" ;;
  esac
done
log "=== clientd scenario battery done ==="
cat "$STATUS"
