# MPTCP bonding on the GL-MT3000 (Beryl AX)

Hardware-specific notes for running the MPTCP multi-WAN bond on a GL.iNet
GL-MT3000. The MT6000 evidence in `MPTCP-*.md` does not transfer unchanged: the
MT3000 has fewer radios, less RAM, and a different AP+STA constraint.

## Hardware

- MediaTek MT7981B, dual-band 2.4/5 GHz, 2 spatial streams.
- One 2.5 GbE WAN and one 1 GbE LAN, USB 3.0.
- About 512 MB RAM and 256 MB flash; the overlay has roughly 180 MB free on a
  clean 25.12.5 install.
- `iw phy` reports one AP+STA channel per radio:

```text
#{ AP, mesh point } <= 16, #{ managed } <= 19, total <= 19,
#channels <= 1, STA/AP BI must match
```

Two radios are therefore enough for one 2.4 GHz uplink and one 5 GHz uplink,
but each radio's AP must share that uplink's channel.

## Channel plan

```text
radio0 (2.4 GHz): APs + STA to the hotspot, same channel
radio1 (5 GHz):   APs + STA to the venue AP, same channel
```

A concrete deployment: hotspot on 2.4 GHz ch5, venue guest on 5 GHz ch36, APs
on ch5/ch36. Moving the 2.4 GHz APs from their old channel to the hotspot's
channel will bounce 2.4 GHz clients; schedule it.

A venue AP on a DFS channel is possible but the AP must follow CSA events; a
weak 5 GHz venue signal (about -78 dBm in the reference site) makes the path
more useful for resilience than for throughput.

## Prerequisites

- OpenWrt 25.12.x with `net.mptcp.enabled=1`.
- `ip-full` (BusyBox `ip` has no `mptcp` subcommand). Verify with
  `ip mptcp limits`, not just `apk info -e ip-full`.
- `kmod-nft-tproxy` for TPROXY steering; `nft` must accept a `tproxy`
  statement.
- `xray-core` (or another proxy that sets `tcpMPTCP`/`IPPROTO_MPTCP`).
- `kmod-wireguard` and `wireguard-tools` only if using the advertised-address
  shape with per-uplink tunnels.

## Kernel CVE floor

CVE-2026-98432 is a remotely reachable IPv4 TCP use-after-free; the proven
root chain uses an MPTCP listener. The fix starts at Linux **6.12.113**:

| OpenWrt | Kernel | Status |
|---|---|---|
| 25.12.5 | 6.12.94 | vulnerable |
| 25.12.6 | 6.12.112 | **still one patch below the floor** |
| snapshot (at time of writing) | 6.18.38 | newer line; not a stable release |

Do not treat "MPTCP disabled" as a mitigation: the UAF fires through ordinary
TCP too. A production device on an untrusted venue network needs a patched
kernel (or an explicit risk acceptance) before it is exposed.

## Two simultaneous STA uplinks

The current upstream manager is single-STA: `disableOtherSTAInterfaces()` and
`CleanupStaleSTAs()` dismantle a second enabled STA, and both
`tollgate_sta_2g`/`tollgate_sta_5g` are written to `network=wwan`. Until that
design issue is implemented, a manual two-STA configuration must be kept away
from the manager, which is not acceptable for a production portal.

Tracked in the repository's design issue for concurrent STA uplinks. The
manual UCI shape used on the bench is:

```sh
# 2.4 GHz uplink
uci set wireless.lab_sta24=wifi-iface
uci set wireless.lab_sta24.device=radio0
uci set wireless.lab_sta24.mode=sta
uci set wireless.lab_sta24.network=wwan
uci set wireless.lab_sta24.ssid='<hotspot>'
uci set wireless.lab_sta24.encryption='psk2'
uci set wireless.lab_sta24.key='<redacted>'
uci set wireless.lab_sta24.disabled=0

# 5 GHz uplink
uci set wireless.lab_sta5=wifi-iface
uci set wireless.lab_sta5.device=radio1
uci set wireless.lab_sta5.mode=sta
uci set wireless.lab_sta5.network=wwan2
uci set wireless.lab_sta5.ssid='<venue>'
uci set wireless.lab_sta5.encryption='none'
uci set wireless.lab_sta5.disabled=0

uci set network.wwan2=interface
uci set network.wwan2.proto=dhcp
uci set network.wwan2.device=phy1-sta0
uci commit wireless
uci commit network
wifi reload
```

Verify both addresses and both default routes before touching the bond.

## Verification

```sh
ip mptcp limits
ip mptcp endpoint show
ip mptcp monitor            # two SF_ESTABLISHED lines, same token
ss -Mti dst <server>        # tcp-ulp-mptcp and subflows:2
nft list chain inet fw4 mptcp_steer   # steering chain only after NDS marks
```

## What is actually proven

- The device runs OpenWrt 25.12.6 (kernel 6.12.112) with `ip-full`, xray,
  WireGuard tools, and a `main`-built TollGate (`v0.6.0-rc4`).
- MPTCP is enabled and `ip mptcp` is functional after installing `ip-full`.
- Two STA sections and a distinct `wwan2` can be written, but the lab link went
  down before association/DHCP and the bonded transfer could be verified.

Do not publish a throughput number for the MT3000 until the per-path baseline
and the bonded run exist; the MT6000 evidence is about 1.2–1.3× best case, not
a multiplier.
