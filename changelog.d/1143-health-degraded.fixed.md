- **`tollgate health` no longer reports `healthy` for a degraded merchant.**
  It now carries `wallet_degraded` / `wallet_reason` and `status: degraded`,
  matching `status`, while `wallet_ok` keeps its historical meaning (“the
  merchant object exists”). No exit-code change.
  ([#1143](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1143)).
