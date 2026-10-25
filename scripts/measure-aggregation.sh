#!/bin/sh
# THE measurement: single-path baselines vs bonded, over two genuinely disjoint
# uplinks (policy routing from uplink-policy-routing.sh must already be applied).
#
# Peer must be MPTCP-capable for the bonded case to mean anything -- a plain-TCP
# listener makes every connection FALL BACK, and a fallback connection has no
# path manager (see the kit's test_no_mptcp_fallback_before_any_measurement).
# The peer check is done from the host side and its result passed in via
# /tmp/peer_ok before this script runs; here we assert the local prerequisites.
out=/tmp/measure.txt
: > "$out"

{
  echo "== preconditions =="
  cat /proc/sys/net/mptcp/enabled
  ip rule show
  ip mptcp endpoint show
  ip mptcp limits
  echo "== peer multipath precheck (local view of negotiation) =="
  grep -o 'MPCapableSYNACKRX[^ ]*' /proc/net/netstat >/dev/null 2>&1 && echo has_counters || echo no_counters

  echo "== A: wired only (source-bound) =="
  iperf3 -c 23.182.128.51 -p 5201 -t 10 --bind 192.168.2.15 2>&1 | grep -E 'receiver|error' | tail -2

  echo "== B: stargate only (source-bound) =="
  iperf3 -c 23.182.128.51 -p 5201 -t 10 --bind 10.2.1.122 2>&1 | grep -E 'receiver|error' | tail -2

  echo "== C: BONDED (both endpoints registered, no bind) =="
  iperf3 -c 23.182.128.51 -p 5201 -t 10 2>&1 | grep -E 'receiver|error' | tail -2

  echo "== subflow evidence from the last connection =="
  ip mptcp monitor > /tmp/mon_after.txt 2>&1 &
  sleep 1
  iperf3 -c 23.182.128.51 -p 5201 -t 6 2>&1 | grep -E 'receiver' | tail -1
  sleep 1
  kill %1 2>/dev/null
  tail -6 /tmp/mon_after.txt 2>/dev/null

  echo DONE_MEASURE
} >> "$out" 2>&1
echo WROTE_MEASURE
