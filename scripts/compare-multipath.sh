#!/bin/sh
# Controlled aggregation comparison: single-path A vs single-path B vs bonded.
#
# WHY THIS EXISTS
# Every earlier "multiplier" number in this repo came from separate runs under
# drifting conditions, so within-configuration variance swamped any effect and the
# result was unresolvable rather than negative. This harness fixes the design:
#
#   * INTERLEAVED order (A B C A B C ...), with the order reversed on alternate
#     rounds, so slow drift in the link hits all three configurations roughly
#     equally instead of favouring whichever ran first.
#   * SHORT runs (default 5 s) so one round occupies a tight window.
#   * PER-TUNNEL BYTE DELTAS on EVERY run. This is the ground truth: the config
#     label says what was INTENDED, the deltas say what actually carried bytes. A
#     "wired-only" run whose wan2 delta is non-zero was not single-path, and this
#     records that instead of silently trusting the label.
#
# Output is structured (RESULT lines) so the host can compute ranges without the
# router doing statistics in BusyBox sh.
#
# Usage: sh compare-multipath.sh [rounds] [seconds]
#   rounds   default 3
#   seconds  default 5

ROUNDS=${1:-3}
SECS=${2:-5}
PEER=${PEER:-23.182.128.51}
PORT=${PORT:-5201}
WIRED=${WIRED:-192.168.2.20}
SG=${SG:-10.2.1.122}

# Read a tunnel's rx/tx as bare numbers.
xfer() {
  wg show "$1" transfer 2>/dev/null | awk '{print $2" "$3}'
}

one() {
  # $1 = label, $2 = optional --bind address
  label=$1
  bind=$2

  # `wg show <if> transfer` prints "rx tx". We track TX (field 2) because it
  # counts what this node SENT down each path -- that is the payload attribution.
  # (An earlier version subtracted rx from tx and printed a cumulative counter.)
  set -- $(xfer mptcp-wan1); w1a=$2
  set -- $(xfer mptcp-wan2); w2a=$2

  if [ -n "$bind" ]; then
    mptcpize run iperf3 -c "$PEER" -p "$PORT" -t "$SECS" --bind "$bind" \
      > /tmp/cmp_$label.txt 2>&1
  else
    mptcpize run iperf3 -c "$PEER" -p "$PORT" -t "$SECS" \
      > /tmp/cmp_$label.txt 2>&1
  fi

  set -- $(xfer mptcp-wan1); w1b=$2
  set -- $(xfer mptcp-wan2); w2b=$2

  d1=$((w1b - w1a))
  d2=$((w2b - w2a))

  # receiver bitrate as printed; empty if the run failed
  mbps=$(grep receiver /tmp/cmp_$label.txt | tail -1 | awk '{print $7}')
  [ -n "$mbps" ] || mbps=0

  loc=$(grep -o 'local [0-9.]*' /tmp/cmp_$label.txt | head -1 | awk '{print $2}')
  [ -n "$loc" ] || loc=none

  echo "RESULT cfg=$label mbps=$mbps wan1=$d1 wan2=$d2 local=$loc"
}

echo "=== compare-multipath rounds=$ROUNDS secs=$SECS peer=$PEER ==="
echo "wired=$WIRED stargate=$SG"
echo

i=1
while [ "$i" -le "$ROUNDS" ]; do
  echo "-- round $i --"
  # Alternate order each round to cancel within-round drift.
  if [ $((i % 2)) -eq 1 ]; then
    one wired "$WIRED"; one sg "$SG"; one bonded ""
  else
    one bonded ""; one sg "$SG"; one wired "$WIRED"
  fi
  i=$((i + 1))
done

echo
echo "DONE"
