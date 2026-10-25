#!/bin/sh
# Bring up the two per-uplink WireGuard tunnels that make the MPTCP endpoints
# PEER-REACHABLE -- the precondition this topology otherwise cannot satisfy.
#
# WHY THIS IS REQUIRED. Measured on this bench: both uplink addresses are private
# (house NAT + Starlink CGNAT), the peer reports both UNREACHABLE, and
# `MPJoinSynRx` stays 0 while `AddAddrTx` rises -- the router advertises an address
# the peer cannot dial. A tunnel does not depend on what an uplink provides: the
# router initiates outbound, so it works behind CGNAT and needs no inbound
# reachability.
#
# This script mirrors what Config.TunnelPlan() + TunnelRoutingScript() render, so
# it is not a parallel implementation -- it is the executable form of the same
# plan, usable when the module is not driving the box.
#
# Every non-obvious line below encodes a defect found by running it on hardware:
#   * `wg set ... bind` DOES NOT EXIST -> fwmark + policy routing
#   * the pinning route must name the UNDERLAY, not the tunnel, or
#     "Nexthop has invalid gateway"
#   * each tunnel needs its OWN keypair, or both present one identity and the
#     handshake never completes (`wg show` reads `transfer: 0 0`)
#   * the tunnel interface needs explicit fw4 acceptance, or inbound tunnel
#     traffic is rejected and the peer sees ICMP unreachable
#
# Inputs come from the environment so no keys are stored here:
#   T1_PRIV T1_PEER_PUB T1_ADDR T1_TABLE   (wired uplink tunnel)
#   T2_PRIV T2_PEER_PUB T2_ADDR T2_TABLE   (stargate uplink tunnel)
#   PEER_HOST PEER_T1_PORT PEER_T2_PORT
#   WIRED_IF WIRED_GW WIRED_LOCAL
#   SG_IF    SG_GW    SG_LOCAL

set -e

WIRED_IF=${WIRED_IF:-lan1}
WIRED_GW=${WIRED_GW:-192.168.2.1}
SG_IF=${SG_IF:-phy0-sta0}
SG_GW=${SG_GW:-10.2.0.1}
PEER_HOST=${PEER_HOST:?need PEER_HOST}
PEER_T1_PORT=${PEER_T1_PORT:-51911}
PEER_T2_PORT=${PEER_T2_PORT:-51912}
T1_TABLE=${T1_TABLE:-301}
T2_TABLE=${T2_TABLE:-302}
T1_MARK=${T1_MARK:-0x3001}
T2_MARK=${T2_MARK:-0x3002}

out=/tmp/tunnels.txt
: > "$out"

echo "== inputs ==" >> "$out"
echo "wired: $WIRED_IF $WIRED_LOCAL gw $WIRED_GW" >> "$out"
echo "sg:    $SG_IF $SG_LOCAL gw $SG_GW" >> "$out"
echo "peer:  $PEER_HOST ports $PEER_T1_PORT/$PEER_T2_PORT" >> "$out"

# --- idempotent teardown of anything left from a previous run ---
for t in mptcp-wan1 mptcp-wan2; do
  ip link del "$t" 2>/dev/null || true
done
for tb in "$T1_TABLE" "$T2_TABLE"; do
  while ip rule del table "$tb" 2>/dev/null; do :; done
  ip route flush table "$tb" 2>/dev/null || true
done

mkdir -p /etc/wireguard
umask 077
printf '%s\n' "$T1_PRIV" > /etc/wireguard/t1.key
printf '%s\n' "$T2_PRIV" > /etc/wireguard/t2.key

# --- tunnel 1: wired uplink ---
ip link add mptcp-wan1 type wireguard
wg set mptcp-wan1 private-key /etc/wireguard/t1.key
wg set mptcp-wan1 listen-port 51901
wg set mptcp-wan1 fwmark "$T1_MARK"
wg set mptcp-wan1 peer "$T1_PEER_PUB" endpoint "$PEER_HOST:$PEER_T1_PORT" \
   allowed-ips 0.0.0.0/0 persistent-keepalive 25
ip -4 address add "$T1_ADDR" dev mptcp-wan1
ip link set mptcp-wan1 up
ip route replace default via "$WIRED_GW" dev "$WIRED_IF" table "$T1_TABLE"
ip rule add fwmark "$T1_MARK" lookup "$T1_TABLE"

# --- tunnel 2: stargate uplink ---
ip link add mptcp-wan2 type wireguard
wg set mptcp-wan2 private-key /etc/wireguard/t2.key
wg set mptcp-wan2 listen-port 51902
wg set mptcp-wan2 fwmark "$T2_MARK"
wg set mptcp-wan2 peer "$T2_PEER_PUB" endpoint "$PEER_HOST:$PEER_T2_PORT" \
   allowed-ips 0.0.0.0/0 persistent-keepalive 25
ip -4 address add "$T2_ADDR" dev mptcp-wan2
ip link set mptcp-wan2 up
ip route replace default via "$SG_GW" dev "$SG_IF" table "$T2_TABLE"
ip rule add fwmark "$T2_MARK" lookup "$T2_TABLE"

# --- firewall: without this the tunnels are REJECTED by fw4's terminal rule ---
nft list chain inet fw4 input >/dev/null 2>&1 && {
  nft insert rule inet fw4 input iifname "mptcp-wan1" accept comment "mptcp-tunnel:wan1" 2>/dev/null || true
  nft insert rule inet fw4 input iifname "mptcp-wan2" accept comment "mptcp-tunnel:wan2" 2>/dev/null || true
}

sleep 3
echo "== result ==" >> "$out"
wg show all >> "$out" 2>&1
echo "-- marks --" >> "$out"
wg show mptcp-wan1 fwmark >> "$out" 2>&1
wg show mptcp-wan2 fwmark >> "$out" 2>&1
echo "-- rules --" >> "$out"
ip rule show >> "$out" 2>&1
echo WROTE_TUNNELS
