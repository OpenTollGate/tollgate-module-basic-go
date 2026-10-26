- **Upstream selection and blacklisting are now BSSID-aware, and the
  connectivity check is layered.** One SSID can be served by several BSSIDs
  across different upstream networks — some routed to the internet, some
  captive or isolated. The manager keyed its blacklist by SSID and never pinned
  the scanned BSSID on the candidate, so a router that settled on a BSSID with
  no internet blacklisted the whole SSID (including any working sibling) and
  could not recover; the ICMP-only check could not tell "gateway but no
  internet" apart from "ok". It now blacklists and pins by BSSID and probes
  with default-route, DNS and an HTTP 204 captive-portal check
  ([#910](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/910)).
