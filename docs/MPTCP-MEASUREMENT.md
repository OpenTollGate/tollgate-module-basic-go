# Measurement: MPTCP subflow formation and aggregation (two disjoint L2 paths)

Date: 2026-10-16 (Ralph loop iteration 1)
Host A: CobradorWave — `wlp58s0` 192.168.2.36 (Wi-Fi → house LAN)
                      `enx6c6e0750c33b` 10.63.101.201 (Ethernet → router, direct)
Host B: GL-MT6000 `tollgate-A04E` — `lan1` 192.168.2.15 (house LAN)
                                    `br-private` 10.63.101.1 (direct)

Both host addresses on the router share one MAC (`94:83:c4:d1:57:58`), confirming
a single dual-homed device reachable two ways.

## Headline

**A real MPTCP second subflow now forms and completes its handshake — proven for
the first time in this investigation. But it carries no data, so there is NO
throughput gain. Single-path 925 Mbit/s vs bonded 919 Mbit/s — within noise.**

The earlier blocker (no subflow at all, `MPJoinSynRx=0`) is **resolved**. The
remaining limit is a different one, stated below.

## 1. Subflow formation — PROVEN

Router `MPTcpExt` counters, decoded by name before/after one measured transfer:

```
counter                    before   after
MPJoinSynRx                0        2        <- subflow JOINs RECEIVED
MPJoinAckRx                0        2        <- JOIN handshakes COMPLETED
MPCurrEstab                0        2
```

Client side, `ss -Min` during a live transfer:

```
ESTAB 10.63.101.201:56064 10.63.101.1:6201
  subflows:1 subflows_total:2 local_addr_used:3 local_addr_max:4
  remote_key token:9359cc82 bytes_sent:1272794981 bytes_acked:1270662501
```

`local_addr_used:3` and `subflows:1` on top of the primary = an additional
subflow exists on a second local address. `ip mptcp monitor` on the router also
captured `[CREATED]`/`[ESTABLISHED]`/`[CLOSED]` events for the token.

Both ends were configured with `signal subflow` endpoints (not `subflow` only) —
that fix is what made this possible.

## 2. Aggregation — NOT achieved, and the reason is visible in conntrack

Router conntrack for the transfer port, mid-flight:

```
src=10.63.101.201:56064 -> 10.63.101.1:6201   packets=53675  bytes=1092909625
src=10.63.101.201:56048 -> 10.63.101.1:6201   packets=9      bytes=789
```

Only **one** of the two MPTCP connections carries data. The second exists
(handshake done) but transfers ~789 bytes — essentially nothing.

Measured throughput, same server, same host:

| Config | Throughput |
|---|---|
| bound to one interface (`--bind-dev`) | 925–928 Mbit/s |
| unbound, MPTCP free to use both | 919–921 Mbit/s |

**No gain.** The bonded case is marginally *lower*, i.e. within run-to-run noise.

## 3. Why: two L2 segments on ONE router is not two independent paths

Both subflows terminate at the *same* router. That router must forward both into
the same upstream, so the two paths converge before they reach the internet. The
bottleneck is shared, and MPTCP's scheduler correctly keeps data on the better
subflow rather than splitting across a shared choke point.

This is a **topology limit, not a code or configuration limit**. To show a real
aggregate, the two subflows must be independent **end-to-end**, e.g.:

- router uplink A = house gateway, router uplink B = a genuinely separate ISP
  (the Starlink/StarGate path), with the client reaching the router over the
  link that carries that uplink; or
- two *different* de-aggregation endpoints on two different hosts.

## 4. What this does and does not prove

**Proven:**
- MPTCP is enabled and working on OpenWrt 24.10.2 from stock feeds.
- A second subflow can be made to form and complete its handshake
  (`MPJoinSynRx`/`MPJoinAckRx` non-zero — the counter that never moved before).
- `signal subflow` (not `subflow`) is required; `subflow`-only silently never
  advertises and no subflow ever forms.
- `mptcpize` enables MPTCP for a normal binary; a static binary can also use
  `IPPROTO_MPTCP` directly.

**Not proven:**
- Any throughput aggregation. Requires two independent end-to-end paths, which
  this lab topology does not have.
- Mid-transfer failover with a live second subflow carrying traffic.

## 5. Honest labelling

Even with two truly independent paths the expected gain is **1.2–1.3×, not 2×**,
and only when the peer is multipath-capable. Say "resilient multi-path, not 2×".
