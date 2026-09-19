#!/bin/sh
# phases/05-faults.sh — INJ-1 / INJ-2 / INJ-3 against a controllable mock mint.
#
# The protocol's §5 fault set needs a mint whose failure mode we control. This
# runs lib/mock-mint.py on the BUILD HOST, temporarily adds its URL to the
# router's accepted_mints (the wallet rejects untrusted mints outright, see
# phases/04-payments.sh), and feeds the wallet a well-formed token whose mint
# URL has been rewritten to point at the mock (lib/rewrite-token-mint.py).
#
# For every injection it records, before and after: store size + sha256,
# wallet balance, service PID, wall time, the CLI's JSON answer, and the mock's
# request log. A fault passes only if the store is byte-identical, the balance
# is unchanged, the service PID is unchanged, and the failure came back as an
# error rather than a crash.
#
# Restores config.json verbatim at the end (SKIP_RESTORE=1 to keep it).
. "$(dirname "$0")/../lib/common.sh"

MOCKPORT="${MOCKPORT:-18080}"
# The router must reach the build host. Resolve the address the router would
# see: the SOURCE address of the route back to ROUTER_IP — picking the first
# 192.168.x address instead is wrong on a host with more than one such
# interface (it once selected the office LAN, reachable only via the router's
# WAN; the run still worked, but the LAN path is the one we mean).
HOST_IP="${HOST_IP:-$(ip -4 route get "$ROUTER_IP" 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") {print $(i+1); exit}}')}"
HOST_IP="${HOST_IP:-$(ip -4 addr show | awk '/inet 192\.168\./{sub("/.*","",$2); print $2; exit}')}"
MOCKURL="http://$HOST_IP:$MOCKPORT"
TOKENMINT="${TOKENMINT:-https://testnut.cashu.space}"
AMOUNT="${AMOUNT:-21}"
CONFBAK=/etc/tollgate/config_backups/config.json.baseline-gonuts-faults
STAMP=$(utc | tr ':' '-')
LOG="$OUT/faults-$STAMP.log"
CSV="$OUT/faults-$STAMP.csv"
MOCKLOG="$OUT/faults-mocklog-$STAMP.log"

log() { echo "$*" | tee -a "$LOG"; }
wait_full_mode() {
    i=0
    while [ "$i" -lt "$1" ]; do
        i=$((i+1))
        case "$(rcurl 'http://127.0.0.1:2121/' 2>/dev/null)" in *'"kind":10021'*) return 0;; esac
        sleep 1
    done
    return 1
}
setmode() { curl -s -m 5 -XPOST "http://127.0.0.1:$MOCKPORT/_mode/$1" >/dev/null 2>&1; }

log "=== 05-faults $STAMP (mock=$MOCKURL mint-of-token=$TOKENMINT) ==="
CONF_SHA_BEFORE=$(rsha "$CONF")
STORE_SHA_BEFORE=$(rsha "$STORE")
log "config sha before: $CONF_SHA_BEFORE"
log "store sha before : $STORE_SHA_BEFORE  balance: $(rssh "$TOLLGATE_CLI -j wallet balance" | tr -d '\n')"

# ---- start the mock -------------------------------------------------------
setsid python3 "$EXPERIMENT_DIR/lib/mock-mint.py" --port "$MOCKPORT" \
    > "$MOCKLOG" 2>&1 < /dev/null &
MOCKPID=$!
echo "$MOCKPID" > "$OUT/faults-mock-$STAMP.pid"
i=0
while [ "$i" -lt 40 ]; do
    i=$((i+1))
    if curl -s -m 2 "http://127.0.0.1:$MOCKPORT/_mode" >/dev/null 2>&1; then break; fi
    sleep 0.5
done
log "mock pid=$MOCKPID  local probe: $(curl -s -m 3 "http://127.0.0.1:$MOCKPORT/_mode")"
log "router -> mock probe: HTTP $(rssh "curl -s -m 5 -o /dev/null -w '%{http_code}' $MOCKURL/_mode")"

