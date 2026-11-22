- **Captive-portal login is now uplink-scoped and cannot silently rotate an
  identity.** `upstream_wifi.portal_login_recipes[]` gives a bond's second STA
  its own recipe; every request, DNS lookup and probe uses `SO_BINDTODEVICE` on
  that netdev, redirects/form actions are limited to the page host plus
  `allow_hosts`, and one cookie jar spans fetch/redirect/submit. Each recipe
  gets one attempt per association/lease generation, a three-attempt cap and a
  sticky `budget_seconds`/`budget_bytes` exhausted state. The client never
  changes a MAC address or regenerates the node key
  ([#1253](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1253)).
