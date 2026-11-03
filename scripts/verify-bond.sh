#!/bin/sh
# Verify the bond is ACTUALLY working, not merely configured.
#
# Configuration is not evidence. This repo has already been misled once by a
# plain-TCP connection that looked like MPTCP (local address br-lan instead of an
# uplink address), and again by `signal` endpoints that advertised an address the
# peer could not dial (MPJoinSynRx stayed 0 while AddAddrTx climbed). So check the
# observable outcomes, in order, and fail loudly on the first that is wrong.
#
# Exits 0 only if BOTH subflows reach SF_ESTABLISHED and both tunnels moved bytes.
#
# Usage: sh verify-bond.sh [peer-ip]
#   peer-ip defaults to 23.182.128.51

PEER=${1:-23.182.128.51}
IF1=${IF1:-mptcp-wan1}
IF2=${IF2:-mptcp-wan2}
PORT=${PORT:-5201}
DUR=${DUR:-8}

fail=0
note() { echo "  $*"; }
bad()  { echo "  FAIL: $*"; fail=1; }

echo "=== 1. tunnels present and handshaked ==="
for i in "$IF1" "$IF2"; do
  if ! ip link show "$i" >/dev/null 2>&1; then bad "interface $i missing"; continue; fi
  t=$(wg show "$i" transfer 2>/dev/null | awk '{print $2, $3}')
  set -- $t
  if [ "${1:-0}" -gt 0 ] && [ "${2:-0}" -gt 0 ]; then
    note "$i transfer rx=$1 tx=$2  OK"
  else
    bad "$i has no transfer (rx=${1:-0} tx=${2:-0}) -- handshake never completed"
  fi
done

echo "=== 2. endpoint flags (informational -- not a pass/fail criterion) ==="
# `signal` is NOT required for the bond to work. Measured on this bench:
#   signal subflow on tunnel addrs -> 2 subflows/token, AddAddrTx advanced
#   subflow ONLY  on tunnel addrs -> 2 subflows/token, AddAddrTx did NOT advance
# i.e. the client originates the subflows itself when its own addresses are
# routable. `signal` matters when you depend on the PEER creating them, which is
# what fails behind NAT/CGNAT (the peer cannot dial a private address).
# So report the flags, but decide pass/fail on observed subflows (section 4).
eps=$(ip mptcp endpoint show 2>/dev/null)
count=$(echo "$eps" | grep -c "signal")
note "$count endpoint(s) carry signal"
note "$(echo "$eps" | grep -c "subflow") endpoint(s) carry subflow"
echo "$eps" | sed 's/^/    /'

echo "=== 3. peer can actually reach the advertised addresses ==="
for want in 10.99.98.2 10.99.97.2; do
  echo "$eps" | grep -q "$want" || continue
  if ping -c1 -W2 "$want" >/dev/null 2>&1; then
    note "$want reachable from this node"
  else
    # Reachability must be tested from the PEER, not here; a local ping only
    # proves the local stack. Warn rather than fail so this script stays usable
    # without peer access.
    note "WARN: $want not locally pingable (test from the peer to be sure)"
  fi
done

echo "=== 4. live transfer with subflow observation ==="
# Counters before, so a stale SF_ESTABLISHED cannot satisfy the check.
: > /tmp/vb_mon.txt
ip mptcp monitor > /tmp/vb_mon.txt 2>&1 &
mon=$!
sleep 1
mptcpize run iperf3 -c "$PEER" -p "$PORT" -t "$DUR" > /tmp/vb_iperf.txt 2>&1
rc=$?
sleep 1
kill "$mon" 2>/dev/null

if [ $rc -ne 0 ]; then
  bad "iperf3 failed (rc=$rc)"
  tail -3 /tmp/vb_iperf.txt | sed 's/^/    /'
fi

# What the local address can and cannot tell us.
#
# It CANNOT prove MPTCP: the initial subflow takes its source from the routing
# table, so a perfectly good MPTCP connection can legitimately show an uplink
# address (192.168.2.20) rather than a tunnel address. Asserting "must be 10.99.*"
# produced a false failure on this bench -- the same run had two subflows on one
# token.
#
# It CAN prove the connection was NOT MPTCP: if the address is neither an uplink
# nor a tunnel address (e.g. br-lan), the transfer ran as plain TCP. That was the
# real defect that invalidated an earlier measurement.
loc=$(grep -o 'local [0-9.]*' /tmp/vb_iperf.txt | head -1 | awk '{print $2}')
if [ -n "$loc" ]; then
  lan1addr=$(ip -4 -o addr show dev lan1 2>/dev/null | awk '{print $4}' | cut -d/ -f1)
  sgaddr=$(ip -4 -o addr show dev phy0-sta0 2>/dev/null | awk '{print $4}' | cut -d/ -f1)
  if [ "$loc" = "$lan1addr" ] || [ "$loc" = "$sgaddr" ]; then
    note "initial subflow from an uplink ($loc) -- normal, route-selected"
  elif [ "${loc#10.99.}" != "$loc" ]; then
    note "initial subflow from a tunnel ($loc)"
  else
    bad "local address $loc is neither an uplink nor a tunnel address -- this was plain TCP"
  fi
fi

sf=$(grep -c "SF_ESTABLISHED" /tmp/vb_mon.txt)
note "SF_ESTABLISHED events: $sf"
[ "$sf" -ge 2 ] || bad "expected >=2 SF_ESTABLISHED, got $sf -- no multi-path bond"

# A fallback connection has no path manager at all, so this is the cheapest way to
# catch the plain-TCP mistake that once invalidated a measurement.
if grep -qi "fallback" /tmp/vb_iperf.txt 2>/dev/null; then
  bad "iperf output mentions fallback -- MPTCP fell back to plain TCP"
fi

# Both subflows should appear for the SAME token: that is what makes it one
# connection over two paths rather than two separate connections.
tokens=$(grep "SF_ESTABLISHED" /tmp/vb_mon.txt | grep -o 'token=[0-9a-f]*' | sort | uniq -c | sort -rn | head -1 | awk '{print $1}')
if [ "${tokens:-0}" -ge 2 ]; then
  note "both subflows share one token ($tokens per token)  OK"
else
  bad "no token had 2 subflows -- connections were separate, not bonded"
fi

grep -E "sender|receiver" /tmp/vb_iperf.txt | tail -2 | sed 's/^/    /'

echo
if [ "$fail" -eq 0 ]; then
  echo "BOND VERIFIED"
else
  echo "BOND NOT VERIFIED -- see FAIL lines above"
fi
exit "$fail"
