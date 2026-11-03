# FIPS overlay goodput ladder — 83 Mbit/s, the 1.9 kbit/s gate is FALSIFIED

Date: 2026-10-20
Two x86 fleet nodes (x280 `fd36:3c0f:…` ⇄ x240 `fd00:6a8e:…`), same-LAN pair,
`fips0` MTU 1280 (effective 1203), FIPS session layer (XK, FSP rekey).
This is the Layer-0 experiment the gap analysis demanded, and its result
invalidates the gating assumption of the whole roadmap.

## Results (iperf3 through fips0, 15 s runs)

| run | throughput (receiver) |
|---|---|
| single stream, x240 → x280 | **82.9 Mbit/s** (149 MB) |
| single stream, x280 → x240 | **76.3 Mbit/s** |
| 4 streams aggregate | **82.8 Mbit/s** (149 MB), split ~12/37/23/12 |
| 4 s earlier confirmation run | 83.2 Mbit/s receiver |

Symmetric, sustained, and NOT single-flow-limited: 4 parallel streams share the
same ~83 Mbit/s ceiling, i.e. the overlay's processing/encapsulation is the
bottleneck — and it sits at **~83 Mbit/s, not 1.9 kbit/s**.

## The 1.9 kbit/s figure is DEAD

The gap analysis and ADR had gated "FIPS over bonding" on a measured
**~1.9 kbit/s** overlay figure, reasoning that one MPTCP subflow already
delivers ~1000× the overlay's capacity so transport work is pointless. That
figure is now unambiguously refuted on a live mesh pair. Whatever the 1.9 kbit/s
observation measured (a long-haul WAN path, a degraded peer, or — most likely —
**the destination node's inbound allowlist silently dropping bulk flows** while
SSH/control traffic passed), it was not the overlay's capacity.

Consequence: **Q1 (FIPS running over multi-WAN bonding) is unblocked** and jumps
from ratio 1.0 to the top of the roadmap. The moat is buildable.

## Why every earlier high-port test failed: `table inet fips`

FIPS installs its own nftables table — a per-node **inbound allowlist** on
`fips0`:

```
table inet fips {
  chain inbound {
    type filter hook input priority filter; policy accept;
    iifname != "fips0" return
    ct state established,related accept
    icmpv6 type echo-request accept
    tcp dport 8443/3001/8200/53/22 accept     (+ per-node extras)
    counter drop                                ← everything else dies
  }
}
```

Per-node extras differ: x280 allows `49000-49999`; x240 allows `9099`. This
explains every confusing observation of the last two sessions:

- port 22 connected 100% of the time (allowlisted),
- 5201/5211/43210 never did (dropped at the destination's `counter drop`),
- the destination's drop counter climbed (176 → 240) with my SYN retransmits,
- `fipsctl probe` worked (session layer, not TCP),
- SSH-over-fips0 "proved TCP works" — true, but only for allowlisted ports.

**Operational rule discovered:** any service exposed over FIPS must use an
allowlisted port or extend `table inet fips` on the node. This includes
TollGate-over-FIPS (Q4): the TollGate portal/API ports must be added to the
allowlist per node, or the bonding-server port for Q1 must be registered too.

## What this means for the stack goal

- **Q1 (FIPS over bond): GO.** Overlay capacity is 83 Mbit/s; a bonded
  two-uplink FIPS node can plausibly use 10–25 Mbit/s of WAN — the overlay is
  no longer the bottleneck by ~4×. Next concrete step: cross-build FIPS for
  OpenWrt aarch64/musl on the MT6000 (or run FIPS on an x86 dual-WAN box), enable
  `TcpConfig.mptcp`, register endpoints over the per-uplink tunnels (Q2's
  machinery), and measure single-vs-bonded FIPS goodput.
- **Q2 (tunnels): proven and load-bearing**, now the template for the FIPS node's
  uplink reachability too.
- **Q3 (TollGate pays bond):** unchanged — the revenue story, design exists,
  needs the metering/settlement wiring.
- **Q4 (TollGate on FIPS):** unchanged — design-only, but now its throughput
  premise also holds; the port-allowlist is the integration detail.

## Reproduce

```sh
# on destination (x280): iperf3 -s -B <local-fd00-addr> -p 49500   # port allowed by ITS table
# on source (x240):       iperf3 -6 -c <dest-fd00-addr> -B <src-fd00-addr> -t 15 -p 49500
```
Check a node's allowlist with `sudo nft list table inet fips`.