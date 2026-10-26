#!/bin/sh
# Q1 lab — router teardown. Verified-by-absence in the recorded run.
kill $(pgrep -f fips-mptcp) 2>/dev/null
ip mptcp endpoint flush
ip rule del pref 100 2>/dev/null
ip rule del pref 101 2>/dev/null
ip route flush table 201 2>/dev/null
ip route flush table 202 2>/dev/null
tc qdisc del dev lan1 root 2>/dev/null
ip link set lan1 down
ip addr flush dev lan1
ip link set lan1 up
ip link set lan1 master br-private
rm -rf /tmp/fipsmpt /tmp/fips-mptcp /tmp/fipsmpt.yaml
rm -f /tmp/q1bench* /tmp/q1tc* /tmp/q1dbg* /tmp/q1split* /tmp/q1check* /tmp/mon.txt /tmp/q1teardown.sh
echo "router cleaned; lan1 back in br-private:"
ip link show lan1 | head -1
