- **The test suites no longer spend the payment rate limit's per-IP budget.**
  The root handler's 10 requests/minute-per-IP limit failed the suites
  mid-run — a test binary is one process whose suites share client IPs, the
  same failure shape the deployed labs hit (#748, rig-verified FIXED). The
  accommodation is the `apiListenAddr` shape: a production-default-off bypass
  whose only writer is the `testenv`-tagged test-context provisioning — now
  audited by the build-purity contract alongside `TOLLGATE_TEST_CONFIG_DIR`
  (an untagged writer fails purity), so `go test -tags testenv` runs
  unthrottled while the shipped default, its Retry-After answer and the
  per-IP semantics are unchanged — both halves pinned by tests, alongside the
  `TOLLGATE_RATE_LIMIT_RPM` override the deployed labs rely on (#778).
