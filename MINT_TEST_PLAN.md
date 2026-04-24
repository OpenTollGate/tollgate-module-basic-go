# Mint Health Tracker — Test Plan

## Checklist

### Automated Tests — Mint Health Tracker
- [x] 1. `TestIsReachable_InitiallyFalse`
- [x] 2. `TestIsReachable_UnknownMint`
- [x] 3. `TestRunInitialProbe_AllReachable`
- [x] 4. `TestRunInitialProbe_NoneReachable`
- [x] 5. `TestRunInitialProbe_MixedReachability`
- [x] 6. `TestRunInitialProbe_ServerRefusesConnection`
- [x] 7. `TestMarkUnreachable`
- [x] 8. `TestMarkUnreachable_ResetsConsecutiveSuccesses`
- [x] 9. `TestMarkUnreachable_UnknownMint_NoPanic`
- [x] 10. `TestProactiveCheck_RecoveryRequiresThreeConsecutiveSuccesses`
- [x] 11. `TestProactiveCheck_FailedProbeResetsConsecutiveCounter`
- [x] 12. `TestProactiveCheck_RemovesPreviouslyReachableMint`
- [x] 13. `TestProactiveCheck_FlapDoesNotRecoverMint`
- [x] 14. `TestProactiveCheck_NilConfig`
- [x] 15. `TestGetReachableMintConfigs_Empty`
- [x] 16. `TestGetReachableMintConfigs_OnlyReachable`
- [x] 17. `TestGetReachableMintConfigs_NilConfig`
- [x] 18. `TestEndToEnd_FullLifecycle`
- [x] 19. `TestEndToEnd_AllMintsDown_NoReachableConfigs`
- [x] 20. `TestEndToEnd_MintGoesDownThenRecoversWithInterruption`
- [x] 21. `TestConcurrentAccess`

### Automated Tests — Degraded Merchant
- [x] 22. `TestMerchantDegraded_CreatePaymentToken_ReturnsError`
- [x] 23. `TestMerchantDegraded_CreatePaymentTokenWithOverpayment_ReturnsError`
- [x] 24. `TestMerchantDegraded_DrainMint_ReturnsError`
- [x] 25. `TestMerchantDegraded_GetBalance_ReturnsZero`
- [x] 26. `TestMerchantDegraded_GetAcceptedMints_ReturnsAllConfigured`
- [x] 27. `TestMerchantDegraded_PurchaseSession_ReturnsNoticeEvent`
- [x] 28. `TestMerchantDegraded_GetAdvertisement_ReturnsNoticeJSON`
- [x] 29. `TestMerchantDegraded_StartPayoutRoutine_NoPanic`
- [x] 30. `TestMerchantDegraded_StartDataUsageMonitoring_NoPanic`
- [x] 31. `TestMerchantDegraded_GetSession_ReturnsError`
- [x] 32. `TestMerchantDegraded_AddAllotment_ReturnsError`
- [x] 33. `TestMerchantDegraded_GetUsage_ReturnsDefault`
- [x] 34. `TestMerchantDegraded_Fund_ReturnsError`
- [x] 35. `TestMerchantDegraded_CreateNoticeEvent_NoMerchantIdentity`
- [x] 36. `TestMerchantDegraded_OnUpgrade_FiresCallback`
- [x] 37. `TestMerchantDegraded_ImplementsMerchantInterface`
- [x] 38. `TestOnFirstReachable_FiredOnce`
- [x] 39. `TestOnFirstReachable_NotFiredIfInitiallyReachable`
- [x] 40. `TestNew_ReturnsDegradedWhenNoMintsReachable`
- [x] 41. `TestOnFirstReachable_SetCallbackResetsHadReachableMint`
- [x] 42. `TestOnFirstReachable_FiredAfterSetOnFirstReachableReset`

