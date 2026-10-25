# Multi-WAN Bonding: a bonded uplink as a paid upstream peer — Architecture Decision

> **Status: Proposed (2026-10-17).** Nothing in this record is in force yet: the
> tag vocabulary, the metering boundary and the enforcement model below are the
> contract that three repository changes implement. Acceptance is a maintainer
> action — the drafting account may not accept its own proposal, and no PR here
> claims it has been accepted.
>
> **Amendment 1 (2026-10-17): the transport shape — proxy-terminated, not
> advertised-address.** See §A1. It adds D10 and corrects one factual error in
> D8. The original text is preserved unmodified.

## Context

The kernel-MPTCP multi-WAN bond landed as an *uplink* concern: `src/mptcp_bonding`
replaces one uplink with a kernel-MPTCP meta-connection spread over several
uplinks, reassembled by a **remote de-aggregation server**. Portal, Cashu
payment, session accounting and merchant mode are untouched by that work.

That leaves the commercial question this record answers. Today the remote
de-aggregation service is a **credential relationship**: the operator configures
a server in `…/tg-mptcp-server` and the router is handed a Shadowsocks password.
There is no price, no meter, and no way for a router that finds a bonding server
to pay it. Every bonding server therefore needs an out-of-band account
relationship — which is the thing a permissionless, pay-as-you-go protocol exists
to avoid.

The design constraint that decides the shape: a bonding server is reached over
the **public internet** by a router that is behind NAT. It has no L2 segment and
no client MAC. The reference implementation's terminal-gateway path keys a
session on the MAC, meters with `ndsctl`, and refuses an unresolvable identity
*before* it reads the payment token (`src/main.go` `device-unresolved`;
`src/merchant/merchant.go` `ValidateMACAddress`). That path is structurally
unavailable to a remote server, and `/whoami` is an unauthenticated echo, not a
proof. So the bonding server cannot be a terminal gateway.

**It does not need to be.** The protocol already has the role this requires, and
it is called *multi-hop*: a TollGate buys connectivity from another TollGate. The
router is already a reseller — `src/upstream_session_manager` buys upstream access
and resells it to guests. The bonding server is that upstream. This record adopts
that reading and pins the vocabulary, boundary and enforcement that make it true.

## Decision

### D1 — The peer model: the bonded uplink is a TollGate upstream peer

The far end of the bond is a remote de-aggregation service that is **itself a
TollGate peer**: discovered by advertisement, paid through the existing reseller
path, metered at the server.

Consequences to hold:

- Portal, Cashu payment, session accounting and merchant mode are **untouched**.
  Only the uplink changes.
