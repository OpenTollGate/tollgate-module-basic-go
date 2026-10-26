- **The installer has its own fail-closed acceptance gate.** A built `.apk`
  can be validated end-to-end on a pristine OpenWrt 25.12 VM exactly the
  way an operator installs it — including the
  `TOLLGATE_ADMIN_PASSWORD` / `TOLLGATE_PRIVATE_WIFI_PASSWORD`
  environment the docs promise — with assertions for the credential
  lifecycle (sha256crypt comparison, no plaintext in output, the
  provisional/first-run fallback), services, listeners, the entry-port
  mapping and reboot persistence, plus serial bootstrap and distrusted
  pristine verification (a false `firstboot` "reset" on one 25.12.5 image
  kept `/etc` and would have silently invalidated every credential
  check). The gate is fail-closed end to end: an unavailable VM or an
  unexecuted required check reports `NOT_VERIFIED` and exits nonzero
  (never a silent PASS or SKIP), `--source-sha` binds the run to the
  commit the artifact was built from, every run writes a machine-readable
  `verdict.json` (per-check statuses, artifact sha256, image identity,
  lane environment), `uci show uhttpd` is inspected so a listenerless
  section is caught even when another instance claims its ports (#899),
  crash-loop signatures (`No sockets bound`, segfaults) are scanned
  before and after a reboot, and an offline selftest plants the #899 and
  #911 regressions to prove the checks can go red. Known-reds ship on
  purpose as acceptance criteria: #888 (the env never reaching the
  uci-defaults script under `apk add`), #890 (the entry-port flip's LuCI
  pair not converging on fresh-image installs) and #911 (apk-tools 25.12
  completing the transaction — package registered, service started —
  despite the failing preinst, reported verbatim)
  ([#892](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/892)).
