- **IPv6 is disabled device-wide until captivity is v6-aware.** This is a
  user-visible behavior change, not only a captive fix: a router with a working
  IPv6 WAN loses it for *every* client (not just pre-auth ones) once this ships.
  Advanced operators can re-enable `network.lan.ipv6` / `network.wan.ipv6` /
  `wan6` deliberately, but doing so reopens the #783 pre-auth bypass until
  v6-aware captivity lands (post-0.6.0).
  ([#874](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/874))
