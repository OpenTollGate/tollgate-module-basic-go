#!/bin/sh
# lib/hzprobe.sh — RUNS ON THE ROUTER. Calibrates USER_HZ (clock ticks per
# second), which is the constant that converts /proc/PID/stat utime+stime into
# seconds for P4 (CPU seconds per payment).
#
# Why calibrate instead of assuming 100: /proc/PID/schedstat (which reports CPU
# time in NANOseconds, kernel-independent) does not exist on this image
# (verified 2026-09-19: CONFIG_SCHEDSTATS off), /proc/config.gz is absent, and
# there is no HZ sysctl. A hardcoded 100 would be an ASSUMPTION inside a
# measured number, which this experiment forbids.
#
# Method: burn a busy loop for BURN seconds and divide its tick delta by the
# wall time. Wall time comes from /proc/uptime, which has centisecond
# resolution on this image; busybox `date` has no %N (nanoseconds), so
# /proc/uptime is the best clock available on-device.
#
# Emits:  burn_cs=<centiseconds burned>
#         ticks=<utime+stime delta of the burning process>
#         user_hz=<ticks * 100 / burn_cs>
BURN="${BURN:-10}"

sh -c 'while :; do :; done' &
B=$!
sleep 1   # let the loop actually get scheduled before the first reading

a=$(awk '{print $14+$15}' "/proc/$B/stat" 2>/dev/null)
t0=$(awk '{printf "%d", $1*100}' /proc/uptime)

sleep "$BURN"

t1=$(awk '{printf "%d", $1*100}' /proc/uptime)
b=$(awk '{print $14+$15}' "/proc/$B/stat" 2>/dev/null)
kill "$B" 2>/dev/null

burn_cs=$(( t1 - t0 ))
ticks=$(( b - a ))

echo "burn_cs=$burn_cs"
echo "ticks=$ticks"
if [ "$burn_cs" -gt 0 ] && [ "$ticks" -gt 0 ]; then
    echo "user_hz=$(( ticks * 100 / burn_cs ))"
else
    echo "user_hz="
fi
