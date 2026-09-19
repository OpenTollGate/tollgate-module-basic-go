#!/bin/sh
# phases/04-payments.sh — S2 (bytes written to flash per payment), P2/P3
# (latency), P4 (CPU), F5/F7 peak, S1/S3 (store growth).
#
# WHY THIS SCRIPT TOUCHES THE ROUTER CONFIG
# -----------------------------------------
# `src/merchant/merchant.go:126` constructs the wallet with
#     tollwallet.NewWalletPort(walletDirPath, mintURLs, false)
# and `src/tollwallet/tollwallet.go:126` then REJECTS any token whose mint is
# not in accepted_mints:
#     "Token rejected. Token for mint %s is not accepted and wallet does not
#      allow swapping of untrusted mints."
# There is no CLI or API flag to override this. So a real receive round-trip
# can only be measured by temporarily adding a *test* mint to accepted_mints.
#
# What it does, in order, all reversible:
#   1. snapshot config.json (sha256 + on-router backup) and the store
#   2. inject the test mint (testnut.cashu.space: cdk-mintd + FakeWallet, so
#      ecash is free) with min_payout_amount set high so the payout scheduler
#      never fires on the test balance
#   3. restart the service, wait for full mode with (N+1) mints
#   4. mint N fresh tokens on the build host and run `tollgate wallet fund`
#      on each, sampling RSS/threads/fds and the eMMC + loop write counters
#   5. drain the test balance to a token so the wallet returns to 0 sats
#   6. RESTORE config.json verbatim, restart, verify the original mint count
#
# Set SKIP_RESTORE=1 to leave the injected config in place (debugging only).
. "$(dirname "$0")/../lib/common.sh"

CYCLES="${CYCLES:-20}"
AMOUNT="${AMOUNT:-21}"
TESTMINT="${TESTMINT:-https://testnut.cashu.space}"
MINTWALLET="${MINTWALLET:-baselinegonuts}"
CONFBAK=/etc/tollgate/config_backups/config.json.baseline-gonuts
SAMPLE_INT="${SAMPLE_INT:-1}"
# STORE_DUMP_EVERY=K pulls a copy of the bolt store every K cycles and runs
# lib/boltdump over it, so S1 (bytes per stored proof) and S3 (growth per
# operation) are measured on the logical DB, not on the capacity-allocated
# file. The copies CONTAIN THE WALLET SEED: they are written to DBSCRATCH,
# which is deliberately outside the repository and must never be committed.
STORE_DUMP_EVERY="${STORE_DUMP_EVERY:-0}"
DBSCRATCH="${DBSCRATCH:-/var/tmp/gonuts-baseline-dbscratch}"
BOLTDUMP="${BOLTDUMP:-$EXPERIMENT_DIR/raw/boltdump}"

STAMP=$(utc | tr ':' '-')
LOG="$OUT/payments-$STAMP.log"
CSV="$OUT/payments-cycles-$STAMP.csv"
SAMPLES="$OUT/payments-samples-$STAMP.csv"
STORETL="$OUT/payments-store-timeline-$STAMP.txt"

# Snapshot the logical store contents (no secrets in the output: bucket names,
# key counts and byte totals only).
dump_store() {
    _label="$1"
    [ -x "$BOLTDUMP" ] || { echo "[store $_label] boltdump not built at $BOLTDUMP" >> "$STORETL"; return; }
    mkdir -p "$DBSCRATCH"
    rssh "cat $STORE" > "$DBSCRATCH/wallet.db.$_label" 2>/dev/null || return
    {
        echo "=== store snapshot: $_label (cycle window, on-device) ==="
        echo "file_bytes_on_router: $(rsize "$STORE")"
        "$BOLTDUMP" "$DBSCRATCH/wallet.db.$_label"
    } >> "$STORETL" 2>&1
}

log() { echo "$*" | tee -a "$LOG"; }
wait_full_mode() {  # $1 = max seconds
    i=0
    while [ "$i" -lt "$1" ]; do
        i=$((i+1))
        case "$(rcurl 'http://127.0.0.1:2121/' 2>/dev/null)" in
            *'"kind":10021'*) return 0;;
        esac
        sleep 1
    done
    return 1
}

# ---------------------------------------------------------------- preflight
log "=== 04-payments $STAMP (cycles=$CYCLES amount=$AMOUNT mint=$TESTMINT) ==="
CONF_SHA_BEFORE=$(rsha "$CONF")
STORE_SHA_BEFORE=$(rsha "$STORE")
STORE_SZ_BEFORE=$(rsize "$STORE")
MINTCOUNT0=$(rssh "jq -r '.accepted_mints|length' $CONF")
log "config sha256 before     : $CONF_SHA_BEFORE"
log "store size/sha256 before : $STORE_SZ_BEFORE / $STORE_SHA_BEFORE"
log "accepted_mints before    : $MINTCOUNT0"
log "test mint /v1/info       : HTTP $(curl -s -m 10 -o /dev/null -w '%{http_code}' "$TESTMINT/v1/info")"

