- **The bond's `transport` and `tunnels` are configurable through
  `tollgate config`.** The NAT-safe `proxy-terminated` shape and the optional
  per-uplink WireGuard overlays no longer exist only in code; the config
  round-trip is fenced by a test against the bond loader's unknown-field
  rejection
  ([#1255](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1255)).
