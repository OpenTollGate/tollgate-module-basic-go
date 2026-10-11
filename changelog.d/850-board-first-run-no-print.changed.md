- **Generated credentials are never printed.** The private-WiFi key the module
  generates when the operator chose none is no longer printed to the install
  output or the log — the owner sets their own at the board, or with `tollgate
  network private set-password`. (The admin credential is no longer generated at
  all; see the 0.6.0 no-generated-credentials entry.) A reinstall that sets
  `TOLLGATE_ADMIN_PASSWORD` / `TOLLGATE_PRIVATE_WIFI_PASSWORD` uses those values
  instead
  ([#850](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/850)).
