# FIPS MPTCP multi-WAN bonding — status vs the four goals, with gap analysis

Date: 2026-10-19
Consultant pass: network-architecture (independent subagent, no prior session
context), grounded in this repo's docs and measurements, `tollgate-rs` design docs,
and `tg-mptcp-server`.

---

## Q1: Do we have FIPS running over multi-WAN bonding?

**NO. Zero FIPS bytes have ever crossed the bond.**

What exists:
- Kernel-level MPTCP bonding is **proven on hardware**: two subflows on one token,
  both tunnels carrying payload, mid-transfer failover surviving loss of the
  dominant subflow (~3 s stall).
- The FIPS fork patch is written and clean: `felixfelix-bot/fips` branch
  `pr/tcp-mptcp-option` @ `36f82c86` — `TcpConfig.mptcp` (default off),
  `Protocol::MPTCP` at socket **creation** (not retrofittable — MPTCP cannot be
  set on an existing socket), cfg-gated Linux, mandatory plain-TCP fallback. 945
  tests pass, darwin target clean.

What does not:
- **No FIPS node exists on the MT6000** — it runs TollGate, not FIPS. The patch
  has no consumer, no cross-build for OpenWrt aarch64/musl, no init script, no
  upstream PR (deliberately, per operator instruction).
- The gap analysis's own Layer-0 experiment ("FIPS on MT6000 with both uplinks,
  single- vs bonded goodput") was **never run**.

**Why it's gated:** the measured FIPS overlay throughput on the fleet link is
**~1.9 kbit/s** (189 ms srtt). If that number is real, bonding FIPS traffic is
premature regardless of transport work: one subflow already delivers ~1000× the
overlay's capacity, so no transport change can produce a user-visible benefit.
**But nobody has re-measured it, and PMTU (1203–1400) vs TCP-over-TCP collapse is
a plausible alternative explanation.**

Gap to close: cross-build FIPS for OpenWrt aarch64 (1–2 weeks, low technical risk,
high drift risk with a 23 MB Rust binary). Pointless until the 1.9 kbit/s figure
is explained.

## Q2: Do we use tunnels for NAT/egress between bonding client and server?

**YES — and this is the load-bearing piece of the whole architecture.**

What exists (measured on hardware, documented in this PR):
- Two per-uplink WireGuard tunnels (`mptcp-wan1`/`mptcp-wan2`), **distinct
  keypairs** (a shared key produces one identity across interfaces — handshake
  never completes), fwmark `0x3001`/`0x3002` + policy tables `301`/`302` (not
  `bind` — `wg set ... bind` does not exist on this wg), tunnel addresses
  registered as MPTCP endpoints.
- **Without reachable addresses, zero subflows form.** `signal` alone does NOT
  fix it: `AddAddrTx` climbs while `MPJoinSynRx` stays 0 — the peer cannot dial
  a CGNAT-ed address. The tunnel is what makes the advertised address reachable.
- Rendered as idempotent IaC: `TunnelPlan()` / `TunnelRoutingScript()` /
  `TunnelFirewallScript()` (fw4 acceptance is part of the IaC — without it the
  tunnels are silently REJECTED by `jump handle_reject`), plus
  `scripts/uplink-tunnels.sh` / `scripts/vps-tunnels.sh` as the executable form.

What does not:
- Nothing is **reconciled**: endpoints are set by hand, no reconnect logic, no
  MTU/PMTU clamp, no per-tunnel health checks, no fwmark allocator.
- No NAT-traversal *protocol* (no rendezvous/hole-punch) — unnecessary for a
  VPS server with a public IP, but relevant if the bonding server is ever
  itself behind NAT.
- The ~9% encapsulation overhead is invisible to the steering logic.

## Q3: Does TollGate pay the upstream bonding server for its services?

**NO — this is the commercially most interesting open gap.**

