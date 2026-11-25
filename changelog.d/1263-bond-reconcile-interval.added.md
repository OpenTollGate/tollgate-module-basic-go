- **The MPTCP bond can repair drift that produced no interface event.**
  `mptcp_bonding.reconcile_interval_s` (default 0, off) re-applies the
  idempotent bond script on a timer, so a DHCP client replacing a metric route,
  a flushed endpoint or a deleted policy rule is repaired without waiting for
  an address change. The event-driven path still skips an unchanged script
  ([#1263](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1263)).
