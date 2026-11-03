# Aggregation measurement — corrected, with real baselines

Date: 2026-10-19
All runs wrapped in `mptcpize` and validated by the local address in the iperf
output. This replaces both the retracted plain-TCP numbers and the unusable
`signal`-shape numbers.

## Preconditions verified

```
lan1       192.168.2.20   (address CHANGED from .15 — see below)
phy0-sta0  10.2.1.122
rule 100:  from 192.168.2.20 lookup 201   -> via 192.168.2.1  dev lan1
rule 101:  from 10.2.1.122   lookup 202   -> via 10.2.0.1    dev phy0-sta0
endpoints: 192.168.2.20 id 1 signal subflow dev lan1
           10.2.1.122   id 2 signal subflow dev phy0-sta0
limits:    add_addr_accepted 4 subflows 4
peer:      mptcp LISTEN on 5201, add_addr_accepted 4
```

**The wired address moved from `192.168.2.15` to `192.168.2.20` from a DHCP
lease renewal.** That silently broke the policy rule AND the MPTCP endpoint,
because both were pinned to the old literal: `ip rule from 192.168.2.15 lookup
201` matched nothing, table 201 was empty, and the wired-bound transfers failed
with `Address not available`. This is a real operational trap — **a pinned
literal address is not stable on a DHCP uplink.** The module must resolve the
address at apply time, not carry a literal.

## Results — 3 runs per configuration, 6 s each (Mbit/s, receiver)

| configuration | run 1 | run 2 | run 3 | spread |
|---|---|---|---|---|
| A: wired only (`--bind 192.168.2.20`) | 8.29 | 5.93 | 12.0 | **5.93–12.0** |
| B: StarGate only (`--bind 10.2.1.122`) | 5.42 | 19.5 | 23.5 | **5.42–23.5** |
| C: unbound (both endpoints active) | 23.0 | 9.66 | 26.9 | **9.66–26.9** |

Every run's local address was an uplink address (`192.168.2.20` or
`10.2.1.122`), confirming genuine MPTCP — not the plain-TCP mistake of the
retracted run.

## Verdict: the measurement cannot resolve an effect

**The within-configuration spread is larger than any between-configuration
difference.** StarGate alone ranges 5.42–23.5; the unbound configuration ranges
9.66–26.9. The best single-path run (23.5) and the best unbound run (26.9) differ
by less than StarGate's own run-to-run variation.

Per the project's own rule: when spread exceeds the effect, **report the ranges
and refuse to average.** There is **no demonstrable multiplier** here, and
equally no demonstrable penalty. The honest statement is: *this bench, as
configured, cannot measure an MPTCP aggregation effect.*

## Why the spread is so large

Not investigated this iteration, and it must be before any further comparison:

- **Retransmit counts were high and variable** (323, 526, 399 in earlier runs) on
  a link whose RTT is ~40 ms. High retransmits on both paths will dominate any
  aggregation signal.
- Table 202 (the StarGate routing table) was **empty** for part of this session
  after the address change, so some runs may have taken an unintended path even
  when source-bound.
- Both paths are now ~40 ms, which is *good* for aggregation, but the absolute
  throughput (5–27 Mbit/s) is inflated and noisy relative to earlier 3–10 Mbit/s
  readings — the underlying link conditions changed.
- **No subflow ever formed** in any run: `MPJoinSynRx` remained 0 while
  `AddAddrTx` rose (6 by the end). So configuration C was **not actually a bond**
  — it was a single MPTCP connection whose path manager had nowhere to add a
  second subflow, because both advertised addresses are unreachable from the
  peer (measured: `WIRED_UNREACHABLE`, `SG_UNREACHABLE`).

## The decisive point

**Configuration C is not a bond.** It is one MPTCP connection. Its numbers reflect
path selection, not aggregation. Any comparison between A, B and C measures
*which single path was used*, never the sum of two.

Which means: **a genuine aggregation measurement is impossible on this topology
until the client has a peer-reachable address** — i.e. until either:
1. an uplink gets a routable/public address (IPv6 prefix), or
2. the per-uplink tunnel from D10's fallback is in place, or
3. the proxy-terminated design opens the second path itself.

This is the same conclusion D10 reached, now reached again from the measurement
side rather than the configuration side.

## Peer-side evidence

```
WIRED_UNREACHABLE    (192.168.2.15 as advertised)
SG_UNREACHABLE       (10.2.1.122 as advertised)
```
Both advertised addresses are unreachable from the peer, which is precisely why
`MPJoinSynRx` stayed 0. The advertisement is sent (`AddAddrTx` rose 0 -> 6) and is
unusable.

## What I am NOT claiming

- No multiplier. No penalty. No mean of these numbers.
- The 1.2–1.3× ceiling remains **unmeasured** on this hardware; what is measured
  is that this topology cannot measure it.
- Configurations A/B/C above are **single-path figures**, correctly obtained, and
  are the only numbers from this session that may be cited as throughput.