What exists:
- The ADR (merged #969, D1) declares **"the bonded uplink is a paid upstream
  peer"** — conceptually the bonding server is a seller of transit.
- Reseller mode already buys and resells upstream access today
  (`upstream_session_manager` + `upstream_detector`).
- `price_per_step` is in the upstream advertisement (D6), and `ShouldSteer` is
  the unwired seam for cost-aware steering (measured separately: `backup` moves
  the split 8.6%→100%; `tc` shaping makes it deterministic).

What does not:
- **No router has ever paid a bonding server.** The shipped Go bond's "payment"
  is a shared Shadowsocks password (`tg-mptcp-server`), not a metered Cashu
  settlement.
- No price, no meter, no payment path wired to the bond. The ADR is **Proposed**,
  not accepted — #927 merged the seam only.
- `tollgate-rs` has the peer model (`tollgate-net`, per-peer `MeterStream`,
  Spilman channel, multi-homed "pays both") but **contains zero MPTCP**.

Gap to close: point the Go router at a bonding server that advertises
`capability=bonding` + `price_per_step`, meter at the server, settle per link.
4–8 weeks, two-language integration, medium-high risk.

**Strategic note (consultant):** this is the revenue story, and it composes with
the honest framing — D5 is explicit that the bond buys **resilience, not
bandwidth** (median 12.9 < 17.7 single-path). Selling "faster" would be selling
something the data refutes; selling "resilient pay-as-you-go uplink" is what the
data supports.

## Q4: Do we do TollGate on top of FIPS?

**NO — design only.**

What exists:
- `tollgate-rs` design docs: `enforcer.kind: fips`, `peering-fips.md`, a
  `fips-exit` instance, budgets fed by FIPS per-peer livestreamed counters,
  `tollgate-suite-wrt` shipping `tollgate-fips-exit`.

What does not:
- **Never run end-to-end** — `tollgate-rs`'s own `llms.txt` says so in writing.
- No FIPS on any TollGate router. No paid FIPS transit measured. The enforcer
  path has no test.

Gap to close: FIPS + `tollgated` on one host, run `two_node` over `fips0`, then
one paid session. Days on x86, weeks on OpenWrt — and it inherits Q1's port work
**and** the untested enforcer. It is the roof, not the foundation.

---

## Ranking — value ÷ effort (consultant)

| # | Gap | Value | Effort | Ratio | Character |
|---|---|---|---|---|---|
| 1 | **Q2** tunnel hardening (reconciled, health, MTU) | 7 | 2 | **3.5** | the foundation works, make it survive |
| 2 | **Q3** TollGate pays the bond | 9 | 5 | **1.8** | the revenue story |
| 3 | **Q1** FIPS over the bond | 8 | 8 | **1.0** | the moat, gated on throughput |
| 4 | **Q4** TollGate on FIPS | 5 | 8 | **0.6** | the roof |

## The single most decision-relevant next experiment

**FIPS goodput ladder on two x86 Debian boxes on one LAN — one hour, no router
port needed:**

1. Unpatched `fips` v0.4.1, `iperf3` through `fips0`: 1 stream and 4 streams,
   MTU 1280 / 1400 / 1500.
2. Same pair, direct-IP baseline (no FIPS) on identical MTU.
3. Loopback-only pair on one host, to isolate crypto/processing cost from path.

**Decision rule:** LAN goodput within ~2× of baseline → the 1.9 kbit/s is
PMTU/TCP-over-TCP collapse, Q1 jumps to #1 and the MTU clamp is the fix. LAN
goodput also in the kbit/s range → processing-bound, Q1 and Q4 are parked
indefinitely, and Q3 (+ Q2 hardening) becomes the entire roadmap.

Nothing else in the lab buys that much certainty that cheaply.

---

## What this PR contains (measurement docs + tooling)

- `MPTCP-AGGREGATION-ACHIEVED.md` — two subflows on one token, the mechanism proof
- `MPTCP-AGGREGATION-MAGNITUDE.md` — per-tunnel byte deltas, 91/9 split,
  cross-checked against receiver bytes
- `MPTCP-CONTROLLED-COMPARISON.md` — interleaved A/B/C, 21 runs: bonded has the
  highest peak (24.3, 1.25×) and lowest median (12.9); the split predicts the
  throughput
- `MPTCP-FAILOVER-PROVEN.md` — mid-transfer failover: connection survives losing
  the dominant subflow, ~3 s stall
- `MPTCP-SPLIT-CONTROL.md` — complete split-control map: `tc tbf` makes the split
  deterministic (50/50 at 5 Mbit/s cap); `backup` re-steers live connections
- `MPTCP-COST-AWARE-STEERING.md` — `backup` moves share 8.6%→100%; price+penalty
  model design for `ShouldSteer`
- `MPTCP-WHY-NO-MULTIPLIER.md` — five measured causes ranked; what a closer VPS
  would and would not fix
- `MPTCP-SIGNAL-NOT-REQUIRED.md` — correction: reachability, not the flag, governs
- `MPTCP-OPERATOR-RUNBOOK.md` — rules that bite, now including rule 1b
  (reachability) and the client-side `mptcpize` trap
- `scripts/verify-bond.sh` — asserts on observable outcomes, not configuration
- `scripts/compare-multipath.sh` — interleaved harness with per-tunnel TX deltas
- `scripts/uplink-tunnels.sh` / `scripts/vps-tunnels.sh` — tunnel IaC, executable form
- `scripts/bench-cmd.py` — bench-lock-holding serial driver (fixes the unlocked-
  UART violation; refuses on lock-held AND on non-locking tty holders)

All measurement claims in these docs are backed by on-device observations; the
one retraction (plain-TCP masquerading as MPTCP) is documented rather than hidden.