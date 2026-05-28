# Mock Removal Plan — Replace Mocked Tests with End-to-End Tests

## Principle

Tests that interact with external systems (UCI, wallet, mint APIs, SSL certs) should be
end-to-end pytest tests on physical routers, not Go unit tests with mocked dependencies.
Go unit tests should only cover pure functions (no I/O, no external calls).

## Audit Results

### Tests to KEEP (pure functions, no mocking)

These Go unit tests test pure logic with no external dependencies:

**PR G — `ssl_test.go` (14 tests):**
- [x] TestExtractDomain (4 sub-tests) — pure certificate parsing
- [x] TestFilterLines (4 sub-tests) — string filtering
- [x] TestListContains (4 sub-tests) — slice membership
- [x] TestSplitCombinedPEM (6 sub-tests) — PEM decoding + filesystem
- [x] TestWritePEM (2 sub-tests) — file write
- [x] TestFileRead (3 sub-tests) — file read
- [x] TestCopyFile (2 sub-tests) — file copy
- [x] TestWriteBackupFile (2 sub-tests) — file write
- [x] TestSSLInstallCerts — filesystem copy + permissions
- [x] TestCleanupStaleTempDirs — temp dir cleanup
- [x] TestSSLStatus_NotConfigured — filesystem check
- [x] TestConfirmOrYes_Flag — boolean logic

**PR E — `merchant_degraded_test.go` (~30 tests with mockWallet):**
- [x] KEEP — tests edge cases of MerchantDegraded internal state transitions
  (wallet loaded vs not loaded, balance queries, overpayment, shutdown, swap lifecycle)
  Key behaviors already covered by e2e test_degraded_mode.py. Unit tests remain as
  fast regression for edge cases.

**PR E/CD — `merchant_provider_test.go` (9 tests with mockMerchantForProvider):**
- [x] KEEP — testing MutexMerchantProvider concurrency primitive.
  Mocking is appropriate for testing a synchronization wrapper.

**PR E/CD — `mint_health_tracker_test.go` (29 tests with mockConfigProvider + httptest):**
- [x] KEEP — httptest servers simulate mint HTTP endpoints for controlled probe testing.
  Equivalent e2e tests exist (iptables blocking in test_degraded_mode.py).

### Tests REMOVED (mocked external dependencies)

**PR G — 8 Tier 2/3 tests that mocked UCI commands via fnRunCommand/fnRunCommandChecked:**

- [x] Remove TestConfigureUhttpd — mocks `uci set`
- [x] Remove TestConfigureDnsmasq — mocks `uci set`
- [x] Remove TestConfigureNodogsplash — mocks `uci set`
- [x] Remove TestAllowPort443 — mocks `uci` firewall rules
- [x] Remove TestRemovePort443Allow — mocks `uci` firewall rules
- [x] Remove TestRestoreUhttpd_FromBackup — mocks `uci get`
- [x] Remove TestUCIGetList / TestUCIGetList_Empty — mocks `uci get`
- [x] Remove TestSSLBackup — mocks `uci` + file writes

### Production Code REVERTED

- [x] Revert `fnRunCommand` / `fnRunCommandChecked` / `fnAskConfirmation` function variables in `ssl.go`
- [x] Restore direct `exec.Command` calls in `runCommand` / `runCommandChecked`
- [x] Remove `defaultRunCommand` / `defaultRunCommandChecked` / `defaultAskConfirmation` wrappers
- [x] Keep `var` for sslDir, backupDir, certDest, keyDest (used by kept filesystem tests)

### E2E Pytest Tests WRITTEN (replacing removed mocks)

- [x] `test_ssl_apply_remove_lifecycle.py` (8 tests) — full self-signed SSL apply→verify→remove→verify-clean
  Replaces: TestConfigureUhttpd, TestAllowPort443, TestRemovePort443Allow,
  TestRestoreUhttpd_FromBackup mocks

- [x] `test_ssl_real_cert_lifecycle.py` (5 tests) — apply real cert PEM→verify dnsmasq+uhttpd+nodogsplash→remove→verify restored
  Replaces: TestConfigureDnsmasq, TestConfigureNodogsplash mocks

- [x] `test_ssl_backup_restore.py` (8 tests) — apply→verify backup dir contents→remove→verify backup cleaned
  Replaces: TestSSLBackup, TestUCIGetList mocks

### New Go Unit Tests WRITTEN (pure functions, no mocks)

- [x] `TestMerchant_SetOnReachableSetChanged_DelegatesToTracker` in PR CD
- [x] `TestMerchant_GetMintHealthTracker_ReturnsTracker` in PR CD
- [ ] `TestCreateNoticeEvent_NilIdentities` — SKIPPED: requires real config manager with identity files (not a pure function)

## Final State

| PR | Go Unit Tests (pure) | Mocked Go Tests (removed) | E2E Pytest (new) |
|---|---|---|---|
| A | 7 transport tests | 0 | 0 |
| B | 9 types tests | 0 | 0 |
| C+D | 12 health + 2 new | 0 | 0 |
| E | 3 wiring + ~30 degraded (mockWallet kept) | 0 | 0 |
| F | 0 | 0 | 7 Playwright |
| G | 14 pure (Tier 1 + kept Tier 2) | **-8 removed** | **+21 new pytest** |
| I | 0 | 0 | 5 pytest |

## Execution Order

1. ~~Create this plan document~~ DONE
2. ~~Revert fnRunCommand refactoring in ssl.go~~ DONE
3. ~~Delete 8 mocked tests from ssl_test.go~~ DONE
4. ~~Write 3 new e2e pytest files~~ DONE (21 tests total)
5. ~~Write 2 new Go unit tests~~ DONE
6. ~~Verify all Go tests pass, push changes~~ DONE
7. ~~Update PR comments and TEST-COVERAGE-PLAN.md~~ DONE
