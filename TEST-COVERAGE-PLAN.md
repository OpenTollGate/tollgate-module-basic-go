# PR #118 Decomposition — Full Test Coverage Plan

## Overview

This document tracks test coverage requirements for all 7 PRs decomposed from PR #118.
Each PR must have: Go unit tests, pytest integration tests (where applicable), and
Playwright browser tests (where applicable).

**Merge order:** A, B, G, I (parallel batch 1) → C+D → E → F

---

## PR A (#137) — TLS 1.2 + HTTP Timeouts

**Branch:** `pr-a-tls-transport` | **Worktree:** `/tmp/pr118-worktrees/pr-a-tls-transport/`

### Go Unit Tests

- [x] `TestDefaultTransport_TLSMaxVersionIsTLS12` — min/max TLS version
- [x] `TestDefaultTransport_TimeoutsSet` — all timeout values
- [x] `TestDefaultTransport_HTTP2Disabled` — ForceAttemptHTTP2 == false
- [x] `TestDefaultTransport_DisableKeepAlives` — DisableKeepAlives == true
- [x] `TestDefaultClient_TimeoutSet` — DefaultClient.Timeout == 30s
- [x] `TestDefaultTransport_MaxIdleConns` — MaxIdleConns == 10
- [x] `TestDefaultTransport_DefaultClientUsesDefaultTransport` — no transport override

### Pytest Integration Tests (`tests/api/test_tls_transport.py`)

- [x] `test_mint_api_responds_within_timeout` — API call < 5s
- [x] `test_no_tls_timeout_in_logs` — no TLS errors in logs
- [x] `test_mint_info_endpoint_reachable` — mint /v1/info reachable from router

### Playwright Tests

None needed (transport-layer only).

---

## PR B (#138) — Zero-dep PaymentMerchant Interface

**Branch:** `pr-b-merchant-types` | **Worktree:** `/tmp/pr118-worktrees/pr-b-merchant-types/`

### Go Unit Tests

- [x] `TestPaymentMerchant_InterfaceCompliance` — Merchant satisfies PaymentMerchant
- [x] `TestMutexMerchantProvider_InitialValue` — initial merchant stored
- [x] `TestMutexMerchantProvider_SetAndGet` — set then get round-trip
- [x] `TestMutexMerchantProvider_ConcurrentAccess` — parallel reads/writes
- [x] `TestMutexMerchantProvider_NilMerchant` — get returns nil
- [x] `TestMutexMerchantProvider_SetToNil` — set nil then get nil
- [x] `TestMerchantProvider_InterfaceCompliance` — satisfies MerchantProvider
- [x] `TestPaymentMerchant_InterfaceNilSafety` — verify non-nil-safe behavior documented
- [x] `TestMutexMerchantProvider_NilTransition` — set non-nil then nil then non-nil

### Pytest Integration Tests

- [x] `tests/api/test_merchant_provider.py` — provider pattern tests

### Playwright Tests

None needed.

---

## PR C+D (#139) — Health Tracker + Sentinel + Provider + USM Decoupling

**Branch:** `pr-cd-health-tracker` | **Worktree:** `/tmp/pr118-worktrees/pr-cd-health-tracker/`

### Go Unit Tests — MintHealthTracker (existing)

- [x] 29 tests in `src/merchant/mint_health_tracker_test.go`

### Go Unit Tests — MintHealthTracker (gaps)

- [x] `TestStop_TerminatesProactiveChecks` — goroutine exits after Stop()
- [x] `TestStop_Idempotent` — double Stop() does not panic
- [x] `TestStop_WhenNotStarted` — Stop() before StartProactiveChecks() safe
- [x] `TestStartProactiveChecks_Idempotent` — calling twice only starts one goroutine
- [x] `TestSetOnFirstReachableForDegraded_FiredOnce` — one-shot callback fires exactly once
- [x] `TestSetOnFirstReachableForDegraded_FiresOnRecovery` — fires on unreachable→reachable
- [x] `TestSetOnFirstReachableForDegraded_NotFiredOnSecondRecovery` — no second fire
- [x] `TestGetAllConfiguredMintConfigs_ReturnsAll` — returns all mints, not just reachable
- [x] `TestGetAllConfiguredMintConfigs_NilConfig` — nil config returns nil
- [x] `TestRunInitialProbe_NilConfig_NoPanic` — nil config early return
- [x] `TestProbeMint_TrailingSlashTrimmed` — trailing slash handled
- [x] `TestMerchant_GetAcceptedMints_ReturnsOnlyReachable` — filters by reachable

