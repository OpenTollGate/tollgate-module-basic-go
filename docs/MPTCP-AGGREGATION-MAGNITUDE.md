# Aggregation magnitude: both paths carry payload, but the split is 91/9

Date: 2026-10-19
Follows `MPTCP-AGGREGATION-ACHIEVED.md` (mechanism proven: two subflows, one token).

## The measurement that matters: per-tunnel byte deltas

Subflow *existence* is not aggregation; both paths must carry payload **on the
same connection**. Measured by reading each tunnel's counters immediately before
and after one `mptcpize` transfer:

| tunnel | uplink | before (tx) | after (tx) | delta | share |
|---|---|---|---|---|---|
| `mptcp-wan1` | wired `lan1` | 109,919,092 | 125,228,688 | **15.31 MB** | **91.4 %** |
| `mptcp-wan2` | StarGate `phy0-sta0` | 36,556,028 | 37,993,304 | **1.44 MB** | **8.6 %** |
| **total** | | | | **16.75 MB** | |

Receiver reported **15.9 MB**, so the sum of the two tunnels is **1.05×** the
payload — consistent (the excess is WireGuard encapsulation). That cross-check
matters: it means the two counters together account for the connection's bytes, so
neither is a phantom.

## Verdict: aggregation is REAL but heavily asymmetric

Both paths carried data simultaneously on one connection. That is genuine
multi-path aggregation — the thing this project set out to prove — and it is now
measured rather than inferred.

**But it is not a bandwidth multiplier.** 91 % of the payload took the wired path.
The StarGate leg carried 8.6 %, which over a 7-run sample of similar transfers was
consistently the smaller share.

This is exactly the behaviour the docs predict, and it is *correct* rather than a
misconfiguration:

- MPTCP's in-kernel path manager prefers the **lowest-RTT** subflow. Both paths
  measure ~190 ms through the tunnels, but the wired leg is far more stable.
- The StarGate leg is a ~190 ms satellite path behind a CGNAT tunnel, with
  observed retransmit bursts (323, 399, 526, 556 retransmits across runs).
- A path that is lossy and high-latency will hold a correspondingly smaller
  congestion window, so it carries less. No scheduler change fixes that; the
  physics does not.

## Throughput across runs (receiver, Mbit/s)

```
20.5   13.1   14.3   14.3   15.4   16.5   23.9(receiver 20.5)
```
Median ~14–16 Mbit/s. **No single-path baseline was taken in the same window**, so
no multiplier is computed. From the earlier corrected run (same bench, same day,
`mptcpize`, correct addresses), single-path figures were:

```
wired only      8.29 / 5.93 / 12.0
stargate only   5.42 / 19.5 / 23.5
```

Bonded runs at 14–16 Mbit/s sit **inside** the single-path spread. Given that
StarGate alone once hit 23.5, **a multiplier is not demonstrable** — the
within-configuration variance still exceeds any plausible effect.

## Honest statement of the result

> Two subflows establish on one MPTCP connection over two independent uplinks and
> **both carry payload** (measured 91 %/9 % split, cross-checked against the
> receiver's byte count). The bond is genuinely multi-path. Throughput does not
> exceed the better single path on this bench, and the transfer is dominated by the
> lower-latency wired leg — consistent with the documented **1.2–1.3× ceiling
> being an upper bound in good conditions, not a guarantee**.

The 1.2–1.3× figure itself remains **unverified here**. What is verified is
`>0 %` on the second path with a real, measured share — not 2×, and not zero.

## What would be needed to show a real multiplier

1. **Paths of comparable quality.** Today wired ≫ StarGate in stability. Two
   similar mid-grade links would split closer to even.
2. **An RTT-matched pair.** The tunnels add ~190 ms to both, but the underlying
   legs differ in jitter and loss, and the scheduler tracks that.
3. **A congestion-control / scheduler that spreads.** This kernel ships only
   `default` — no `roundrobin`, no BPF scheduler (`available_schedulers` absent).
   So the split is whatever the path manager's default preference produces.
4. **Paired A/B/bonded runs in ONE window, repeated,** with ranges — the only
   shape that can survive the variance seen here.

## Reproduce

`scripts/uplink-tunnels.sh` + `scripts/vps-tunnels.sh` bring up the tunnels;
register the tunnel addresses `signal subflow`; then read `wg show <if> transfer`
before and after a `mptcpize run iperf3` transfer. The delta *is* the per-path
payload.