- The bond is a *replacement for one uplink*, not a second account.
- All subflows terminate at **one** remote host, so this buys **resilience and
  session continuity — not a bandwidth multiplier** (see D5's claim discipline).
- **Rejected:** introducing "bonding server" as a new service category distinct
  from a TollGate. It would need a new advert kind and a second identity and
  payment path for no gain, and it would fork the thing this protocol exists to
  make uniform.

### D2 — The behavioural reference is `tollgate-rs`; the code is Go

`tollgate-rs`'s `tollgate-net` is the **behavioural reference** for the upstream
peer: the budget model (`units_to_payer` / `units_from_payer`, cumulative
counters, `consumed` vs `authorized`, a reserved rate, the never-block-payment
invariant) is spec-grade prose, and it ships with a normative CDDL. It is
MIT-licensed, so reading and re-expressing it is unencumbered.

Three things must be recorded plainly:

- **`tollgate-rs` contains no MPTCP content at all** (`search/code` for `mptcp`
  in that repo returns `0`). It is therefore *not* the reference for the bonding
  mechanism — the mechanism reference is the kernel and OpenWrt work already
  recorded in this repo's own `docs/MPTCP-*.md`.
- The **code** is Go because `tollgate-module-basic-go` is the production
  reference router, already owns the relevant seams (`valve` metering, the
  reseller path, `config_manager` with schema drift tests, `upstream_detector`
  interface events), and is the operator-fixed destination.
- **Nothing in this programme opens a pull request on `tollgate-rs`.** It is read,
  never written. A half-baked PR there is exactly the spamming this destination
  choice exists to avoid.

### D3 — The metering boundary: the server-side tunnel boundary

The billable number is the bytes the **de-aggregation service actually carried
for that client at its tunnel-side boundary** — not the router's per-WAN
counters, and not the server's internet-side counters.

- Per-path metering can bill the same byte twice: MPTCP loss recovery
  retransmits on a subflow, and a retransmit that crosses the metering boundary
  is counted again. Metering at the tunnel boundary, summing both directions,
  removes the *direction and aggregation* artifacts by construction. It does
  **not** by itself remove retransmission artifacts, and this record does not
  claim it does — that is an open measurement (see "Open questions").
- Substrate: an **`nft` named `counter` per client per direction is the billable
  number**, a **per-client `nft` `quota` is the hard budget enforced in the
  datapath**, and **one `tc HTB` class per client is the rate floor**. This is the
  same shape `tollgate-rs`'s `ip` enforcer uses (nftables + `tc`), which is why
  D2's reference is useful here.
- **Topology trap, recorded verbatim because it is silent:** the Shadowsocks
  server is **not** a netfilter forwarder. It terminates the proxy and opens its
  own sockets behind `POSTROUTING MASQUERADE`, so the client leg is counted in
  `LOCAL_IN`/`LOCAL_OUT` and a `forward` rule sees nothing of it. The downlink must
  be keyed on `ip daddr <client> tcp sport <proxy-port>`, and a "simplified"
  forward/egress rule silently switches the meter to internet-side bytes — making
  the customer pay for the server's own retransmissions.
- The proxy's own per-port counter is **not billable**: it samples every 5 s, it
  cannot be reset per session, and it counts ciphertext rather than plaintext. It
  is a **cross-check oracle only**.
- **Enforcement must be default-deny.** A metered chain in an accept-policy
  configuration does not gate anything: the quota expression signals over-quota
  by restoring the continue verdict and falling through to the *next rule*, so an
  accept-policy metered chain delivers over-quota traffic. Every metered chain is
  `policy drop` with an explicit terminal `drop` and an allow-list ahead of it.
  A chain in the opposite shape is a free tunnel, and it looks perfectly healthy.

### D4 — The identity model for a remote peer

Two identity conventions already exist in the protocol and must not be blended by
accident:

- **Nostr pubkey identity** — the kind-10021 advertisement's `pubkey` *is* the
  TollGate identity, and a kind-1022 session names the customer via the `p` tag.
- **device-identifier / IP identity** — the reseller path keys an upstream session
  on `GatewayIP` and carries no pubkey plumbing.

**Decision:** the remote peer's identity is the **advertisement signing pubkey**,
pinned by the operator. The `host:port` carried in the advertisement is the
*transport address* and may change. Rationale: an IP-keyed paid session cannot be
authenticated, and it silently re-homes when a server moves; a pubkey is already
the protocol's identity primitive and it makes the server's kind-1022 session and
kind-21023 notice events addressable.

Two consequences that must be recorded rather than discovered:

- This is a **real change**, not a restatement: the router code does not do this
  today (the reseller path is IP-keyed). This record says which side ships it.
- The server therefore needs a Nostr keypair, which is **a secret on the server**
  and is a secret-scanner concern: this repo's `.secret-patterns.txt` actively
  scans for `nsec1…`.

**Scope note:** the pubkey plumbing is **in scope for this record and out of
scope for the first code PR**; the first code PR ships the capability tag read and
the config contract (D6, D7) with the transport address, and the pubkey pinning
follows. Stated here so the split is a decision, not an omission.

### D5 — The enforcement and rate model

- Server side: an `authorized` budget against a cumulative `consumed`, with a
  `tc` floor at budget zero — i.e. the `tollgate-rs` shape expressed as
  `nft`-quota plus a `tc` class.
- **The never-block invariant carries over verbatim:** protocol and payment
  traffic for the paying peer must never be blocked or shaped, or the payment
  that restores delivery is cut off. For this repo that means the payment surface
  stays reachable while the data path is gated. This is also a live constraint on
  the *router* side: `:2121` is deliberately reachable unauthenticated while a
  guest is gated, so mid-flow exhaustion does not lock a customer out of topping
  up.
- Router side in this stage: **the router only steers.** Enforcement of guest
  access stays exactly where it is (`valve`/ndsctl gating forwarded guest
  traffic). **No enforcement is added or moved by this programme.**
- **Two refusal semantics, both fail-closed:** an advertisement lacking the
  bonding capability tag is treated as **not a bonding server** — never as
  "bonding with defaults" — and an unknown or absent capability never enables
  steering.
- **Claim discipline, recorded as a rule:** MPTCP's path manager prefers the
  lowest-RTT subflow and OpenWrt ships no BPF goodput maximiser, so the measured
  best-bonded ÷ best-single ratio is **≈1.2–1.3×**, and some runs were *slower*
  than the better single path. Every user-facing claim must be worded as
  **resilient multi-path, not "2×"**. Mid-transfer failover is **untested**, not
  disproven, and must not be claimed.

### D6 — The tag vocabulary and its owner

- **`kind 10021` is reused. No new event kind.** The parser switches on tag name
  over a fixed set and has no default branch, so an unknown tag is ignored — an
  added tag is forward- *and* backward-compatible for existing readers.
- The added tags are **extension tags on an existing kind**, published by the
  server and read by the router. The strings this programme proposes, to be
  recorded concretely rather than left to implementation:
  `capability=bonding`, `bonding=mptcp`, `endpoint=<host:port>`,
  `accounting=tunnel-bytes`.
  These are **this programme's proposal**, not an agreed specification.
- **A bonding server's advertisement must still be a valid ordinary
  advertisement** — the parser rejects it without `metric`, a non-zero
  `step_size`, and at least one valid `price_per_step`. That constraint is a
  feature: it means the bonding server *is* a TollGate and needs no special parse
  path.
- **`tips` is explicitly rejected as the activation gate.** It is parsed and
  stored but has **no consumer in this codebase**, so gating on it would be a
  claim about machinery with no reader. The router must read the capability tag.
- **The owner is this repository.** One-way arrow: this record plus exported
  constants beside the existing parser → the server's advertisement → the E2E
  assertions. Nothing upstream of this repo is on the critical path, and no
  specification PR gates the programme (see D8).

### D7 — The config contract, migration policy, and the version claim

- The `mptcp_bonding` section is **opt-in and disabled by default**, so a router
  that never opts in behaves exactly as before.
- **Migration: additive-only, decided explicitly.** No migration branch is added,
  because every field's zero value is a valid "feature off" state and
  `omitempty` keeps an existing config byte-stable. This is a decision the code
  does not currently settle — `migrateConfig` has no `MPTCPBonding` branch today
  — and recording it here is what stops the gap being read as an oversight.
  **Rejected:** the "detect the missing section by a field no operator would set
  to zero" pattern (the repo's precedent for that is
  `UpstreamWifi.ScanIntervalSeconds == 0`). It is rejected here because for this
  section the zero value *is* meaningful and already correct.
