- **Leaked owed-grant monitors and the late-receive recorder no longer spray
  AUTH lines into later tests.** Every recorded owed grant owns a retry
  monitor goroutine (first attempt at 5 s + jitter, backoff to 30 s) that
  keeps exec-ing `ndsctl auth` after its test ends — resolved through
  whichever later test's PATH-scoped fake is live — and the late-receive
  recorder's completion path starts one the moment the harness cleanup
  releases the wallet. Both outlived their tests and failed
  `TestFirstTimePurchaseStillRefusedWhenNdsDoesNotKnowTheClient` 1-in-8
  suite runs (`ndsctl auth calls = 1, want 0`; causally reproduced 30/30 in
  a padded trio, #845). The harnesses now drain the record map at cleanup —
  every monitor's next wake is terminal — and the late-outcome harness
  waits out the recorder inside its own cleanup, before any later test's
  fake exists
  ([#846](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/846)).