### Automated Tests — Offline Kickstart (KICKSTART_DEADLOCK fix)
- [x] 43. `TestKickstart_WalletLoaded_OfflineBalanceAvailable`
- [x] 44. `TestKickstart_WalletLoaded_GetAcceptedMintsReturnsAllConfigured`
- [x] 45. `TestKickstart_WalletLoaded_CreatePaymentTokenWithOverpayment`
- [x] 46. `TestKickstart_WalletLoaded_CreatePaymentTokenWithOverpayment_Error`
- [x] 47. `TestKickstart_WalletNotLoaded_FirstBoot_NoPanic`
- [x] 48. `TestKickstart_WalletNotLoaded_StubsReturnZero`
- [x] 49. `TestKickstart_WalletNotLoaded_PaymentTokenFails`
- [x] 50. `TestKickstart_WalletNotLoaded_GetAcceptedMintsStillReturnsAllConfigured`
- [x] 51. `TestKickstart_WalletNotLoaded_NoConfiguredMints`
- [x] 52. `TestKickstart_WalletFactoryReceivesAllConfiguredMintURLs`
- [x] 53. `TestKickstart_WalletLoaded_OtherStubsStillWork`
- [x] 54. `TestKickstart_ImplementsWalletInterface`
- [x] 55. `TestKickstart_Integration_DegradedToFullUpgrade`
- [x] 56. `TestKickstart_EndToEnd_OfflineKickstartWithWalletBalance`
- [x] 57. `TestKickstart_EndToEnd_FirstBootNoWallet_FallsBackToStubs`
- [x] 58. `TestGetAllConfiguredMintConfigs`
- [x] 59. `TestGetAllConfiguredMintConfigs_NilConfig`
- [x] 60. `TestWalletBridge_ImplementsWallet`

### Automated Tests — Merchant Provider (STALE_MERCHANT_REFERENCE fix)
- [x] 61. `TestNewMutexMerchantProvider_InitialValue`
- [x] 62. `TestMutexMerchantProvider_SetMerchant`
- [x] 63. `TestMutexMerchantProvider_NilInitial`
- [x] 64. `TestMutexMerchantProvider_ImplementsProvider`
- [x] 65. `TestMutexMerchantProvider_ConcurrentGetSet`
- [x] 66. `TestMutexMerchantProvider_DoubleSwap`
- [x] 67. `TestMutexMerchantProvider_MultipleReadersSeeSameValue`
- [x] 68. `TestMutexMerchantProvider_SwapPropagatesToConsumers`
- [x] 69. `TestMutexMerchantProvider_SetToNil`

### Automated Tests — USM Provider Integration
- [x] 70. `TestMockMerchantProvider_BasicSwap`
- [x] 71. `TestMockMerchantProvider_NilMerchant`
- [x] 72. `TestMockMerchantProvider_ConcurrentSwapAndRead`
- [x] 73. `TestUpstreamSessionManager_StoresMerchantProvider`
- [x] 74. `TestUpstreamSessionManager_SwapPropagates`
- [x] 75. `TestUpstreamSession_MerchantProviderPropagates`
- [x] 76. `TestUpstreamSession_MultipleSessionsShareProvider`

### Automated Tests — CLI Server Provider Integration
- [x] 77. `TestCLIServer_MerchantProvider_SeesInitial`
- [x] 78. `TestCLIServer_MerchantProvider_SwapPropagates`
- [x] 79. `TestCLIServer_MerchantProvider_NilMerchant`
- [x] 80. `TestCLIServer_MultipleServersShareProvider`

### Easy Production Verification Tests
- [x] 1. Reachable mint included in payouts
- [x] 2. Unreachable mint excluded from payouts
- [x] 3. Service starts without error with 4 mints configured
- [ ] 7. All mints down (no panic, no goroutine leak)
- [x] 9. "Token already spent" does NOT mark unreachable
- [ ] 11. Empty accepted_mints list
- [ ] 14. Offline kickstart with existing wallet balance
- [ ] 15. Degraded-to-full upgrade propagates to USM (see manual edge cases below)
- [ ] 16. Degraded-to-full upgrade propagates to CLI (see manual edge cases below)

