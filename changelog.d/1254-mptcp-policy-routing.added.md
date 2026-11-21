- **Each MPTCP subflow is pinned to its own uplink.** The bond now renders a
  dedicated routing table and `ip rule from <uplink-address>` for every IPv4
  member, so the second subflow cannot follow the primary default route with a
  foreign source; the disable path removes the rules and tables it installed
  ([#1254](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1254)).