# ---- inject the mock mint AND the dead port -------------------------------
# BOTH URLs go into accepted_mints. The dead-port token must be *trusted*
# before the wallet will even attempt a connection to it — the first version of
# this file injected only the mock's URL, so the "connection refused" case
# never dialled: the wallet rejected the token locally
# ("Token for mint http://…:18081 is not accepted and wallet does not allow
# swapping of untrusted mints") and the test measured the trust gate instead of
# the network failure it was written for.
DEADURL="http://$HOST_IP:$((MOCKPORT+1))"
rssh "mkdir -p /etc/tollgate/config_backups && cp -p $CONF $CONFBAK"
log "backup sha: $(rsha $CONFBAK)"
rssh "jq --arg mock '$MOCKURL' --arg dead '$DEADURL' 'def mk(\$u): {\"url\":\$u,\"min_balance\":64,\"balance_tolerance_percent\":10,\"payout_interval_seconds\":600,\"min_payout_amount\":1000000,\"price_per_step\":1,\"price_unit\":\"sat\",\"purchase_min_steps\":0}; .accepted_mints += [mk(\$mock), mk(\$dead)]' $CONF > /tmp/cfg.inj && cat /tmp/cfg.inj > $CONF && chmod 600 $CONF"
log "accepted_mints now: $(rssh "jq -r '.accepted_mints|length' $CONF") (expect 9: 7 production + mock + dead)"
rssh '/etc/init.d/tollgate-wrt restart' >/dev/null 2>&1
if wait_full_mode 240; then log "full mode: yes"; else log "full mode: TIMEOUT"; fi

# ---- craft one well-formed token pointing at the mock ---------------------
export PATH="$HOME/.local/bin:$PATH"
REALTOK=""
timeout 90 cashu -t -y -h "$TOKENMINT" -w faults-tokens invoice "$AMOUNT" >/dev/null 2>&1
REALTOK=$(timeout 90 cashu -t -y -h "$TOKENMINT" -w faults-tokens send "$AMOUNT" 2>/dev/null | grep -m1 '^cashu')
[ -n "$REALTOK" ] || { log "could not mint a base token — aborting"; exit 1; }
MOCKTOK=$(python3 "$EXPERIMENT_DIR/lib/rewrite-token-mint.py" "$REALTOK" "$MOCKURL")
DEADTOK=$(python3 "$EXPERIMENT_DIR/lib/rewrite-token-mint.py" "$REALTOK" "$DEADURL")
log "base token mint: $(printf '%s' "$REALTOK" | cut -c1-14)..."
log "  token A mint (mock) : $MOCKURL"
log "  token B mint (dead) : $DEADURL"
echo "$MOCKTOK" > "$OUT/faults-mocktoken-$STAMP.txt"

# ---- run the injections ---------------------------------------------------
echo "inj,mode,url,expect,elapsed_s,balance_sats_before,balance_sats_after,store_sha_before,store_sha_after,store_ok,pid_before,pid_after,service_alive,cli_rc,err_reported,verdict,cli_json" > "$CSV"