### Involved Production Verification Tests
- [ ] 4. Mint goes down mid-session
- [ ] 5. Recovery after outage (hysteresis)
- [ ] 6. Service restart recovery (initial probe bypasses threshold)
- [ ] 8. PurchaseSession failure marks unreachable
- [ ] 10. Flaky mint stability (no flapping)
- [ ] 12. Mint returns 2xx with invalid JSON
- [ ] 13. Mint timeout (slow response)

---

## Automated Tests (passing)

All tests across `src/merchant/`, `src/upstream_session_manager/`, and `src/cli/` — **80 tests, all passing with `-race`**.

Run with:
```bash
cd src/merchant && go test -v -count=1 -race
cd src/upstream_session_manager && go test -v -count=1 -race
cd src/cli && go test -v -count=1 -race
```

### Unit Tests — Mint Health Tracker (21 tests)

| # | Test | What it verifies |
|---|------|-----------------|
| 1 | `TestIsReachable_InitiallyFalse` | Mints start unreachable before any probe |
| 2 | `TestIsReachable_UnknownMint` | Unknown mints always return unreachable |
| 3 | `TestRunInitialProbe_AllReachable` | Initial probe marks all reachable mints |
| 4 | `TestRunInitialProbe_NoneReachable` | 503 response leaves mint unreachable |
| 5 | `TestRunInitialProbe_MixedReachability` | Only 2xx mints become reachable |
| 6 | `TestRunInitialProbe_ServerRefusesConnection` | Connection refused leaves mint unreachable |
| 7 | `TestMarkUnreachable` | Reactive removal works |
| 8 | `TestMarkUnreachable_ResetsConsecutiveSuccesses` | Recovery counter resets on MarkUnreachable |
| 9 | `TestMarkUnreachable_UnknownMint_NoPanic` | No panic on unknown mint URL |
| 10 | `TestProactiveCheck_RecoveryRequiresThreeConsecutiveSuccesses` | Hysteresis: 3 successes needed to re-add |
| 11 | `TestProactiveCheck_FailedProbeResetsConsecutiveCounter` | Single failure resets recovery counter to 0 |
| 12 | `TestProactiveCheck_RemovesPreviouslyReachableMint` | Proactive check can remove a reachable mint |
| 13 | `TestProactiveCheck_FlapDoesNotRecoverMint` | Flapping mint does not recover prematurely |
| 14 | `TestProactiveCheck_NilConfig` | Nil config doesn't panic |
| 15 | `TestGetReachableMintConfigs_Empty` | No reachable mints returns empty slice |
| 16 | `TestGetReachableMintConfigs_OnlyReachable` | Only reachable mints returned |
| 17 | `TestGetReachableMintConfigs_NilConfig` | Nil config returns nil |

### Integration Tests — Mint Health Tracker (3 tests)

| # | Test | What it verifies |
|---|------|-----------------|
| 18 | `TestEndToEnd_FullLifecycle` | Initial probe → reactive removal → recovery (3 probes) → proactive removal of another mint |
| 19 | `TestEndToEnd_AllMintsDown_NoReachableConfigs` | All mints down returns empty, no panic |
| 20 | `TestEndToEnd_MintGoesDownThenRecoversWithInterruption` | Recovery counter resets on interruption, full recovery after 3 clean probes |

### Concurrency Tests — Mint Health Tracker (1 test)

| # | Test | What it verifies |
|---|------|-----------------|
| 21 | `TestConcurrentAccess` | 10 concurrent readers + 5 concurrent writers, no race conditions |

### Unit Tests — Degraded Merchant (16 tests)