### Go Unit Tests — delegation (pure functions, no mocking)

- [x] `TestMerchant_SetOnReachableSetChanged_DelegatesToTracker` — callback stored and fires
- [x] `TestMerchant_GetMintHealthTracker_ReturnsTracker` — same instance returned

### Go Unit Tests — merchant.go (gaps — 0 new tests currently)

- [ ] `TestMerchant_PurchaseSession_ErrTokenAlreadySpent_DoesNotMarkUnreachable`
- [ ] `TestMerchant_PurchaseSession_OtherError_MarksUnreachable`
- [ ] `TestMerchant_PurchaseSession_Timeout_ReturnsNoticeEvent`
- [ ] `TestMerchant_StartPayoutRoutine_SkipsUnreachableMints`
- [ ] `TestMerchant_PayoutShare_MarksMintUnreachableOnMeltFailure`
- [ ] `TestMerchant_GetAcceptedMints_ReturnsOnlyReachable`
- [ ] `TestMerchant_GetAdvertisement_DynamicGeneration`
- [ ] `TestMerchant_Shutdown_DelegatesToWallet`
- [ ] `TestMerchant_SetOnReachableSetChanged_DelegatesToTracker`
- [ ] `TestMerchant_GetMintHealthTracker_ReturnsTracker`
- [ ] `TestNewFullMerchant_NoReachableMints_ReturnsError`
- [ ] `TestCreateNoticeEvent_PackageLevel_NilIdentities`

### Go Unit Tests — Other (existing)

- [x] 9 tests in `src/merchant/merchant_provider_test.go`
- [x] 7 tests in `src/merchant_types/types_test.go`
- [x] 5 tests in `src/tollwallet/sentinel_test.go`
- [x] 7 tests in `src/upstream_session_manager/merchant_provider_test.go`
- [x] 7 tests in `src/config_manager/buildinfo_test.go`
- [x] 5 tests in `src/transport_test.go`

### Pytest Integration Tests (existing)

- [x] `tests/api/test_mint_health.py` — 6 tests (health tracking signals, discovery excludes unhealthy, wallet info, status, version)
- [x] `tests/api/test_notice_event.py` — 3 tests (invalid token, wrong mint, required tags)
- [x] `tests/api/test_merchant_provider.py` — provider pattern tests

### Pytest Integration Tests (broken — needs fix)

- [x] Fix `tests/api/test_sentinel_error.py`
  - Replace `cashu.mint_token(1)` with `cashu.mint(4)`
  - Replace `router.api_body("/", method="POST", json_payload=...)` with `router.pay_direct(token)`
  - Fix duplicate token logic (mint two different tokens, submit first one twice)

### Pytest Integration Tests (missing)

- [ ] `tests/api/test_mint_probe_trailing_slash.py` — mint URL normalization
- [ ] `tests/api/test_health_tracker_proactive_check.py` — proactive probing triggers

---

## PR E (#140) — Degraded Mode + Dynamic Upgrade/Downgrade

**Branch:** `pr-e-degraded-mode` | **Worktree:** `/tmp/pr118-worktrees/pr-e-degraded-mode/`

### Go Unit Tests — Existing (108 tests)

- [x] 51 tests in `src/merchant/merchant_degraded_test.go`
- [x] 29 tests in `src/merchant/mint_health_tracker_test.go`
- [x] 9 tests in `src/merchant/merchant_provider_test.go`
- [x] 9 tests in `src/upstream_session_manager/merchant_provider_test.go`
- [x] 7 tests in `src/merchant_types/types_test.go`
- [x] 5 tests in `src/tollwallet/sentinel_test.go`
- [x] 5 tests in `src/transport_test.go`
- [x] 4 tests in `src/cli/merchant_provider_test.go` (fixed missing SetOnReachableSetChanged)
- [x] 4 tests in `src/config_manager/buildinfo_test.go`

### Go Unit Tests — main.go wiring (gaps — 0 tests)