- **Version claim:** the shipped `ConfigVersion` must agree with the
  docs-versus-code contract. `check-docs-facts.py` binds the README's stated
  schema version to the `config_schema.go` default, so the two move together or
  the doc-facts gate fails. Whatever number ships, the README and the schema
  default ship with it in the same change.
- **Also true today and recorded so it is not mistaken for a new defect:**
  `CONTRIBUTING.md` states a module count for `make go-battery` while the tree
  carries more `go.mod` files than that count. `tests/contract/check-docs-facts.py`
  enforces the claim literally. A change that adds a module must update the claim
  in the same change.

### D8 — Out of scope

- Any change to the specification repository (`OpenTollGate/tollgate`). The tag
  set is proposed as a **named follow-up**, to be sequenced with maintainers; note
  that **TIP-03 is already claimed** by an open draft, so a new number needs a
  maintainer conversation. It does not gate anything here.
- Any pull request to `OpenTollGate/tollgate-rs` (MIT reference only; `0` MPTCP
  references).
- Any pull request to the dormant parent of the server repo.
- Payment or accounting for a foreign bonding server beyond what the server
  repository itself implements; the merged bonding seam deliberately carries none.
- Router-side enforcement of the bond (steering only, this stage).
- MPTCP over FIPS — recorded elsewhere as a bounded internal spike, not a stage.
- Vendoring any `bondingshouldbefree` component. **The licence recorded here
  originally said "GPL-2.0-only"; that is corrected in A1.4** — the published
  installers are **AGPL-3.0-or-later**, and the no-vendor posture stands for the
  structural reason given there, not the licence.

  *(text above preserved as originally drafted; see A1.4 for the correction)*
- "2× bandwidth" and mid-transfer-failover claims (measured ≈1.2–1.3×; failover
  untested).
- A BPF MPTCP scheduler or goodput maximisation (needs kernel ≥5.19 plus BPF, not
  in OpenWrt).
- Multi-radio hardware provisioning, mwan3 replacement, and roaming/`ADD_ADDR`
  redesign.

### D9 — Rollout sequence

Each step is independently reviewable, and the sequence is fixed by one
dependency: this record carries the tag vocabulary as **text**, so the other two
repositories need it *open*, not merged.

1. **This record** plus its changelog fragment (this PR). No code.
2. **This repository** — the payment feature: the capability tag read (fail-closed),
   the config contract, and the tests.
3. **The server repository** — advertisement publication with the tag strings, and
   the metering and enforcement roles.
