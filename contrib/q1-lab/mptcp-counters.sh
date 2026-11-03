#!/bin/sh
# Q1 lab — MPTCP counters (MP_JOIN / MPCapable / fallback) from /proc/net/netstat.
grep -A1 '^MPTcpExt:' /proc/net/netstat | awk 'NR==1{n=split($0,H," ")} NR==2{split($0,V," "); for(i=2;i<=n;i++) if (H[i] ~ /MPJoin|MPCapable|AddAddr/) printf "%s=%s ", H[i], V[i]; print ""}'
