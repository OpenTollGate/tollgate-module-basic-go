#!/bin/sh
# VPS side of the two per-uplink tunnels.
#
# One wg interface PER router tunnel, on its own port, with its OWN keypair. A
# shared key makes both tunnels present a single identity and the handshakes never
# complete (measured: `wg show` reads `transfer: 0 0` while everything else looks
# configured).
#
# Two wg interfaces in the SAME namespace is deliberate -- an earlier attempt used
# network namespaces and added a routing layer that was never validated for peer
# resolution.
#
# Prints the private keys it generated so the caller can install them on the
# router. Treat that output as secret.
set -e

P1=${P1:-51911}
P2=${P2:-51912}
SRC1=${SRC1:-10.99.98.1/24}
SRC2=${SRC2:-10.99.97.1/24}

sudo mkdir -p /etc/wireguard
umask 077

# Keys: generate once, keep stable across re-runs.
for n in 1 2; do
  if [ ! -s "/etc/wireguard/srv$n.key" ]; then
    sudo sh -c "wg genkey > /etc/wireguard/srv$n.key"
    sudo sh -c "wg pubkey < /etc/wireguard/srv$n.key > /etc/wireguard/srv$n.pub"
  fi
done

# Fresh interfaces each run so the peer keys can change.
for i in wgt1 wgt2; do sudo ip link del "$i" 2>/dev/null || true; done

sudo ip link add wgt1 type wireguard
sudo ip link add wgt2 type wireguard
sudo wg set wgt1 private-key /etc/wireguard/srv1.key listen-port "$P1"
sudo wg set wgt2 private-key /etc/wireguard/srv2.key listen-port "$P2"
sudo ip -4 address add "$SRC1" dev wgt1
sudo ip -4 address add "$SRC2" dev wgt2
sudo ip link set wgt1 up
sudo ip link set wgt2 up

echo "SRV1_PUB=$(sudo cat /etc/wireguard/srv1.pub)"
echo "SRV2_PUB=$(sudo cat /etc/wireguard/srv2.pub)"
echo "SRV1_PRIV=$(sudo cat /etc/wireguard/srv1.key)"
echo "SRV2_PRIV=$(sudo cat /etc/wireguard/srv2.key)"
sudo ss -ulnp 2>/dev/null | grep -E ":$P1|:$P2" | head -4