run_inj() {
    _id="$1"; _mode="$2"; _url="$3"; _tok="$4"; _expect="$5"
    setmode "$_mode"
    sleep 1
    sb=$(rsha "$STORE"); balb=$(rssh "$TOLLGATE_CLI -j wallet balance" | jq -r '.data.balance_sats')
    pidb=$(rssh "pidof $WALLET_PROC")
    t0=$(date +%s)
    # BOUNDED REMOTELY, via rssh_t — `timeout` cannot exec the rssh FUNCTION
    # (it needs a real binary), so the first version of this file ran NOTHING
    # and still recorded a tidy "store unchanged" row. See lib/common.sh.
    # `|| rc=$?` is required: the experiment runs under `set -e`, and a bare
    # `VAR=$(cmd)` assignment inherits cmd's exit status — a 124 here would end
    # the whole fault sweep at the first (expected!) timeout.
    rc=0
    CLI_RAW=$(rssh_t 75 "$TOLLGATE_CLI -j wallet fund '$_tok'" 2>&1) || rc=$?
    t1=$(date +%s)
    CLI=$(printf '%s' "$CLI_RAW" | tr -d '\n')
    el=$(( t1 - t0 ))
    sa=$(rsha "$STORE"); bala=$(rssh "$TOLLGATE_CLI -j wallet balance" | jq -r '.data.balance_sats')
    pida=$(rssh "pidof $WALLET_PROC")
    # Written as `if` blocks, never `[ ... ] && x`, because a failing AND-list
    # is itself a non-zero command and `set -e` then kills the sweep.
    alive=no; if [ -n "$pida" ]; then alive=yes; fi
    ok=no;    if [ "$sb" = "$sa" ]; then ok=yes; fi
    balok=no; if [ "$balb" = "$bala" ]; then balok=yes; fi
    # A broken mint must produce a REPORTED FAILURE, not merely an unchanged
    # store: an unchanged store with a "success" answer would be the worst
    # possible outcome (customer paid, nothing credited), and an unchanged
    # store with no request attempted (the 2026-09-18 bug) proves nothing.
    err=no
    case "$CLI" in *'"success": false'*|*'"success":false'*) err=yes;; esac
    if [ "$rc" = "124" ]; then
        err=yes
        log "$_id: CLI hit the 75s bound (rc=124)"
    fi
    verdict=FAIL
    if [ "$ok" = "yes" ] && [ "$balok" = "yes" ] && [ "$alive" = "yes" ] && [ "$pidb" = "$pida" ] && [ "$err" = "yes" ]; then
        case "$CLI" in *panic*|*goroutine*) verdict=PANIC;; *) verdict=PASS;; esac
    fi
    echo "$_id,$_mode,$_url,$_expect,$el,$balb,$bala,$sb,$sa,$ok,$pidb,$pida,$alive,$rc,$err,$verdict,\"$(echo "$CLI" | sed 's/"/\\"/g' | head -c 400)\"" >> "$CSV"
    log "$_id mode=$_mode url=$_url elapsed=${el}s store_ok=$ok balance_ok=$balok alive=$alive cli_rc=$rc err_reported=$err verdict=$verdict"
    log "   cli: $(echo "$CLI" | head -c 240)"
    # logread tail for this injection (ring buffer rotates — grab it now)
    rssh 'logread' 2>/dev/null | tail -12 | sed "s/^/   log| /" >> "$LOG"
}

# Expectations are comma-free on purpose: the CSV is comma-separated and an
# unquoted comma in a mid-row field shifts every later column (it silently
# mangled the first analysis run).
run_inj INJ-1a refused  "$DEADURL" "$DEADTOK" "typed rejection; no dial; store unchanged; service alive"
run_inj INJ-1b slow     "$MOCKURL" "$MOCKTOK" "times out; store unchanged; service alive"
run_inj INJ-2  http-500 "$MOCKURL" "$MOCKTOK" "5xx retryable; not terminal spent; store unchanged"
run_inj INJ-3a malformed "$MOCKURL" "$MOCKTOK" "typed parse error before any state change"
run_inj INJ-3b truncated "$MOCKURL" "$MOCKTOK" "typed parse error before any state change"

# ---- restore --------------------------------------------------------------
log "stopping mock (pid $MOCKPID)"
kill "$MOCKPID" 2>/dev/null || true

if [ "${SKIP_RESTORE:-0}" = "1" ]; then
    log "SKIP_RESTORE=1 — leaving the injected config in place"
else
    log "--- restoring config.json verbatim ---"
    rssh "cat $CONFBAK > $CONF && chmod 600 $CONF"
    CONF_SHA_AFTER=$(rsha "$CONF")
    if [ "$CONF_SHA_AFTER" = "$CONF_SHA_BEFORE" ]; then
        log "RESTORE OK: config.json byte-identical"
    else
        log "RESTORE MISMATCH: $CONF_SHA_BEFORE -> $CONF_SHA_AFTER"
    fi
    rssh '/etc/init.d/tollgate-wrt restart' >/dev/null 2>&1
    if wait_full_mode 240; then log "full mode after restore: yes"; else log "full mode after restore: TIMEOUT"; fi
    log "post-restore mints/tags : $(rssh "jq -r '.accepted_mints|length' $CONF") / $(rcurl 'http://127.0.0.1:2121/' | grep -o 'price_per_step' | wc -l)"
    log "post-restore status     : $(rssh "$TOLLGATE_CLI -j status" | tr -d '\n')"
    log "post-restore store sha  : $(rsha "$STORE")"
fi

log "=== 05-faults done; artefacts: $CSV $MOCKLOG ==="