4. **The test-automation repository** — end-to-end assertions, feature-detected so
   they skip cleanly when the feature is absent.
5. **Follow-up, not gating** — a specification change extending the advertisement
   with the tag set, at a number agreed with maintainers.

## Open questions this record does not close

Recorded so that a green result elsewhere is not mistaken for these being settled.

- **Whether one reassembled MPTCP connection presents one conntrack tuple at the
  tunnel side.** The lab shows **two** 5-tuples at the peer for a two-subflow
  connection while the MPTCP-level socket reports one. The chosen rule key matches
  all of them, which makes the design *accidentally* correct — and therefore a
  property that must be tested, not assumed. A regression test over
  N ∈ {1,2,4} subflows is required before enforcement is enabled.
- **The magnitude of retransmission over-count.** Metering at the boundary is
  immune to direction and aggregation artifacts, and bounded by the retransmit
  fraction for the rest. That fraction has not been measured, and it must be,
  before a price is published.
- **Per-key dynamic quota maps are unavailable on stock `nft`.** Creating or
  updating a map element returns `EOPNOTSUPP`, so every client's objects must be
  pre-provisioned. If provisioning silently no-ops, the meter bills nobody and
  nothing reports an error.
- **Whether the target kernel reproduces the local lab's expression matrix.**
  Every such result was taken on a different kernel and is unverified on the
  deployment target.
- **State durability.** Table counters and quota state do not survive reboot or a
  flush while the wallet balance does, so usage accounting must be journaled
  before any kernel mutation and reconciled on startup. Until that is proven, the
  system runs **advisory-only**: metered and shown, not enforced.

## Consequences

- A bonding server becomes payable with the protocol's existing payment path and
  **needs no accounts and no subscriptions** — which is the point of the change.
- The bond remains a **resilience** feature. Any material presented to a user as a
  bandwidth multiplier is a violation of D5's claim discipline.
- The server repository becomes the owner of a **meter**, which is new
  correctness-critical surface there: it must be default-deny, journal before it
  mutates, and fail closed on an unprovisioned client.
- Three repositories now share one vocabulary owned by this one. The record is the
  contract; a change to the strings is a change to this record first.

---

## Amendment 1 (2026-10-17) — the transport shape

**Status of this section:** it *proposes a new decision* (D10) and *corrects a
factual error* in D8. It does not clarify an existing one. Recorded separately
because D1–D9 were drafted without the hardware result below; they are preserved
as written.

The bonding seam merged in this repository advertises the router's **own uplink
addresses** to its peer, and relies on the peer's MPTCP path manager connecting
back to them. That mechanism was then exercised on a physical router
(GL-MT6000, OpenWrt 24.10.2 / kernel 6.6.93) against a genuinely multipath peer.
It does not work behind NAT, and the reason is structural rather than a bug.

### A1.1 — The measured failure: access to the advertising router

MPTCP forms a second subflow by having the **peer connect back** to an address the
client **advertised** via `ADD_ADDR`. Both uplink addresses on the test router are
private — one behind house NAT (`192.168.2.x`), one behind Starlink CGNAT
(`10.2.1.x`) — and **neither is reachable from the peer**. Measured from the peer:

- `MPTcpExtAddAddrTx` never advances across completed transfers on the router.
- The peer's `MPTcpExtAddAddr` stays **0**: it has never received one.
- `MPTcpExtMPJoinNoTokenFound` climbs (JOIN attempts with no usable token).
- The satellite subflow sits at ~1.7 KB — handshake only — indefinitely.

So the peer never learns an address it can dial, and the second subflow never
forms. **No amount of router-side configuration fixes this**, because the missing
piece is peer-to-router reachability, which a NAT-ed or CGNAT-ed uplink cannot
provide.

### A1.2 — D10: the bond is **proxy-terminated**, not advertised-address

**Decision.** The de-aggregation server is the **terminating end of a proxy**, not
a host that dials back into the client. The client initiates every path; the
server never needs to reach the client at all.

Consequences to hold:

- **The client needs no routable address.** This is the whole point: it makes the
  bond work behind CGNAT, carrier NAT and a plain consumer router, with no IPv6
  prefix, no port-forward and no ISP cooperation. Under D1 the router is already
  buying from an upstream peer it dials — this makes that explicit in the
  transport.
- **`ADD_ADDR` becomes informative, not load-bearing.** Address advertisement may
  still occur; the bond must not *depend* on the peer acting on it.
- **`endpoint=<host:port>` in D6 is the only address the client needs.** One
  client-initiated address replaces N advertised ones. This is consistent with
  D6's tag already carrying a single `endpoint`.
