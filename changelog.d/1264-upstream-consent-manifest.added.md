- **Open upstreams now require an explicit per-AP consent entry before an
  automatic join.** `upstream_wifi.consent_manifest[]` records `pre-accepted`
  or `per-session` networks; an unknown open AP is observed, never joined, and
  `allow_any_open` (default `false`) is the explicit override. TollGate peers,
  the `Private-` management SSID and saved encrypted networks are exempt
  ([#1264](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1264)).
