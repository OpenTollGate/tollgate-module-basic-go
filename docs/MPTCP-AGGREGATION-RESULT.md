# Aggregation measurement — real numbers, negative result

Date: 2026-10-19
Conditions: first time all preconditions held at once.

## Preconditions (all verified, under the bench mutex)

```
lan1       192.168.2.15   -> via 192.168.2.1  dev lan1      table 201
phy0-sta0  10.2.1.122     -> via 10.2.0.1    dev phy0-sta0
ping -I 192.168.2.15 8.8.8.8   0% loss, 46.6/53.4/60.2 ms
ping -I 10.2.1.122   8.8.8.8   0% loss, 39.5/43.7/47.9 ms
```
Two **genuinely disjoint** paths with comparable RTT — the best conditions this
investigation has had. Note how much closer the paths are now than the earlier
~155 ms satellite reading: the RTT-gap explanation for path preference no longer
applies.

Peer: `mptcp LISTEN` on 5201 (not plain TCP), `add_addr_accepted 4 subflows 4`,
internet OK.

## Results (6 s per run, 3 runs per configuration)

| Configuration | Run 1 | Run 2 | Run 3 |
|---|---|---|---|
| A: wired only (`--bind 192.168.2.15`) | **10.5** | *err* | **10.7** |
| B: StarGate only (`--bind 10.2.1.122`) | **4.42** | *err* | **3.41** |
| C: bonded (both endpoints) | **3.75** | *err* | **3.07** |

Mbit/s, receiver side. `err` = `Connection reset by peer` (3 of 9 runs; the peer
ran a single `iperf3 -s`, so a fast-following run can collide with the previous
one's teardown — recorded, not dropped).

**The bonded configuration was slower than either single path.** No multiplier
was demonstrated; there is no aggregation to show.

## Why: no subflow ever formed

Raw `MPTcpExt` counters, read from the device after the runs:

```
MPCapableSYNTX        = 12      the client sent 12 MPTCP SYNs
MPCapableSYNACKRX     = 12      the peer answered with MPTCP SYN-ACKs
MPCapableFallbackACK  = 0       NOT a fallback connection
MPJoinNoTokenFound    = 0       no failed JOIN attempts
MPJoinSynRx           = 0       ZERO subflows received
SF_ESTABLISHED count  = 0       (ip mptcp monitor)
```

So the connection **is** genuinely MPTCP — this is not the plain-TCP fallback
trap from earlier — and yet **not one subflow was attempted**. All traffic rode
the single default path, which is why "bonded" tracked whichever path the default
route pointed at and came out slower than wired-only.

## The finding, stated plainly

**Registering endpoints as `subflow` only does not, by itself, make the client
open a second subflow.** This falsifies a prediction made earlier in this work:
that the proxy-terminated shape would let the client initiate subflows over its
own uplinks with no address advertisement at all.

The mechanism is visible in the counter names. Linux MPTCP's in-kernel path
manager creates the *additional* subflow from the side that learns a new address
— normally the peer, via `ADD_ADDR`, which is exactly what `signal` produces. A
`subflow`-only endpoint on the client declares an address *usable* for subflows
but does not cause the client to originate one against the same single remote
address. With one remote address and no advertisement, nothing initiates.

**Consequence for the decision record:** D10 (proxy-terminated) is still the right
*shape* — the client must not depend on being reachable — but the subflow that
carries the second path must be created by the proxy, not hoped for from the
kernel. A proxy-terminated implementation has to establish its second connection
itself (a separate MPTCP socket per path, or an equivalent multiplexing layer);
the kernel will not synthesise it. The earlier BSBF measurement (~1.17×) is
consistent with this: that stack runs Xray at both ends and does its own
multiplexing rather than relying on kernel subflow creation.

## Honest labelling

- **No multiplier demonstrated.** Bonded was slower than wired-only.
- Single-path figures above are real and repeatable (wired ~10.5–10.7 Mbit/s,
  StarGate ~3.4–4.4 Mbit/s).
- The 1.2–1.3× ceiling claim remains **unmeasured on this hardware**. What is
  measured is 0×: no subflow formed at all.
- This is a **negative result**, and a useful one: it identifies that the missing
  piece is subflow *origination*, not address advertisement and not the scheduler.

## What to try next (in order)

1. **Register the endpoints `signal subflow` and re-run with the tunnel path
   available** — i.e. the advertised-address shape, which is the only shape the
   in-kernel path manager actually drives. That measures the kernel's real
   capability, and separates "the mechanism works" from "the mechanism cannot
   reach us behind CGNAT".
2. **Establish subflow origination in the proxy** — the D10-correct fix. Until
   the proxy opens the second path itself, a proxy-terminated deployment carries
   everything on one path regardless of how many uplinks exist.
3. Only then re-measure; and only then is a 1.2–1.3× claim testable.
