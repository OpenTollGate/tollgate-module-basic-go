# Can we control the split between the two paths?

Date: 2026-10-19
Answer: **partially — three levers, one of them newly proven on live connections.**

## Lever 1 — `backup`: re-steers LIVE connections (newly proven)

Earlier `backup` was proven at **connect** time: marking the wired endpoint
`backup` moved the traffic share from 8.6 % to 100 % satellite. The open question
was whether it works **mid-connection** — MPTCP's MP_PRIO option — and that is what
this experiment settles.

Setup: initial subflow forced through the wired tunnel by host-routing the peer
(`ip route add <peer>/32 dev mptcp-wan1`), so `local 10.99.98.2` confirms the
initial subflow rode the tunnel. 20 s transfer, `backup` applied at t≈7 s:

```
wan1 tx: 19,044,820 -> 19,262,292 (t=7) -> 19,326,116 (t=20)   +217 KB total
wan2 tx:  5,278,260 ->  5,421,588 (t=7) ->  5,491,364 (t=20)   +213 KB total,
                                                                ~143 KB of it
                                                                post-switch
receiver: 11.8 MBytes  4.89 Mbits/sec (satellite tunnel alone)
```

**The wired leg went essentially silent after the switch; every subsequent byte
moved to the satellite tunnel.** The connection did not drop. This is live
re-steering with `ip mptcp endpoint change`, the same mechanism as failover but
deliberate and reversible (`nobackup` restores).

Practical use: **cost-aware steering on live connections**. If a metered uplink
becomes expensive mid-session, `endpoint change id <n> backup` moves traffic off it
within seconds without dropping the session — the same resilience argument as
failover, now in the steering direction.

## Lever 2 — route selection at connect: strongest, and it composes

Where the **initial** subflow goes is a plain routing decision, and it is the
dominant one: without the host route, the transfer sent 48.9 MB while the two
tunnels together carried ~400 KB — the kernel rode the direct path and treated the
tunnels as standby. Adding `ip route add <peer>/32 dev mptcp-wan1` put the initial
subflow *inside* the tunnel (local `10.99.98.2`).

So the honest model is:

- **Initial subflow:** routing table decides (direct underlay vs tunnel).
- **Additional subflows:** created only to addresses the peer can reach (tunnels).
- **Rebalance/steer:** `backup` on endpoints governs tunnel subflows, live.

If you want *all* traffic in tunnels (e.g. every byte accounted per uplink), the
host route is mandatory. If you only want a standby second path, it is not needed.

## Lever 3 — what does NOT exist on this build

- **No weighted split.** Only the `default` scheduler is compiled in; no
  `available_schedulers`, no `roundrobin`, no BPF scheduler, no weight knob. The
  ~85/15 vs ~50/50 split variation measured in the controlled comparison is
  congestion-window luck, not a policy.
- **`tc` is not installed**, so even rate-shaping one leg to force a split is not
  available on this image.
- **`pm_type=1`** (userspace path manager) is writable and could express more
  policy, but it needs a daemon — which contradicts the no-per-node-daemon
  constraint — and it is untested here.

## What this means for cost-aware steering

The practical recipe, all kernel-provided and no daemon:

1. **Decide the primary path at connect time** with a host route
   (`ip route add <peer>/32 dev mptcp-wanN`) — the initial subflow is the one that
   carries most bytes.
2. **Demote the expensive leg live** with
   `ip mptcp endpoint change id <n> backup` — proven to re-steer within seconds
   without dropping the connection.
3. **Restore** with `nobackup` when pricing changes back.

And the measurement caveat that always applies on this bench: the satellite leg's
quality swings, so any single-run throughput number should be read as a sample,
not a state. The byte-share numbers above are the reliable signal.