# Q1 lab — FIPS over multi-WAN MPTCP bonding, fully reproducible

This reproduces `docs/Q1-FIPS-OVER-BONDING-PROVEN.md` (commit `a89010e5`):
a patched-FIPS session between an OpenWrt router with two disjoint WAN
uplinks and a VPS peer, two MPTCP subflows, and the interleaved A/B/C
iperf series.

Hardware used: GL.iNet MT6000 (aarch64, OpenWrt 24.10.2, kernel 6.6.93),
wired uplink `lan1` (house gateway) + StarGate satellite (`phy0-sta0`).
Peer: any Linux VPS with a public IP (here 23.182.128.51).

## 0. Prerequisites

- FIPS fork branch `pr/tcp-mptcp-option` (felixfelix-bot/fips) at `5e150bdf`
  or later — the MPTCP connect (`36f82c86`) + listener (`5e150bdf`) patches.
  Build for the router per `docs/CROSS-BUILD-AARCH64-MUSL.md` in that repo;
  build x86_64 for the VPS (`cargo build --release`).
- Router: `opkg install ip-full mptcpize iperf3 tc kmod-sched-core kmod-tun`
  (BusyBox `ip` lacks the `mptcp` subcommand).
- Router sysctl: `net.mptcp.enabled=1`.
- VPS: `iperf3` present; ufw (or equivalent) allows the FIPS test port (17443/tcp).
- Serial console for the router with the bench mutex held
  (`worktrees/bench-mutex` tooling) — every router command below was executed
  through `console-exec.py`, one transaction per batch.

## 1. VPS peer setup

```sh
mkdir -p /tmp/fipsmpt/data /tmp/fipsmpt/run
# config = vps-fipsmpt.yaml from this directory
setsid nohup /path/to/fips --config /tmp/fipsmpt.yaml > /tmp/fipsmpt.log 2>&1 < /dev/null &
sudo ip mptcp endpoint add <VPS_PUBLIC_IP> subflow
sudo ip mptcp limits set subflows 4 add_addr_accepted 4
sudo ufw allow 17443/tcp
```

Notes: the fleet daemon on the same host owns `/run/fips/control.sock` and
`/dev/net/tun` — the test node starts DEGRADED (no TUN) which is fine for
transport-level proof; set `node.control.socket_path` (NOT `control_socket`)
to silence the control-socket collision. The VPS ACL is allowlist-strict
(`/etc/fips/peers.deny` contains `ALL`); append the router's test npub to
`/etc/fips/peers.allow` or every handshake dies with
`acl: Rejected peer ... decision=denylist match`.

## 2. Router setup (the disjoint uplinks)

```sh
# wired uplink: pull lan1 from the LAN bridge, DHCP from the wired gateway
ip link set lan1 nomaster
ip link set lan1 up
udhcpc -i lan1 -n -q            # -> 192.168.2.8/24, gw 192.168.2.1 (in this lab)

# policy routing so each source address uses its own uplink
ip rule add from 192.168.2.8 table 201 pref 100
ip rule add from 10.2.1.122 table 202 pref 101
ip route add default via 192.168.2.1 dev lan1 table 201
ip route add default via 10.2.0.1 dev phy0-sta0 table 202

# MPTCP: one endpoint per uplink
ip mptcp endpoint add 192.168.2.8 subflow dev lan1
ip mptcp endpoint add 10.2.1.122 subflow dev phy0-sta0
ip mptcp limits set subflows 4 add_addr_accepted 4
```

Verify with `ip route get <VPS_IP> from 192.168.2.8` and
`... from 10.2.1.122` — each must leave via its own interface.

Start the router test node with `router-fipsmpt.yaml` (the peer entry needs
`connect_policy: auto_connect` — without it the config-floor peering layer
never dials). Read the VPS test node's npub from its log (`npub:` line) and
put it in the router config's peer entry first.

## 3. Proof + measurement

```sh
# session proof: look for in the router log
#   INFO fips::node::handlers::handshake: Peer promoted to active peer=vps-test
# and two ESTABLISHED legs, one per source address:
netstat -tn | grep 17443
# and zero-fallback MPTCP counters:
sh mptcp-counters.sh

# A/B/C throughput (the iperf server on the VPS MUST be MPTCP too, or every
# dial falls back to plain TCP and "bonded" silently measures one path):
#   VPS:  setsid nohup mptcpize run iperf3 -s > /tmp/iperf.log 2>&1 < /dev/null &
sh bench-abc.sh          # interleaved A/B/C x3, 8s runs + split deltas
sh bench-tc-capped.sh    # wired capped at 10mbit, bond keeps flowing
```

`mptcpize` on the SERVER is the load-bearing detail: a plain `iperf3 -s`
answers MPTCP SYNACKs without MPCapable and the client falls back — the
counter `MPCapableFallbackSYNACK` climbing is the symptom.

## 4. Teardown (verified-by-absence in the recorded run)

Run `teardown.sh` on the router (kills the test node, flushes endpoints,
rules 100/101, tables 201/202, tc, returns lan1 to `br-private`, deletes
lab files) and on the VPS: kill the test node + iperf, remove the nft 5201
allow rule if added, remove the test npub from `peers.allow`, delete
`/tmp/fipsmpt*`, `/tmp/fips.key`, `/tmp/start-vps-fips.sh`.

## Recorded results (2026-10-20/21)

| config | runs (Mbit/s) | median |
|---|---|---|
| A wired-only | 28.9 / 35.9 / 26.1 | 28.9 |
| B satellite-only | 4.12 / 3.60 / 5.78 | 4.12 |
| C bonded | 21.8 / 14.3 / 13.4 | 14.3 |
| C bonded, wired tc-capped 10mbit | 11.8 / 7.7 / 11.9 | 11.8 |

Split deltas per bonded run: lan1 134–543 KB vs sta 90–127 KB — both paths
always carry bytes, unevenly (the known asymmetric-path apportioning).
Transport counters after the session series: `MPCapableSYNTX=8,
MPCapableSYNACKRX=8, MPJoinSynAckRx=8`, zero fallbacks during the FIPS
session (later fallback counts come from the plain-TCP A/B iperf runs, by
design).
