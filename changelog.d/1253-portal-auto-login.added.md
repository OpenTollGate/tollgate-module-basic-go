- **An unattended upstream can log in to a venue captive portal.** Opt-in
  `upstream_wifi.portal_login_*` recipes submit the portal's click-through form
  or configured credentials, verify real internet afterwards, and fall back to
  the existing manual window; credentials are host-restricted and never logged
  ([#1253](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1253)).
