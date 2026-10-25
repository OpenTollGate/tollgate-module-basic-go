- **Captive clients can resolve hostnames again.** The nodogsplash pre-auth
  allow list allowed DNS on tcp/53 only (the feed default), so the UDP query
  every stub resolver sends first was dropped and hostname resolution from an
  unpaid client timed out — while the portal itself is reached by name. The
  setup script now asserts both `allow tcp port 53` and `allow udp port 53`
  with protocol-aware, idempotent guards, on every install path
  ([#769](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/769)).