- **One client identity, one port, one meter.** A proxy termination point per
  client is also the natural key for D3's per-client counter and quota — the
  metering boundary and the transport boundary become the same boundary, which
  removes a class of mis-attribution rather than adding one.
- **The multi-hop reading in D1 is unchanged.** The peer is still a TollGate that
  is paid; only the transport direction is pinned.

**Rejected — "advertise a reachable address instead":** requiring per-uplink
reachability (a public address, a delegated IPv6 prefix, or a NAT port-forward per
uplink) is exactly the operator cooperation the protocol exists to avoid, and it
cannot be satisfied at all behind CGNAT. A per-uplink tunnel to a public endpoint
was implemented and does restore the reachability the path manager wants
(measured: both tunnels handshake, both tunnel addresses answer the peer at 0%
packet loss, and a subflow does establish — `SF_ESTABLISHED` with a tunnel source
address). It is **retained as a supported fallback**, not the primary shape,
because it still requires the client to be *addressable on the advertised
address*, and it adds one tunnel and one address per uplink to the config
surface for no metering benefit.

**Claim discipline is unaffected:** this section changes the transport, not the
ceiling. The measured ratio remains **≈1.2–1.3×**, and the honest reading of the
measurement is unchanged — to a genuine multipath peer, three runs per
configuration gave within-configuration spread *larger* than the
between-configuration difference, and the aggregate runs were not the fastest
(StarGate-only 8.68/8.78/6.19; wired-only 2.48/0.83/5.78; both 3.72/–/3.83 MB per
10 s). D5's wording rule still governs every user-facing number.

### A1.3 — D11: the server terminates one inbound per client, and must not require a shared port

Siting the de-aggregation proxy on a host that already terminates other services
must not collide with them. The reference stack this shape is modelled on
publishes **one inbound per client, keyed by a client identifier, on a
client-specific port**, so a server can co-exist with whatever else owns the
conventional web ports.

**Decision.** The per-client inbound is addressed by the client identifier carried
in D6's `endpoint`, and the server must not require a single shared port across
clients. On a host already terminating TLS on the standard port, the shared-port
form is a collision rather than a simplification.

**Consequence to record:** the client's `server_port` is therefore
**per-client configuration**, not a fleet constant, and it must be published in
the advertisement alongside `endpoint` — otherwise a client cannot construct the
address it is being told to dial.

### A1.4 — Correction: the `bondingshouldbefree` licence in D8 is wrong

D8 states the `bondingshouldbefree` stack is **GPL-2.0-only**. That is incorrect.
Fetched from the three published installers, the actual identifier is:

```
#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2025-2026 Chester A. Unal <chester.a.unal@arinc9.com>
```

— **AGPL-3.0-or-later** on `srv.`, `cld.` and `owrt.bondingshouldbefree.org`
alike. Corrected here rather than silently edited, because a licence claim is what
a reviewer relies on and an unrecorded change to it is worse than a wrong one.

**The no-vendor conclusion stands, for a structural reason instead of a licence
one.** That stack is a packaged client (`bsbf-bonding` from the OpenWrt feed) plus
a server-side systemd component, driven by install scripts. There is no library to
vendor and nothing for this repository's Go code to import; invocations are
process-level. Amending D8's bullet to the AGPL identifier plus that reasoning is a
follow-up edit to this record, not a licence re-evaluation.

Two further facts about that stack, recorded because they will save a repeat
investigation:

- **It is proxy-terminated in the same shape as A1.2** (Xray at both ends, with
  the TCP-MPTCP socket option enabled on the inbound), which is corroborating
  evidence that the shape above is the one that works behind NAT. It is cited as
  *prior art for the shape*, not as a dependency — and note D8 forbids vendoring
  it regardless.
- **It selects endpoints from default routes that carry a metric.** With no
  metriced default route it flushes all endpoints and does nothing, so any lab
  comparison against it needs multiple metriced default routes or it will appear
  to "drop" an interface that is up.

### A1.5 — What this amendment does not change

- D2 (`tollgate-rs` as behavioural reference; no PR there), D4 (pubkey identity),
  D5 (enforcement, never-block, claim discipline), D6 (tag vocabulary — the
  existing `endpoint` tag already suffices for A1.2), D7 (config contract) and D9
  (rollout sequence) are unchanged.
- A1.2 does not add a rollout step. It constrains step 3's shape (the server
  repository), which is already the step that owns the de-aggregation endpoint.
- The router-side enforcement boundary in D5 is untouched: the router still only
  steers.
- D8's prohibition on vendoring `bondingshouldbefree` is unchanged (A1.4).