# Restore material must be byte-identical: copy + verify on the router.
rssh "mkdir -p /etc/tollgate/config_backups && cp -p $CONF $CONFBAK && sha256sum $CONFBAK | tee /dev/null"
rssh "cat $CONF" > "$OUT/payments-config-before-$STAMP.json"
log "backup sha256            : $(rsha $CONFBAK)"
rssh 'cat /proc/diskstats' > "$OUT/payments-diskstats-before-$STAMP.txt"

# ------------------------------------------------------------- sample start
log "--- sampler ---"
sampler_start "$RSH_TMP/samples-payments.csv" "$SAMPLE_INT" | tee -a "$LOG"

# ------------------------------------------------------------- CPU constant
# P4 is reported in CPU *seconds*, and the only on-device CPU counter is
# /proc/PID/stat utime+stime in clock ticks — /proc/PID/schedstat (nanoseconds)
# does not exist here. So USER_HZ is calibrated on the device rather than
# assumed; see lib/hzprobe.sh.
USER_HZ=$(ruser_hz)
HZF="$OUT/user-hz-$STAMP.txt"
{ echo "=== USER_HZ calibration (on-device, $STAMP) ==="; \
  echo "method: burn a busy loop for 10 s, divide its utime+stime delta by /proc/uptime delta"; \
  rssh_script "$EXPERIMENT_DIR/lib/hzprobe.sh"; echo "user_hz_used=$USER_HZ"; } > "$HZF" 2>&1
log "USER_HZ (calibrated)     : ${USER_HZ:-CALIBRATION FAILED}  ($HZF)"
[ -n "$USER_HZ" ] && [ "$USER_HZ" -gt 0 ] 2>/dev/null || { log "FATAL: USER_HZ calibration returned nothing — P4 would be a guess"; exit 1; }

# ------------------------------------------------------------- inject mint
log "--- injecting test mint into accepted_mints ---"
rssh "jq --arg url '$TESTMINT' '.accepted_mints += [{\"url\":\$url,\"min_balance\":64,\"balance_tolerance_percent\":10,\"payout_interval_seconds\":600,\"min_payout_amount\":1000000,\"price_per_step\":1,\"price_unit\":\"sat\",\"purchase_min_steps\":0}]' $CONF > /tmp/cfg.inj && cat /tmp/cfg.inj > $CONF && chmod 600 $CONF"
rssh "cat $CONF" > "$OUT/payments-config-injected-$STAMP.json"
MINTCOUNT1=$(rssh "jq -r '.accepted_mints|length' $CONF")
log "accepted_mints after     : $MINTCOUNT1 (expected $((MINTCOUNT0+1)))"

log "--- restarting service for the injected config ---"
rssh '/etc/init.d/tollgate-wrt restart' >/dev/null 2>&1
if wait_full_mode 240; then log "full mode: yes"; else log "full mode: TIMEOUT"; fi
TAGS=$(rcurl 'http://127.0.0.1:2121/' | grep -o 'price_per_step' | wc -l)
log "price_per_step tags      : $TAGS"
log "wallet status            : $(rssh "$TOLLGATE_CLI -j status" | tr -d '\n')"

# --------------------------------------------------------------- pay loop
echo "cycle,ts_epoch,latency_ms,amount_sats,store_bytes,store_sha12,mmc_wc_delta,mmc_bytes_written,mmc_ms_writing_delta,loop_wc_delta,loop_bytes_written,cpu_ticks_delta,cpu_ms,cli_json" > "$CSV"

# Store timeline (S1/S3): a snapshot before the first payment and then every
# STORE_DUMP_EVERY cycles.
if [ "$STORE_DUMP_EVERY" -gt 0 ] 2>/dev/null; then : > "$STORETL"; dump_store "c000-before"; fi

