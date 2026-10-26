# Controlled comparison: the split predicts the throughput

Date: 2026-10-19
The first **interleaved** comparison on this bench (A/B/C alternating, order reversed
each round), so drift affects all three configurations roughly equally instead of
favouring whichever ran first. 5 s per run.

## Raw result — 21 runs, interleaved

| configuration | n | min | median | max | mean |
|---|---|---|---|---|---|
| wired only (`--bind 192.168.2.20`) | 7 | 10.9 | **17.7** | 19.4 | 16.9 |
| satellite only (`--bind 10.2.1.122`) | 7 | 9.5 | 13.6 | 18.8 | 13.9 |
| **bonded** (both endpoints active) | 7 | 9.7 | **12.9** | **24.3** | 15.4 |

## The finding: bonded has the highest peak and the lowest median

- Best single-path run: **19.4 Mbit/s**
- Best bonded run: **24.3 Mbit/s** — **1.25×** the best single path
- Bonded **median** is *lower* than either single path

That combination — higher ceiling, lower typical — is the signature of a
high-variance system with a real but unreliable benefit. Averaging it would hide
both facts. Reporting the range is the only honest option.

## The mechanism, measured

Correlating each bonded run's **byte share on the lossy path** with its throughput:

| bonded throughput | bytes on wired | bytes on satellite |
|---|---|---|
| 12.9 Mbit/s | **85.4 %** | 14.6 % |
| 21.4 Mbit/s | **83.3 %** | 16.7 % |
| 18.4 Mbit/s | **58.8 %** | 41.2 % |
| **24.3 Mbit/s** | **50.1 %** | **49.9 %** |

**The two best bonded runs are the two where the split was near even.** The worst
bonded run had only 14.6 % on the second path.

This is the causal chain, and it is now measured rather than assumed:

1. MPTCP's in-kernel path manager apportions by congestion window, which on a
   lossy path stays small.
2. So the split is uneven (typically ~85/15) far more often than even (~50/50).
3. An uneven split means the second path contributes ~15 % of bytes — which is
   **less than the ~9 % protocol/encapsulation overhead** of running both paths
   under WireGuard, so the bonded result lands *below* single-path.
4. When the split does come out near even, the second path adds real capacity and
   the result exceeds the best single path by ~25 %.

So the multiplier is **not absent — it is intermittent, and its prevalence depends
on how evenly the scheduler happens to split.** That reframes the whole question:
it is not "does MPTCP aggregate on this bench" but "what makes the split even, and
can it be held there".

## What would make the split even (and why it is not wired up here)

- **A spreading scheduler.** This kernel ships only `default` (no
  `available_schedulers`, no `roundrobin`, no BPF), so the split is whatever
  congestion-window growth produces. On two paths with different loss this is
  inherently uneven. This is the single biggest lever and it is **not available on
  this image**.
- **Paths of comparable loss.** Wired is stable; the satellite leg showed 323–556
  retransmits per transfer. Equalising quality would push the split toward even.
- **`pm_type=1` (userspace PM).** Writable here, so a userspace path manager could
  in principle force a policy — but that is a daemon, it contradicts the
  no-per-node-daemon constraint, and it has not been tested.

## Honest summary

> With an even split, bonded reached **1.25×** the best single path (24.3 vs
> 19.4 Mbit/s). With the typical uneven split it fell below single-path. The
> benefit is real but **not reliable on this bench**, and its reliability is
> governed by a scheduler this kernel does not provide.

The 1.2–1.3× figure is therefore **consistent with observation** — but as a
best-case ceiling reached only when the split is even, not as a typical result.
That is a narrower and more defensible claim than either "it aggregates" or "it
does not".

## Caveats

- 7 samples per configuration, 5 s each. Enough to show the pattern, not enough to
  put confidence intervals on it.
- 5 s runs do not reach steady state, so absolute numbers are lower than a longer
  transfer would give; the *comparison* is what is valid, since all three used the
  same duration in an interleaved order.
- The satellite uplink's quality varies with its RF link, which is exactly the
  variance this design cannot remove — only spread across all three arms.

## Reproduce

`scripts/compare-multipath.sh [rounds] [secs]` — interleaved A/B/C with per-tunnel
**TX** deltas per run, so the label ("wired only") can be checked against what
actually carried bytes. Note the instrumentation history: an earlier version
subtracted rx from tx and printed a cumulative counter rather than a delta, which
made every per-path column meaningless until fixed.
