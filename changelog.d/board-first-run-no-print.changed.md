- **Generated credentials are never printed.** The private-WiFi key the module
  once generated when the operator chose none is no longer printed to the
  install output or the log — the owner sets their own at the board, or with
  `tollgate network private set-password`. (The package no longer generates or
  applies a credential at all; see the 0.6.0 no-generated-credentials entry, and
  the private AP is now left disabled until a passphrase is set.)
  ([#850](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/850)).
