#!/bin/sh
# Wired-uplink diagnosis. Deliberately free of single quotes so it can be
# transferred over the serial console line-by-line.
out=/tmp/diag.txt
{
  echo "== uplinks =="
  ip -4 -br addr show
  echo "== default routes =="
  ip route show default
  echo "== wired: ping its gateway =="
  ping -c2 -W3 -I 192.168.2.15 192.168.2.1 2>&1 | tail -2
  echo "== wired: DNS through that gateway (count) =="
  nslookup one.one.one.one 192.168.2.1 2>&1 | grep -c Address
  echo "== wired: TCP to 1.1.1.1:443 =="
  nc -z -w4 1.1.1.1 443 && echo TCP_OK || echo TCP_FAIL
  echo "== default-path: TCP to VPS:5201 =="
  nc -z -w6 23.182.128.51 5201 && echo VPS_OK || echo VPS_FAIL
  echo "== stargate: TCP to VPS:5201 =="
  nc -z -s 10.2.1.122 -w6 23.182.128.51 5201 && echo SG_OK || echo SG_FAIL
  echo "== arp on lan1 =="
  ip neigh show dev lan1 | head -4
  echo "== carrier / operstate =="
  cat /sys/class/net/lan1/carrier
  cat /sys/class/net/lan1/operstate
} > "$out" 2>&1
echo WROTE_DIAG
