- **A shut-down merchant now stops its usage sweep.** Every full merchant
  starts a 2-second usage sweep at construction and nothing ever stopped it:
  `Shutdown` shut down only the wallet, so the runtime downgrade path leaked
  one sweep goroutine per downgrade/upgrade cycle — on a router, forever,
  still metering stale sessions and running client-list reconciliations
  through ndsctl. In the test binary the leaked sweeps sprayed DEAUTH execs
  into whichever later test's PATH-scoped fake was live — the mechanism
  behind the #733 under-load flakes (`deauths = 2, want 1`, 0/50 isolated).
  `Shutdown` stops the sweep, the three upgrade hand-offs shut down a
  merchant with no consumer instead of dropping it with live background
  work, and the harnesses drain what they construct (#733)
  ([#887](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/887)).
