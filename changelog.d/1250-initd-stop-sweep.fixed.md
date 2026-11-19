- **`service tollgate-wrt stop|restart` actually stops the daemon.** The
  init script's `stop()` override only wrote a log line — an overridden
  `stop()` replaces rc.common's procd_kill teardown, so stop/restart did
  nothing and stray manual/setsid instances kept serving :2121 with stale
  in-memory state (rate limiter, wallet caches) while the procd instance
  crash-looped on the busy port. The sweep now lives in the supported
  `stop_service()` hook (TERM, wait, KILL escalation, pidfile cleanup),
  the managed instance registers a pidfile, and a stubbed rc.common
  regression harness guards the shape in CI
  ([#1250](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1250)).
