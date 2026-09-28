# Discovery signaling — how an AP says "connect to me to verify" (decision + option record)

## Status: Decided (2026-09-28) — v0.6 ships SSID-prefix recognition; everything else documented and deferred

Two questions were separated during the #618 design review, and this record
keeps them separate:

1. **Verification** — "is this candidate actually a TollGate?" Answered by
   capability: a validly-signed kind-10021 advertisement served on `:2121`
   (pre-auth-reachable per the [nodogsplash port 2121
   decision](nodogsplash-port-2121-decision.md); the router-to-router probe
   already exists as `probeTollGateGateway`). Signed by the responder, so a
   cloned beacon cannot spoof it.
2. **Discovery signaling** — "who do we even ask?" An AP needs a cheap,
   pre-association way to indicate *"please connect to me to verify that I
   am a TollGate."*

**The v0.6 answer to (2) is the SSID prefix** (`TollGate-` / `Net4sats-`,
case-insensitive — #618). It is the Tier-3 option below: zero client
requirements, zero new dependencies, spoofable-but-harmless because it only
selects candidates for the signed probe. Everything richer is deferred past
v0.6 and recorded here so nobody re-researches it.

---

## The option space (researched 2026-09-28)

Three tiers, each with prior art. All of them are *signals*: none of them
authenticate anything — authentication stays with the signed advertisement.

### Tier 1 — 802.11u / Hotspot 2.0 (Passpoint): the standards-track answer

The industry's solution to exactly this problem, in two layers:

- **Beacon tier:** Interworking element (network type, "internet" bit) plus a
  Roaming Consortium element carrying up to three organization identifiers
  (OIs) — registry-assigned 3-byte IDs meaning "this AP participates in
  network X". The standardized version of an SSID prefix.
- **Query tier (ANQP over GAS):** any client can query the AP pre-association
  — before an IP address exists — for venue, operator friendly name,
  **domain name**, NAI realms, WAN metrics, connection capabilities. Android
  and iOS implement this natively (Passpoint is in AOSP; the supplicant
  provides GAS/ANQP), and `wpa_cli anqp_get` exists on the STA side.

Why it is attractive for us: ANQP's **Domain Name element is
DNS-namespaced** — a TollGate AP could answer `tollgate.me` + operator name
+ WAN metrics with **no registry, no custom app, no OUI assignment**, and a
stock phone's WiFi picker surfaces that class of network without the user
installing anything. A reseller router could use `anqp_get` instead of
custom `iw scan -u` parsing, sidestepping the tiny-`iw` problem entirely.

Costs: hostapd interworking configuration per-AP; an OS-level story only as
good as each platform's Passpoint surface; another moving part to test.
Deferred.

### Tier 2 — vendor-specific IE ("beacon stuffing"): the semi-standard answer

The research lineage (Chandra et al., *Beacon-Stuffing*, HotMobile 2007 →
Zehl et al., *LoWS*, 2016 → current fog/IoT work) confirms the shape we
already have in `vendor_element_manager.go`, with two useful refinements:

- The 255-byte limit is **per element, not per beacon**: multiple
  vendor-specific IEs are legal up to a ~2320-byte beacon frame, and
  fragmentation across successive beacons is an established technique. (The
  encoder's per-element overflow policy — truncate the mint TLV, report it —
  is #620; multi-IE emission would be its successor if this tier is ever
  built.)
- Every paper hits the same wall: **the client side needs driver or app
  changes** (LoWS shipped modified Android builds), and SSID-stuffing
  variants flood the WiFi picker with bogus entries.

Net: viable for router-to-router where we control both ends; a dead end for
stock phones. Its prerequisites remain as listed in the #618 review:
production emitter, a raw-IE scan source, hotplug re-apply of the
runtime-only ubus state, a registered OUI (`212121` is unassigned), and the
`VendorIEDiscovery` flag actually being read.

### Tier 3 — structured SSID: the pragmatic answer (what v0.6 ships)

Formalized in the literature (Di Sorte et al. 2007) and commercialized by
Boingo, whose client tool recognized partner APs **from the SSID** using an
operator dictionary. Zero client requirements, 32-byte budget, spoofable,
no security — acceptable exactly and only because it selects candidates for
the signed probe rather than authenticating them. `TollGate-<code>` /
`Net4sats-<code>` with the brand-prefix recognition set (`hasTollGateSSID`)
is this tier, done deliberately.

### Rejected outright (recorded with evidence so they stay closed)

- **BSSID signaling** — 48 bits cannot carry a key; truncation destroys
  binding; the kernel owns interface addresses; measured on the bench
  2026-09-28: a BSSID cannot be changed while the AP runs, so a price change
  would tear down the AP and drop every associated customer; duplicate
  BSSIDs violate 802.11 uniqueness and present the evil-twin signature;
  `identity.DeriveMAC` would make one sighting reveal an operator's fleet.
- **A beaconed npub** — pre-auth clients have no uplink, beacons are
  replayable, and a permanent broadcast of a money-linked identity is a
  wardriving archive of operator keys. If a discovery key is ever needed it
  must be separate, rotatable, and non-financial.

Post-association broadcast (mDNS/DNS-SD) is not in the option space: it
cannot answer a pre-association question.

---

## When this reopens

Signals to revisit this record: (a) customers need pre-association *price*
in the WiFi picker (pulls toward Tier 1/ANQP), (b) reseller discovery needs
to survive SSID renames (ANQP domain name or the vendor IE), (c) a
registered OUI/OI is obtained. Until then: SSID prefix selects, the signed
advertisement on `:2121` verifies.
