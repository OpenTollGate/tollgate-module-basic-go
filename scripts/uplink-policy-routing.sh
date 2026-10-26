#!/bin/sh
# Establish the two uplinks as genuinely DISJOINT paths.
#
# Why this is required (measured): `ip route get <peer> from 192.168.2.15` still
# answers `via 10.2.0.1 dev phy0-sta0` -- the source address does NOT influence
# the route lookup. So a socket bound to the wired address still egresses the
# StarGate uplink, carrying a source address that does not belong to that
# interface, and the packets die. Without per-uplink policy routing there is only
# ONE usable path no matter how many interfaces are up.
#
# This is the same mechanism the production bond installs (fwmark/rule/table per
# uplink); here it is keyed on the SOURCE ADDRESS so a plain iperf3 --bind works
# as a single-path probe.
set -e

WIRED_IP=192.168.2.15
WIRED_GW=192.168.2.1
WIRED_IF=lan1
WIRED_TABLE=201

SG_IP=10.2.1.122
SG_GW=10.2.0.1
SG_IF=phy0-sta0
SG_TABLE=202

out=/tmp/policy.txt
: > "$out"

echo "== BEFORE ==" >> "$out"
ip rule show >> "$out" 2>&1

# Idempotent: remove any prior rules for these tables, then re-add.
while ip rule del table "$WIRED_TABLE" 2>/dev/null; do :; done
while ip rule del table "$SG_TABLE" 2>/dev/null; do :; done

# Wired: traffic sourced from the wired address uses the wired gateway.
ip rule add from "$WIRED_IP" lookup "$WIRED_TABLE" priority 100
ip route replace default via "$WIRED_GW" dev "$WIRED_IF" table "$WIRED_TABLE"

# StarGate: traffic sourced from the StarGate address uses the StarGate gateway.
ip rule add from "$SG_IP" lookup "$SG_TABLE" priority 101
ip route replace default via "$SG_GW" dev "$SG_IF" table "$SG_TABLE"

echo "== AFTER: rules ==" >> "$out"
ip rule show >> "$out" 2>&1
echo "== AFTER: tables ==" >> "$out"
ip route show table "$WIRED_TABLE" >> "$out" 2>&1
ip route show table "$SG_TABLE" >> "$out" 2>&1

echo "== route lookup with wired source (must now be lan1) ==" >> "$out"
ip route get 23.182.128.51 from "$WIRED_IP" >> "$out" 2>&1
echo "== route lookup with stargate source (must be phy0-sta0) ==" >> "$out"
ip route get 23.182.128.51 from "$SG_IP" >> "$out" 2>&1

echo WROTE_POLICY
