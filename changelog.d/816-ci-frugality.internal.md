- **GitHub CI is shaped as PR-cheap / main-proof / tag-release.** The PR
  lane keeps the fast signals (module matrix, contract checks, gitleaks)
  and now cancels superseded pushes; the expensive proof moved to where its
  inputs change — the happy-path (45-minute docker+playwright lane) to its
  own paths-filtered workflow that still gates every runtime-affecting PR,
  spec-quote drift to a push-only job (its pip-from-git preamble no longer
  runs per PR), repro-check and router-test to merge time, and
  build-package's ~22-job matrix to PRs that actually touch packaging
  paths. gitleaks' schedule went daily → weekly. On a public repo this
  saves ~zero metered minutes — the wins are queue latency, runner
  availability, and already-frugal habits if the org ever loses
  public-repo freebies
  ([#816](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/816)).
