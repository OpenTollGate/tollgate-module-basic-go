- **The package postinst now fails loudly when nodogsplash was
  orphan-removed.** An aborted opkg transaction can orphan-remove runtime
  dependencies and leave zero-byte husks — the nodogsplash binary and init
  script, plus libmicrohttpd, curl, socat, jq — while the old postinst's
  `restart 2>/dev/null || true` reported success over the dead captive
  portal, hiding the damage until every downstream symptom looked like a
  product bug. The postinst now refuses to claim success when the
  nodogsplash binary or init script is empty and points at the
  remediation (`opkg update`, then reinstall nodogsplash and this
  package)
  ([#715](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/715)).
