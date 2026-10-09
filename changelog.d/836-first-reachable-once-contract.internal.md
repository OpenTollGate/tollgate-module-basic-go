- **The first-reachable once-test now pins the contract the wire actually
  depends on.** `TestSetOnFirstReachableForDegraded_FiredOnce` asserted "no
  callback when already reachable from initial probe" — the exact opposite of
  `TestOnFirstReachable_FiredAfterSetOnFirstReachableForDegradedReset`, and of
  production: the wallet-failure degraded start registers the trigger while
  mints are already reachable (the failure was the wallet, not the mints), and
  the setter's latch reset is what fires the upgrade attempt on the next
  check. Both tests could only coexist because the once-test read its counter
  before the callback's goroutine could land — passing ~92% of runs on
  scheduler luck, flaking the other ~8% (#732). It now waits deterministically
  and asserts the real contract: exactly one fire, never a second.
  ([#836](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/836)).
