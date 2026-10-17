- **The hardware test fleet under `tests/` is retired pending migration.**
  Router-test coverage is consolidated into
  [physical-router-test-automation](https://github.com/OpenTollGate/physical-router-test-automation)
  as the single source of truth; the legacy pytest fleet is deprecated,
  not deleted — each file is removed once its PRTA equivalent has run
  green on hardware, per the migration audit in PRTA's
  `docs/authoritative-test-suite.md`. The contributor guide now points
  router-visible-change testing at PRTA's `tests/api/` payment suite
  ([#916](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/916)).