| # | Test | What it verifies |
|---|------|-----------------|
| 22 | `TestMerchantDegraded_CreatePaymentToken_ReturnsError` | Wallet-dependent methods return "wallet not initialized" error |
| 23 | `TestMerchantDegraded_CreatePaymentTokenWithOverpayment_ReturnsError` | Overpayment variant returns error |
| 24 | `TestMerchantDegraded_DrainMint_ReturnsError` | Drain returns error |
| 25 | `TestMerchantDegraded_GetBalance_ReturnsZero` | All balance methods return 0/empty |
| 26 | `TestMerchantDegraded_GetAcceptedMints_ReturnsAllConfigured` | Returns all configured mints (not just reachable) for pricing compatibility |
| 27 | `TestMerchantDegraded_PurchaseSession_ReturnsNoticeEvent` | Returns kind 21023 notice with code "service-unavailable" |
| 28 | `TestMerchantDegraded_GetAdvertisement_ReturnsNoticeJSON` | Returns valid JSON with kind 21023 notice |
| 29 | `TestMerchantDegraded_StartPayoutRoutine_NoPanic` | Logs warning, no panic |
| 30 | `TestMerchantDegraded_StartDataUsageMonitoring_NoPanic` | Logs warning, no panic |
| 31 | `TestMerchantDegraded_GetSession_ReturnsError` | Returns "wallet not initialized" error |
| 32 | `TestMerchantDegraded_AddAllotment_ReturnsError` | Returns "wallet not initialized" error |
| 33 | `TestMerchantDegraded_GetUsage_ReturnsDefault` | Returns "-1/-1" without error |
| 34 | `TestMerchantDegraded_Fund_ReturnsError` | Returns "wallet not initialized" error |
| 35 | `TestMerchantDegraded_CreateNoticeEvent_NoMerchantIdentity` | Returns error when no merchant identity in config |
| 36 | `TestMerchantDegraded_OnUpgrade_FiresCallback` | OnUpgrade callback fires when invoked |
| 37 | `TestMerchantDegraded_ImplementsMerchantInterface` | Compile-time interface check |

### Unit Tests — onFirstReachable Callback (5 tests)

| # | Test | What it verifies |
|---|------|-----------------|
| 38 | `TestOnFirstReachable_FiredOnce` | Callback fires exactly once after 3 consecutive successful probes |
| 39 | `TestOnFirstReachable_NotFiredIfInitiallyReachable` | Callback does NOT fire when mints were reachable at initial probe |
| 40 | `TestNew_ReturnsDegradedWhenNoMintsReachable` | Tracker sets `hadReachableMint = false` when no mints reachable after initial probe |
| 41 | `TestOnFirstReachable_SetCallbackResetsHadReachableMint` | `SetOnFirstReachable()` resets `hadReachableMint` to false |
| 42 | `TestOnFirstReachable_FiredAfterSetOnFirstReachableReset` | Callback fires after reset + proactive check when mint is reachable |

### Unit Tests — Offline Kickstart (18 tests)

| # | Test | What it verifies |
|---|------|-----------------|
| 43 | `TestKickstart_WalletLoaded_OfflineBalanceAvailable` | Wallet loaded from disk reports correct balance in degraded mode |
| 44 | `TestKickstart_WalletLoaded_GetAcceptedMintsReturnsAllConfigured` | GetAcceptedMints returns all configured mints (not just reachable) |
| 45 | `TestKickstart_WalletLoaded_CreatePaymentTokenWithOverpayment` | Payment token creation delegates to loaded wallet |
| 46 | `TestKickstart_WalletLoaded_CreatePaymentTokenWithOverpayment_Error` | Insufficient balance returns error from wallet |
| 47 | `TestKickstart_WalletNotLoaded_FirstBoot_NoPanic` | First boot with no BoltDB doesn't crash |
| 48 | `TestKickstart_WalletNotLoaded_StubsReturnZero` | Balance methods return zero when wallet not loaded |
| 49 | `TestKickstart_WalletNotLoaded_PaymentTokenFails` | Payment token creation fails gracefully when no wallet |
| 50 | `TestKickstart_WalletNotLoaded_GetAcceptedMintsStillReturnsAllConfigured` | GetAcceptedMints still returns all configured mints without wallet |
| 51 | `TestKickstart_WalletNotLoaded_NoConfiguredMints` | No configured mints handled gracefully |
| 52 | `TestKickstart_WalletFactoryReceivesAllConfiguredMintURLs` | Wallet factory receives all configured mint URLs |
| 53 | `TestKickstart_WalletLoaded_OtherStubsStillWork` | Payout/data monitoring stubs still work with loaded wallet |
| 54 | `TestKickstart_ImplementsWalletInterface` | Compile-time interface check |
| 55 | `TestKickstart_Integration_DegradedToFullUpgrade` | Upgrade callback fires with full merchant |
| 56 | `TestKickstart_EndToEnd_OfflineKickstartWithWalletBalance` | Full end-to-end: degraded with wallet → payment works |
| 57 | `TestKickstart_EndToEnd_FirstBootNoWallet_FallsBackToStubs` | Full end-to-end: first boot → stubs, no crash |
| 58 | `TestGetAllConfiguredMintConfigs` | Returns all configured mints regardless of reachability |
| 59 | `TestGetAllConfiguredMintConfigs_NilConfig` | Nil config returns nil |
| 60 | `TestWalletBridge_ImplementsWallet` | Compile-time interface check |

