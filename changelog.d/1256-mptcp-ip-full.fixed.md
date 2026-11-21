- **The MPTCP bond declares `ip-full` instead of discovering BusyBox `ip` at
  boot.** The package now pulls the only `ip` that has an `mptcp` subcommand,
  and the runbook covers both the apk and opkg eras
  ([#1256](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1256)).
