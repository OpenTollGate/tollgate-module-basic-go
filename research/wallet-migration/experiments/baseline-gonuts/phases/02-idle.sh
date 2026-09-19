#!/bin/sh
# phases/02-idle.sh — F5/F6/F7/F8 at idle.
#
# Samples the wallet-bearing process for IDLE_SECS (default 300 s = the
# protocol's "idle 5 min after wallet-ready"), plus one-shot structural
# snapshots (/proc/PID/maps, the fd table, limits).
#
# F6 (PSS) is UNMEASURABLE on this image: the kernel is built without
# CONFIG_PROC_PAGE_MONITOR, so /proc/PID/smaps and /proc/PID/smaps_rollup do
# not exist. The script proves that and records it rather than guessing.
. "$(dirname "$0")/../lib/common.sh"

IDLE_SECS="${IDLE_SECS:-300}"
SAMPLE_INT="${SAMPLE_INT:-1}"
STAMP=$(utc | tr ':' '-')
LOG="$OUT/idle-$STAMP.log"
SAMPLES="$OUT/idle-samples-$STAMP.csv"

log() { echo "$*" | tee -a "$LOG"; }

log "=== 02-idle $STAMP (${IDLE_SECS}s @ ${SAMPLE_INT}s) ==="
rssh "$TOLLGATE_CLI -j status" | tee -a "$LOG"
log "advertisement: $(rcurl 'http://127.0.0.1:2121/' | head -c 120)"
PID=$(rssh "pidof $WALLET_PROC | awk '{print \$1}'")
log "wallet-bearing PID: $PID"

# --- F6 feasibility proof (record the negative result, do not guess) --------
log "--- F6 feasibility ---"
rssh "for f in smaps smaps_rollup statm; do printf '/proc/$PID/%s: ' \$f; [ -r /proc/$PID/\$f ] && echo present || echo MISSING; done" | tee -a "$LOG"

# --- structural snapshots --------------------------------------------------
rssh "cat /proc/$PID/maps"    > "$OUT/idle-maps-$STAMP.txt"    2>&1 || true
rssh "ls -l /proc/$PID/fd"    > "$OUT/idle-fds-$STAMP.txt"     2>&1 || true
rssh "cat /proc/$PID/limits"  > "$OUT/idle-limits-$STAMP.txt"  2>&1 || true
rssh "cat /proc/$PID/status"  > "$OUT/idle-status-$STAMP.txt"  2>&1 || true
rssh "ls /proc/$PID/task"     > "$OUT/idle-tasks-$STAMP.txt"   2>&1 || true
rssh "cat /proc/diskstats"    > "$OUT/idle-diskstats-start-$STAMP.txt" 2>&1 || true

# --- run the sampler ------------------------------------------------------
log "--- sampler ---"
sampler_start "$RSH_TMP/samples-idle.csv" "$SAMPLE_INT" | tee -a "$LOG"

log "sampling for ${IDLE_SECS}s ..."
sleep "$IDLE_SECS"

log "stopped: $(sampler_stop "$RSH_TMP/samples-idle.csv" | tail -1) rows"
rssh "cat $RSH_TMP/samples-idle.csv" > "$SAMPLES" 2>&1 || true
rssh "cat /proc/diskstats" > "$OUT/idle-diskstats-end-$STAMP.txt" 2>&1 || true

ROWS=$(wc -l < "$SAMPLES" 2>/dev/null || echo 0)
log "sample rows: $ROWS"
if [ "$ROWS" -gt 2 ]; then
  # min/median/max of a column; sort(1) rather than gawk's asort() for mawk.
  log "--- F5/F7/F8 summary (idle, n=$((ROWS-1))) ---"
  stat3() { sort -n | awk '{v[NR]=$1} END{printf "min=%s median=%s max=%s", v[1], v[int((NR+1)/2)], v[NR]}'; }
  for spec in "2:VmRSS   KiB" "3:VmHWM   KiB" "4:Threads " "5:Open FDs"; do
    col=${spec%%:*}; label=${spec#*:}
    s=$(tail -n +2 "$SAMPLES" | cut -d, -f"$col" | stat3)
    log "$label: $s"
  done
fi

# free -m after the window (did the wallet footprint move the box?)
rssh 'free -m' | tee -a "$LOG"
log "=== 02-idle done ==="
