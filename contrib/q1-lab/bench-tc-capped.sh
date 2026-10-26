#!/bin/sh
# Q1 lab — resilience leg: cap the wired uplink at 10mbit, the bond must keep flowing.
VPS=23.182.128.51
out=/tmp/q1tc.txt
: > $out
tc qdisc add dev lan1 root tbf rate 10mbit burst 32kbit latency 400ms
sleep 1
for i in 1 2 3; do
  lan1_a=$(grep lan1: /proc/net/dev | awk '{print $2}')
  sta_a=$(grep phy0-sta0: /proc/net/dev | awk '{print $2}')
  mptcpize run iperf3 -c $VPS -t 8 2>&1 | grep -E "receiver|error" >> $out
  lan1_b=$(grep lan1: /proc/net/dev | awk '{print $2}')
  sta_b=$(grep phy0-sta0: /proc/net/dev | awk '{print $2}')
  echo "split lan1_tx=$((lan1_b-lan1_a)) sta_tx=$((sta_b-sta_a))" >> $out
done
tc qdisc del dev lan1 root
echo DONE_TC >> $out
cat $out
