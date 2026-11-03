# Cost-aware steering: can we send traffic down the cheaper path?

Date: 2026-10-19
Question: if the satellite path is significantly cheaper per byte, can the bond be
steered toward it?

**Answer: yes, with `backup`. Measured — the share moved from 8.6 % to 100 %.**
But there are exactly three mechanisms on this build, with very different
guarantees, and only one of them is a hard policy.

## The control surface (verified on the device)

```
ip mptcp endpoint change [ id ID ] [ ADDRESS ] CHANGE-OPT
CHANGE-OPT := [ backup | nobackup | fullmesh | nofullmesh ]
FLAG       := [ signal | subflow | backup | fullmesh ]
/proc/sys/net/mptcp/pm_type     ->  0 (kernel)  |  1 (userspace)
/proc/sys/net/mptcp/scheduler   ->  "default" only; no available_schedulers
```

## Mechanism 1 — `backup` (hard policy, and what you want on a metered link)

Mark the path you do **not** want to pay for as `backup`. It is then used only
when no primary is usable — i.e. exactly failover semantics.

```sh
# wired is the expensive path -> demote it
ip mptcp endpoint change id 1 backup
```

Measured effect, same bench, same peer, per-tunnel byte deltas:

| configuration | wired share | satellite share |
|---|---|---|
| both equal (baseline) | **91.4 %** | 8.6 % |
| wired marked `backup` | **0.0 %** | **100.0 %** |

That is a complete inversion: with the wired endpoint demoted, **every byte went
down the satellite tunnel** and none down the wired one.

**This is the recommendation for a per-byte-priced uplink.** It is a policy
declaration, not a hint — the kernel will not prefer the backup while a primary is
healthy.

**Caveat measured on the same run:** the connection's *initial* path is chosen at
connect time from the routing table, and that still pointed at the wired uplink
(`ip route get <peer>` → `via 192.168.2.1 dev lan1`). So for that one transfer the
tunnel counters showed only 6.41 MB while the receiver reported 17.5 MB — the
shortfall travelled the *underlay* route, not a tunnel, and therefore was not
counted by either tunnel. To make steering airtight for **all** traffic, the
initial route must also prefer the cheap uplink (or the bond must be the only
route). `backup` governs the subflows; the underlay default route is a separate
concern.

## Mechanism 2 — cost-aware path selection in the module (what to build)

`backup` is static. For "cheapest path wins, automatically", the decision belongs
in the module, and the seam already exists: **`ShouldSteer`** is the single place
the bond decides. A cost model is a small addition there:

```
effective_cost(path) = price_per_byte(path)      # from the peer's advertisement
                     + retransmit_penalty(path)  # measured, not assumed
```

Points that make this honest rather than decorative:

- **The price is in the advertisement already.** The bonding-server advert of
  D6/D10 carries `price_per_step`; the router can read the per-byte price of each
  upstream instead of guessing.
- **`backup` is the actuator.** The cost model's job is only to decide *which
  endpoints carry the flag*; it should not invent a new datapath.
- **Re-evaluate on change, not on a timer.** Advisory paths change when an
  advertisement updates or a path degrades — both are already events the module
  consumes.
- **Never let "cheap" mean "unusable".** A cheap path that is lossy costs more in
  retransmits than it saves. The measured evidence on this bench is exactly that:
  the satellite leg showed 323–556 retransmits per transfer, so a price-only model
  would pick a path that performs badly. Cost must be price **plus** measured
  penalty.

## Mechanism 3 — `pm_type=1` (userspace path manager) and the scheduler

Two escape hatches, both worth knowing and neither needed for cost steering:

- **`pm_type` switching to 1 hands path management to userspace** (writable on this
  build: `echo 1 > /proc/sys/net/mptcp/pm_type` succeeded). That is the route if
  you ever need a policy the kernel cannot express — e.g. per-application or
  time-of-day pricing. It needs a userspace daemon, so it is the *expensive*
  option in engineering terms and should not be the first move.
- **The scheduler cannot be changed here.** Only `default` is compiled in; there
  is no `available_schedulers`, no `roundrobin` module, and no BPF
  (`ip mptcp endpoint change … fullmesh` is also rejected on some kernels). So
  "spread 50/50 for cost averaging" is **not available** on this image. The
  available levers are `backup` (primary/fallback) and `pm_type` (hand it to
  userspace); there is no proportional weighted split.

## What is NOT possible on this build

- **Weighted proportional split** (e.g. "60 % to the cheap path"). No spreading
  scheduler is compiled in, and the in-kernel path manager has no weight knob.
- **Per-flow or per-application steering inside one MPTCP connection.** The bond
  is per-connection; steering happens at the path level, not per socket option.
  (Per-*connection* steering is possible: choose which endpoints exist for that
  connection.)
- **Automatic price discovery without the advertisement.** The router needs the
  upstream's advertised price to price a path; there is no local oracle for cost.

## Recommendation

1. **Now, no code:** use `backup` to demote the expensive uplink. Measured to work
   completely (8.6 % → 100 % on the cheap path), and it is failover-safe by
   construction.
2. **Also fix the underlay route** so the initial path also prefers the cheap
   uplink — otherwise first-transfer bytes leak onto the expensive underlay (the
   0.37× accounting anomaly measured here).
3. **Then, in the module:** make `ShouldSteer` cost-aware using
   `price_per_step` from the advertisement plus a **measured** retransmit penalty,
   with `backup` as the actuator. Do not implement a price-only model — the
   satellite leg's 323–556 retransmits per transfer show price alone would choose
   a path that costs more in reality than it saves on paper.