### Unit Tests — Merchant Provider (9 tests)

| # | Test | What it verifies |
|---|------|-----------------|
| 61 | `TestNewMutexMerchantProvider_InitialValue` | Provider returns initial merchant |
| 62 | `TestMutexMerchantProvider_SetMerchant` | SetMerchant updates the returned value |
| 63 | `TestMutexMerchantProvider_NilInitial` | Provider starts with nil, can be set later |
| 64 | `TestMutexMerchantProvider_ImplementsProvider` | Compile-time interface check |
| 65 | `TestMutexMerchantProvider_ConcurrentGetSet` | 100 concurrent readers + writers, no race |
| 66 | `TestMutexMerchantProvider_DoubleSwap` | Sequential swaps correctly reflect last value |
| 67 | `TestMutexMerchantProvider_MultipleReadersSeeSameValue` | 50 concurrent readers all see the same merchant |
| 68 | `TestMutexMerchantProvider_SwapPropagatesToConsumers` | Swap changes the merchant visible to consumers |
| 69 | `TestMutexMerchantProvider_SetToNil` | Can set merchant to nil |

### Integration Tests — USM Provider (7 tests)

| # | Test | What it verifies |
|---|------|-----------------|
| 70 | `TestMockMerchantProvider_BasicSwap` | USM uses real MutexMerchantProvider, swap works |
| 71 | `TestMockMerchantProvider_NilMerchant` | Nil merchant handled without crash |
| 72 | `TestMockMerchantProvider_ConcurrentSwapAndRead` | Concurrent swap + read on provider |
| 73 | `TestUpstreamSessionManager_StoresMerchantProvider` | USM stores and returns provider |
| 74 | `TestUpstreamSessionManager_SwapPropagates` | USM sees new merchant after provider swap |
| 75 | `TestUpstreamSession_MerchantProviderPropagates` | Session sees new merchant after provider swap |
| 76 | `TestUpstreamSession_MultipleSessionsShareProvider` | Multiple sessions share one provider, all see swap |

### Integration Tests — CLI Server Provider (4 tests)

| # | Test | What it verifies |
|---|------|-----------------|
| 77 | `TestCLIServer_MerchantProvider_SeesInitial` | CLI server resolves initial merchant from provider |
| 78 | `TestCLIServer_MerchantProvider_SwapPropagates` | CLI server sees new merchant after swap |
| 79 | `TestCLIServer_MerchantProvider_NilMerchant` | Nil merchant handled without crash |
| 80 | `TestCLIServer_MultipleServersShareProvider` | Multiple CLI servers share one provider |

---

## Production Verification (tested on router)

### Completed

