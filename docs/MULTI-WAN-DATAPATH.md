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
rule selection is by `src_ip`, not by mark**. Stock
[mwan3.sh](https://github.com/openwrt/packages/blob/master/net/mwan3/files/lib/mwan3/mwan3.sh)
reads `proto`, `src_ip`, `src_port`, `dest_ip` and friends for a rule
(`mwan3_set_user_iptables_rule`) and has **no** UCI `mark` option. An active
rule carrying `option mark` would therefore silently match everything and the
per-client choice would be lost.

So the renderer emits one active rule per client address:

```
config rule 'mw_client_0'
	option family 'ipv4'
	option proto 'all'
	option src_ip '<client address>'
	option use_policy 'mw_policy_<objective>'
```

with a single `src_ip '0.0.0.0/0'` catch-all pointing at
`default_objective` when no client has chosen yet. The caller supplies the
client→objective assignment (the live session map); the renderer validates that
every named objective exists and refuses an unknown one.
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

## Applying

`Applier.Apply(ctx, cfg, objectives, clients)` uses an injected `Runner` for
all commands and file I/O; this package supplies no production command runner.
It validates and refuses disabled configurations, then renders both artifacts
before any router I/O. The caller must validate the enclosing MPTCP mark as
above, supply trusted client addresses, and serialize all Apply/Restore calls
and other writers to these files. Use a trusted, absolute `BackupDir`; its
permissions are set to 0700. The runner must execute literal argv, preserve
`os.ErrNotExist`, and should replace files atomically and durably.

An identical `/etc/config/mwan3` is a strict no-op: no backup, writes, commit,
restart, or fragment reconciliation, even if the marks fragment is missing or
stale. Otherwise the old configuration must be readable and is backed up as
`<BackupDir>/mwan3.<UTC timestamp with nanoseconds>.bak` (0600); an existing
backup path is refused rather than overwritten. After the backup succeeds,
the applier writes the new configuration (0600), runs `uci commit mwan3`, then
`/etc/init.d/mwan3 restart`. **mwan3 restart drops existing conntrack-based
policy decisions for in-flight connections**; schedule it as a disruptive
operation, not a seamless policy update.

Only then does it compare the rendered marks with
`/etc/nftables.d/50-multiwan-marks.nft`, treating a missing fragment as changed.
A changed fragment is written (0644) and loaded via `fw4 reload`; identical
content does not trigger a reload. Firewall state lives only in
`/etc/nftables.d/*.nft` files: no runtime `nft add` or `nft insert` is executed.
The rendered sets remain intentionally empty; this applier does not invent
client membership.

**`fw4 reload` procd-restarts nodogsplash and wipes its runtime trust/auth
state. The keepalive contract restores it** by re-establishing the live
sessions' NDS trust/auth state after the restart. The caller must ensure that
keepalive recovery is active; this applier neither runs it nor waits for NDS
readiness, so an access interruption is possible.

Errors stop subsequent steps and return the partial `Result`, including the
backup path as soon as the backup succeeds. `Changed` means the configuration
write succeeded, and `MWAN3Restarted` means the restart command succeeded, not
that the service is ready. There is no automatic rollback or transaction across
UCI, mwan3, and fw4. The durable backup precedes the first configuration write.
If a later step fails (or the process dies), use
`Restore(ctx, backupPath)` to write the old configuration, commit UCI, and
restart mwan3 before retrying. Restore does not undo an already-written marks
fragment or reload fw4: the caller must separately reconcile that fragment and
ensure NDS keepalive recovery. A blind Apply retry after a partial failure can
hit the strict identical-config no-op and will not finish the failed steps.

## Non-goals

No automatic client assignment, probe execution, production runner wiring, or
packet-forwarding claim. The picker UI and client-assignment integration are
later steps. Fake-runner tests verify ordering, failures and idempotence only;
nothing here has been run against a router. UCI parsing, mwan3 restart timing,
fw4/NDS recovery, nft parser acceptance and actual forwarding remain unverified
and require the router-integration owner before this is enabled in production.
