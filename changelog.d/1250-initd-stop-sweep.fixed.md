- **`service tollgate-wrt stop|restart` now sweeps stray daemon processes.**
  procd's stop only terminates the instance it spawned — a
  manual/setsid-started `tollgate-wrt` survives every stop/restart and
  keeps serving :2121 with stale in-memory state (rate limiter, wallet
  caches) while a restarted procd instance crash-loops on the busy port.
  The sweep lives in the supported `stop_service()` hook (TERM, wait,
  KILL escalation, pidfile cleanup); the managed instance registers a
  pidfile; the file-local `stop()` this replaces was dead code under
  `USE_PROCD=1` (rc.common defines `stop()` after sourcing the init
  file) that only misled readers (#1208); a stubbed rc.common
  regression harness guards the shape in CI
  ([#1250](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1250)).
