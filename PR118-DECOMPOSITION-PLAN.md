# PR #118 Decomposition Plan — Mint Health Tracking with Graceful Degradation

**Source PR:** [OpenTollGate/tollgate-module-basic-go#118](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/118)
**Test Coverage Issue:** [OpenTollGate/physical-router-test-automation#14](https://github.com/OpenTollGate/physical-router-test-automation/issues/14)
**Date:** 2026-05-27
**Status:** Planning

---

## Overview

PR #118 is a monolithic squash commit (+9,585/-4,551 across 66 files) that bundles at least **7 distinct features**. This document decomposes it into 8 independently testable, mergeable PRs (PRs A–H) plus 1 CI infrastructure PR (PR I), ordered by dependency.

### Guiding Principles

1. Each PR must be independently deployable and testable on physical routers
2. Each PR must have unit (Go), integration (Go + hardware), end-to-end (pytest on physical routers), and Playwright (browser) test coverage where applicable
3. pytest is used for setup/teardown of physical router tests; Makefile targets orchestrate pytest runs
4. No PR should break existing tests on `main`
5. PRs marked with `pytest.mark.pr(XXX)` should be ungated when their PR merges to `main`

---

## Dependency Graph

```
PR A (TLS 1.2 Transport)
  │
  ├──► PR B (merchant_types Decoupling) ──┬──► PR C (Mint Health Tracker)
  │                                        │
  │                                        ├──► PR G (SSL Rewrite) [parallel with C-E]
  │                                        │
  │                                        └──► PR I (CI/Infra) [parallel with C-E]
  │
  └──► PR D (ErrTokenAlreadySpent Sentinel)
           │
           └──► PR E (Degraded Mode + Dynamic Upgrade/Downgrade)
                  │
                  └──► PR F (Captive Portal Degraded-Mode UI)

PR H (WGM Improvements) — ALREADY MERGED via PR #122
```

### Critical Path
```
A → B → C → D → E → F
```

PRs G, I, and H are independent and can merge in parallel once B lands.

---

## PR A — TLS 1.2 + HTTP Transport Hardening

### Why Needed
Go's pure TLS 1.3 `ClientHello` times out on the router's network path to Cashu mints. Without this fix, mint API calls hang indefinitely, making all subsequent mint-health features non-functional. This is a **prerequisite** for every other PR.

### Scope

| File | Change |
|---|---|
| `src/main.go` | `init()`: override `http.DefaultTransport` with TLS 1.2 `MaxVersion`, dial/connect/response timeouts |
| `src/go.mod` | Add `crypto/tls`, `net` imports (standard library) |
| `src/go.sum` | No new external deps |

**Lines changed:** ~25 lines added

### Depends On
Nothing — this is the foundation PR.

### Test Coverage

#### Go Unit Tests (tollgate-module-basic-go)

| Test File | Test Functions | Status |
|---|---|---|
| `src/main_test.go` | `TestGetTollgatePaths_*` (3 tests) | **Existing** |
| `src/transport_test.go` | **NEW** — `TestDefaultTransport_TLSMaxVersion12`, `TestDefaultTransport_Timeouts`, `TestDefaultTransport_HTTP2Disabled` | **GAP — must create** |

**New Go test requirements (in `src/transport_test.go`, run with `-tags testenv`):**
- Verify `http.DefaultTransport.(*http.Transport).TLSClientConfig.MaxVersion == tls.VersionTLS12`
- Verify `ResponseHeaderTimeout == 30s`, `TLSHandshakeTimeout == 20s`
- Verify `ForceAttemptHTTP2 == false`
- Verify `DisableKeepAlives == true`
- Verify `http.DefaultClient.Timeout == 30s`
- **Note:** These tests run in `package main` after `init()` — requires `-tags testenv` to provide test config directory

#### API Integration Tests (physical-router-test-automation)

| Test File | Test Functions | Status |
|---|---|---|
| `tests/api/test_health.py` | `test_root_endpoint`, `test_pay_endpoint`, `test_balance_endpoint`, `test_whoami_endpoint` | **Existing** |
| `tests/api/test_mint_health.py` | `test_status_command_works`, `test_version_matches_installed` | **Existing** |

**New pytest requirements:**
- `test_tls_version_negotiated`: Verify mint API call completes in <2s (regression test for the TLS 1.3 hang)

#### Playwright Tests
None needed — TLS is a backend transport concern.

#### End-to-End on Physical Routers
- Makefile target: `r-check-merchant` — confirms mint API responded and merchant initialized

### Merge Gate
- [ ] `go test ./...` passes (all packages)
- [ ] `test_health.py` smoke tests pass
- [ ] Manual: `ssh root@$router "wget -qO- https://nofee.testnut.cashu.space/v1/info"` returns in <1s

---

## PR B — `merchant_types` Package + USM Decoupling

### Why Needed
`upstream_session_manager/go.sum` grows by ~490 lines because USM transitively depends on `lightning`, `tollwallet`, `valve`, `btcwallet`, `lnd` etc. just to consume `MerchantProvider`. This is an **architectural regression**. A zero-dependency `merchant_types` package with a narrow `PaymentMerchant` interface (4 methods) eliminates ~60 lines of transitive deps from USM.

### Scope

| File | Change |
|---|---|
| `src/merchant_types/go.mod` | **New** — depends only on `config_manager` |
| `src/merchant_types/go.sum` | **New** |
| `src/merchant_types/types.go` | **New** — `PaymentMerchant` interface (4 methods), `MerchantProvider` interface, `MutexMerchantProvider` struct |
| `src/upstream_session_manager/go.mod` | Replace `merchant` dep with `merchant_types` |
| `src/upstream_session_manager/go.sum` | Shrinks from ~490 lines to ~10 |
| `src/upstream_session_manager/upstream_session_manager.go` | Import `merchant_types` instead of `merchant` |
| `src/upstream_session_manager/session.go` | Use `merchant_types.PaymentMerchant` |
| `src/upstream_session_manager/token_recovery.go` | Use `merchant_types.PaymentMerchant` |
| `src/upstream_session_manager/types.go` | Remove old type definitions |
| `src/upstream_session_manager/merchant_provider_test.go` | Updated mock (4 methods) |

**Lines changed:** +355/-40 (net -40 from USM dep reduction)

### Depends On
- **PR A** (transport config must be in place for any mint API calls)

### Test Coverage

#### Go Unit Tests (tollgate-module-basic-go)

| Test File | Test Functions | Status |
|---|---|---|
| `src/upstream_session_manager/merchant_provider_test.go` | `TestMockMerchantProvider_BasicSwap`, `TestMockMerchantProvider_NilMerchant`, `TestMockMerchantProvider_ConcurrentSwapAndRead`, `TestUpstreamSessionManager_StoresMerchantProvider`, `TestUpstreamSessionManager_SwapPropagates`, `TestUpstreamSession_MerchantProviderPropagates`, `TestUpstreamSession_MultipleSessionsShareProvider` (7 tests) | **Existing** |
| `src/merchant_types/types_test.go` | **NEW** — `TestPaymentMerchant_Interface_SatisfiedByMerchant`, `TestMerchantProvider_Interface`, `TestMutexMerchantProvider_ConcurrentAccess` | **GAP — must create** |

#### API Integration Tests (physical-router-test-automation)

| Test File | Test Functions | Status |
|---|---|---|
| `tests/api/test_merchant_provider.py` | All provider propagation tests (see PR E) | **Existing** |
| Full regression suite | All existing tests pass identically | **Existing** |

**New pytest requirements:** None — pure refactoring, no behavior change.

#### Playwright Tests
None needed — no UI changes.

#### End-to-End on Physical Routers
- Full regression: `pytest tests/api/ -m smoke` passes identically to before

### Merge Gate
- [ ] `go build ./...` passes (all packages compile with new import graph)
- [ ] `go test ./...` passes identically
- [ ] USM `go.sum` line count reduced
- [ ] Full pytest smoke suite passes (no regressions)

---

## PR C — Mint Health Tracker + Merchant Provider Infrastructure

### Why Needed
This is the core health probing infrastructure. `MintHealthTracker` probes each mint via `GET /v1/info` with a 5-second timeout. At startup, it determines which mints are reachable. `MutexMerchantProvider` wraps the merchant instance with an `RWMutex` so consumers always see the current merchant after any swap. Without this PR, there is no way to detect mint reachability changes or swap merchants safely.

### Scope

| File | Change |
|---|---|
| `src/merchant/mint_health_tracker.go` | **New** — health probing, hysteresis (3 consecutive successes), callbacks |
| `src/merchant/mint_health_tracker_test.go` | **New** — 29 unit tests |
| `src/merchant/merchant_provider.go` | **New** — `MutexMerchantProvider` with `RWMutex` |
| `src/merchant/merchant_provider_test.go` | **New** — 9 unit tests |
| `src/merchant/merchant.go` | Modified — extract `newFullMerchant()`, add `RunInitialProbe()`, use reachable mints for wallet init |
| `src/merchant/go.mod` | Updated deps |
| `src/merchant/go.sum` | Updated deps |
| `src/config_manager/config_manager_config.go` | Loud WARN on dev mint injection |
| `src/config_manager/buildinfo.go` | `GitBranch` variable |
| `src/config_manager/buildinfo_test.go` | **New** — 4 tests for branch detection |
| `src/main.go` | Wire `MintHealthTracker` into merchant startup |

**Lines changed:** ~800 lines source + ~900 lines tests

### Depends On
- **PR A** (TLS transport for mint probing)
- **PR B** (merchant_types for provider interface)

### Test Coverage

#### Go Unit Tests (tollgate-module-basic-go)

| Test File | Test Functions | Status |
|---|---|---|
| `src/merchant/mint_health_tracker_test.go` | 29 tests: `TestRunInitialProbe_*`, `TestProactiveCheck_*`, `TestOnReachableSetChanged_*`, `TestConcurrentAccess`, `TestEndToEnd_*`, etc. | **Existing** |
| `src/merchant/merchant_provider_test.go` | 9 tests: `TestNewMutexMerchantProvider_*`, `TestMutexMerchantProvider_*` | **Existing** |
| `src/config_manager/buildinfo_test.go` | 4 tests: `TestIsDevBuild_*`, `TestNewDefaultConfig_MintsForBranch`, etc. | **Existing** |
| `src/config_manager/config_manager_test.go` | 3 tests: config round-trip | **Existing** |

#### API Integration Tests (physical-router-test-automation)

| Test File | Test Functions | Status |
|---|---|---|
| `tests/api/test_mint_health.py` | `test_logs_show_mint_health_tracking`, `test_logs_show_dynamic_rebuild`, `test_discovery_excludes_unhealthy_mints`, `test_wallet_info_shows_mint_count`, `test_status_command_works`, `test_version_matches_installed` (6 tests) | **Existing** |
| `tests/api/test_try_all_mints.py` | `test_first_mint_unreachable_second_works`, `test_all_mints_unreachable_degraded` (2 tests) | **Existing** |
| `tests/api/test_discovery_mints.py` | `test_unreachable_mint_not_in_discovery`, `test_bad_mint_handled_gracefully` (2 tests) | **Existing** |
| `tests/api/test_mint_502_fake.py` | `test_fake_502_mint_triggers_degraded`, `test_fake_502_service_stays_up` (2 tests) | **Existing** |
| `tests/api/test_mint_502_handling.py` | 7 tests for 502 mint handling | **Existing** |
| `tests/api/test_health.py` | 4 tests for endpoint health | **Existing** |

**New pytest requirements:** None — existing coverage is comprehensive for tracker infrastructure.

#### Playwright Tests
None needed at this stage — no UI changes yet.

#### End-to-End on Physical Routers

| Makefile Target | What It Verifies |
|---|---|
| `r-check-merchant` | Service boots as full merchant when mints reachable |
| `r-block-mint` + `r-restart-service` + `r-check-degraded` | Mint blocking triggers degraded mode |
| `r-unblock-mint` + `r-wait-recovery` + `r-check-merchant` | Recovery from degraded |

### Merge Gate
- [ ] `go test ./src/merchant/...` passes (29 tracker + 9 provider tests)
- [ ] `go test ./src/config_manager/...` passes (4 buildinfo + 3 config tests)
- [ ] `test_mint_health.py` + `test_try_all_mints.py` + `test_discovery_mints.py` pass on hardware
- [ ] Manual: `r-check-merchant` confirms full merchant mode on both routers

---

## PR D — `ErrTokenAlreadySpent` Sentinel + `Shutdown()`

### Why Needed
`tollwallet.Receive()` used `strings.Contains(err.Error(), "Token already spent")` for error detection — fragile if the upstream library changes. Downstream code (merchant) needs `errors.Is()` instead. The `Shutdown()` method releases BoltDB file locks, which is required for the degraded→full merchant in-process upgrade in PR E.

### Scope

| File | Change |
|---|---|
| `src/tollwallet/tollwallet.go` | Add `ErrTokenAlreadySpent` sentinel, `Shutdown()` method, wrap upstream error with `%w` |
| `src/tollwallet/go.mod` | Updated deps |
| `src/tollwallet/go.sum` | Updated deps |
| `src/merchant/merchant.go` | Use `errors.Is(err, tollwallet.ErrTokenAlreadySpent)` in `Fund()` |

**Lines changed:** ~40 lines

### Depends On
- **PR A** (transport config)
- **PR C** (merchant.go uses the sentinel in Fund())

### Test Coverage

#### Go Unit Tests (tollgate-module-basic-go)

| Test File | Test Functions | Status |
|---|---|---|
| `src/tollwallet/tollwallet_test.go` | `TestNew`, `TestReceive`, `TestSend`, `TestGetBalance`, `TestContains` (5 tests) | **Existing** |
| `src/tollwallet/sentinel_test.go` | **NEW** — `TestErrTokenAlreadySpent_IsMatch`, `TestErrTokenAlreadySpent_WrapsUpstreamError`, `TestShutdown_NilWallet_NoPanic`, `TestShutdown_ReleasesResources` | **GAP — must create** |

**New Go test requirements:**
- `errors.Is(fmt.Errorf("%w: ...", ErrTokenAlreadySpent), ErrTokenAlreadySpent) == true`
- Non-wrapped errors don't match
- `Shutdown()` on nil wallet returns nil
- `Shutdown()` on active wallet closes BoltDB

#### API Integration Tests (physical-router-test-automation)

| Test File | Test Functions | Status |
|---|---|---|
| `tests/api/test_edge_tokens.py` | `test_duplicate_token_immediate_reuse` | **Existing** |

**New pytest requirements:**
- `test_duplicate_token_returns_already_spent_code`: Verify the error response contains a structured "already-spent" indicator (not just a generic 500)

#### Playwright Tests
None needed — backend-only change.

#### End-to-End on Physical Routers
- Submit same token twice via portal → second attempt shows "already spent" error (not crash)

### Merge Gate
- [ ] `go test ./src/tollwallet/...` passes (existing 5 + new sentinel tests)
- [ ] `test_edge_tokens.py::test_duplicate_token_immediate_reuse` passes
- [ ] Manual: `r-test-cashu-payment` — verify payment flow handles already-spent gracefully

---

## PR E — Degraded Mode + Dynamic Upgrade/Downgrade

### Why Needed
This is the **core feature** of PR #118. When all mints are unreachable, the service starts in degraded mode (offline wallet, cached keysets, stub payment operations) instead of crashing. When mints recover, it upgrades in-process. When mints go offline mid-operation, it downgrades dynamically. This is the feature that makes the tollgate router resilient to mint outages.

### Scope

| File | Change |
|---|---|
| `src/merchant/merchant_degraded.go` | **New** — offline wallet (BoltDB read-only), stub operations, `Shutdown()`, `OnUpgrade()` callback |
| `src/merchant/merchant_degraded_test.go` | **New** — 51 unit tests |
| `src/merchant/test_helpers_test.go` | **New** — shared test helpers (`newDegradedSetup`, `newUnreachableServer`) |
| `src/merchant/offline_wallet_integration_test.go` | **New** — 9 integration tests (build-tagged `integration`) |
| `src/merchant/merchant.go` | Modified — degraded boot path, dynamic advertisement (no cache), `SetOnReachableSetChanged` |
| `src/main.go` | Modified — `merchantTypesProvider` adapter, `swapMerchant()`, `registerReachableSetChangedCallback()`, degraded→full and full→degraded wiring |

**Lines changed:** ~1,200 lines source + ~3,500 lines tests

### Depends On
- **PR B** (merchant_types provider interface)
- **PR C** (MintHealthTracker with `onFirstReachable` and `onReachableSetChanged` callbacks)
- **PR D** (`Shutdown()` for BoltDB lock release during merchant swap)

### Test Coverage

#### Go Unit Tests (tollgate-module-basic-go)

| Test File | Test Functions | Status |
|---|---|---|
| `src/merchant/merchant_degraded_test.go` | 51 tests: stub operations, `OnUpgrade`, `Kickstart` wallet loading, `Shutdown`, `NewMerchantDegradedFromFull`, interface compliance | **Existing** |
| `src/merchant/offline_wallet_integration_test.go` | 9 tests: first boot offline, offline reload, degraded merchant, recovery+upgrade, full lifecycle E2E, BoltDB lock release | **Existing** (build-tagged `integration`) |
| `src/merchant/mint_health_tracker_test.go` | `TestOnReachableSetChanged_*` (5 tests) | **Existing** |

#### API Integration Tests (physical-router-test-automation)

| Test File | Test Functions | Status |
|---|---|---|
| `tests/api/test_degraded_mode.py` | `test_service_health_while_mints_reachable`, `test_cli_health_shows_mint_status`, `test_block_all_mints_service_stays_up`, `test_degraded_mode_returns_retry_notice`, `test_mint_recovery_after_unblock`, `test_block_one_mint_others_still_work`, `test_degraded_mode_notice_event_content`, `test_service_survives_restart_in_degraded`, `test_dynamic_downgrade_without_restart`, `test_dynamic_reupgrade_full_lifecycle`, `test_boltdb_lock_release_on_swap` (11 tests) | **Existing** |
| `tests/api/test_cli_degraded_operations.py` | CLI wallet commands in degraded mode (drain/fund/info/balance return structured errors) | **Existing** |
| `tests/api/test_merchant_provider.py` | Provider propagation after swap, concurrent requests during swap, HTTP endpoint coverage | **Existing** |
| `tests/api/test_recovery_lifecycle.py` | `test_multiple_recovery_cycles`, `test_health_tracker_alive_after_recovery`, `test_flapping_mint_hysteresis` (3 tests) | **Existing** |
| `tests/api/test_concurrent_payments.py` | Concurrent requests during merchant swap | **Existing** |

**New pytest requirements:** None — coverage is comprehensive.

#### Playwright Tests
None at this stage — UI changes come in PR F.

#### End-to-End on Physical Routers

| Test File | Test Functions | Status |
|---|---|---|
| `tests/scenarios/test_mint_health.py` | `test_block_mint_via_hosts`, `test_restart_into_degraded_mode`, `test_offline_wallet_operations`, `test_unblock_mint`, `test_recovery_to_full_merchant`, `test_full_degraded_lifecycle`, `test_first_boot_offline`, `test_no_configured_mints` (8 tests) | **Existing** |
| `tests/scenarios/test_boot_hygiene.py` | `test_degraded_recovery_no_restart`, `test_dynamic_merchant_rebuild`, `test_wallet_preserved_through_degraded_cycle` (3 tests) | **Existing** |

| Makefile Target | What It Verifies |
|---|---|
| `r-smoke-degraded` | Full lifecycle: setup → fund → block → degraded → unblock → recover |
| `r-smoke-dynamic-rebuild` | Full→degraded→recovery→full→degraded-again cycle |
| `r-test-first-boot-offline` | Fresh boot with no mints → degraded mode |
| `r-test-no-mints` | No configured mints → degraded mode with stubs |
| `r-wait-recovery` | Automatic recovery detection |

### Merge Gate
- [ ] `go test ./src/merchant/... -count=1` passes (51 degraded + 9 integration + 29 tracker + 9 provider = 98 tests)
- [ ] `test_degraded_mode.py` all 11 tests pass on hardware
- [ ] `test_recovery_lifecycle.py` all 3 tests pass
- [ ] `test_mint_health.py` (scenarios) full degraded lifecycle passes on both routers
- [ ] Manual: `r-smoke-degraded` on both alpha and beta

---

## PR F — Captive Portal Degraded-Mode UI

### Why Needed
When mints are unreachable, the captive portal must show an error message, display a retry indicator, and hide payment input tabs. This is the user-facing half of degraded mode — without it, users see a broken or confusing portal during mint outages.

### Scope

| File | Change |
|---|---|
| `packaging/files/tollgate-captive-portal-site/assets/index-*.js` | Bundle rebuild (zero-net line change — replaces old bundle) |
| `packaging/files/tollgate-captive-portal-site/assets/index-*.css` | Bundle rebuild |
| `packaging/files/tollgate-captive-portal-site/splash.html` | Updated asset references, apple-touch-icon |
| `packaging/files/tollgate-captive-portal-site/balance.html` | Updated asset references |
| `packaging/files/tollgate-captive-portal-site/locales/en.json` | Added `retrying`, `TG005_*`, `no-reachable-mints_*` strings |
| `packaging/files/tollgate-captive-portal-site/404.html` | Renamed from `index.html` |
| `src/000_main_test_env.go` | Build tag `testenv` |

**Lines changed:** ~0 net (bundle rebuild) + ~15 lines config/locales

### Depends On
- **PR E** (merchant returns degraded-mode advertisement `kind:21023` that the UI reads)

### Test Coverage

#### Go Unit Tests
None needed — this is a frontend bundle rebuild.

#### API Integration Tests (physical-router-test-automation)

| Test File | Test Functions | Status |
|---|---|---|
| `tests/api/test_captive_api.py` | Captive portal API endpoint tests | **Existing** |

**New pytest requirements:** None — the API behavior is unchanged.

#### Playwright Tests (physical-router-test-automation)

| Test File | Test Functions | Status |
|---|---|---|
| `tests/protocol/captive-portal.spec.mjs` | `shows error message when mints unreachable`, `shows retrying indicator`, `hides payment inputs` (3 degraded-mode tests), `cashu tab has no bare "0"`, `lightning tab has no bare "0"`, `API returns valid advertisement`, `portal shows cashu token input`, `portal shows lightning amount input`, `portal shows mint selection pricing buttons` (9 total tests) | **Existing** |
| `tests/browser/captive_portal.spec.mjs` | `splash page loads`, `payment form element`, `connect or pay button` (3 tests) | **Existing** |
| `tests/scenarios/test_captive_portal_browser.py` | `test_portal_no_bare_zero_literals`, `test_api_returns_valid_advertisement`, `test_degraded_mode_shows_error`, `test_degraded_mode_hides_payment`, `test_portal_has_cashu_input`, `test_portal_has_lightning_input`, `test_portal_has_mint_options` (7 tests) | **Existing** |

#### Phone Tests (physical-router-test-automation)

| Test File | Test Functions | Status |
|---|---|---|
| `tests/phone/test_degraded_mode.py` | `test_phone_portal_visible_during_degraded_mode`, `test_phone_can_pay_after_mint_recovery` (2 tests) | **Existing** |

#### End-to-End on Physical Routers

| Makefile Target | What It Verifies |
|---|---|
| `r-test-captive-portal` | Full Playwright captive portal test suite |
| `r-test-captive-portal-happy` | Happy-path portal tests (requires merchant mode) |
| `r-test-cashu-payment` | Mint token → portal → submit → checkmark |

### Merge Gate
- [ ] Playwright `captive-portal.spec.mjs` all 9 tests pass
- [ ] Playwright `captive_portal.spec.mjs` (browser) all 3 tests pass
- [ ] `test_captive_portal_browser.py` all 7 tests pass
- [ ] `test_degraded_mode.py` (phone) both tests pass
- [ ] Manual: `r-test-cashu-payment` — full e2e payment works

---

## PR G — CLI SSL Rewrite + SSL Wrapper Scripts

### Why Needed
SSL certificate management was previously shell scripts. This PR rewrites it in Go (`tollgate ssl apply/remove/status`) for reliability, cross-platform support, and testability. The shell wrapper scripts (`tollgate-apply-ssl`, `tollgate-remove-ssl`) provide backward compatibility. **This feature is unrelated to mint health tracking** and should not be bundled with PR E/F.

### Scope

| File | Change |
|---|---|
| `src/cmd/tollgate-cli/ssl.go` | **New** — Go SSL management (850 lines) |
| `src/cli/go.mod` | Updated deps |
| `src/cli/go.sum` | Updated deps |
| `src/cli/server.go` | SSL subcommand registration |
| `src/cli/network.go` | Network utilities for SSL |
| `src/cli/merchant_provider_test.go` | **New** — 4 provider tests |
| `packaging/files/usr/bin/tollgate-apply-ssl` | **New** — shell wrapper |
| `packaging/files/usr/bin/tollgate-remove-ssl` | **New** — shell wrapper |

**Lines changed:** ~1,700 lines

### Depends On
- **PR B** (merchant_types import for CLI server)

### Test Coverage

#### Go Unit Tests (tollgate-module-basic-go)

| Test File | Test Functions | Status |
|---|---|---|
| `src/cli/server_test.go` | 8 tests for CLI commands | **Existing** |
| `src/cli/merchant_provider_test.go` | 4 provider tests | **Existing** |
| `src/cmd/tollgate-cli/ssl_test.go` | **NEW** — `TestSSLApply_SelfSigned`, `TestSSLRemove`, `TestSSLStatus`, `TestSSLIdempotent` | **GAP — must create** |

**New Go test requirements:**
- Self-signed cert generation and validation (SAN, CN, expiry)
- Remove cleans up cert, key, backup
- Status reports correct state
- Idempotent apply produces same cert

#### API Integration Tests (physical-router-test-automation)

| Test File | Test Functions | Status |
|---|---|---|
| `tests/api/test_hostname.py` | `test_hostname_set_to_tollgate`, `test_hostname_persists_after_restart`, `test_https_not_configured_by_default`, `test_no_cert_files_by_default`, `test_https_port_not_listening_by_default`, `test_tollgate_setup_ssl_exists`, `test_tollgate_setup_ssl_enables_https`, `test_tollgate_setup_ssl_cert_generated`, `test_tollgate_setup_ssl_https_port_listening`, `test_setup_hostname_function_exists`, `test_hostname_only_set_when_default`, `test_no_auto_https_in_uci_defaults` (12 tests) | **Existing** |

**New pytest requirements:**
- `tests/api/test_ssl_cli.py` — **NEW FILE** — pytest equivalent of Makefile SSL tests:
  - `test_ssl_apply_self_signed`
  - `test_ssl_status_shows_cert`
  - `test_ssl_removes_cleanly`
  - `test_ssl_wrapper_scripts_work`
  - `test_ssl_idempotent_apply`
  - `test_ssl_reapply_overwrites`
  - `test_ssl_remove_no_backup_errors`
  - `test_ssl_cert_san_cn_expiry`
  - `test_ssl_nodogsplash_port_443`

#### Playwright Tests
None needed — SSL is CLI/config-level.

#### End-to-End on Physical Routers

| Makefile Target | What It Verifies |
|---|---|
| `r-test-ssl-self-signed` | Apply self-signed cert via Go CLI |
| `r-test-ssl-remove` | Remove SSL via Go CLI |
| `r-test-ssl-status` | Status command output |
| `r-test-ssl-full` | Full lifecycle: apply → verify → remove → verify cleanup |
| `r-test-ssl-comprehensive` | All self-signed tests in sequence (11 sub-targets) |
| `r-test-ssl-wrappers` | Wrapper scripts `tollgate-apply-ssl` / `tollgate-remove-ssl` |
| `r-test-ssl-idempotent` | Apply twice — state consistent |
| `r-test-hostname` | Hostname set correctly |

### Merge Gate
- [ ] `go test ./src/cli/...` passes
- [ ] `test_hostname.py` all 12 tests pass
- [ ] `r-test-ssl-comprehensive` passes on hardware (11 sub-targets)
- [ ] New `test_ssl_cli.py` pytest file passes (9 tests)

---

## PR H — WGM Improvements (Already Merged)

### Status
**MERGED** via [PR #122](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/122) (`fix/wgm-improvements`).

### What It Brought
- TollGate-aware upstream detection (probe `gateway:2121`)
- Extended connectivity-loss patience (6 checks vs default 2)
- Cross-radio DHCP nudge (dual-trigger `ifup wwan`)
- Startup connectivity hygiene (emergency scan+switch on dead STA)

### Remaining in PR #118
Only 4 lines of whitespace formatting in `upstream_manager.go` — should be dropped as a trivial formatting fix.

### Test Coverage (Already on `main`)

| Test File | Test Functions | Status |
|---|---|---|
| `src/wireless_gateway_manager/upstream_manager_test.go` | 43 tests | **Existing on main** |
| `src/wireless_gateway_manager/connector_test.go` | 22 tests | **Existing on main** |
| `src/wireless_gateway_manager/scanner_test.go` | 10 tests | **Existing on main** |
| `tests/scenarios/test_upstream_wifi.py` | 10 tests | **Existing** |
| `tests/scenarios/test_recovery.py` | 5 tests | **Existing** |
| `tests/scenarios/test_two_router.py` | 5 tests (3 classes) | **Existing** |

---

## PR I — CI/Packaging/Test Infrastructure

### Why Needed
Build tag fixes (`testenv`), config path integration tests, default mint configuration validation, and CI workflow updates. These are orthogonal to the core feature logic but necessary for the test infrastructure to work correctly.

### Scope

| File | Change |
|---|---|
| `.github/workflows/build-package.yml` | CI workflow pinning |
| `.gitignore` | Updated ignore patterns |
| `packaging/Makefile` | Build target updates |
| `packaging/files/etc/uci-defaults/99-tollgate-setup` | Setup script changes |
| `packaging/scripts/build-sdk-package.sh` | Build script fix |
| `src/000_main_test_env.go` | Build tag `testenv` |
| `src/config_path_integration_test.go` | **New** — 6 config path tests |
| `src/main_test.go` | Updated with path tests |
| `src/go.mod` | Test deps |

**Lines changed:** ~300 lines

### Depends On
- **PR A** (build tag fix depends on transport config being in `init()`)

### Test Coverage

#### Go Unit Tests (tollgate-module-basic-go)

| Test File | Test Functions | Status |
|---|---|---|
| `src/config_path_integration_test.go` | `TestConfigPathIntegration_ConfigManagerReadsFromExactFile`, `TestConfigPathIntegration_ProductionPathWhenEnvUnset`, `TestConfigPathIntegration_TestDirPathWhenEnvSet`, `TestConfigPathIntegration_DefaultConfigMatchesWrittenFile`, `TestConfigPathIntegration_NoTestMintOnMainBranch`, `TestConfigPathIntegration_TestMintOnFeatureBranch` (6 tests) | **Existing** |
| `src/main_test.go` | `TestGetTollgatePaths_Default`, `TestGetTollgatePaths_TestEnv`, `TestGetTollgatePaths_TestEnvUnset` (3 tests) | **Existing** |
| `src/config_manager/buildinfo_test.go` | 4 tests | **Existing** |
| `src/e2e_test.go` | 7 tests | **Existing** |

#### API Integration Tests
None needed — infrastructure-only.

#### Playwright Tests
None needed.

#### End-to-End on Physical Routers
- Build and deploy binary → verify all smoke tests pass

### Merge Gate
- [ ] `go test ./... -tags testenv` passes
- [ ] `go build ./...` produces working binary
- [ ] Binary deploys to router and smoke tests pass

---

## Test Coverage Gaps — Summary of New Tests Required

### Go Tests to Create (tollgate-module-basic-go)

| File | Tests | For PR |
|---|---|---|
| `src/transport_test.go` | TLS 1.2 MaxVersion, timeout config, HTTP/2 disabled | PR A |
| `src/tollwallet/sentinel_test.go` | `ErrTokenAlreadySpent` wrapping, `errors.Is`, `Shutdown` | PR D |
| `src/merchant_types/types_test.go` | Interface compliance, concurrent access | PR B |
| `src/cmd/tollgate-cli/ssl_test.go` | SSL apply/remove/status/idempotent | PR G |

### Python Tests to Create (physical-router-test-automation)

| File | Tests | For PR |
|---|---|---|
| `tests/api/test_tls_transport.py` | TLS version negotiated, mint API latency regression | PR A |
| `tests/api/test_ssl_cli.py` | 9 SSL CLI tests (pytest equivalent of Makefile targets) | PR G |
| `tests/api/test_sentinel_error.py` | Duplicate token returns structured already-spent error | PR D |

### Playwright Tests to Create (physical-router-test-automation)
All existing Playwright coverage is adequate. No new spec files needed.

---

## Complete Test Matrix by PR

| PR | Go Unit | Go Integration | pytest API | pytest Scenarios | Playwright Protocol | Playwright Browser | Phone Tests | Makefile Targets |
|---|---|---|---|---|---|---|---|---|
| A | 3+3 new | — | 4 existing + 1 new | — | — | — | — | r-check-merchant |
| B | 7 existing + 3 new | — | Full regression | — | — | — | — | — |
| C | 29+9+4+3 = 45 | — | 6+2+2+2+7 = 19 | — | — | — | — | r-check-merchant, r-block/unblock, r-restart, r-wait-recovery |
| D | 5 existing + 4 new | — | 1 existing + 1 new | — | — | — | — | r-test-cashu-payment |
| E | 51+9+5 = 65 | 9 integration | 11+4+3+1 = 19 | 8+3 = 11 | — | — | — | r-smoke-degraded, r-smoke-dynamic-rebuild, r-test-first-boot-offline, r-test-no-mints |
| F | — | — | — | 7 | 9 | 3 | 2 | r-test-captive-portal, r-test-cashu-payment |
| G | 8+4 existing + 4 new | — | 12 existing + 9 new | — | — | — | — | r-test-ssl-comprehensive (11 sub-targets), r-test-hostname |
| H | 43+22+10 = 75 | — | 10+5+5 = 20 | — | — | — | — | (already merged) |
| I | 6+3+4+7 = 20 | — | — | — | — | — | — | (CI only) |

---

## Recommended Merge Order and Workflow

```
1. PR A (TLS)           ← day 1, no blockers
2. PR B (merchant_types) ← day 1, depends on A
3. PR I (CI/infra)       ← day 1-2, parallel with B
4. PR C (health tracker) ← day 2-3, depends on A+B
5. PR G (SSL rewrite)    ← day 2-3, parallel with C (depends on B only)
6. PR D (sentinel)       ← day 3, depends on C
7. PR E (degraded mode)  ← day 3-5, depends on B+C+D — THE CORE FEATURE
8. PR F (portal UI)      ← day 5, depends on E
```

### Pre-Merge Checklist (each PR)
- [ ] Branch created from latest `main`
- [ ] Only files listed in scope are modified
- [ ] All new Go tests pass (`go test ./...`)
- [ ] All existing pytest tests pass (no regressions)
- [ ] New pytest tests pass on physical hardware
- [ ] Playwright tests pass (where applicable)
- [ ] Makefile hardware targets pass (where applicable)
- [ ] No stray markdown files at repo root
- [ ] PR description references this decomposition plan

---

## Appendix A: Existing Test File Inventory

### Go Tests (tollgate-module-basic-go/src/)

| File | # Tests |
|---|---|
| `merchant/mint_health_tracker_test.go` | 29 |
| `merchant/merchant_degraded_test.go` | 51 |
| `merchant/merchant_provider_test.go` | 9 |
| `merchant/offline_wallet_integration_test.go` | 9 |
| `merchant/session_test.go` | 3 |
| `merchant/lightning_test.go` | 2 |
| `upstream_session_manager/merchant_provider_test.go` | 7 |
| `config_manager/buildinfo_test.go` | 4 |
| `config_manager/config_manager_test.go` | 3 |
| `main_test.go` | 14 |
| `config_path_integration_test.go` | 6 |
| `e2e_test.go` | 7 |
| `tollwallet/tollwallet_test.go` | 5 |
| `cli/server_test.go` | 8 |
| `cli/merchant_provider_test.go` | 4 |
| `utils/utils_test.go` | 1 |
| `wireless_gateway_manager/upstream_manager_test.go` | 43 |
| `wireless_gateway_manager/scanner_test.go` | 10 |
| `wireless_gateway_manager/connector_test.go` | 22 |
| `wireless_gateway_manager/reseller_mode_test.go` | 1 |
| **Total** | **238** |

### Python Tests (physical-router-test-automation/)

| File | # Tests |
|---|---|
| `tests/api/test_degraded_mode.py` | 11 |
| `tests/api/test_mint_health.py` | 6 |
| `tests/api/test_try_all_mints.py` | 2 |
| `tests/api/test_merchant_provider.py` | ~8 |
| `tests/api/test_cli_degraded_operations.py` | ~6 |
| `tests/api/test_recovery_lifecycle.py` | 3 |
| `tests/api/test_concurrent_payments.py` | ~3 |
| `tests/api/test_mint_502_fake.py` | ~2 |
| `tests/api/test_mint_502_handling.py` | 7 |
| `tests/api/test_discovery_mints.py` | 2 |
| `tests/api/test_health.py` | 4 |
| `tests/api/test_edge_tokens.py` | 4 |
| `tests/api/test_e2e_portal_payment.py` | 1 |
| `tests/api/test_hostname.py` | 12 |
| `tests/api/test_captive_api.py` | ~4 |
| `tests/scenarios/test_mint_health.py` | 8 |
| `tests/scenarios/test_boot_hygiene.py` | 9 |
| `tests/scenarios/test_upstream_wifi.py` | 10 |
| `tests/scenarios/test_recovery.py` | 5 |
| `tests/scenarios/test_two_router.py` | 5 |
| `tests/scenarios/test_captive_portal_browser.py` | 7 |
| `tests/scenarios/test_reseller_mode.py` | 5 |
| `tests/phone/test_degraded_mode.py` | 2 |

### Playwright Specs (physical-router-test-automation/)

| File | # Tests |
|---|---|
| `tests/protocol/captive-portal.spec.mjs` | 9 |
| `tests/browser/captive_portal.spec.mjs` | 3 |
| `tests/captive-portal.spec.mjs` (root) | ~9 |
| `tests/protocol/tollgate-payment-protocol.spec.mjs` | 1 |
| `tests/protocol/data-allotment.spec.mjs` | ~5 |
| `tests/protocol/payment-lifecycle.spec.mjs` | ~5 |
| `tests/protocol/router-network-config.spec.mjs` | ~5 |

### Makefile Hardware Targets (physical-router-test-automation/mint-health/Makefile)

3089 lines with ~100 test targets covering:
- Mint health: `r-smoke-degraded`, `r-smoke-dynamic-rebuild`, `r-test-first-boot-offline`, `r-test-no-mints`, `r-wait-recovery`
- Captive portal: `r-test-captive-portal`, `r-test-captive-portal-happy`, `r-test-cashu-payment`
- SSL: `r-test-ssl-*` (15 targets)
- Upstream WiFi: `r-scan`, `r-list`, `r-connect`, `r-test-edge-cases`, `r-test-cleanup`
- Two-router: `r-smoke-degraded-upstream`, `r-smoke-pin-upstream`
- Boot: `r-test-startup-hygiene`, `r-test-startup-hygiene-dead-only`
- Utilities: `r-fund-wallet`, `r-deploy`, `r-cleanup`, `r-block-mint`, `r-unblock-mint`

---

## Appendix B: GitHub Links

### Go Repo (tollgate-module-basic-go)
- PR #118: https://github.com/OpenTollGate/tollgate-module-basic-go/pull/118
- PR #120 (merged): https://github.com/OpenTollGate/tollgate-module-basic-go/pull/120
- PR #122 WGM (merged): https://github.com/OpenTollGate/tollgate-module-basic-go/pull/122

### Test Automation Repo (physical-router-test-automation)
- Issue #14 (test coverage): https://github.com/OpenTollGate/physical-router-test-automation/issues/14
- `tests/api/`: https://github.com/OpenTollGate/physical-router-test-automation/tree/main/tests/api
- `tests/scenarios/`: https://github.com/OpenTollGate/physical-router-test-automation/tree/main/tests/scenarios
- `tests/protocol/`: https://github.com/OpenTollGate/physical-router-test-automation/tree/main/tests/protocol
- `mint-health/Makefile`: https://github.com/OpenTollGate/physical-router-test-automation/blob/main/mint-health/Makefile

### Infrastructure Repo (tollgate-infrastructure-kit)
- act-runner: https://github.com/OpenTollGate/tollgate-infrastructure-kit/tree/main/act-runner
- CI playbook: https://github.com/OpenTollGate/tollgate-infrastructure-kit/blob/main/ansible/playbooks/27-act-runner.yml
