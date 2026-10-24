# Why the second subflow carries no payload: NAT-ed ADD_ADDR

Date: 2026-10-16 (Ralph loop iteration 15)
Question chased: force per-uplink subflow pinning and see whether both uplinks
can be made to carry payload. Answer: **no, and now I know exactly why -- it was
never the scheduler.**

## The mechanism: the advertised address is private and unreachable

MPTCP's second subflow is created by the PEER. The client sends `ADD_ADDR`
advertising its other addresses; the peer then connects BACK to one to open a
subflow. That reverse connection is impossible here:

| Router address | Advertised as | Reachable by the peer? |
|---|---|---|
| `192.168.2.15` | private, house LAN | no -- NAT-ed behind `192.168.2.1` |
| `10.2.1.122` | private, `/16` | no -- **Starlink CGNAT** |

So the VPS receives a non-routable private address, tries to join it, and fails.

## The evidence, from the peer itself

```
$ ss -tn | grep -cE "10.2.1.122|192.168.2.15"
0                       <- the VPS NEVER sees these addresses as peers

$ nstat -az | grep -i mptcp
MPTcpExtMPJoinNoTokenFound     58      <- 58 JOIN attempts, all failed
```

`MPJoinNoTokenFound` increments on the side that tries to create a subflow and
cannot find the token for the address it was told about. Fifty-eight attempts,
zero successes. Meanwhile on the router the StarGate subflow sits at ~1.7 KB
forever -- a completed handshake with no payload, exactly what the idle-subflow
signature reports.

## Everything else was already correct

- Both uplinks are registered `signal subflow`, so `ADD_ADDR` IS being sent.
- The peer's `add_addr_accepted` is 4, so the peer IS willing to accept.
- `MPJoinSynRx`/`MPJoinAckRx` are non-zero on the router: subflow signalling works.
- The paths are genuinely separate (different ISPs) and each carries data alone
  (StarGate ~4 Mbit/s, wired ~4-9 Mbit/s to the VPS).

The ONLY broken step is the reverse connection to a private address.

## Why my earlier "scheduler" conclusion was wrong

I inferred "the kernel ships only the `default` scheduler, therefore it cannot
spread" from the absence of `available_schedulers` and the sched modules. That
absence is real and correctly measured -- but it is not the operative cause,
because **no scheduler can use a subflow whose establishment fails**. I reached
the right facts and attached the wrong explanation to them. The `MPJoinNoTokenFound`
counter is the direct evidence that the failure is upstream of scheduling.

Recorded correction: the scheduler situation is still a true fact about the build
(only `default` is compiled in), but it is a *limitation on tuning*, not the
reason one path is idle. The reason is NAT on the advertised addresses.

Also noted: `fullmesh` is not settable on this kernel
(`ip mptcp endpoint change <addr> fullmesh` -> `RTNETLINK answers: Invalid
argument`), so that route is closed here too.

## What would actually fix it

The subflow needs an address the PEER can reach. Options, in order of directness:

1. **A public address on the second uplink.** If the Starlink link had a routable
   v6 prefix (or a public v4), the router could advertise something the VPS can
   connect to, and the subflow would form. Starlink's CGNAT blocks v4; a routed
   IPv6 prefix is the realistic path.
2. **A tunnel/overlay that terminates on a public endpoint**, so the advertised
   address is reachable. This is the general solution for multi-WAN behind NAT,
   and it is a design change (a WG/overlay endpoint per uplink), not a config
   tweak.
3. **Two uplinks with public addresses.** Not available in this lab.

**This is a topology/addressing problem, not a code problem in this PR.** Nothing
in `mptcp_bonding` can make a private address reachable from the peer.

## Honest status of the aggregation claim

Unchanged and now fully explained: **no throughput multiplier was demonstrated,
and it cannot be in this topology.** Subflow signalling works; subflow
establishment fails because the advertised addresses are NAT-ed. The documented
ceiling if it did work remains 1.2-1.3x, not 2x.
