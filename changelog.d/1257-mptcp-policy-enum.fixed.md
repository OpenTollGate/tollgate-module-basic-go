- **The MPTCP bond's `policy` value is now settable through the CLI.** The
  schema advertised `always` while the bond validates `all`, so one value was
  rejected at load time and the other could not be set at all
  ([#1257](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1257)).
