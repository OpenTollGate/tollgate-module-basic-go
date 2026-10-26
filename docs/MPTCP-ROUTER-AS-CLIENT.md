# Router-as-client topology: the operator's correction

Date: 2026-10-16 (Ralph loop iteration 13)
Operator challenge, and it was right: *"are you saying the router is the bonding
server and that its bonding clients only have the router as their gateway? What
if we run the bonding server on one of the VPS machines and the MT6000 acts as
the client?"*

## Correction: the earlier measurements used the WRONG topology

Every earlier measurement in this investigation put the **iperf3 server on the
router**. Traffic then travelled client -> router and stopped at the router's own
socket, **never entering the uplinks at all**. No uplink topology could show up
in that number. I twice diagnosed the "shared switch" between the client and the
router, which was true of *that* pair of paths but was **not** the topology that
answers the aggregation question.

## The correct topology, now tested

```
MT6000 (MPTCP CLIENT) ──┬─ lan1      192.168.2.15 ──> wired WAN  ──┐
                        │                                            ├──> VPS 23.182.128.51
                        └─ phy0-sta0 10.2.1.122  ──> StarGate   ──┘
```

Two genuinely different first hops (different ISPs), reconverging only at the
far end. The router is the client; the VPS is the peer.

Setup performed:
- VPS `23.182.128.51` (`testserver2`) was **already reachable** — the earlier
  `Permission denied` was my wrong SSH user (`root` vs the `vps2fips` alias /
  `debian` user), not a dead host.
- Installed `iperf3` + `mptcpize` on the VPS (kernel 6.12.111, MPTCP enabled).
- Raised the VPS to `add_addr_accepted 4 subflows 4`. **Default was 0**, which
  would have refused the router's ADD_ADDR and blocked subflows outright.
- Opened `5201/tcp` in UFW — the VPS runs `INPUT policy DROP`, and the server was
  listening while the firewall silently dropped inbound. Another "looks like a
  routing bug" that was a firewall default.

## Measurements (each repeated 3x for variance)

Router as client, to the VPS:

| Path | Runs | Notes |
|---|---|---|
| StarGate only | 3.92 / 3.75 / 2.90 Mbit/s | |
| Wired only | 5.58 / 9.44 / 9.66 Mbit/s | |
| Both (unbound) | 2.83 Mbit/s | slower than either alone |

Both uplinks are **far below their link-layer rates** (StarGate radio reported
104-156 Mbit/s). The bottleneck is therefore upstream of the radio — Starlink
backhaul and the house WAN, both contended/slow — not the local links. That is
the first time in this investigation the uplinks themselves have been the
constraint, which is what makes this topology the right one.

## Why "both" was slowest: only ONE uplink carried the data

Mid-transfer conntrack, sampled while a 40 s transfer was running:

```
src=192.168.2.15   (wired)     pkts=11728  bytes=34736249   <- ALL the data
src=10.2.1.122     (StarGate)  pkts=24     bytes=1738       <- handshake only
```

The StarGate subflow **formed and then carried nothing**. So there was no
aggregation — and the "both" run was *slower* than wired-only because the
scheduler spent time on a path it could not use productively.

This is exactly the failure mode `AssessPaths()` names: two subflows exist, but
they are not carrying the connection on two paths. On this topology the check
would report `AllCarryPayload=false`.

## What this establishes, honestly

**Confirmed by measurement:**
- The router-as-client topology is the correct one, and it is now working: two
  uplinks with different ISPs, registered as `signal subflow`, with a
  VPS peer that accepts ADD_ADDR.
- Both uplinks are individually usable to the VPS and are now the real bottleneck
  (3-9 Mbit/s), so an aggregate gain would be visible here — unlike the earlier
  LAN-hop measurements.
- MPTCP created subflows (`MPJoinSynRx` non-zero throughout).

**NOT demonstrated:** a throughput gain. Only the wired uplink carried payload;
the StarGate subflow stayed idle. Why the scheduler kept traffic on one path
while the other sat idle is the open question, and the two candidate causes are
(a) the latency difference between wired and StarGate makes the default
latency-preferring scheduler avoid the slower path, and (b) this kernel exposes
only the `default` scheduler, so a spreading scheduler cannot be selected to
test (a).

**Still not claimed:** 2x, or even 1.2-1.3x. What is now true and was not before
is that a valid test bed exists, the bottleneck is genuinely at the uplinks, and
the next experiment can be meaningful.

## Next experiment

Set `net.mptcp.scheduler` if any spreading scheduler is built, or force
diversity by pinning subflows: add a specific endpoint per uplink and confirm
with per-subflow counters that BOTH carry payload. If only one ever does with the
default scheduler, the honest conclusion is that aggregation needs a scheduler
this kernel does not ship — a finding about the kernel, not about the config.
