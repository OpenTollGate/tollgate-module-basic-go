- **A `count-soak` CI lane now gates the state-leak family at merge time.**
  Every PR-gating Go lane ran `-count=1`, which cannot see package state that
  outlives its test — the shape behind #821/#732/#841/#845, all found by hand
  soaks. A new push/PR-triggered lane re-runs the two stateful offenders under
  `-race` with `-count>1` (`src/valve` at `-count=5`, `src/merchant` at
  `-count=2` with a temporary `-skip` of the #845 victim until #846 lands), in
  its own workflow file because ngit-ci bounds an act invocation at 1800 s; the
  nightly `-count=5` soak also gets the `-timeout=45m` its merchant row has
  always needed — without it the row panicked at go test's 10 m default
  instead of soaking, which is how the family stayed invisible to it
  ([#852](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/852)).
  ([#905](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/905)).
