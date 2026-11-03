#!/bin/sh
# Q1 lab — interleaved A/B/C throughput over disjoint uplinks.
# VPS must run: mptcpize run iperf3 -s   (MPTCP on the server, or clients fall back)
VPS=23.182.128.51
out=/tmp/q1bench.txt
: > $out
for i in 1 2 3; do
  echo "=== round $i ===" >> $out
  echo "A_wired:" >> $out
  iperf3 -c $VPS -t 8 -B 192.168.2.8 2>&1 | grep -E "receiver|error" >> $out
  sleep 1
  echo "B_sat:" >> $out
  iperf3 -c $VPS -t 8 -B 10.2.1.122 2>&1 | grep -E "receiver|error" >> $out
  sleep 1
  echo "C_bonded:" >> $out
  lan1_a=$(grep lan1: /proc/net/dev | awk '{print $2}')
  sta_a=$(grep phy0-sta0: /proc/net/dev | awk '{print $2}')
  mptcpize run iperf3 -c $VPS -t 8 2>&1 | grep -E "receiver|error" >> $out
  lan1_b=$(grep lan1: /proc/net/dev | awk '{print $2}')
  sta_b=$(grep phy0-sta0: /proc/net/dev | awk '{print $2}')
  echo "split lan1_tx=$((lan1_b-lan1_a)) sta_tx=$((sta_b-sta_a))" >> $out
  sleep 1
done
sh "$(dirname "$0")/mptcp-counters.sh" >> $out
echo DONE_ABC >> $out
cat $out