| # | Test | Result | Evidence |
|---|------|--------|----------|
| 1 | Reachable mint included in payouts | PASS | `mint.mountainlake.io`, `mint.coinos.io`, `mint.minibits.cash` appear in payout logs |
| 2 | Unreachable mint excluded from payouts | PASS | `cf7cyp25976k.mints.orangesync.tech` absent from payout logs entirely |
| 3 | Service starts without error with 4 mints configured | PASS | No crash, payouts running on 60-second interval |
| 9 | "Token already spent" does NOT mark unreachable | PASS | Drained wallet, spent the e-cash, then used it to purchase internet. Zero "unreachable" or "spent" hits in logs. Payout at 12:49:37 confirms `minibits.cash` still reachable ~8 min after purchase attempt. |

---

## Production Verification (remaining)

### High Priority

| # | Test | Steps | Expected Result | Status |
|---|------|-------|-----------------|--------|
| 4 | Mint goes down mid-session | 1. Start with all mints reachable. 2. Block a mint (e.g., add firewall rule or DNS redirect). 3. Wait 5 min for proactive check. 4. Check payout logs. | Mint disappears from payout logs after next proactive check cycle | TODO |
| 5 | Recovery after outage (hysteresis) | 1. With a mint marked unreachable, unblock it. 2. Watch payout logs for ~20 min. 3. Mint should reappear after 3 consecutive successful probes (~15 min). | Mint absent for first ~10 min, reappears after ~15 min | TODO |
| 6 | Service restart recovery | 1. Block a mint, wait for it to be marked unreachable. 2. Unblock it. 3. `service tollgate-wrt restart`. 4. Check payout logs immediately. | Mint is reachable immediately after restart (initial probe bypasses 3-success threshold) | TODO |
| 7 | All mints down | 1. Block all mints (e.g., disable DNS). 2. Wait for proactive check. 3. Check logs for panics. 4. Re-enable DNS. | No panics, no goroutine leaks, payouts silently skip. Mints recover after DNS restored + 3 probes. | TODO |

### Medium Priority

| # | Test | Steps | Expected Result | Status |
|---|------|-------|-----------------|--------|
| 8 | PurchaseSession failure marks unreachable | 1. During an active purchase, block the mint serving it. 2. Check that the mint is marked unreachable. 3. Check logs for "MarkUnreachable" or equivalent. | Mint removed from reachable set after receive failure | TODO |
| 10 | Flaky mint stability | 1. Set up a mint that alternates between reachable/unreachable. 2. Monitor for ~30 min. | Mint does not flap in/out of accepted set due to 3-success hysteresis | TODO |

### Low Priority

| # | Test | Steps | Expected Result | Status |
|---|------|-------|-----------------|--------|
| 11 | Empty accepted_mints list | 1. Set `"accepted_mints": []` in config. 2. Restart service. | No crash, no panic, payouts silently skip | TODO |
| 12 | Mint returns 2xx with invalid JSON | 1. Point a mint URL to a server returning 200 with non-JSON body. | Treated as unreachable (response body parsing fails gracefully) | TODO |
| 13 | Mint timeout (slow response) | 1. Point a mint URL to a server that hangs. 2. Verify timeout behavior. | Request times out, mint marked unreachable on that probe cycle | TODO |

### Critical — KICKSTART_DEADLOCK fix (code complete, needs manual verification)

| # | Test | Steps | Expected Result | Status |
|---|------|-------|-----------------|--------|
| 14 | Offline kickstart with existing wallet balance | 1. Fund router wallet with e-cash while online. 2. Disconnect internet (block WAN). 3. Reboot router. 4. Place another TollGate router running as gateway nearby. 5. Verify customer router connects to upstream gateway via WiFi. 6. Verify customer router pays upstream gateway using existing wallet balance. 7. Verify customer router gets internet after payment. | Service starts without crash, router connects to upstream gateway at layer 2, payment succeeds using cached wallet balance, internet access established | TODO — automated tests 43-60 pass. Manual verification needed on hardware. |

### Critical — STALE_MERCHANT_REFERENCE fix (code complete, needs manual verification)

