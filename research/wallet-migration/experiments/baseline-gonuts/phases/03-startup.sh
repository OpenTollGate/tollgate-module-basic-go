#!/bin/sh
# phases/03-startup.sh — P1: startup time to wallet-ready (on-device).
#
# Restarts the router service ONCE and polls, at 0.25 s resolution, for:
#   t_proc    : procd reports the service running and the PID exists
#   t_http    : :2121 answers with a well-formed kind:10021 advertisement
#   t_wallet  : `tollgate -j status` reports wallet_ok == true
#
# P1 headline = t_wallet (the wallet is usable), with t_proc/t_http as context.
# The service is restarted exactly once here; the payments phase restarts it
# again for its own config window, so do not run this in a loop.
. "$(dirname "$0")/../lib/common.sh"

STAMP=$(utc | tr ':' '-')
LOG="$OUT/startup-$STAMP.log"

echo "P1: restarting $WALLET_PROC on $ROUTER_IP and polling for wallet-ready" | tee "$LOG"

# Record what we are starting from.
rssh "pidof $WALLET_PROC 2>/dev/null; uptime" | tee -a "$LOG"

# The poll runs ON the router so that the measurement is not polluted by SSH
# round-trip jitter. It writes a single line: t_proc t_http t_wallet (epoch.mmm)
rssh 'sh -s' > "$OUT/startup-poll-$STAMP.txt" 2>&1 <<'EOS' &
set -u
PROC=tollgate-wrt
start=$(cut -d' ' -f1 /proc/uptime)
ms() { awk -v s="$start" -v n="$(cut -d' ' -f1 /proc/uptime)" 'BEGIN{printf "%.2f", (n-s)}'; }
t_proc=""; t_http=""; t_wallet=""
/etc/init.d/tollgate-wrt restart >/dev/null 2>&1
i=0
# busybox sleep(1) rejects fractional seconds on this image, so the poll
# granularity is 1 s. The timestamps themselves come from /proc/uptime, which
# is centisecond-resolution — so the resolution limit is the 1 s sampling
# cadence, and P1 is reported with that granularity stated.
while [ $i -lt 120 ]; do
  i=$((i+1))
  if [ -z "$t_proc" ] && pidof "$PROC" >/dev/null 2>&1; then t_proc=$(ms); fi
  if [ -z "$t_http" ]; then
    body=$(curl -s -m 2 http://127.0.0.1:2121/ 2>/dev/null)
    case "$body" in *'"kind":10021'*) t_http=$(ms);; esac
  fi
  if [ -z "$t_wallet" ]; then
    # NOTE: `tollgate -j status` emits MarshalIndent JSON, so the field is
    # `"wallet_ok": true` WITH a space. An exact-match pattern silently never
    # fires and the poll loops to its ceiling — match with a regex instead.
    st=$(/usr/bin/tollgate -j status 2>/dev/null)
    if echo "$st" | grep -q '"wallet_ok"[[:space:]]*:[[:space:]]*true'; then t_wallet=$(ms); fi
  fi
  if [ -n "$t_wallet" ]; then break; fi
  sleep 1
done
echo "t_proc=$t_proc t_http=$t_http t_wallet=$t_wallet"
EOS
POLLPID=$!

# Bounded wait: never let a mis-matched poll pattern hang the phase (that
# failure mode cost a 300 s timeout once — see the grep-vs-case note above).
WAITED=0
while kill -0 "$POLLPID" 2>/dev/null && [ "$WAITED" -lt 150 ]; do
    sleep 1
    WAITED=$((WAITED+1))
done
if kill -0 "$POLLPID" 2>/dev/null; then
    echo "poll did not finish in ${WAITED}s — giving up on it" | tee -a "$LOG"
    kill "$POLLPID" 2>/dev/null || true
fi

# Capture the service log for the same window (ring buffer, so grab it now).
rssh 'logread' > "$OUT/startup-logread-$STAMP.txt" 2>&1 || true

echo "--- poll result ---" | tee -a "$LOG"
cat "$OUT/startup-poll-$STAMP.txt" | tee -a "$LOG"

# Post-conditions: full mode with the configured mint count, wallet_ok true.
echo "--- post-restart state ---" | tee -a "$LOG"
rssh '/usr/bin/tollgate -j status' | tee -a "$LOG"
rssh 'curl -s -m 5 http://127.0.0.1:2121/ | head -c 200; echo; apk list -I 2>/dev/null | grep -i tollgate' | tee -a "$LOG"
rssh "pidof $WALLET_PROC" | tee -a "$LOG"
