#!/bin/sh
# lib/common.sh — shared env for the gonuts baseline experiment.
# Source me. No absolute home paths: OUT defaults next to this experiment.
#
# Router access: the lab default password is "password"; override with
#   ROUTER_PW=<pw> ROUTER_IP=<ip> ./run.sh <phase>
# Nothing here contains a real secret.
#
# Image-specific facts baked in below (measured, 2026-09-18, OpenWrt 25.12.5
# on GL-MT6000): busybox has NO stat(1), NO pkill(1), NO nohup(1), and its
# sleep(1) rejects fractional seconds. Hence: `wc -c <` for sizes, a pidfile
# for process control, setsid(1) for detaching, integer sample intervals.

set -eu

EXPERIMENT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${OUT:-$EXPERIMENT_DIR/raw}"
ROUTER_IP="${ROUTER_IP:-192.168.1.1}"
ROUTER_USER="${ROUTER_USER:-root}"
ROUTER_PW="${ROUTER_PW:-password}"
WALLET_PROC="${WALLET_PROC:-tollgate-wrt}"
TOLLGATE_CLI="${TOLLGATE_CLI:-/usr/bin/tollgate}"
# Remote scratch (tmpfs on OpenWrt) — the durable artefact store is $OUT.
RSH_TMP="/tmp/gonuts-baseline"
# The wallet store and its config, as deployed.
STORE="${STORE:-/etc/tollgate/wallet.db}"
CONF="${CONF:-/etc/tollgate/config.json}"

# ssh helper as a FUNCTION, never a quoted variable (kit pitfall 2026-08-24).
rssh() {
    sshpass -p "$ROUTER_PW" ssh \
        -o ConnectTimeout=10 \
        -o StrictHostKeyChecking=accept-new \
        -o PubkeyAuthentication=no \
        -o PreferredAuthentications=password,keyboard-interactive \
        "$ROUTER_USER@$ROUTER_IP" "$@"
}

# Run a local script on the router via stdin redirect (no sftp-server on OpenWrt).
rssh_script() { rssh 'sh -s' < "$1"; }

# Like rssh(), but BOUNDED. Needed wherever the remote command is *expected* to
# hang (fault injection against a mint that accepts the connection and never
# answers). `timeout` cannot wrap the rssh FUNCTION — it execs a real binary —
# so `timeout 75 rssh ...` fails with "failed to execute process: No such file
# or directory (os error 2)" and the command never runs. That bug made the whole
# first 05-faults run a no-op (2026-09-18): every row recorded a store that was
# unchanged because *nothing was attempted*. The timeout has to be applied to
# the ssh binary itself, from inside a function.
rssh_t() {
    _t="$1"; shift
    timeout "$_t" sshpass -p "$ROUTER_PW" ssh \
        -o ConnectTimeout=10 \
        -o StrictHostKeyChecking=accept-new \
        -o PubkeyAuthentication=no \
        -o PreferredAuthentications=password,keyboard-interactive \
        "$ROUTER_USER@$ROUTER_IP" "$@"
}

# CPU ticks (utime+stime) consumed by the wallet-bearing process so far.
rcpu() { rssh "awk '{print \$14+\$15}' /proc/\$(pidof $WALLET_PROC)/stat" | tr -d ' \r'; }

# USER_HZ, calibrated on the device (see lib/hzprobe.sh) — never assumed.
ruser_hz() { rssh_script "$EXPERIMENT_DIR/lib/hzprobe.sh" | awk -F= '/^user_hz=/{print $2}'; }

# curl executing ON the router (for the router's own :2121 API).
rcurl() { rssh "curl -s -m 10 $*"; }

utc() { date -u '+%Y-%m-%dT%H:%M:%SZ'; }

# sha256 of a router-side file (busybox sha256sum exists).
rsha() { rssh "sha256sum $1" | awk '{print $1}'; }

# Size in bytes of a router-side file (stat(1) is absent; wc -c is present).
rsize() { rssh "wc -c < $1" | tr -d ' \r'; }

# Disk write counters, labelled and newline-separated so fields can never
# concatenate ambiguously:
#   MMC <writes_completed> <sectors_written> <ms_writing>
#   LOOP <writes_completed> <sectors_written> <ms_writing>
rdisk() {
    rssh 'awk "\$3==\"mmcblk0\"{printf \"MMC %s %s %s\n\", \$8, \$10, \$11} \$3==\"loop0\"{printf \"LOOP %s %s %s\n\", \$8, \$10, \$11}" /proc/diskstats'
}

# ---- sampler control -------------------------------------------------------
# Stage the sampler as a file and launch it detached. Busybox lacks nohup(1),
# and `setsid sh -s` cannot work because it needs the ssh channel on stdin —
# so the script is staged by path and detached with setsid + </dev/null.
sampler_start() {
    _csv="$1"; _int="${2:-1}"
    rssh "mkdir -p $RSH_TMP"
    rssh "cat > $RSH_TMP/router-sampler.sh" < "$EXPERIMENT_DIR/lib/router-sampler.sh"
    rssh "chmod +x $RSH_TMP/router-sampler.sh"
    rssh "SAMPLE_OUT=$_csv SAMPLE_INT=$_int SAMPLE_PROC=$WALLET_PROC setsid $RSH_TMP/router-sampler.sh </dev/null >$RSH_TMP/sampler.out 2>&1 & echo sampler-launched"
    sleep 2
    rssh "head -1 $_csv; echo pid=\$(cat $_csv.pid 2>/dev/null)"
}

sampler_stop() {
    _csv="$1"
    rssh "if [ -f $_csv.pid ]; then kill \$(cat $_csv.pid) 2>/dev/null && echo sampler-stopped; else echo no-pidfile; fi"
    sleep 1
    rssh "wc -l < $_csv 2>/dev/null || echo 0"
}

mkdir -p "$OUT"
