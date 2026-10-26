# Buyer-chosen gateway objectives: a measurable multi-WAN contract — Architecture Decision

> **Status: Proposed (2026-10-26).** Nothing here is in force. It proposes a
> decision, and it explicitly supersedes one clause of
> [`multi-wan-bonding-decision.md`](multi-wan-bonding-decision.md) (D8's
> "mwan3 replacement" exclusion) for the narrow purpose described below. The
> drafting account may not accept its own proposal; no PR claims acceptance.

## Context

The bonded uplink landed as an uplink concern: `src/mptcp_bonding` replaces one
uplink with a kernel-MPTCP meta-connection and leaves portal, payment, session
accounting and merchant mode untouched. Measured on hardware, that bond is
worth **≈1.2–1.3× best-single** — resilience with modest aggregation, not a
multiplier.

That leaves a product question the code cannot answer today: a router may have
several upstreams of *different* cost and quality, and a buyer currently has no
way to say which trade-off they want. The router's own configuration has no
weight or preference concept anywhere (`mptcp_bonding` carries `policy:
guest_only|always` and nothing finer), and the provisioning repository now
supports a second engine, `mwan3`, whose distinguishing property is exactly the
one missing here: **per-flow weighted balancing**.

`mwan3` does not aggregate one connection across links, and it must not be
presented as if it does. What it gives is a controllable share of *connections*
per uplink — a policy a buyer can choose and an operator can sell.

## Decision

### D1 — The buyer chooses an OBJECTIVE, never weights

Three objectives, fixed in meaning:

| id | rule | honest consequence |
|---|---|---|
| `cost` | winner-takes-all: the eligible uplink with the lowest normalised sats/GiB carries every connection | cheapest per GiB, one link, no fallback |
| `speed` | winner-takes-all: the eligible uplink with the highest measured download rate carries every connection | best single-connection rate, highest price, no fallback |
| `balanced` | proportional across all eligible uplinks, weights ∝ measured Mbit/s ÷ sats/GiB | highest total capacity across many connections, survives one link dropping, mid price |

Weights are **derived from measurements at session start** and frozen for the
session. A buyer who typed weights would create one `mwan3` policy per distinct
weight set — unbounded router config — and could not know what the numbers mean.

**Rejected — capacity-weighted `speed` across all links.** A draft did this and
produced a "fastest" option with a *lower* per-connection rate than `balanced`,
because it gave the slower line a share. With per-flow balancing the aggregate
is roughly the sum of member rates, so spreading is the capacity/robustness
choice, not the speed choice. `cost` and `speed` are therefore single-link by
construction.

### D2 — Every number shown to a buyer is derived from a measurement, with a window

Uplinks carry a sampler: measured download/upload rate, RTT, loss, sample count,
over a configured window. A line enters the eligible set only if it is up, has
at least `min_samples` recent samples and its loss is within `max_loss_pct`.
Anything outside that set is excluded from every objective and is rendered to
the buyer as **not measured** — never defaulted, never estimated silently.

The buyer-visible quantities are computed from the same set: per-connection rate
(weighted mean of members, hard ceiling = fastest member), aggregate across many
connections (sum of members), latency (weighted mean, worst member in details),
and price (weighted cost for the purchased volume).

### D3 — Cost provenance: read it from an upstream TollGate, ask only for a plain line

A price must come from the thing being paid for.

- `source: tollgate` — the uplink is itself a TollGate upstream. Its price
  arrives with the upstream session (`price_per_step` with a per-GiB, per-MiB or
  per-minute unit). The router **normalises** it for comparison and stores no
  local copy. A locally-set cost on a `tollgate` uplink is a **config error**,
  because a hand-typed duplicate silently drifts from what is actually charged.
- `source: plain` — an ordinary ISP line with no protocol price. The operator
  enters `cost{value, unit}` once: `sats_gib`, `sats_mib`, `sats_min` or
  `flat_month`.
- Normalisation: `sats_mib → ×1024`; `sats_min → value × 8192/(60 × down_mbps)`
  sats/GiB (1 GiB = 1024 MiB, rates in Mbit/s). A per-minute line's cost per GiB
  therefore moves with its measured speed, and the UI says so.
- `flat_month` is **not comparable** per GiB without an expected-volume
  assumption. It is displayed as flat and excluded from the `cost` objective.
  Converting it silently would be inventing a number.

### D4 — The engine is exclusive and the mark space is allocated, not shared

`multi_wan.engine` is `mwan3` or `mptcp_bond`. Only one may be live; switching
requires the other to be torn down first. They must not both install policy
routing rules, because both own packet marks: the bond uses `fwmark` /
`fwmark_table` and TPROXY, while `mwan3` owns a mark space by default
(`mmx_mask 0x3F00`). `multi_wan.mark_base` is validated as disjoint from both,
and a config that overlaps is refused at load time rather than debugged on a
router.

This is the one clause that supersedes `multi-wan-bonding-decision.md` D8: that
record excluded "mwan3 replacement" as out of scope for the bonding programme.
This record does not replace the bond — it adds a second, mutually exclusive
engine for a product purpose the bond cannot serve (weights), and it keeps the
bond's own claim discipline.

### D5 — Price is derived, and the price you see is the mix you get

The buyer's price for a volume is the weighted sats/GiB of the frozen weights,
shown before payment. Because measurements drift, weights are recomputed only at
session start; a session never changes the mix it was sold. An operator may pin
an objective's weights (`weight_override`), but then the buyer must see a *fixed*
mix rather than a computed one — the distinction is rendered, not hidden.

### D6 — Claim discipline is unchanged and now has three named traps

1. Per-flow balancing does not make one connection faster than its link; the
   aggregate applies across many connections.
2. Nothing here promises a bandwidth multiplier; the bonding record's ≈1.2–1.3×
   measurement still governs any bonded-path claim.
3. A single-link objective has **no fallback**. If that link dies, the session
   ends. This is a product decision recorded here, not an implementation detail
   to be discovered in the field; operators who want continuity for a price
   should sell `balanced`.

## Consequences

- The router needs a sampler before any of this can be shown honestly. That is
  a prerequisite, not a follow-up.
- The config surface grows by one optional section, absent by default; a router
  that never opts in is byte-identical to today.
- `mwan3` policies become a product artefact: one per objective, bounded. An
  operator who edits weights by hand grows that set and must accept the
  consequence.
- The portal gains a picker and a status screen whose numbers are all derived;
  the operator gains one console. Neither may display a metric the router has
  not measured.
- Metering at the tunnel boundary remains an open question from the bonding
  record; none of this depends on it, and none of it claims per-link billing.

## Open questions this record does not close

- Whether a failed single-link objective should fail over to the next cheapest
  eligible line (recomputing weights mid-session) or end the session. Both are
  defensible; the first changes the price the buyer already paid.
- Whether `flat_month` should ever participate, via an operator-declared expected
  monthly volume.
- Whether the sampler's window and minimum sample count belong per uplink rather
  than globally.
- Whether a per-minute upstream's price should be shown as a range (its cost per
  GiB depends on the speed it happens to deliver) rather than a single number.
