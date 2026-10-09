- **A renamed firewall zone no longer silently un-ships the module's firewall
  rules — the zone refs follow the router's own config and a skipped rule
  says so in logread.** The package's firewall sections referenced the stock
  zone by name; fw4 skips a wrong-name section with exit 0 and nothing in
  syslog, so a zone rename left a portal that appeared to work with no rules
  (bench-verified on rc1: the rule vanished from the ruleset and a trusted
  client lost forwarding with only the zone name differing). The zone that
  owns the captive bridge is now resolved from the NDS gatewayinterface
  through the network config at section creation and on every setup pass,
  with a warned fallback to the stock name when unresolvable — and every
  fallback warning reaches both the setup log and syslog; a referenced zone
  that does not exist is named on both surfaces too, and the postinst
  surfaces fw4's own skip diagnostics instead of discarding them. Only the
  captive-side rule (`tollgate_in`) is resolved; the wan-side
  `tollgate_protocol` ref is asserted-loud rather than resolved — there is
  no authoritative wan-zone source equivalent to the gatewayinterface
  (#755, #785).
