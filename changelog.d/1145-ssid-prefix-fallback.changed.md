- **Captive-SSID prefixes are configurable instead of compiled in.**
  Recognition is now `TollGate` (always) + `/etc/tollgate/brand` + optional
  `/etc/tollgate/ssid-prefixes`, so a downstream skin adds its name without a
  PR to this repository. The prefix remains a candidate-selection hint; the
  signed kind-10021 probe is still the authority.
  ([#1145](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1145)).
