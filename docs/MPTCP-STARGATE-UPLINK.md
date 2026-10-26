# StarGate uplink: effect of moving the gateway closer

Date: 2026-10-16 (Ralph loop iteration 12)
Operator action: physically moved the Starlink/StarGate gateway closer to the
router, then asked "did it help?"

**Answer: yes, measurably.** The StarGate uplink's link rate nearly doubled.

## Before / after

| Metric | Before the move | After |
|---|---|---|
| Signal (avg) | -73 dBm | **-72 dBm** (ack signal -70) |
| TX bitrate | 104.0 MBit/s, MCS 13 | **156.0 MBit/s, MCS 21** |
| RX bitrate | 58.5 MBit/s, MCS 18 | **up to 156.0 MBit/s** |
| Link quality | 37/70 | **38/70** |

Two modulation steps gained (MCS 13 -> MCS 21) from what is barely a 1 dB
signal change. So what improved is not raw received power but the **SNR /
interference margin at the same power**, which is what lets the rate controller
pick a faster modulation. Near the top of a rate table, a quiet dB is worth more
than the number suggests.

## Latency and loss

```
ping -c5 -I 10.2.1.122 9.9.9.9
5 packets transmitted, 5 received, 0% loss
min/avg/max = 23.4 / 30.4 / 37.3 ms
```

Healthy. The StarGate uplink is usable and stable.

## What this does NOT change (important)

The aggregation measurement is unchanged, because the bottleneck was never the
StarGate uplink. In the measured topology both of the client's legs terminate on
the same house LAN switch:

```
cobrador wlp58s0 ─┐
                  ├─ house switch ─→ router ─→ {wired WAN, StarGate}
cobrador enx...  ─┘
```

A faster StarGate uplink makes the **router** a better gateway for its own
clients. It cannot give the **client** two disjoint first hops, which is what a
measured throughput multiplier requires. Expected ceiling remains **1.2-1.3x, not
2x**, and the client's own ~1 GbE links are the binding constraint.

## Measurement caveat, stated honestly

The 156 Mbit/s figure is the **link-layer rate** from `iw`/`iwinfo` — a solid,
observed number. An **end-to-end** StarGate throughput figure was NOT obtained:
the attempted fetch from the router to `speedtest.tele2.net` hung, and BusyBox
`wget` has no `--bind-address` to steer a fetch out a chosen uplink. No
end-to-end number is claimed here.

To get one: run an iperf3 server reachable on the internet side and originate the
transfer from the router bound to each source address in turn.
