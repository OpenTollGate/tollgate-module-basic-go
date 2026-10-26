#!/bin/sh
# Aggregation measurement, Rule 7 compliant: 3 configurations x 3 runs each.
#
# Design under test: PROXY-TERMINATED semantics -- endpoints are `subflow` only,
# never `signal`. The client initiates every MP_JOIN, so no inbound
# reachability to the client is required. Counter directionality (Rule 6):
# an INITIATOR advances MPJoinSynTx / MPJoinSynAckRx.
#
# Single-path isolation uses policy routing keyed on source address
# (uplink-policy-routing.sh must already be applied); --bind-dev segfaults here.
# No single quotes anywhere: this file is transferred over the serial console
# line by line, because BusyBox here has neither base64 nor paste.

out=/tmp/agg.txt
peer=23.182.128.51
port=5201
dur=6

: > "$out"

# TRANSPORT: SIGNAL=1 selects the advertised-address shape (signal subflow),
# otherwise proxy-terminated (subflow only). Both are measured with the same
# runner so the two shapes are comparable.
flags="subflow"
if [ "$SIGNAL" = "1" ]; then flags="signal subflow"; fi
echo "flags=$flags" >> "$out"
ip mptcp endpoint flush
ip mptcp limits set subflows 4 add_addr_accepted 4
ip mptcp endpoint add 192.168.2.15 dev lan1 $flags
ip mptcp endpoint add 10.2.1.122 dev phy0-sta0 $flags

echo "=== MPTCP state ===" >> "$out"
printf "enabled: " >> "$out"
cat /proc/sys/net/mptcp/enabled >> "$out"
echo "endpoints:" >> "$out"
ip mptcp endpoint show >> "$out" 2>&1
echo "limits:" >> "$out"
ip mptcp limits >> "$out" 2>&1
echo "rules:" >> "$out"
ip rule show >> "$out" 2>&1

# Print selected MPTCP counters as name=value pairs.
counters() {
  awk "NR==1{for(i=1;i<=NF;i++)h[i]=\$i} NR==2{for(i=1;i<=NF;i++) if(h[i] ~ /MPJoinSynTx|MPJoinSynAckRx|MPAddAddrTx|MPJoinSynRx|MPJoinAckRx|MPCapableSYNACKRX|MPCapableFallback/) printf \"%s=%s \", h[i], \$i}" /proc/net/netstat >> "$out" 2>&1
  echo "" >> "$out"
}

echo "=== counters BEFORE ===" >> "$out"
counters

run() {
  label="$1"
  shift
  echo "--- $label ---" >> "$out"
  iperf3 -c "$peer" -p "$port" -t "$dur" "$@" 2>&1 | grep -E "receiver|error" | tail -2 >> "$out"
}

echo "=== A: wired only (source-bound) x3 ===" >> "$out"
run A1 --bind 192.168.2.15
run A2 --bind 192.168.2.15
run A3 --bind 192.168.2.15

echo "=== B: stargate only (source-bound) x3 ===" >> "$out"
run B1 --bind 10.2.1.122
run B2 --bind 10.2.1.122
run B3 --bind 10.2.1.122

echo "=== C: bonded (both endpoints, unbound) x3 ===" >> "$out"
run C1
run C2
run C3

echo "=== counters AFTER ===" >> "$out"
counters

echo "=== subflow monitor during one more bonded run ===" >> "$out"
ip mptcp monitor > /tmp/mon.txt 2>&1 &
sleep 1
iperf3 -c "$peer" -p "$port" -t 5 2>&1 | grep -E "receiver" | tail -1 >> "$out"
sleep 1
kill %1 2>/dev/null
echo "SF_ESTABLISHED count:" >> "$out"
grep -cE "SF_ESTABLISHED" /tmp/mon.txt >> "$out" 2>&1
echo "monitor head:" >> "$out"
head -8 /tmp/mon.txt >> "$out" 2>&1

echo DONE_AGG >> "$out"
