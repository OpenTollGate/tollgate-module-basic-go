# PROGRESS — FIPS MPTCP multi-WAN bonding (Ralph loop)

## Iteration 1 — 2026-10-16

### State verified at start
- FIPS fork `felixfelix-bot/fips` @ `pr/tcp-mptcp-option` = `36f82c86` (pushed ✓)
- TollGate `pr/mptcp-bonding-demo` = `a43f7a6` (pushed ✓), PR #927 OPEN, head matched
- Worktrees present: `~/worktrees/fips-mptcp`, `~/worktrees/pr-mptcp-setsockopt`

### Finding 1 — lab topology was NOT what the handoff said
The handoff claimed "a single physical link between hosts, aggregation
impossible". Re-checked with ARP: the router is **dual-homed on two disjoint L2
segments**, reachable from CobradorWave two independent ways:

| Path | Client iface | Client addr | Router addr |
|---|---|---|---|
| Wi-Fi → house LAN | `wlp58s0` | 192.168.2.36 | 192.168.2.15 (`lan1`) |
| Ethernet, direct | `enx6c6e0750c33b` | 10.63.101.201 | 10.63.101.1 (`br-private`) |

Both router addresses share MAC `94:83:c4:d1:57:58`.
**Status:** usable two-disjoint-L2 setup exists. Earlier note superseded.

### Finding 2 — client endpoints were `subflow`-only (real bug)
Cobrador's endpoints lacked `signal`, so no `ADD_ADDR` was ever sent. Fixed to
`signal subflow` on both interfaces. This was a genuine blocker.

### Finding 3 — SUBFLOW FORMATION PROVEN (first time)
Router `MPTcpExt` deltas across one measured transfer:
- `MPJoinSynRx 0 → 2` (JOINs received) — the counter that never moved before
- `MPJoinAckRx 0 → 2` (handshakes completed)
- `ss -Min`: `subflows:1 subflows_total:2 local_addr_used:3`

**Status:** done. `docs/MPTCP-MEASUREMENT.md` committed as `c443f21`, pushed.

### Finding 4 — aggregation NOT achieved, topology-limited
| Config | Throughput |
|---|---|
| single path (`--bind-dev`) | 925–928 Mbit/s |
| unbound (both paths) | 919–921 Mbit/s |

Conntrack mid-flight: only ONE MPTCP connection carries data
(`53675 packets / 1092909625 bytes` vs `9 packets / 789 bytes`). Both subflows
converge at the same router with one upstream → shared bottleneck → the
scheduler correctly keeps traffic on the better subflow.

**Status:** honest negative, documented. Needs independent *end-to-end* paths
(e.g. router uplink B = separate ISP) for a real aggregate.

### Commits this iteration
- `c443f21` docs(mptcp): lab measurement — subflow formation proven,
  aggregation topology-limited (+ `docs/MPTCP-MEASUREMENT.md`). Pushed ✓.

## Work queue (next)
1. ~~PR #927 body: add FIPS section~~ — DONE (REST PATCH, verified live)
2. Aggregation measurement — **BLOCKED**: needs independent end-to-end paths
   (operator decision required; see below)
3. ~~Thin applier~~ — DONE (`d927deb`)
4. Operator docs for enabling MPTCP on OpenWrt — NEXT
5. Wire `ShouldSteer` into `upstream_detector` events — not started

## Iteration 2 — 2026-10-16

### Finding — applier built and proven idempotent ON DEVICE

`Config.ApplyScript()` renders the whole bond as one ordered, re-runnable shell
script (pure renderer, no I/O). Ordering enforced by test:
preconditions → limits → endpoints. Each endpoint is preceded by a delete of the
same address.

Verified by EXECUTING IT TWICE on the router and reading the post-condition from
the device:

```
RUN1_OK, RUN2_OK
endpoint set byte-identical before/after both runs (no duplicates, no failures)
limits: add_addr_accepted 4 subflows 4
```

Also `sh -n` passes (valid shell), 4 tests RED-first, coverage 90.3%, vet+fmt
clean.

**Status:** done. Commit `d927deb`, pushed ✓.

### Commits this iteration
- `d927deb` feat(mptcp): idempotent applier (+ some tests, `apply.go`)

## Operator decision needed

Aggregation cannot be shown on this lab: two disjoint L2 segments but **one
upstream**, so both subflows converge at the same bottleneck (conntrack shows
only one connection carrying data). Options: (a) put a second ISP on the router
(Starlink/StarGate as uplink B) and retry, or (b) accept "subflow formation
proven, aggregation topology-blocked" as the documented state.
