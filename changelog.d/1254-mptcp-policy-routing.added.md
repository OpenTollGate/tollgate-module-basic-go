- **Each MPTCP subflow is pinned to its own uplink, and the second path is
  actually opened.** The bond renders a dedicated table and `ip rule from
  <uplink-address>` for every IPv4 member, a main-table default with metrics
  10/20 (required by the in-kernel path manager), and a static per-uplink
  `gateway`; proxy-terminated endpoints use `subflow fullmesh`. Measured on the
  MT3000: `SF_ESTABLISHED` from the hotel uplink and payload on both paths
  ([#1254](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1254)).