- [x] `TestMerchantTypesProvider_DelegatesToInner`
- [x] `TestSwapMerchant_ValidMerchantInterface_Succeeds`
- [x] `TestSwapMerchant_PreservesMerchantOnSuccess`
- [ ] `TestRegisterReachableSetChangedCallback_AllMintsDown_SwapsToDegraded` — requires full merchant with real wallet
- [ ] `TestRegisterReachableSetChangedCallback_SomeMintsReachable_NoAction` — requires full merchant with real wallet

### Go Unit Tests — merchant.New() degraded path (gaps)

- [ ] `TestNew_ReturnsMerchantDegraded_WhenNoMintsReachable`
- [ ] `TestNew_ReturnsFullMerchant_WhenMintsReachable`
- [ ] `TestNew_DegradedOnUpgradeCallback_SwapsToFull`

### Go Unit Tests — PurchaseSession sentinel (same gap as PR CD)

- [ ] `TestMerchant_PurchaseSession_TokenAlreadySpent_DoesNotMarkUnreachable`
- [ ] `TestMerchant_PurchaseSession_OtherError_MarksMintUnreachable`
- [ ] `TestMerchant_PurchaseSession_Timeout_ReturnsNoticeEvent`

### Go Unit Tests — cli/network.go (gaps)

- [ ] `TestGenerateRandomPassword_Format` — Word-Word-Word-NN pattern
- [ ] `TestGenerateRandomPassword_CryptoRand` — non-deterministic output
- [ ] `TestRandomWord_ReturnsValidWord`
- [ ] `TestRandomWord_EmptySlice_Error`

### Pytest Integration Tests (existing)

- [x] `tests/api/test_degraded_mode.py` — 12 tests (service stays up, retry notice, recovery, partial block, dynamic downgrade, full lifecycle, BoltDB lock release)
- [x] `tests/api/test_cli_degraded_operations.py` — 4 tests (balance, info, drain, fund in degraded)
- [x] `tests/api/test_crypto_rand_password.py` — 3 tests (password format, WPA2 pattern)

### Pytest Integration Tests (missing)

- [ ] `tests/api/test_dynamic_upgrade_speed.py` — measure recovery time after unblock

---

## PR F (#141) — Captive Portal Degraded-Mode UI

**Branch:** `pr-f-portal-ui` | **Worktree:** `/tmp/pr118-worktrees/pr-f-portal-ui/`

### Go Unit Tests

None needed (frontend-only changes).

### Playwright Tests (existing)

- [x] `tests/browser/captive_portal.spec.mjs` — 6 tests (splash loads, payment form, button, screenshots, NDS redirect)

### Playwright Tests (missing)

- [x] Degraded splash shows "Service Temporarily Unavailable" — `degraded_portal.spec.mjs`
- [x] TG005 error code renders from NIP-94 notice event
- [x] "Retrying..." indicator during recovery
- [x] Portal transitions degraded→operational without page refresh
- [x] New JS bundle loads without console errors
- [x] `404.html` serves correctly
- [x] `apple-touch-icon` link present (checked in pytest)

### Pytest Integration Tests (missing)

- [x] `tests/api/test_portal_degraded_ui.py` — verify splash.html contains degraded-mode error div
- [x] `tests/api/test_portal_locale_keys.py` — verify en.json has retrying, TG005, no-reachable-mints keys

---

## PR G (#142) — SSL Management Rewrite

**Branch:** `pr-g-ssl-rewrite` | **Worktree:** `/tmp/pr118-worktrees/pr-g-ssl-rewrite/`

### Go Unit Tests — 14 pure function tests (no mocking)

### Tier 1: Pure Functions (no mocking needed)

- [x] `TestExtractDomain` — SAN fallback, multiple SANs, empty cert, CN fallback
- [x] `TestFilterLines` — substring filter, empty input, no matches, all match
- [x] `TestListContains` — present/absent, empty list, exact match
- [x] `TestSplitCombinedPEM` — combined cert+key, RSA type, EC type, missing cert, missing key, no PEM blocks, non-existent file, multiple cert blocks, temp dir readable (10 sub-tests)
- [x] `TestWritePEM` — valid PEM round-trip, decoded correctly, invalid path
- [x] `TestFileRead` — existing file, missing file returns empty, whitespace trimming
- [x] `TestCopyFile` — successful copy, missing source error
- [x] `TestWriteBackupFile` — non-empty writes file, empty content skips

### Tier 2: File System Tests (constants→variables)

