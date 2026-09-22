# Management Subnet Selection — Deterministic by Default, Random on Collision

## Status: Decided (2026-09-22)

The management (`private`) network's /24 is **derived from the LAN when that is
safe and drawn at random when it is not**. Safe means: overlapping neither the
LAN's own network nor any network the router is attached to on the upstream
(WAN) side. The LAN-adjacent /24 stays the normal answer; randomness is the
escape hatch for the case the deterministic rule cannot see.

## The rule

```sh
candidate_prefix="$(octet_of "$candidate_prefix" 1).$(octet_of "$candidate_prefix" 2).${candidate_octet3}"   # x.y.(z+1)
private_prefix=$(choose_private_subnet "$candidate_prefix" \
    "${lan_ip}/$(netmask_to_prefixlen "$lan_mask")" \
    $(upstream_networks))
private_ip="${private_prefix}.1"
```

`upstream_networks()` reads **two** places, because an upstream address lives
in exactly one of them:

- `network.wan.ipaddr` (+ `netmask`) — static WAN configurations carry their
  address in UCI;
- `ip -4 -o addr show` — a DHCP, STA, PPP or FIPS/mesh uplink has no UCI
  address at all, so the kernel is the only source.

The LAN and the private bridge are excluded by name (`network.lan.device`,
`network.private.device`, `lo`), everything else counts as upstream-side.
`choose_private_subnet()` then keeps the candidate if it conflicts with
nothing, and otherwise draws `10.<r>.<r>.0/24` from `/dev/urandom` (BusyBox
`hexdump` idiom, same as the RSSID suffix), retrying up to 32 times, with a
walk of `10/8` as the pathological fallback and a logged warning if even that
finds nothing free.

## Why the deterministic rule is not enough

Ninety-nine routers in a hundred are fine with "the LAN's /24 plus one". The
hundredth is the one the operator cannot reach any more. A WAN side that is
itself a private LAN — a hotel, a site uplink, another TollGate's LAN — in the
same /24 the rule picked puts the management subnet and the uplink subnet in
the same network, and the private bridge's default route then points at an
address the router also owns: traffic for the upstream's client range is
routed back into the router instead of out of the WAN. The same class of
failure appears whenever the LAN's own mask is wider than `/24` — a
`192.168.0.1/16` LAN already contains the `192.168.1.0/24` the old rule
derived, so the check covers the LAN's real mask too, not a hardcoded /24.

Nothing in the tree detected either case. `IPAddressRandomized` in
`src/config_manager/config_manager_install.go` is only ever *logged*
(`src/main.go`) and never set or acted upon, and `DeriveIPv4` in
`src/identity/identity.go` (CGNAT `100.64/10`) exists without being wired into
runtime network setup — so a collision had to be found and fixed by hand on the
device.

## Why random rather than "the next free /24"

A deterministic scan (`10.0.0.0/24`, then `10.0.1.0/24`, …) would answer the
same subnet for every device on every network, which re-creates the same
collision class one octet later (an upstream in `10.0.0.0/24`, an operator's
own VPN route, a second TollGate in the same venue) and makes two colliding
routers indistinguishable in a scan. Drawing from `10/8` distributes the
choice, and the space is large enough that a collision with the router's own
upstream networks is improbable — and still checked.

Randomness is deliberately *secondary*: it only happens when the deterministic
candidate is unsafe, so the common, documented, operator-expected address is
unchanged.

## Consequences

- Fresh installs on a non-colliding upstream keep `x.y.(z+1).0/24` exactly as
  before; installations whose WAN side shares the candidate /24 (or whose LAN
  mask is wider than `/24`) now come up on a random, checked `10/8` /24.
- The pick is logged (`/tmp/tollgate-setup.log`, `Private subnet …/24 overlaps
  an upstream-side network; selecting a random non-overlapping /24`) so a
  field report can show why a router is not on the expected subnet.
- Everything downstream is unchanged: `network.private` stays a static
  `255.255.255.0` with the same DHCP pool, firewall zone and forwarding rules,
  so only the third-and-second octets differ.
- On a colliding router a re-run of the **full** setup draws again, so an
  in-place full re-setup can move the management subnet (previously it was
  stable because it was derived). Paired devices re-lease on the new subnet;
  SSID and PSK are preserved as before. Persisting the chosen prefix
  (a UCI option or the setup flag file) is the cheap follow-up if that ever
  bites.
- **Follow-up, out of scope here:** the Go service should own this decision
  instead of the shell (setting `IPAddressRandomized` and acting on it, and
  persisting the result) so that a management-subnet change is a service
  action rather than a side effect of re-running first-boot setup. This
  decision only fixes the address the bridge is configured with.
- `tests/uci-defaults-private-subnet_test.sh` (offline: fake `uci`, fake `ip`,
  fake `hexdump`, no network) pins the behaviour: the deterministic candidate
  is kept when nothing collides, a colliding DHCP or static upstream forces a
  random non-overlapping /24, a colliding draw is retried, a wider-than-/24
  LAN mask triggers the same fallback, the `.255` third octet still steps
  down, two runs over the same collision pick different subnets, and a router
  with no upstream information at all still yields the deterministic answer.
