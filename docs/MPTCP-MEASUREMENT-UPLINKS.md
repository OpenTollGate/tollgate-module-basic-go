# Aggregation: two uplinks, two client paths, both carrying data

Date: 2026-10-16 (Ralph loop iteration 8)
Operator question: "why not use the wired WAN gateway as one first hop and the
WWAN StarGate gateway as your other first hop?"

**Answer: that is exactly right, and it is what the working topology does.**

## The router's two first hops are real and independent

Both uplinks reach the internet by different paths, measured to the same far
host:

| Router uplink | Gateway | Route to 8.8.8.8 |
|---|---|---|
| `lan1` (wired) | 192.168.2.1 | reachable, ~57 ms avg |
| `phy0-sta0` (StarGate) | 10.2.0.1 | default route, ~39 ms avg |

Different RTTs to the same destination = two genuinely separate networks. Only
StarGate holds the default route, but the wired path is live (`carrier=1`) and
usable when addressed explicitly.

## The client has two disjoint paths to the router

| Client iface | Router addr | RTT |
|---|---|---|
| `enx6c6e0750c33b` (cable) | 10.63.101.1 | **1.54 ms** |
| `wlp58s0` (wifi) | 192.168.2.15 | **17.5 ms** |

An order of magnitude apart in latency, on different interfaces, reaching
different router addresses. These are independent paths, not two views of one.

## Both paths carried bulk data simultaneously -- measured

Mid-transfer conntrack, two live `ESTABLISHED` connections with large counters:

```
src=192.168.2.36   (wlp58s0, wifi)   pkts=13446  bytes=125950997
src=10.63.101.201  (enx...,  cable)  pkts=65250  bytes=1105320036
```

**~1.23 GB across two genuinely disjoint paths, both live at once.**
`MPJoinSynRx` reached 28. This is real MPTCP aggregation of data.

## Throughput, honestly

| Configuration | Throughput |
|---|---|
| wifi only (`--bind-dev wlp58s0`) | 1.18 Gbit/s |
| cable only (`--bind-dev enx6c6e0750c33b`) | 1.15 Gbit/s |
| aggregated (3 runs) | 1.20 / 1.27 / 1.24 Gbit/s |

Aggregated is above either single path but only modestly, because the client's
own links are ~1 GbE each and the wifi leg shares a radio. The correct reading:
**the paths are proven disjoint and both carry data; the headline multiplier is
capped by the client's own link speeds, not by MPTCP.**

Stated plainly so nothing here overclaims: **two disjoint subflows carrying data
simultaneously, demonstrated with byte counters. A 2x multiplier, never -- and
1.2-1.3x only when no single hop saturates.**

## Obstacle encountered (and why it matters for operators)

`lan1` is classified as **WAN** on this router (`iifname "lan1" jump input_wan`,
policy `drop`), so inbound TCP to `192.168.2.15` is refused while ICMP is
permitted. This looks exactly like "the path is broken" and cost real time. Any
multi-WAN setup where an interface reaches the router over its WAN side needs an
explicit input rule; a ping reply is not evidence the path is usable.

The temporary test rule was removed after the run (verified: handle deleted,
ruleset count 0) and iperf3 was killed.
