# Aggregation: two disjoint client paths, measured

Date: 2026-10-16 (Ralph loop iteration 6)
Follows `MPTCP-MEASUREMENT-2GATEWAY.md`.

## What changed from the previous attempt

Previously the client reached the router on ONE local path, so both subflows
landed on the same 5-tuple path and the second added zero capacity
(conntrack: 869 MB vs 725 B).

This run made the client use its **two local paths to different router
addresses**:

| Client iface | Client addr | Router addr reached | Path |
|---|---|---|---|
| `wlp58s0` | 192.168.2.36 | 192.168.2.15 | wifi -> house LAN |
| `enx6c6e0750c33b` | 10.63.101.201 | 192.168.2.15 / 10.63.101.1 | direct cable |

A firewall obstacle had to be removed first: `lan1` is classified as **WAN** on
this router (`iifname "lan1" jump input_wan`, policy `drop`), so inbound TCP to
`192.168.2.15` was refused while ICMP was permitted. A temporary
`tcp dport 7402 accept` rule on `lan1` was inserted to run the test.

## Evidence that two subflows carried data simultaneously

Mid-transfer conntrack, two live connections moving bulk data at once:

```
src=192.168.2.36   -> 192.168.2.15  sport=35282  pkts=49823  bytes=510695705
src=10.63.101.201  -> 192.168.2.15  sport=46249  pkts=85187  bytes=1256176480
```

Two **different client source addresses** (two different local interfaces), both
in `ESTABLISHED` with large byte counts. `MPJoinSynRx` also advanced to 16,
i.e. many subflow JOINs were received. So MPTCP demonstrably created and used
subflows on both local paths.

## Throughput: no gain, and the reason is the shared physical path

| Configuration | Throughput |
|---|---|
| wifi only (`--bind-dev wlp58s0`) | 1.25 Gbit/s |
| cable only (`--bind-dev enx6c6e0750c33b`) | 1.20 Gbit/s |
| **aggregated (unbound), 3 runs** | **1.19 / 1.21 / 1.27 Gbit/s** |

Aggregated is **within noise of either single path**. Note both single-path
figures now exceed the earlier 925 Mbit/s: the client's links are faster than
first measured, so the earlier number was not a hard ceiling.

**Why no gain despite two active subflows:** the client's wifi (`wlp58s0`) and
its cable (`enx6c6e0750c33b`) both terminate on the **same house LAN switch**.
The two subflows are logically distinct (different 5-tuples, different local
addresses) but physically share one network. MPTCP cannot create capacity that
the underlying medium does not have — and its scheduler correctly declines to
split traffic across two subflows competing for the same bottleneck.

This is the same lesson as before, one layer further out: **subflows must be
disjoint all the way down**, not merely distinct at the socket level. Distinct
local addresses are necessary but not sufficient; the paths must diverge before
they share a bottleneck.

## What is proven now

- MPTCP forms subflows across **two different local interfaces** of the client,
  with real data on both (`MPJoinSynRx` 2 -> 16; both conntrack entries with
  large byte counts in `ESTABLISHED`).
- Two independent **upstream gateways** can be provisioned on the router
  (wired `192.168.2.1` + StarGate `10.2.0.1`) and both work.
- `signal subflow` endpoints are required; `subflow`-only silently never
  advertises.
- Only the `default` MPTCP scheduler exists on this kernel.

## What is still not proven

**A throughput gain.** Not for lack of multipath, but because every topology
available in this lab eventually funnels through one shared physical segment
(the house LAN switch, or the single upstream). To show a gain the two subflows
must diverge before any shared bottleneck — for example the client attaching to
two genuinely separate networks (two ISPs, or one ISP plus a metered cellular
link), which this lab does not have.

Stated plainly so nothing here overclaims: **two active subflows, yes;
measurable aggregation, no — and the shared physical path is why.**

## Honest labelling

Even with genuinely disjoint paths, expect **1.2-1.3x, not 2x**, and only when
the peer is multipath-capable. Resilient multi-path, not doubled bandwidth.