| # | Test | Steps | Expected Result | Status |
|---|------|-------|-----------------|--------|
| 15 | Degraded-to-full upgrade propagates to USM | 1. Boot router with all mints blocked → degraded mode. 2. Unblock one mint. 3. Wait for proactive check (3 successes ~15 min) to trigger upgrade. 4. Trigger an upstream gateway connection. 5. Verify USM creates payment using the full merchant (not degraded stubs). | USM payment succeeds after upgrade; no "wallet not initialized" or "service-unavailable" errors from stale degraded reference. | TODO — automated tests 70-76 pass. Manual verification needed on hardware. |
| 16 | Degraded-to-full upgrade propagates to CLI | 1. Boot router with all mints blocked → degraded mode. 2. Unblock one mint, wait for upgrade. 3. Run `tollgate-cli wallet balance` and `tollgate-cli wallet info`. 4. Verify the CLI reports real wallet balance (not 0). | CLI shows actual wallet balance after upgrade, not stale zero from degraded mode. | TODO — automated tests 77-80 pass. Manual verification needed on hardware. |

---

## Manual Edge Cases (production-only, cannot be automated)

These scenarios involve real network conditions, hardware behavior, or timing that cannot be reliably reproduced in unit tests.

### Upgrade Timing

| # | Scenario | What to watch for | Risk |
|---|----------|-------------------|------|
| E1 | Payment in-flight during upgrade | USM calls `sendPayment()` right as `swapMerchant()` fires. The provider's RWMutex ensures one consistent merchant per `GetMerchant()` call, but verify no partial state. | Medium — automated test 65 covers concurrent get/set but not real payment timing |
| E2 | CLI command during upgrade | User runs `tollgate-cli wallet drain` right as upgrade fires. Should either get degraded error or full merchant response, never a nil pointer crash. | Low — provider returns nil-safe |
| E3 | Multiple rapid upgrades | Mint goes reachable, gets marked unreachable again, goes reachable again within seconds. `onFirstReachable` should fire exactly once (guarded by `hadReachableMint`). | Low — test 38 covers this |

### Offline Kickstart

| # | Scenario | What to watch for | Risk |
|---|----------|-------------------|------|
| E4 | Corrupted BoltDB wallet | Wallet file exists but is corrupted. `tollwallet.New()` should fail, degraded falls back to stubs gracefully. | Medium — test 48 covers the fallback path but not corruption |
| E5 | Wallet loaded but balance insufficient | Router has 10 sats but upstream wants 100 sats. USM should report "no compatible mints with sufficient funds", not crash. | Low — covered by `selectCompatiblePricingWithFunds` logic |
| E6 | Multiple mints on disk, some with balance | Wallet has keys for 4 mints but only 2 have balance. `GetBalanceByMint()` should return correct per-mint amounts. | Low — standard wallet behavior |

### Network Recovery

| # | Scenario | What to watch for | Risk |
|---|----------|-------------------|------|
| E7 | WAN comes up during initial probe | Router boots, initial probe runs, WAN appears mid-probe. Some mints may be marked unreachable even though WAN is now up. Proactive checks should recover within ~15 min. | Low — expected behavior, not a bug |
| E8 | DNS flaps during proactive check | DNS resolves intermittently during the 3-probe recovery window. Recovery counter resets, delaying re-add. | Low — by design (hysteresis prevents flapping) |
| E9 | Firewall allows outgoing to mint but blocks response | SYN succeeds but no response. HTTP client timeout should mark mint unreachable for that probe cycle. | Low — standard HTTP timeout behavior |

### Resource Pressure

| # | Scenario | What to watch for | Risk |
|---|----------|-------------------|------|
| E10 | High memory during upgrade | Upgrade creates full merchant (new wallet instance) while degraded merchant still referenced briefly. Verify no memory spike from dual wallet instances. | Low — degraded is garbage collected after swap |
| E11 | Many concurrent CLI connections during upgrade | Multiple `tollgate-cli` commands run simultaneously during upgrade. Each calls `GetMerchant()`, should see consistent state per-call. | Low — RWMutex allows concurrent reads |
