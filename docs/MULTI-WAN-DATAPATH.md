# Multi-WAN datapath core

This is the second step after #1124 (objectives/configuration and derivation).
The branch depends on #1124 and must merge after it. It does not replace that API.

## Sampling

`NewSampler(window, clock)` accepts a positive duration and an injected clock.
`Add` consumes cumulative download `Bytes` counters with timestamps, RTT and
loss; it returns an error for invalid or backward observations. `SetUp` records
external link state. `Measurements` and `Prune` expire observations older than
`clock()-window`; the exact cutoff is included. An empty window is down, a
single sample has zero rate, and pricing is always nil.

Download Mbit/s is byte delta times eight divided by elapsed seconds and one
million. Counter-reset crossing intervals are excluded. RTT is the median
(including the average middle pair), loss the arithmetic mean. The clock is
the only source of current time; rollback cannot resurrect expired samples.
Methods are mutex-protected. The supplied clock must not reenter the sampler.

The requested `Sample` shape contains one byte counter, not independent receive
and transmit counters; #1124's `Measurement` has no upload-rate field.
Consequently `DownMbps` and `UpMbps` input hints are not aggregated, and no
upload throughput is invented. A future schema extension is needed to expose it.

## Rendering

`RenderMWAN3` returns UCI interface, member and policy sections, plus **commented
rule templates only**. Stock [mwan3.sh](https://github.com/openwrt/packages/blob/master/net/mwan3/files/lib/mwan3/mwan3.sh)
(`mwan3_set_user_iptables_rule`) does not read a UCI `mark` option. Emitting
those templates as active rules would silently create catch-all policies.
A supported mark-to-policy integration is a blocker, not implemented here;
this renderer therefore does not yet provide the requested active marked rules.
Uplink IDs must be existing UCI logical network interface names (alphanumeric
or underscore); the renderer does not invent device names. Members within one
objective share a metric so their weights balance traffic; metrics differ
between objectives. Distinct metrics per uplink would implement failover rather
than the requested weighted policy. Zero-weight members are not policy members;
empty policies fail closed with `last_resort unreachable`.

`Outcome` has no ID: pass outcomes in the same order as `cfg.Objectives`.
Rendering then sorts by objective ID and uplink ID without mutating inputs.
Sorted objective index i receives `mark_base + i`. Both renderers invoke
`Validate`, refuse disabled/empty configurations, and additionally check every
allocated mark for overflow and collision with mwan3's reserved member mask.

`RenderMarks` emits a fragment to include inside an IPv4/inet nftables table,
with auditable mark-offset comments and per-objective IPv4 client sets. Since
#1124 has no client-assignment fields, these sets are intentionally empty:
the caller must populate disjoint sets with client addresses before use. No
client or interface is guessed. Rules preserve all bits outside the union of allocated objective marks while
setting the objective selector. The commented UCI templates are not active matches.

The section also lacks the enclosing MPTCP fwmark. Callers **must** call
`Validate(cfg, effectiveMPTCPFWMark)` before rendering and check all allocated
objective marks against that mark, not just the base. These two signatures
cannot validate a fwmark they are not given. This is not live integration.

## Non-goals

No applier, file writes, UCI commit, nft load, probe execution or router calls.
No automatic client assignment or packet-forwarding claim. The picker UI and
client-assignment integration are later steps. Exact string tests verify output;
OpenWrt/mwan3/nft parser acceptance and actual forwarding still require the
router-integration owner before any apply feature ships.
