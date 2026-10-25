- **MPTCP multi-WAN bonding is wired in as an uplink-only seam.** A new
  `src/mptcp_bonding` package holds the validated core — pure config
  loading/steering decisions (`ShouldSteer` in exactly one place, shared by
  the generated firewall rule and any Go caller so rule and code cannot
  drift), disjoint-uplink checks, idempotent apply/reconcile, and a per-uplink
  tunnel renderer — with no syscalls, netlink or file I/O in the config path.
  Portal, payment, session accounting and merchant mode are untouched: the
  bond replaces only the uplink that holds the default route. Operator
  surface: an `mptcp` config schema section (additive; existing deployments
  unaffected) plus CLI operator-settings wiring, measurement docs and a
  counter-reading script
  ([#927](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/927)).
