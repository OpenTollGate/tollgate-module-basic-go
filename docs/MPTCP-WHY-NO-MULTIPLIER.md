# Would a closer VPS prove the multiplier? And what is actually blocking it

Date: 2026-10-19
Answers two questions with measurements taken on the bench.

## Question 1: would a physically closer bonding server help?

**Honestly: probably yes, but distance is not the binding constraint — and I now
have the evidence to say why.**

### Measured latencies

| destination | over wired | over satellite |
|---|---|---|
| `8.8.8.8` (general internet) | 25–53 ms | 34–48 ms |
| **the VPS `23.182.128.51`** | **173–203 ms** | **177–214 ms** |

The VPS is **~4–8× further away than the general internet** on both paths. So the
relay is badly located relative to this lab, and a nearer one would reduce RTT
substantially.

**But the tunnels were not the cause.** With both tunnels deleted — removing
WireGuard encapsulation and 40 ms of doubled relay latency — the raw path to the
same VPS still measured **~198 ms on both uplinks**. The latency is in the
internet path to that host, not in the bond.

### Why closer would help anyway

Aggregation needs each subflow to fill a **congestion window**; achievable
throughput per path is roughly `window / RTT`. At 198 ms you need an enormous
window to fill even 20 Mbit/s, and the kernel's autotuning was measured topping out
around **150 KB** (seen in the `Cwnd` column). Two paths at ~150 KB each with
198 ms RTT cannot sum to much — which is exactly the ~15–25 Mbit/s ceiling
observed. At 20 ms RTT the same window would carry ~10× more.

### The counter-evidence that matters more

**A closer VPS would NOT fix the dominant problem.** The measurement that decides
this is below: with no tunnels and no address advertisement, the counters showed
**zero subflows formed**. Proximity cannot help a subflow that is never created.

So the honest ordering is:

1. **Subflow creation** (blocking — see Question 2). A nearer VPS does nothing for
   this.
2. **RTT/window** (real but secondary). A nearer VPS helps here.
3. **Path quality asymmetry** (the scheduler prefers the stable path).

## Question 2: what is actually blocking the multiplier?

Four distinct causes, all measured on this bench. They compound, which is why the
result has been stubbornly ~1× rather than 1.2–1.3×.

### Cause 1 — NO SECOND SUBFLOW IS CREATED without address advertisement

This is the decisive one, and it is newly measured. Configuration: no tunnels, no
`signal`, endpoints registered `subflow` only, client wrapped in `mptcpize`:

```
MPCapableSYNTX    62 -> 64   (+2)   MPTCP handshake used
MPCapableSYNACKRX 62 -> 64   (+2)   peer answered MPTCP
MPJoinSynRx        0 ->  0   (+0)   NO subflow attempted
MPJoinAckRx       21 -> 21   (+0)   no completion
AddAddrTx          8 ->  9   (+1)   an advertisement was sent
```

The earlier claim in this project's notes — *"a subflow-only endpoint is useless;
it must be `signal subflow`"* — is **confirmed by direct measurement**. Registering
an endpoint `subflow` only declares an address *usable*; it does not make either
side create a subflow toward the same single remote address. **`signal` is
mandatory.** With `signal` and private addresses, `AddAddrTx` rises and the peer
still cannot join (Cause 2). With `signal` and reachable tunnel addresses, two
subflows DO form (proven separately).

### Cause 2 — NAT/CGNAT makes the advertised address undiallable

Measured from the peer: `192.168.2.15` and `10.2.1.122` both **UNREACHABLE**. The
router advertises an address the peer cannot connect to, so `MPJoinSynRx` stays 0
while `AddAddrTx` climbs. Only the per-uplink tunnel fixes this, by giving the
client an address the peer can reach through a connection the client initiated.

### Cause 3 — RTT/window starvation at ~198 ms

Per-path throughput ≈ `congestion_window / RTT`. Measured `Cwnd` ceiling ~150 KB
against ~198 ms RTT caps a path near 6 Mbit/s regardless of link capacity. Two
such paths cannot sum to a large number. This is a property of the *route*, not of
MPTCP.

### Cause 4 — the scheduler splits 91/9, not 50/50

Even with two subflows established and both carrying payload, the measured split
was **91.4 % / 8.6 %**. Only the `default` scheduler is compiled into this kernel
(no `available_schedulers`, no `roundrobin`, no BPF), and it prefers the
lowest-RTT/most-stable subflow. The satellite leg is lossy (323–556 retransmits
per transfer), so it holds a smaller window and carries less. **There is no
weighted-split knob on this build.**

### Cause 5 (measurement-level) — variance exceeds the effect

Single-path runs on this bench spanned **5.4–23.5 Mbit/s**; bonded runs spanned
**9.7–26.9 Mbit/s**. The within-configuration spread is larger than any
aggregation effect, so a multiplier cannot be resolved statistically even when it
exists. Any future comparison needs paired A/B/bonded runs in one window, repeated,
with ranges reported.

## What a closer VPS would and would not fix

| cause | fixed by a nearer VPS? |
|---|---|
| 1. no subflow without `signal` | **No** — configuration |
| 2. NAT/CGNAT unreachable address | **No** — needs tunnel or public address |
| 3. RTT/window starvation | **Yes** — directly |
| 4. 91/9 scheduler split | **Partly** — better-matched paths would split closer to even |
| 5. measurement variance | **Yes, helps a lot** — shorter RTT is less noisy |

So: proximity is worth doing **after** causes 1 and 2, and it would mainly buy
*resolvability* (Cause 5) plus a better window (Cause 3), not a magic 2×.

## The realistic upper bound, restated

If causes 1–2 are solved (done: tunnels), and 3 is improved (nearer peer), and 4
remains as-is (can't change the scheduler), the expected outcome is **paths of
similar quality splitting nearer to even, bounded by each path's
`window/RTT`** — which is the documented **1.2–1.3×** region, not 2×. The 2×
figure would require the two paths to be (a) equal in quality, (b) both saturated,
and (c) spread evenly — none of which held here.
