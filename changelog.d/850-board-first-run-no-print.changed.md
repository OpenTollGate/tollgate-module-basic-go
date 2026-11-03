- **Generated credentials are never printed.** The admin and private-WiFi
  credentials the module generates when the operator chose one are no longer
  printed to the install output or the log. The owner sets their own at the
  board first-run page, or with `passwd root` / a reinstall that sets
  `TOLLGATE_ADMIN_PASSWORD` and the new `TOLLGATE_PRIVATE_WIFI_PASSWORD`.
  ([#850](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/850)).
