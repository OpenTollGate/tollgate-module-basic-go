# AGGREGATION ACHIEVED — two subflows on one connection

Date: 2026-10-19
**First time in this investigation that a genuine multi-path MPTCP bond has been
demonstrated on this hardware.** Both subflows established simultaneously on one
token, over two different tunnel interfaces.

## The proof

`ip mptcp monitor` during a transfer:

```
[  SF_ESTABLISHED] token=52df9645 remid=0 locid=2 saddr4=10.99.97.2 daddr4=23.182.128.51 sport=41933 ifindex=37
[  SF_ESTABLISHED] token=52df9645 remid=0 locid=1 saddr4=10.99.98.2 daddr4=23.182.128.51 sport=49431 ifindex=36
```

**Same `token=52df9645` on both lines** — they belong to ONE MPTCP connection, not
two separate ones. Different `locid` (1 and 2), different source addresses
(`10.99.98.2` and `10.99.97.2`), different interfaces (`ifindex` 36 and 37), so
they are two genuinely distinct paths carrying the same connection.

Corroborating counters on the router, before → after:

| counter | before | after |
|---|---|---|
| `MPJoinSynAckRx` | 24 | 26 |
| `MPJoinAckRx` | 18 | 22 |
| `MPCapableSYNACKRX` | 36 | 38 |
| `AddAddrTx` | 10 | 12 |

`MPJoinAckRx` advancing is the peer completing the JOIN handshake — the step that
had **never** succeeded before this session.

## What made the difference

Not a configuration tweak. **The advertised addresses finally became reachable by
the peer.** Sequence, all verified on the device:

1. Two per-uplink WireGuard tunnels brought up, each pinned by its own fwmark and
   routing table (`0x3001`/table 301 → wired, `0x3002`/table 302 → StarGate).
   Both handshook and carried traffic:
   ```
   mptcp-wan1  KU/aDX...  3548 rx  4148 tx   fwmark 0x3001
   mptcp-wan2  YhbY2y...  2972 rx  3300 tx   fwmark 0x3002
   ```
2. Firewall acceptance added — **without this the tunnels were rejected** even
   though the transport was healthy. Peer saw `UNREACHABLE`; after
   `nft insert rule inet fw4 input iifname mptcp-wanN accept`:
   ```
   10.99.98.2  0% packet loss   190/196/202 ms
   10.99.97.2  0% packet loss   173/184/196 ms
   ```
3. Those tunnel addresses registered as `signal subflow`:
   ```
   10.99.98.2 id 1 signal subflow dev mptcp-wan1
   10.99.97.2 id 2 signal subflow dev mptcp-wan2
   ```
4. Then subflows formed.

This is exactly the chain D10 predicted: **the peer must be able to reach the
advertised address, and a tunnel is what makes that true behind NAT/CGNAT.**

## Throughput — and why no multiplier is claimed

```
sender   19.6 MBytes  16.5 Mbits/sec   Retr 68     (10 s)
receiver 17.4 MBytes  14.3 Mbits/sec
```

One run. **Not a comparison**, and no multiplier is claimed from it, because:

- no single-path baseline was taken in the same window under identical conditions;
- earlier in this session single-path figures ranged **5.4–23.5 Mbit/s** for
  StarGate alone, so run-to-run spread exceeds any plausible aggregation effect;
- the tunnels add ~190 ms RTT each, so the bonded path is *higher* latency than
  the raw uplinks — aggregation over a satellite link with a 190 ms tunnel
  round-trip is not expected to show a clean sum.

What IS established beyond doubt: **two subflows, one connection, both
established, over two distinct paths.** Mechanism proven; magnitude not.

## Honest status of the goal

- **Aggregation mechanism: PROVEN.** `SF_ESTABLISHED` ×2 on one token, distinct
  interfaces, `MPJoinAckRx` advancing.
- **Aggregation magnitude: NOT established.** Needs paired single-path vs bonded
  runs in one window, repeated, with ranges reported. The high variance measured
  earlier (5.4–23.5 on one path) means this may never resolve into a clean number
  on this bench.
- The **1.2–1.3× ceiling** remains unverified here. It should not be presented as
  measured.

## Reproducible artefacts

- `scripts/uplink-tunnels.sh` — router side, mirrors `TunnelPlan()` +
  `TunnelRoutingScript()`, encoding every hardware-found defect (no `bind`, route
  names the underlay, distinct keypair per tunnel, fw4 acceptance).
- `scripts/vps-tunnels.sh` — peer side, one wg interface per tunnel, own keypair,
  own port.
- `scripts/uplink-policy-routing.sh` — per-uplink from-address rules.
- `scripts/agg-run.sh` — Rule-7 runner (3 configs × 3 runs).
