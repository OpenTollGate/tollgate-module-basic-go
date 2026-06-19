<!--
Target: Comment on https://github.com/OpenTollGate/tollgate-module-basic-go/issues/154
Title idea: "Status correction: prerequisites checkboxes are stale (PR #104 fully landed)"
-->

## Prerequisites section is stale — all of PR #104 has landed

The release plan's Prerequisites checklist still shows five unchecked items
under "Merge PR #104". All of them shipped in PR #104 and are documented in
`CHANGELOG.md`:

| Plan item (current checkbox state) | Actual status | Evidence |
|---|---|---|
| `[x]` Mint URL case mismatch — `strings.EqualFold` | ✅ Done | CHANGELOG "Payment correctness" line; PR #104 |
| `[ ]` Spent-token detection — error code matching | ✅ Done | CHANGELOG "proper spent-token detection"; PR #104 |
| `[ ]` Valve re-auth — always call `ndsctl auth` | ✅ Done | CHANGELOG "valve re-auth without a stale in-memory cache"; PR #104; `src/valve/valve.go:OpenGateUntil` always calls `authorizeMAC()` |
| `[ ]` Proxy header trust — only honor X-Forwarded-For from localhost | ✅ Done | CHANGELOG "trust `X-Forwarded-For` only from localhost"; PR #104 |
| `[ ]` IP/MAC input validation before shell commands | ✅ Done | CHANGELOG "IP/MAC input validation"; PR #104; `src/valve/valve.go:isValidMAC` |
| `[ ]` Request body size cap at 1MB | ✅ Done | CHANGELOG "1 MB request-body cap"; PR #104 |

Suggest updating the checkboxes to reflect reality.

## Hardware Validation — status per the full tag-readiness report

Cross-referencing the hardware-validation items against the full tag-readiness
report at `main @ 04ae54e` (one commit before current `main`):

<https://github.com/OpenTollGate/physical-router-test-automation/blob/feat/tag-readiness-suite/docs/tag-readiness-reports/TEST-REPORT-main-04ae54e.md>

| Hardware-validation item | Actual status |
|---|---|
| Install `v0.5.0-alpha.1` on GL.iNet MT3000 via release explorer | ✅ Done — both routers running `04ae54e` |
| Run E2E test suite (29 tests) | Partial — funded autopay verified, bandwidth-close-cycle not yet (see #88) |
| Test upgrade path from v0.4.0 | ⚠️ Not covered by tag-readiness — see #103 (AP-setup recovery missing) |
| Test upstream WiFi Manager | ✅ Verified — discovery + connect + TollGate advertisement validation on HW |
| Test mint resilience | ⚠️ Unit-test-only — `smoke-degraded` skipped due to lab mint-funding blocker |
| Test Lightning checkout | ⚠️ Not exercised in tag-readiness run |
| Test SSL management | ⚠️ SSL setup works on HW; QR-camera-via-HTTPS not addressed by design (see #95) |

## Risk Areas — current severity

| Area | Plan's risk | Updated take |
|---|---|---|
| Upgrade from v0.4.0 | HIGH | **Still HIGH.** #103 is unfixed and is now strictly worse (no recovery path). Filed design issue for the actual fix. |
| WGM rewrite | MEDIUM | **Demoted to LOW.** Verified working on HW per tag-readiness. |
| Mint resilience | MEDIUM | **Still MEDIUM.** Covered by Go unit tests; HW smoke skipped. |
| Lightning checkout | MEDIUM | **Still MEDIUM.** Not exercised in the readiness run. |
| Packaging overhaul | LOW-MEDIUM | **LOW.** ipk installs succeeded on both routers. |

## Suggested updates to this plan

1. Tick the six prerequisite boxes (all done).
2. Replace "Tag `v0.5.0-alpha.1`" with the actual alpha cadence — `alpha1`
   (May 29) and `alpha2` (Jun 17) are out; we're now deciding on `alpha3` or
   jumping to `-beta1`.
3. Add the four real stable-blockers from the tag-readiness report (see
   #169): smoke-degraded on HW, `upstream_detector/go.mod` fix, hermetic
   root test, land PR #170.
4. Add two uncovered risk lines:
   - Bandwidth-metered reseller close-cycle (#88) — hardware-unverified.
   - QR scanner in captive portal (#95) — known architectural constraint.

## Not Included

The plan says ~~#124~~ was "merged then reverted." For clarity: #124 was
reopened as #147 (config schema, CLI `--json`, test infrastructure) and
**merged on 2026-05-29**. CHANGELOG entries under "Schema-driven
configuration with a `--json` CLI" reference #147. So the feature is in
v0.5.0; the original PR number was just reorganized.
