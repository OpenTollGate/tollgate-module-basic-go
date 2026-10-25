# Split control: complete map — `tc` shaping makes the split deterministic

Date: 2026-10-19
Extends `MPTCP-SPLIT-CONTROL.md`. Answers "can we control the split between the
two paths?" with the last missing lever tested.

## The fourth lever: `tc` rate-shaping works and makes the split DETERMINISTIC

`tc` installs from **stock OpenWrt feeds** (`opkg install tc kmod-sched-core` — it
was previously reported missing; it was merely not installed). With the initial
subflow host-routed through the wired tunnel and a 5 Mbit/s tbf qdisc on that
tunnel:

```
wan1 (shaped 5 Mbit/s):  468,003,216 -> 473,788,560   +5.79 MB  ≈ 5.4 Mbit/s
wan2 (unshaped):         162,334,112 -> 168,045,364   +5.71 MB  ≈ 4.2 Mbit/s
receiver:                9.62 MBytes over 12.2 s       sum matches both paths
```

**The shaped leg sat at its cap; the excess moved to the other leg; total = the
sum.** This is a deterministic ~50/50 split — the thing the missing scheduler
cannot do, achieved with an ordinary qdisc. The MPTCP congestion controller does
the routing naturally: cap one path and the surplus spills to the other.

## Why shaping without the host route does nothing

First attempt shaped the tunnel with no host route: 23.5 MB transferred, wan1's
counter moved 299 KB. **The initial subflow rode the direct underlay path**
(`192.168.2.20` via lan1), completely bypassing the shaped qdisc — tbf on the
tunnel only shapes tunnel subflows. So the ordering is:

1. `ip route add <peer>/32 dev mptcp-wanN` — force the initial subflow into a
   tunnel (verify with the iperf `local` address);
2. **then** `tc qdisc add dev mptcp-wanN root tbf rate X` — shape it.

Both are needed. Shaping a tunnel while the initial subflow rides the underlay
shapes nothing that matters.

## Two instrumentation traps, both caught before answering

1. **`awk '{print $2}'` reads RX, not TX.** In these tests the router is the
   *sender*, so field 3 (TX) is the payload attribution. An earlier reading had
   "11.5 MB received but only 0.53 MB of tunnel deltas" — that mismatch was this
   bug (RX ≈ ACKs), not a third subflow.
2. **Field 1 is the peer's public key**, not a byte counter — easy to grab the
   wrong column and get plausible-looking nonsense.

The reliable recipe remains: TX delta per tunnel, before/after, and the receiver's
byte count must equal the sum of both deltas (± encapsulation).

## Complete control surface

| lever | controls | when | deterministic? |
|---|---|---|---|
| host route (`ip route add <peer>/32 dev mptcp-wanN`) | which tunnel carries the **initial** subflow | connect time | yes |
| `tc tbf` on a tunnel | that path's **share** (cap it and surplus spills) | anytime | **yes — measured 50/50 at 5 Mbit/s cap** |
| `ip mptcp endpoint change id N backup` | primary/fallback policy on **live** connections | anytime | yes (policy, not ratio) |
| `signal` flag | peer-initiated subflow creation | connect time | yes |
| scheduler | proportional spread | — | **not available** (only `default`) |
| `pm_type=1` | arbitrary userspace policy | anytime | needs a daemon — excluded by constraint |

## Practical recipes

**Deterministic 50/50 (bandwidth cost averaging):**
```sh
ip route add <peer>/32 dev mptcp-wan1            # initial subflow in tunnel 1
tc qdisc add dev mptcp-wan1 root tbf rate 5mbit burst 32kbit latency 50ms
# measure; adjust rate until the desired split; remove with:
# tc qdisc del dev mptcp-wan1 root && ip route del <peer>/32 dev mptcp-wan1
```

**Cost-aware steering (metered uplink):** keep the cheap leg unshaped and cap the
expensive one at what you are willing to pay, e.g. `rate 2mbit` — the surplus
automatically spills to the cheap path. This composes with `backup` for a hard
off-switch when the expensive path is congested.

## Caveats

- tbf shapes the *tunnel device*; per-*uplink* shaping would go on `lan1` /
  `phy0-sta0` and would affect all traffic on that uplink, not just the bond's
  share.
- The 5 Mbit/s / 50/50 numbers are single-run measurements on a bench with
  variable satellite quality; the *mechanism* (cap → spill) is the durable result.
- tbf on `mptcp-wanN` only shapes the router's own subflows; the peer's inbound
  congestion behaviour is unaffected.