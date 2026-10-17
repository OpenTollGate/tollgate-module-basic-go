- **Multi-WAN bonding: the transport shape is now decided — the bond is
  proxy-terminated, and the client needs no routable address.** Exercising the
  merged seam on a physical router (MT6000, OpenWrt 24.10.2 / kernel 6.6.93)
  against a genuinely multipath peer showed that advertising the router's own
  uplink addresses cannot work behind NAT: both uplinks were private (house NAT
  and Starlink CGNAT), the peer never received an `ADD_ADDR`, and the second
  subflow never formed. MPTCP forms a second subflow by having the *peer connect
  back* to an advertised address, so the missing piece is peer-to-router
  reachability, which no router-side configuration supplies. D10 siting the
  de-aggregation server as the terminating end of a proxy removes the requirement
  entirely; D11 records that the per-client inbound is keyed by client identifier
  and must not assume a shared port. The per-uplink tunnel that was built to
  restore reachability is retained as a supported fallback. Claim discipline is
  unchanged: the measured ratio remains ≈1.2–1.3×, not 2×.

- **Corrected the `bondingshouldbefree` licence recorded in this decision.** The
  out-of-scope list called it GPL-2.0-only; the published installers are
  `SPDX-License-Identifier: AGPL-3.0-or-later`. Recorded as a dated correction
  rather than a silent edit, and the no-vendor conclusion is restated on
  structural grounds (a packaged feed client plus a server-side systemd
  component, with no library for Go code to import)
  ([#979](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/979)).
