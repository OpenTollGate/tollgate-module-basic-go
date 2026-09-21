# Renewal-policy simulation results (#460)

Deterministic discrete-event model of the documented renewal mechanics
(`tests/sim/renewal_sim.py`), anchored to the real bytes lab
(`renewal_e2e.sh` on merged `main` @ the #442/#450 code: **14 PASS / 0 FAIL**,
quantized purchases and cumulative doubling matching the model exactly:
110,100,480 → 220,200,960 → 330,301,440).

## Findings

**F1 — The 10 s tracker throttle, not the offset, caps fast links.**
A renewal trigger can fire at most every 10 s, so sustained throughput
ceils at `increment / 10 s` regardless of offset tuning:

| increment | 50 MB | 125 MB | 250 MB | 500 MB | 1 GB | 2.5 GB |
|---|---|---|---|---|---|---|
| ceiling | 40 Mbps | 100 Mbps | 200 Mbps | 400 Mbps | 800 Mbps | 2 Gbps |

Measured: 1 Gbps link achieves 3.5% / 8.8% / 19.4% / 38.8% / 79.3% / 100%
across that column; 100 Mbps achieves 100% from 250 MB up; ≤25 Mbps links
never bind. Offset fraction (10/25/40%) changed nothing on fast links.

**F2 — The shipped #450 defaults (500 MB preferred, 125 MB offset) are
well-chosen for ≤400 Mbps uplinks** (100% of link at 100 Mbps, zero
stalls). Gigabit resellers need 1–2.5 GB increments to use their pipe;
at 500 MB they cap at ~39%.

**F3 — The offset's only real job is covering payment latency.** It binds
exactly when `offset < payment_RTT × link_rate`: at 100 Mbps with an
injected 8 s mint RTT, 10% of a 1 GB increment (100 MB = 8 s of runway)
stalls (97.8% achieved) while 25% (16 s of runway) is clean (99.9%).
Rule: **offset ≥ 2 × RTT × link_rate**. The shipped 125 MB covers
e.g. 1 s RTT at gigabit or 8 s at 125 Mbps — adequate outside extreme
combos; the #442 clamp keeps a too-large offset from misfiring.

**F4 — Slow/expensive links: increment is a capital-vs-churn dial, nothing
else.** At 3 Mbps every configuration ran stall-free; renewals/h scale as
`throughput / increment` (30.5/h at 50 MB → 1/h at 2.5 GB).

**F5 — Capital at risk = increment × price per renewal.** At ~1 sat/GB
everything is dust (≤50 sats locked). At 20 sats/GB, a 2.5 GB tank locks
50k sats per renewal. At ~$10/GB-class links, 50–125 MB increments cap
the loss of a dead session; large prepays strand real money.

**F6 — Milliseconds metric:** the 10 s default offset tolerates
multi-second payment RTTs (no session-gap stall while the renewal is in
flight) as long as F3's rule holds for the bytes metric's tank size.

## Operator table

| uplink profile | increment | offset | rationale |
|---|---|---|---|
| 3G (~3 Mbps) | 50–125 MB | 25% | stall-impossible; keep capital light |
| 4G (~25 Mbps) | 125–250 MB | 25% | churn ≤ ~80/h |
| Cable/fiber ≤100 Mbps | 250–500 MB | 25% | **shipped default fits** |
| Satellite (20 Mbps, high RTT) | 125–250 MB | 25% | RTT covered at this rate |
| ≥1 Gbps | 1–2.5 GB | 10–25% | throttle-bound; 500 MB caps at ~39% |
| ~$10/GB-class pricing | 50–125 MB | 25% | cap stranded capital |

## Verdict: static-per-profile now, adaptive later

Static defaults serve every profile ≤400 Mbps (the shipped pair is right
for the common cases). The gigabit and fast-and-expensive quadrants want
an **`auto` mode** as a follow-up spec, not a protocol change:

- `increment = clamp(EMA(throughput) × 90 s, 50 MB, 2.5 GB)` — targets a
  ~90 s renewal cadence, staying far from the 10 s churn boundary.
- `offset = max(0.25 × increment, 2 × EMA(payment_RTT) × EMA(throughput))`
- both bounded by a max-capital-at-risk budget the operator sets.

Both EMAs already exist in effect (usage polling is 1 s; payment RTT is
observable per renewal), so the mode is config + the existing tracker.

## Method

`renewal_sim.py` implements the mechanics as read from the code: 1 s
polls, `remaining ≤ min(offset, allotment/2)` trigger (#442 clamp),
non-blocking payment with 5 s retry, ≥10 s trigger throttle, floor
quantization to 22,020,096-byte steps, cumulative allotment, stranded
capital on gateway session loss. Full sweep: 5 link profiles × 6
increments × 3 offset fractions + pathological RTT cells (0.6–8 s) +
price ladder with fault injection. Anchored to the live bytes lab via
`renewal_e2e.sh` (see above). Deterministic; rerun with
`python3 tests/sim/renewal_sim.py [--json]`.
