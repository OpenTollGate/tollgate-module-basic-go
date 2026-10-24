#!/bin/sh
# Single-path baselines. BusyBox nc has no -z, so throughput is measured with
# iperf3 and results are written to files (serial echo lags).
#
# StarGate is the default route. To force traffic over the WIRED uplink we bind
# the client socket to the wired source address, which is the same mechanism the
# production bond uses.
out=/tmp/baseline.txt
: > "$out"
echo "== default route ==" >> "$out"
ip route show default >> "$out" 2>&1

echo "== stargate (default path, 10s) ==" >> "$out"
iperf3 -c 23.182.128.51 -p 5201 -t 10 -J > /tmp/sg.json 2>&1
tail -c 400 /tmp/sg.json >> "$out"

echo "== wired via source bind 192.168.2.15 (10s) ==" >> "$out"
iperf3 -c 23.182.128.51 -p 5201 -t 10 --bind 192.168.2.15 -J > /tmp/wd.json 2>&1
tail -c 400 /tmp/wd.json >> "$out"

echo DONE_BASELINE >> "$out"
echo WROTE_BASELINE
