#!/bin/sh
# lib/router-sampler.sh — runs ON THE ROUTER. Samples the wallet-bearing
# process and the block-device write counters, appending one CSV row per tick.
#
# Staged to the router as a FILE and launched detached with setsid(1); it
# writes its own pidfile so the host can stop it (busybox has no pkill(1))
# and so the host never has to guess a PID.
#
#   SAMPLE_OUT=/tmp/gonuts-baseline/samples.csv SAMPLE_INT=1 \
#     setsid /tmp/gonuts-baseline/router-sampler.sh </dev/null >/dev/null 2>&1 &
#
# SAMPLE_INT is INTEGER SECONDS: busybox sleep(1) rejects fractional values
# ("sleep: invalid number '0.5'"), so sub-second sampling is not available on
# this image. Per-operation peaks therefore come from VmHWM (a cumulative
# high-water mark, independent of sampling resolution), not from the sampler.
#
# Columns (14, no field contains a comma):
#   ts_epoch,vmrss_kb,vmhwm_kb,threads,fds,vm_size_kb,utime_ticks,stime_ticks,
#   mmc_writes_completed,mmc_sectors_written,mmc_ms_writing,
#   loop_writes_completed,loop_sectors_written,loop_ms_writing

PROC="${SAMPLE_PROC:-tollgate-wrt}"
OUT="${SAMPLE_OUT:-/tmp/gonuts-baseline/samples.csv}"
INT="${SAMPLE_INT:-1}"
MMC="${SAMPLE_MMC:-mmcblk0}"
LOOP="${SAMPLE_LOOP:-loop0}"

echo $$ > "$OUT.pid"
pidof "$PROC" >/dev/null 2>&1 || { echo "sampler: no process $PROC" >&2; exit 1; }

echo "ts_epoch,vmrss_kb,vmhwm_kb,threads,fds,vm_size_kb,utime_ticks,stime_ticks,mmc_writes_completed,mmc_sectors_written,mmc_ms_writing,loop_writes_completed,loop_sectors_written,loop_ms_writing" > "$OUT"

# "writes_completed,sectors_written,ms_writing" for one block device.
disk() {
    awk -v d="$1" '$3==d {printf "%s,%s,%s", $8, $10, $11}' /proc/diskstats
}

while :; do
    P=$(pidof "$PROC" 2>/dev/null | awk '{print $1}')
    if [ -n "$P" ] && [ -d "/proc/$P" ]; then
        set -- $(awk '/^VmRSS:/{r=$2} /^VmHWM:/{h=$2} /^Threads:/{t=$2} /^VmSize:/{v=$2} END{print r, h, t, v}' "/proc/$P/status")
        # utime/stime in clock ticks (USER_HZ, normally 100/s) — lets the host
        # compute P4 (CPU seconds per payment) from the window deltas.
        set -- "$@" $(awk '{print $14, $15}' "/proc/$P/stat")
        fd=$(ls "/proc/$P/fd" 2>/dev/null | wc -l | tr -d ' ')
        echo "$(date +%s),$1,$2,$3,$fd,$4,$5,$6,$(disk "$MMC"),$(disk "$LOOP")" >> "$OUT"
    fi
    sleep "$INT"
done
