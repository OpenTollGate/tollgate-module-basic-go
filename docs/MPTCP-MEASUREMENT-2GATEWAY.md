# Measurement: aggregation with two INDEPENDENT gateways (StarGate + wired)

Date: 2026-10-16 (Ralph loop iteration 6)
Operator decision: option (a) — put StarGate on the router as a second uplink
and retry for a real aggregate number.

## Topology achieved

Router `tollgate-A04E` (GL-MT6000) now has **two uplinks with independent
gateways**, which is what the previous attempt lacked:

| Uplink | Interface | Address | Gateway | ISP |
|---|---|---|---|---|
| A (wired) | `lan1` | 192.168.2.15/24 | 192.168.2.1 | house wired |
| B (wireless) | `phy0-sta0` | 10.2.1.122/16 | 10.2.0.1 | **StarGate (Starlink)** |

Construction: STA configured via `uci` (`sta_stargate`: `mode=sta`,
`ssid=StarGate`, `psk2`, `network=wwan2`), applied with `wifi reload` — never
`iw` surgery, which tears down the APs on MT7986. StarGate associated at
**-73 dBm**, DHCP gave `10.2.1.122/16`, and its gateway `10.2.0.1` pings at
**2.1 ms**. Independent reachability confirmed: StarGate reaches `8.8.8.8` at
35 ms.

MPTCP endpoint registered: `10.2.1.122 dev phy0-sta0 signal subflow`.
Both ends have `signal subflow` endpoints; limits `subflow 4 add_addr_accepted 4`.

## Result: still no aggregation — and now the reason is unambiguous

| Configuration | Throughput |
|---|---|
| single path (`--bind-dev=enx6c6e0750c33b`) | 925–927 Mbit/s |
| bonded (unbound, both local addresses) | **926–928 Mbit/s** |

Subflow formation is still healthy (`MPJoinSynRx` delta = 2, `MPCurrEstab` = 2),
so the mechanism works. But there is **no throughput gain**.

## Why: the client link is the bottleneck, not the uplinks

```
client enx6c6e0750c33b link speed = 1000 Mbit/s
measured MPTCP throughput        = 925-928 Mbit/s
```

The client reaches the router over a **single 1 GbE link**, and the measurement
is already at **~93% of wire speed**. The router's two uplinks sit *upstream* of
that link, so spreading traffic across them cannot move more bytes through a
local hop that is already saturated.

This is a different blocker from the previous run:

- **Previous run:** two L2 segments but ONE upstream → subflows converge at a
  shared bottleneck upstream.
- **This run:** two genuinely independent upstreams → but the *local* link
  between measurement endpoints is the ceiling.

**To demonstrate aggregation you need the bottleneck to be where the multiple
paths are.** Concretely: the client must connect to the router over *two*
independent links (or a link slower than the uplink aggregate), so that no
single hop is saturated. A useful shape would be a client on a ≤100 Mbit/s
link with two ≥100 Mbit/s uplinks, or two client links that each map to a
different uplink.

## What this does and does not change

**Unchanged (still proven):**
- MPTCP works on OpenWrt 24.10.2 from stock feeds.
- A second subflow forms and completes its handshake
  (`MPJoinSynRx`/`MPJoinAckRx` non-zero, `subflow:1`, `MPCurrEstab=2`).
- `signal subflow` is mandatory; `subflow`-only silently never advertises.
- Two independent gateways can be provisioned and used (StarGate on `phy0-sta0`).

**Still not proven:** any throughput aggregation — now for a *measured,
quantified* reason rather than an assumed one: the single 1 GbE client link is
saturated at 925–928 Mbit/s, i.e. wire speed.

## A methodological flaw in this measurement (stated plainly)

The test above is **not a valid aggregation test**, and the reason is structural,
not numeric: the iperf3 **server runs on the router itself**.

```
client ──(1 GbE)──► router  [iperf3 server]
                       │
                       └── uplinks (lan1, phy0-sta0)   ← never traversed
```

Traffic travels client → router and stops at the router's own socket. It
**never enters the uplinks**, so no amount of multipath upstream could show up in
the number. The 925–928 Mbit/s is simply the router's 1 GbE LAN link at wire
speed, and it was always going to be ~930 regardless of uplink count.

Two independent defects, both mine:

1. **Wrong endpoint placement.** For uplinks to matter, the far end must be
   *beyond* the uplinks — an internet or upstream host, not the router.
2. **Wrong bottleneck.** Even with a corrected endpoint, a 1 GbE client link
   carrying 925 Mbit/s is saturated, so a fast uplink aggregate still could not
   show through it.

**A valid aggregation test needs:** client → router → {uplink A, uplink B} →
a server on the far side of the uplinks; and no single hop in that chain
saturated. On this lab the far side would have to be a host reachable over both
the wired gateway and StarGate.

Because of this, the correct conclusion is **not** "aggregation failed" but
**"aggregation was not tested by this run"** — the mechanism (subflow formation)
is proven, and the throughput question remains open pending a correctly shaped
test.

## Per-subflow evidence: the second subflow forms on the SAME path

Router conntrack during a live 30 s transfer, showing both MPTCP connections:

```
src=10.63.101.201 sport=51104 dport=7201  packets=41747  bytes=869303545   <- all data
src=10.63.101.201 sport=51090 dport=7201  packets=8      bytes=725         <- handshake only
```

Both entries carry the **same source and destination addresses**
(`10.63.101.201 -> 10.63.101.1`), differing only in source port. So the second
subflow was created on the **same path** as the first: it completes its
handshake, carries ~725 bytes, and adds **zero capacity**.

That is the mechanism of the "no gain" result, and it is distinct from both
earlier explanations. The subflow exists and is healthy; it simply is not a
*different* path. Diversifying would require the peer to advertise an address
that is genuinely reached over a different local interface, and for the client
to have a matching local address on that path — which this client does not have
for the router's uplink-side addresses.

## Scheduler

Only the `default` scheduler is available on this kernel: `/proc/sys/net/mptcp/
scheduler` reads `default` and roundrobin/redundant writes are refused
(`mptcp_sched_roundrobin` is not built in). So the latency-preferring default
cannot be swapped for a spreading scheduler here. Note the default scheduler is
*correct* to prefer one path; forcing a spread across a shared bottleneck would
not create capacity either.

## Honest labelling

Even with a correctly shaped topology, expect **1.2–1.3×, not 2×**, and only
when the peer is multipath-capable. This is resilient multi-path, not doubled
bandwidth.
