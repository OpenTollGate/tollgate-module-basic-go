<!--
Target: New issue on https://github.com/OpenTollGate/physical-router-test-automation
Title: "Test: bandwidth-metered reseller session closes at allotment on hardware (regression-guard #88, #170)"
Labels: test, two-router
-->

## Why

`tollgate-module-basic-go#88` claims that bandwidth-metered reseller sessions
never close because the upstream's `ndsctl` doesn't know about MACs that paid
via router-to-router autopay. The full tag-readiness report on `main @ 04ae54e`
(<https://github.com/OpenTollGate/physical-router-test-automation/blob/feat/tag-readiness-suite/docs/tag-readiness-reports/TEST-REPORT-main-04ae54e.md>)
verified that the funded autopay **opens** a gate and internet flows, but did
**not** verify the **close cycle** for byte-metered sessions.

PR [#170](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/170)
lands the first-attempt reliability fix but is not by itself sufficient to
close #88.

## Goal

Add a hardware regression test that proves: when alpha pays beta for a byte-
metered session and then generates LAN traffic through beta, beta's `ndsctl`
reports non-zero usage for alpha's MAC and the session closes when the byte
allotment is reached.

This is the missing half of `TestTwoRouterFunded::test_funded_autopay_opens_session`.

## Scope

### New test: `test_funded_autopay_bandwidth_session_closes_at_allotment`

Lives in `tests/scenarios/test_two_router.py`, next to the existing funded
autopay test. Uses the `two_router_funded_upstream` fixture.

**Topology:** alpha (client) → WiFi → beta (upstream, reseller-mode). Beta
forwards to house internet. Both routers at the same pinned commit.

**Preconditions:**
- Beta's `/etc/tollgate/config.json` configured with `"metric": "bytes"` and a
  small `step_size` (e.g. 1 MiB) for the accepted mint alpha pays with, so the
  allotment is reached quickly.
- Beta's `data_monitoring_interval` is short (e.g. 500 ms) for fast feedback.
- Alpha funded at `nofee.testnut.cashu.space` (per the tag-readiness report
  workaround) so the payment succeeds.
- PR #170 merged on both routers (eliminate first-attempt flakiness noise).

**Steps:**
1. Trigger alpha to discover + connect to beta's `TollGate-*` SSID (existing
   `two_router_funded_upstream` flow).
2. Trigger the autopay purchase so alpha pays beta and the gate opens.
3. Assert gate-open on beta: `ndsctl json <alpha-mac>` returns a non-empty
   JSON object (state=authenticated or similar).
4. Generate sustained downstream traffic from alpha through beta — e.g.
   `curl --max-time 30 http://speedtest.tele2.net/1MB.zip > /dev/null` in a
   loop, or `iperf3 -c <wan-side-echo-host> -t 30 -b 10M` if a host is
   reachable. Target ~3–5 MiB total so it exceeds the allotment.
5. Poll beta's `ndsctl json <alpha-mac>` every `data_monitoring_interval` (or
   every 1 s, whichever is greater) and record `downloaded` + `uploaded`.
6. **Assertions:**
   - After traffic, `downloaded + uploaded > 0` (proves #88's "0 usage
     forever" path is not happening).
   - `downloaded + uploaded` increases monotonically across polls.
   - Within a bounded wall-clock budget (e.g. 90 s from traffic start), the
     session closes on beta: either `ndsctl json <alpha-mac>` returns `{}` /
     "client not found", or beta logs a session-close for the MAC.
7. After close, assert alpha is deauthenticated: `ndsctl json <alpha-mac>`
   returns the not-found path, and an HTTP request from alpha no longer
   reaches the WAN.

**Failure modes the test must distinguish:**
- "0 usage forever" (the #88 bug) — `ndsctl json` keeps returning `{}` or 0
  bytes despite traffic → mark as `REGRESSION (#88)`.
- "usage tracked but never closes" — bytes grow past allotment but session
  stays open → separate bug, log accordingly.
- "closed early" — closed before allotment reached → likely valve/timer race,
  log accordingly.
- "payment failed entirely" — preconditions broke; skip, do not fail.

### Fixtures / config changes

- Add a `TOLLGATE_RESALER_BYTES_STEP_KB` env (or fixture parameter) so the
  allotment can be tuned without editing config files on the routers.
- Reuse the existing `two_router_funded_upstream` fixture — do not invent a
  parallel one.
- The funded path is already gated on `MINT_TOKEN_BIN` being set; reuse that
  gate so CI skips cleanly when the test mint is broken (current state).

### Repro / single-command run

```sh
MINT_TOKEN_BIN=$PWD/scripts/mint-token/mint-token \
TOLLGATE_TEST_MINT_URL=https://nofee.testnut.cashu.space \
TOLLGATE_RESALER_BYTES_STEP_KB=1024 \
pytest tests/scenarios/test_two_router.py::TestTwoRouterFunded::test_funded_autopay_bandwidth_session_closes_at_allotment \
    --no-deploy -s
```

## Out of scope

- The deeper #88 architectural fix (decouple usage tracking from nodogsplash)
  — that's a code change on `tollgate-module-basic-go`, tracked separately.
- Cloud two-router variant — `tests/scenarios/test_two_router_cloud.py` exists
  and could be extended later, but the nodogsplash/ndsctl behavior under test
  is router-specific so hardware is the source of truth for #88.

## Acceptance

- New test method lands in `test_two_router.py`.
- Runs green against `main @ <commit-with-#170-merged>` on the two-router HW
  fleet.
- One full run recorded under `docs/tag-readiness-reports/` so future
  regressions are diffable.
- Companion comment on `tollgate-module-basic-go#88` linking the merged test
  PR, with the result and a verdict (closed-by-#170 vs deeper-fix-needed).
