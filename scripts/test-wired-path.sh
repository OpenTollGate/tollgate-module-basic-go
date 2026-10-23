#!/bin/sh
# Test the wired path in isolation, writing everything to a file so serial lag
# cannot swallow the result.
{
  echo "== who am I / routes =="
  ip route get 23.182.128.51
  echo "-- with source bind --"
  ip route get 23.182.128.51 from 192.168.2.15 2>&1
  echo "== iperf3 wired-bound, 6s =="
  iperf3 -c 23.182.128.51 -p 5201 -t 6 --bind 192.168.2.15 2>&1 | tail -8
  echo "== rc=$? =="
} > /tmp/wiredtest.txt 2>&1
echo WROTE_WIREDTEST
