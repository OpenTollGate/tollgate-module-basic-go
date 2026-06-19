<!--
Target: Comment on https://github.com/OpenTollGate/tollgate-module-basic-go/issues/169
Title idea: "Status correction: issue body is stale; full report shows much better status"
-->

## Status correction: issue body is stale

The issue body (the "READY-WITH-CAVEATS" verdict) was the **initial**
assessment written before the two-router campaign ran. The full tag-readiness
report (linked from this issue at the bottom) shows materially better status.
Pulling the actual numbers forward so this issue reflects reality:

## Actual status per the full report

<https://github.com/OpenTollGate/physical-router-test-automation/blob/feat/tag-readiness-suite/docs/tag-readiness-reports/TEST-REPORT-main-04ae54e.md>

> Verdict: ✅ READY (materially de-risked) — suitable for `v0.5.0`

| Area (test) | Result | What it validates |
|---|---|---|
| Tier 0 `go build ./...` | ✅ | Compiles |
| Tier 0 `go vet ./...` | ✅ | Static correctness |
| Tier 0 `go test` 12/14 modules | ✅ 12 · 🔧 2 | 2 env-only (root test non-hermetic, upstream_detector not tidy) |
| Tier 1 alpha API smoke | ✅ 21 · ⏭️ 30 · 🔧 1 | Backend API on GL-MT3000 |
| Tier 1 beta API smoke | ✅ 27 · ⏭️ 24 · 🔧 1 | Backend API on 2nd GL-MT3000 |
| Preflight (both routers) | ✅ 5/5 | Reachable, version, SSIDs, topology, no crash loop |
| Postflight (both routers) | ✅ 4/4 | Service alive, no panics, no leftover mint blocks |
| **Tier 2 WiFi discovery + connect (alpha→beta)** | ✅ log-verified | **v0.5.0 headline feature: scan, connect, detect beta as TollGate, validate advertisement — works on HW** |
| Tier 2 `TestRouterLockCoordination` | ✅ 2/2 | Multi-session mutex |
| **Tier 2 funded autopay (`test_funded_autopay_opens_session`)** | ✅ **VERIFIED** | **End-to-end alpha→beta payment + session + internet flows. The router-to-router purchase works at `04ae54e`.** |
| Tier 2 `smoke-degraded` (degraded mode on HW) | ⏭️ skipped | Mint-funding toolchain blocked; covered by Go unit tests |
| Tier 3 reboot-recovery (beta) | ✅ | Service auto-starts after reboot, build persists |

The issue body's "alpha router is on OpenWrt 21.02, can't run the package"
caveat is **resolved** — alpha was reflashed to 24.10.4 and the funded
autopay test was run on it.

## What's still pending for stable `v0.5.0`

Smaller list than the issue body's three caveats:

1. **`smoke-degraded` on hardware.** Mint-unreachable → degraded → recover
   cycle is currently skipped because the lab test-mint funding tooling is
   broken. The mint itself isn't a code regression; degraded-mode is covered
   by Go unit tests in CI. Need to either fix `scripts/mint-token/go.mod`
   (gonuts v0.6.1 BOLT11 regression) or repoint the harness default mint at
   `nofee.testnut.cashu.space` (the report's workaround).
2. **`upstream_detector/go.mod` is broken, not just untidy.** Filed as a
   separate code-hygiene issue: it's missing `replace` directives for
   `merchant_types` and `utils`, so `go mod tidy` fails and the module can't
   be built standalone. Trivial fix.
3. **Root-module test hermeticity.** `src/main_test.go`,
   `src/e2e_test.go`, `src/transport_test.go` depend on `/etc/tollgate/config.json`
   existing on the host. Need a temp config dir override for off-router runs.
4. **Hardware-only first-attempt flakiness in autopay.** The same report
   notes that the autopay sometimes fails its first `open gate` attempt and
   recovers via token-recovery within ~60-90 s. PR #170 lands the fix. Not a
   release blocker but should land before stable.

## Items this issue does NOT cover (filed separately)

- **Bandwidth-metered close-cycle** for router-to-router autopay (#88) — not
  verified on hardware. The funded autopay test only proves the gate opens,
  not that a byte-metered session closes at the allotment. Filed a test plan
  on `physical-router-test-automation`.
- **AP-setup recovery on reinstall** (#103) — bug is worse than originally
  described; filed a separate design issue for the real fix.

## Recommendation

- **For `v0.5.0-alpha3` / `-beta1` from current `main` (`612860a`)**: the
  commit is post-alpha2 with three additional features (SNI multi-cert TLS,
  HTTPS serving, aggressive mint startup retry) on top of the verified
  `04ae54e`. The full report already supports tagging a beta; the additional
  commits don't touch the verified code paths.
- **For stable `v0.5.0`**: clear the four items above (smoke-degraded,
  upstream_detector go.mod, hermetic root test, land #170) and re-run
  `make tag-readiness-full` once. That should be sufficient.

Suggest updating the issue body to point at the full report as the
authoritative status, and using this issue purely as the "to-stable"
tracker.
