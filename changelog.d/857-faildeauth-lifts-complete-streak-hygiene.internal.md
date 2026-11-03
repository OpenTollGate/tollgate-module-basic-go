- **The `failDeauth`-lift discipline is now complete for every site that
  spends a close streak.** #842 taught `closeGateCleanup` to reconcile an
  abandoned streak, but three sites still starved it: the bounded unmeterable
  escalation test never lifted its marker (every later `-count` iteration
  short-circuited on the surviving streak — 7.28s → 0.82s per iteration, plus
  a cleanup ERROR line each run), the startup refused-close test accumulated
  one unconfirmed attempt per run toward a `-count≥9` false zero, and the
  zombie abandonment test lifted its marker only on the success path. All
  three now lift in a LIFO cleanup (and the startup test resets its gate via
  `resetGateState` before arming), so the valve state the suite hands between
  tests is actually clean
  ([#857](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/857)).
