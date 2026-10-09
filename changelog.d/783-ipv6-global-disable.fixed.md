- **IPv6 is disabled globally on the router until captivity is v6-aware.** The rc1
  bench caught a pre-auth client fetching the real internet over IPv6 (AAAA) while
  the identical IPv4 state was correctly captive (#783): nodogsplash manages only
  the IPv4 dimension, and the existing LAN-side disable from #148 (`ra`/`dhcpv6`
  off, `ip6assign=0`) does not cover a v6-capable WAN. `setup_disable_ipv6` now
  also clears `network.lan.ipv6`, sets `network.wan.ipv6='0'`, and disables the
  stock `wan6` section when present (a missing section is adoption, not an
  error), re-asserted on every setup pass so upgrades converge too. Proper
  v6-aware captivity stays tracked in #783 (post-0.6.0); #794 pins the test axis
  ([#783](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/783)).
