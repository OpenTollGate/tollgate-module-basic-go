# Admin LAN Subnet Selection — Detect Collision With the Upstream, Then Let the Operator Decide

## Status: Proposed (2026-09-28)

The management (`private`) network already dodges subnet collisions — it is derived
from the LAN when that is safe and drawn at random when it is not, against every
network the router is attached to on the upstream side (see
[`private-subnet-collision-avoidance-decision.md`](private-subnet-collision-avoidance-decision.md)).

The **admin LAN is not covered by that rule.** `network.lan` is `192.168.1.1/24` by
convention, is not compared against the upstream, and is not operator-configurable.
When the operator's own network is also `192.168.1.0/24`, the box's LAN subnet *is*
its upstream subnet, and the box goes dark from the operator's point of view — the
same failure the private network documents, now on the surface the operator needs to
reach to fix anything.

This is a **proposal**, not a decision. It describes the problem, the options, and
what would have to be true before it ships.

## The failure mode

A box installed into a network whose gateway is `192.168.1.1` — an extremely common
home default — ends up with:

- **the admin surface unreachable or ambiguous**: both the operator's upstream router
  and the box answer `192.168.1.1`; which one a given client reaches depends on
  topology nobody inside the box can see;
- **upstream traffic routed back inside**: an address in the upstream subnet is also a
  connected route on the box's LAN bridge, so traffic destined upstream can be handed
  to the wrong side;
- **a support burden that looks like a brick**: from the operator's side the box is
  simply gone, and the fix (change the LAN) is reachable only through the surface that
  is gone.

Who it hurts: an operator (or review-club member) whose home/office LAN is
`192.168.1.0/24`, i.e. a large fraction of first installations. It is invisible in the
lab when the bench upstream is `10.x` or `192.168.2.x`.

## Why the existing private-network logic does not cover it

`choose_private_subnet()` consults `upstream_networks()`, which reads both
`network.wan.ipaddr` (+ netmask, for static uplinks) and `ip -4 -o addr show`
(everything that is not `lo`, the LAN device or the private bridge — so DHCP, STA,
PPP and FIPS/mesh uplinks are all covered). That machinery is exactly what the LAN
check needs, and it is already reviewed and tested **for the private bridge**.

What it does *not* do is apply any of it to `network.lan`. The LAN address is read
and used (`lan_ip=$(uci -q get network.lan.ipaddr … || echo 192.168.1.1)`) but never
validated against the upstream, and the private-network code derives its own candidate
*from* the LAN — so a colliding LAN is inherited rather than questioned.

## What changes if the LAN subnet moves

Not free — this is why the change needs its own decision rather than riding along:

- **TLS certificate SANs.** The admin surface serves a certificate for the LAN
  address (`tollgate ssl covers` exists precisely because this is brittle); a moved
  LAN changes what the cert must cover.
- **Captive-portal and admin URLs.** Portal pages, redirects and documentation
  (`curl http://192.168.1.1:8080/` appears in user-facing prose) name the LAN address.
- **DHCP scope and lease churn.** Moving the bridge re-leases every client on it.
- **Operator expectation.** `192.168.1.1` is the address people type. Silently moving
  it is a surprise; moving it because it *collides* is a fix, but it must be legible.
- **Existing boxes.** A box already deployed on a non-colliding LAN must keep its
  address; a box whose LAN was deliberately customised must be left alone.

## Options

| option | summary | trade-off |
|---|---|---|
| **A** Document only | ship a support note: "if your network is 192.168.1.0/24, change the LAN" | cheapest; leaves the failure in place and unaddressed |
| **B** Detect at setup, pick a non-colliding `/24` | extend the private rule to the LAN, keeping `192.168.1.1` when safe | smallest diff that fixes the common case; blind at first boot if the upstream is not yet known |
| **C** Operator-configurable LAN | LAN address as a setting, on **both** the config file and the admin UI | required by the standing dual-surface rule; the UI that changes the LAN is served over the LAN, so it needs an explicit confirm + reconnect path |
| **D** Runtime detect-and-warn | after the upstream associates, re-check and surface a clear warning | necessary because the upstream may only be known later; warning only — no automatic re-addressing |

## Proposed rule (B + D-warn, with C as the override)

1. At setup, check the LAN address against `upstream_networks()` using the existing
   `subnet_conflicts()` / `netmask_to_prefixlen()` helpers. Keep `192.168.1.1` when it
   is safe; move to a non-overlapping `/24` **only** when it collides.
2. After the uplink associates, re-check and, if the LAN now collides, **warn**
   (admin surface + log) rather than move anything by itself.
3. Expose the LAN address as a dual-surface setting (config file **and** admin UI), so
   the human has the last word and an intentional customisation is preserved.

Detection, then explicit override — never a silent surprise re-addressing.

## Required evidence before implementation

- a fresh install with the upstream on `192.168.1.0/24` → box reaches the admin
  surface, and the LAN address chosen does not overlap the upstream;
- a fresh install with a non-colliding upstream → LAN stays `192.168.1.1` (no
  gratuitous change);
- an upgrade of an already-deployed box → address unchanged, no re-lease storm;
- an uplink that only appears *after* setup and collides → warning surfaced, nothing
  moved;
- the configured-address path set from the CLI **and** from the admin UI;
- certificate/redirect behaviour on a moved LAN (does `tollgate ssl covers` still hold?).

## Rollback

The rule keeps `192.168.1.1` in the safe case, so the common installation is
unchanged. The failure path is a box that moved its LAN and should not have: an
operator-set address wins over detection, and clearing it returns the box to the
deterministic rule.

## Open questions

- Should a moved LAN be **reported** in the admin surface as a one-time notice, or
  only in the log? (An unexplained different address is confusing; a notice is state.)
- Does the certificate need to cover both the old and new address during transition,
  or is regeneration on the next setup pass acceptable?
- Does anything downstream (portal bundle, nsite boards, docs) hard-code
  `192.168.1.1` in a way that would need the same treatment?

## Related

- [`private-subnet-collision-avoidance-decision.md`](private-subnet-collision-avoidance-decision.md)
  — the rule this proposal extends, and the source of `upstream_networks()` /
  `subnet_conflicts()`.
- [`lan-port-management-bridge-decision.md`](lan-port-management-bridge-decision.md) —
  which ports are on the LAN bridge.
- [`luci-https-pre-auth-reachability-decision.md`](luci-https-pre-auth-reachability-decision.md)
  — how the admin surface is reached before payment.
