- **`TG_VERSION` is now the one override for the release version, and every
  build asserts the binary's stamp matches its package.** The rc5 feed
  package self-reported `v0.6.0-rc4` — the tag and package said rc5 while
  the binary's stamp derived from a `VERSION` file the release commit
  forgot to bump (#941). The version is now resolved in one place
  (`tg_version` in `packaging/build-env.sh`: `TG_VERSION` wins, the
  `VERSION` file is the fallback) and consumed by `local-build-ipk.sh`,
  `build-sdk-package.sh`, the release workflow's new `workflow_dispatch`
  `version` input, and the build/harness lanes (`repro-test.sh`,
  `repro-variance.sh`, `shc-build-package.sh` — the latter two pin the
  resolved string into reprotest's varied builds and the remote VM build,
  so no downstream environment re-derives the version); tag-triggered CI
  builds stamp from the git tag itself, so a lagged `VERSION` file can no
  longer mis-stamp a release. After every build,
  `tg_assert_version_consistency` verifies the packaged binary embeds the
  expected version — a loud warning locally, a hard failure in both
  release workflows (`TG_REQUIRE_VERSION_CONSISTENCY=1`: the GitHub lane
  and the ngit build-of-record stage-1 shard, whose CLI binary also
  gains the `main.version` stamp this fence requires).
  ([#942](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/942))