export PATH="$HOME/.local/bin:$PATH"
i=1
while [ "$i" -le "$CYCLES" ]; do
  # Fresh token from the test mint (FakeWallet auto-pays the quote).
  TOK=""; j=0
  while [ -z "$TOK" ] && [ "$j" -lt 3 ]; do
    j=$((j+1))
    timeout 90 cashu -t -y -h "$TESTMINT" -w "$MINTWALLET" invoice "$AMOUNT" >/dev/null 2>&1
    TOK=$(timeout 90 cashu -t -y -h "$TESTMINT" -w "$MINTWALLET" send "$AMOUNT" 2>/dev/null | grep -m1 '^cashu')
  done
  if [ -z "$TOK" ]; then log "cycle $i: TOKEN MINT FAILED — aborting"; break; fi

  B=$(rdisk); SB=$(rsize "$STORE")
  CPUB=$(rcpu) || CPUB=0
  t0=$(date +%s%N)
  OUTJSON=$(rssh "$TOLLGATE_CLI -j wallet fund '$TOK'" 2>&1 | tr -d '\n')
  t1=$(date +%s%N)
  CPUA=$(rcpu) || CPUA=0
  A=$(rdisk); SA=$(rsize "$STORE"); SSHA=$(rsha "$STORE" | cut -c1-12)

  lat=$(( (t1 - t0) / 1000000 ))
  # P4: CPU burned by the whole daemon across this one receive, in ticks and in
  # milliseconds (converted with the CALIBRATED USER_HZ — see lib/hzprobe.sh).
  cticks=$(( ${CPUA:-0} - ${CPUB:-0} ))
  cms=$(( cticks * 1000 / USER_HZ ))
  b_mwc=$(echo "$B" | awk '$1=="MMC"{print $2}');  a_mwc=$(echo "$A" | awk '$1=="MMC"{print $2}')
  b_msw=$(echo "$B" | awk '$1=="MMC"{print $3}');  a_msw=$(echo "$A" | awk '$1=="MMC"{print $3}')
  b_mms=$(echo "$B" | awk '$1=="MMC"{print $4}');  a_mms=$(echo "$A" | awk '$1=="MMC"{print $4}')
  b_lwc=$(echo "$B" | awk '$1=="LOOP"{print $2}'); a_lwc=$(echo "$A" | awk '$1=="LOOP"{print $2}')
  b_lsw=$(echo "$B" | awk '$1=="LOOP"{print $3}'); a_lsw=$(echo "$A" | awk '$1=="LOOP"{print $3}')
  mbytes=$(( (a_msw - b_msw) * 512 )); lbytes=$(( (a_lsw - b_lsw) * 512 ))
  amt=$(echo "$OUTJSON" | jq -r '.data.amount_received? // "na"' 2>/dev/null)

  echo "$i,$(date +%s),$lat,$amt,$SA,$SSHA,$((a_mwc-b_mwc)),$mbytes,$((a_mms-b_mms)),$((a_lwc-b_lwc)),$lbytes,$cticks,$cms,\"$(echo "$OUTJSON" | sed 's/"/\\"/g' | head -c 300)\"" >> "$CSV"
  log "cycle $i: lat=${lat}ms amt=$amt store=${SB}->${SA}B mmc_bytes=$mbytes mmc_write_ops=$((a_mwc-b_mwc)) loop_bytes=$lbytes cpu=${cms}ms (${cticks} ticks @ ${USER_HZ}Hz)"
  if [ "$STORE_DUMP_EVERY" -gt 0 ] 2>/dev/null && [ $((i % STORE_DUMP_EVERY)) -eq 0 ]; then
      dump_store "c$(printf '%03d' "$i")"
  fi
  # Let writeback land in this interval rather than the next one.
  sleep 3
  i=$((i+1))
done

# -------------------------------------------------------------- drain back
log "--- draining the test balance so the wallet returns to 0 sats ---"
rssh "$TOLLGATE_CLI -j wallet drain cashu" | head -c 500 | tee -a "$LOG"
log "balance: $(rssh "$TOLLGATE_CLI -j wallet balance" | tr -d '\n')"

# ------------------------------------------------------------- stop sample
log "stopped: $(sampler_stop "$RSH_TMP/samples-payments.csv" | tail -1) rows"
rssh "cat $RSH_TMP/samples-payments.csv" > "$SAMPLES" 2>&1 || true
rssh 'logread' > "$OUT/payments-logread-$STAMP.txt" 2>&1 || true
rssh "$TOLLGATE_CLI -j wallet info" > "$OUT/payments-wallet-info-after-$STAMP.json" 2>&1 || true

# ----------------------------------------------------------------- restore
if [ "${SKIP_RESTORE:-0}" = "1" ]; then
  log "SKIP_RESTORE=1 — leaving the injected config in place"
else
  log "--- restoring config.json verbatim ---"
  MINTCOUNT_BEFORE_RESTORE=$(rssh "jq -r '.accepted_mints|length' $CONF")
  rssh "cat $CONFBAK > $CONF && chmod 600 $CONF"
  CONF_SHA_AFTER=$(rsha "$CONF")
  log "config sha256 after      : $CONF_SHA_AFTER"
  if [ "$CONF_SHA_AFTER" = "$CONF_SHA_BEFORE" ]; then
    log "RESTORE OK: config.json byte-identical to the pre-run state"
  else
    log "RESTORE MISMATCH: $CONF_SHA_BEFORE -> $CONF_SHA_AFTER (investigate)"
  fi
  rssh '/etc/init.d/tollgate-wrt restart' >/dev/null 2>&1
  if wait_full_mode 240; then log "full mode after restore: yes"; else log "full mode after restore: TIMEOUT"; fi
  log "post-restore mint count/tags: $(rssh "jq -r '.accepted_mints|length' $CONF") / $(rcurl 'http://127.0.0.1:2121/' | grep -o 'price_per_step' | wc -l)"
  log "post-restore status      : $(rssh "$TOLLGATE_CLI -j status" | tr -d '\n')"
  log "post-restore wallet       : $(rssh "$TOLLGATE_CLI -j wallet info" | tr -d '\n')"
fi

log "=== 04-payments done; artefacts: $CSV $SAMPLES ==="