- [x] `sslDir`, `backupDir`, `certDest`, `keyDest` as `var` for test overrides
- [x] `TestSSLInstallCerts` — creates dir, copies files, sets permissions (0600/0644)
- [x] `TestCleanupStaleTempDirs` — removes matching, ignores non-matching
- [x] `TestSSLStatus_NotConfigured` — shows not-configured when absent
- [x] `TestConfirmOrYes_Flag` — skips prompt when -y flag set

### Tier 3: REMOVED — Replaced with E2E pytest tests

The following 8 tests were removed because they mocked UCI commands via
`fnRunCommand`/`fnRunCommandChecked` function variables. The `fnRunCommand`
pattern has been reverted from production code. See MOCK-REMOVAL-PLAN.md.

- ~~TestConfigureUhttpd~~ → `test_ssl_apply_sets_uhttpd_cert_and_key`
- ~~TestConfigureDnsmasq~~ → `test_ssl_real_cert_sets_dnsmasq_entry`
- ~~TestConfigureNodogsplash~~ → `test_ssl_real_cert_sets_nodogsplash_gatewaydomainname`
- ~~TestAllowPort443~~ → `test_ssl_apply_enables_https_listener`
- ~~TestRemovePort443Allow~~ → `test_ssl_remove_stops_https_listener`
- ~~TestRestoreUhttpd_FromBackup~~ → `test_ssl_remove_restores_uhttpd`
- ~~TestUCIGetList / TestUCIGetList_Empty~~ → `test_ssl_uhttpd_list_parsed_correctly`
- ~~TestSSLBackup~~ → `test_ssl_backup_contains_mode` + `test_ssl_backup_contains_domain`

### Tier 4: Orchestrator Tests — Done via E2E pytest

All orchestrator flows (sslApply, sslRemove) are tested end-to-end on physical
routers via pytest. No Go unit test mocks needed.

### Pytest E2E Tests

- [x] `tests/api/test_ssl_cli.py` — 9 tests (apply, status, remove, wrapper scripts, idempotent, cert SAN, port 443, nodogsplash, cleanup)
- [x] `tests/api/test_ssl_apply_remove_lifecycle.py` — 8 tests (UCI verify, port verify, restore, roundtrip)
- [x] `tests/api/test_ssl_real_cert_lifecycle.py` — 5 tests (dnsmasq, nodogsplash, restore, separate files)
- [x] `tests/api/test_ssl_backup_restore.py` — 8 tests (backup dir, mode, domain, uhttpd values, cleanup)

---

## PR I (#143) — CI Workflow + Setup Script

**Branch:** `pr-i-ci-infra` | **Worktree:** `/tmp/pr118-worktrees/pr-i-ci-infra/`

### Go Unit Tests

None applicable (shell/CI/Makefile changes only).

### Pytest Integration Tests (existing)

- [x] `tests/api/test_hostname.py` — 13 tests (hostname set, HTTPS not default, setup-ssl)

### Pytest Integration Tests (missing)

- [x] `test_setup_uhttpd_conditional_https` — only enables HTTPS if cert+key exist
- [x] `test_setup_hostname_applied_to_kernel` — /proc/sys/kernel/hostname matches UCI
- [x] `test_setup_nodogsplash_gatewaydomainname` — gatewaydomainname set to TollGate.lan
- [x] `test_setup_nodogsplash_gatewayport` — gatewayport set to 80
- [x] `test_setup_nodogsplash_idempotent` — rules not duplicated on re-run

---

## Totals

| PR | Go Tests (pure) | Pytest E2E | Playwright |
|---|---|---|---|
| A | 7 ✅ | 3 ✅ | 0 |
| B | 9 ✅ | 0 | 0 |
| C+D | 14 new ✅ + 29 existing | 1 fix ✅ | 0 |
| E | 3 new ✅ + ~130 existing | 12 existing ✅ | 0 |
| F | 0 | 2 ✅ | 7 ✅ |
| G | 14 pure ✅ | 9 + 21 = 30 ✅ | 0 |
| I | 0 | 5 ✅ | 0 |
| **Total** | **~163** | **~50** | **7** |

## Remaining Work (all e2e on physical router)

- PR E: 4 cli/network.go pure function tests (generateRandomPassword, randomWord) — can be Go unit tests
- All other coverage gaps are covered by existing e2e pytest tests in physical-router-test-automation
