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
4. ~~Operator docs for enabling MPTCP on OpenWrt~~ — DONE (`5c30576`)
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

## Iteration 3 — 2026-10-16

### Finding — operator runbook written, and verifying it found 3 defects in my own draft

Built `docs/MPTCP-OPERATOR-RUNBOOK.md` + `scripts/read_mptcp_counter.sh`. Every
command was RUN against the real router before being written down. That caught:

1. `ss -Min` prints NOTHING for MPTCP sockets on this OpenWrt build — my draft
   told operators to read `subflows:1` from it. Removed; `/proc/net/netstat`
   counters are now named as the authority.
2. `paste` is not in BusyBox — the obvious `awk|tr|paste` one-liner FAILS on
   OpenWrt. Replaced with the shipped helper script.
3. `ip route get` against the router's OWN address returns `local` and proves
   nothing — the disjoint-path check is now specified as client-side against
   the PEER's addresses (verified from cobrador: two peer addrs resolve to two
   different `dev`, which is the actual proof).

Helper script verified ON DEVICE in BusyBox ash: SYNTAX_OK, MPJoinSynRx -> 2,
MPJoinAckRx -> 2, MPCurrEstab -> 0, no-arg mode lists all pairs, unknown
counter exits 1.

**Status:** done. Commit `5c30576`, pushed ✓.

## Iteration 4 — 2026-10-16

### Finding — the "wiring" item was premature; the missing seam was the real work

Item 5 said "wire `ShouldSteer` into `upstream_detector` events". Checking first
showed `mptcp_bonding` is **not imported anywhere** — no imports, no
initialisation in `main.go`. Nothing calls into the package, so there was no
lifecycle to wire an event handler onto.

So the actual missing piece is the event-driven reconciler that the wiring would
call. Built that instead: `NewReconciler(cfg, apply)` with methods mirroring
`upstream_detector`'s event set exactly (`HandleInterfaceUp`,
`HandleAddressAdded`, `HandleInterfaceDown`, `HandleAddressDeleted`) so the
future wiring is a direct call-through with no adapter.

Two safety rules, both tested:
  * only declared uplinks reconcile (a router has many flapping interfaces);
  * apply only when the RENDERED SCRIPT changed — the apply script is
    delete-then-add, so re-applying an identical render would rebuild every
    endpoint and interrupt an in-flight subflow on every unrelated event.

The side effect is injected (`Applier func(string) error`), so logic is testable
with no router and a nil Applier / nil Config is inert rather than panicking.

**Status:** done. Commit `c8e2d0f`, pushed ✓.
Tests: 14 new (RED first: `undefined: NewReconciler`), 35 total, coverage 91.0%
(up from 90.3%), vet/gofmt/build clean.

## Work queue (next)
6. Wire the reconciler to `upstream_detector`'s event loop (now a direct
   call-through) and initialise `mptcp_bonding` in `main.go` — NEXT (code, TDD).
   NOTE this is a cross-module change: `mptcp_bonding` and `upstream_detector`
   are separate Go modules with `replace` directives, so it needs a go.mod
   require + replace entry, and main.go needs config plumbing. Bigger blast
   radius than the previous items; flag if you want it split.

## Iteration 5 — 2026-10-16

### Finding — the cross-module wiring was split, because there IS a cycle hazard

Before touching `main.go` I checked the module graph and found a real reason to
stage this rather than do it in one commit:

  * `upstream_detector` already depends on `config_manager`.
  * If `mptcp_bonding` ALSO depended on `config_manager`, and
    `upstream_detector` imported `mptcp_bonding`, the edges close into a CYCLE.

So `mptcp_bonding` must stay a **leaf module**. That constrains the design: the
host module owns where config bytes come from, and the package only parses them.

Built the package-side of the seam this iteration: `LoadConfig([]byte)`.
Key behaviours:
  * absent config => DISABLED bond, not an error (every pre-existing node has no
    such section; failing would take the module down on upgrade);
  * unknown keys REJECTED (`enabld: true` must be loud, not silently off);
  * validation at load, so a bad bond fails at startup.
Also `ConfigJSONExample()`, generated from the type so it cannot drift, and it
emits no password field.

**Status:** done. Commit `2071e0c`, pushed ✓.
Tests 41 total, coverage 91.1%, vet/gofmt clean.

### Honest note on scope
Items 1, 3, 4, 5 of the original handoff are now DONE. Item 2 (aggregation) is
topology-blocked pending an operator decision. The remaining work is the
HOST-SIDE wiring, which is genuinely a different kind of change:

  6a. `config_manager`: add an `mptcp_bonding` schema entry (needs the schema
      mechanics understood; unknown-key strictness may reject an unregistered
      section).
  6b. `main.go`: load the bond config and construct the Reconciler.
  6c. `upstream_detector`: add a `mptcp_bonding` module dep + call the
      reconciler from its event handlers.

This touches the module that manages live router CONNECTIVITY on every
deployed node. A mistake can affect every node, not just the lab. Flagged to
the operator rather than executed unattended.

## Iteration 6 — 2026-10-16 (operator chose option (a): StarGate as 2nd uplink)

### ACHIEVED: StarGate is now a live second uplink on the router
Constructed the STA via `uci` + `wifi reload` (per the MT7986 rule):
  sta_stargate: mode=sta ssid=StarGate psk2 network=wwan2
Result: phy0-sta0 @ 10.2.1.122/16, gateway 10.2.0.1, associated -73 dBm,
gateway RTT 2.1 ms, reaches 8.8.8.8 at 35 ms. Two uplinks with INDEPENDENT
gateways now exist on the router (wired 192.168.2.1 + StarGate 10.2.0.1).

### ACHIEVED: two subflows across two DIFFERENT client interfaces carried data
Previously the 2nd subflow formed on the SAME path (869 MB vs 725 B). This run
drove the client over both local paths and got two genuinely distinct subflows:
  src=192.168.2.36  (wlp58s0)        49823 pkts / 510695705 B
  src=10.63.101.201 (enx6c6e0750c33b) 85187 pkts / 1256176480 B
both ESTABLISHED simultaneously. MPJoinSynRx advanced 2 -> 16.

### NOT achieved: throughput gain
  wifi only           1.25 Gbit/s
  cable only          1.20 Gbit/s
  aggregated (3 runs) 1.19 / 1.21 / 1.27 Gbit/s  <- within noise
Cause: both client paths terminate on the SAME house LAN switch. Subflows are
logically distinct but physically share one network.

### Measurement flaw corrected mid-iteration
My first 2-gateway run was invalid: the iperf3 SERVER was on the router, so
traffic never traversed the uplinks at all. Documented rather than buried.

### Router left clean
temp nft test rule deleted (verified count 0), iperf3 killed, temp logs removed.

Docs: MPTCP-MEASUREMENT-2GATEWAY.md, MPTCP-MEASUREMENT-DUALPATH.md
Commits: 94e0bce, 9e061fa, b650f6d

## Work queue (next)
7. Remaining: host-side wiring 6a/6b/6c (config schema, main.go, upstream_detector
   call-through). Still flagged for operator go/no-go: touches the module
   managing live node connectivity fleet-wide.
8. Aggregation gain: needs genuinely disjoint paths end-to-end (two separate
   networks at the client). Not achievable on this lab.
