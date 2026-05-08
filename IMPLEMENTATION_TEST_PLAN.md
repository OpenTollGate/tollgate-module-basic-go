# Implementation Test Plan — Dynamic Merchant Rebuild + Pin Upstream

**DELETE BEFORE MERGING TO MAIN**

## Phase A: Dynamic Merchant Rebuild

### A1. Production Code Changes

| File | Change |
|------|--------|
| `src/merchant/mint_health_tracker.go` | Add `onReachableSetChanged func()` callback + setter. Fire in `runProactiveCheck()` when reachable set changes. |
| `src/merchant/merchant.go` | Add `Shutdown() error` to full Merchant. |
| `src/main.go` | Register `onReachableSetChanged` callback that rebuilds merchant on any change. |

### A2. Unit Tests

| Test | File | What it verifies |
|------|------|-----------------|
| `TestMintHealthTracker_OnReachableSetChanged_FiresOnRemoval` | `mint_health_tracker_test.go` | Remove mint from reachable → callback fires |
| `TestMintHealthTracker_OnReachableSetChanged_FiresOnRecovery` | `mint_health_tracker_test.go` | Mint recovers after 3 consecutive → callback fires |
| `TestMintHealthTracker_OnReachableSetChanged_DoesNotFireIfNoChange` | `mint_health_tracker_test.go` | Same mints reachable → no callback |
| `TestMintHealthTracker_OnReachableSetChanged_NilCallback` | `mint_health_tracker_test.go` | nil callback → no panic |
| `TestMerchant_Shutdown_ReleasesWallet` | `merchant_test.go` | Full merchant Shutdown() calls tollwallet.Shutdown() |
| `TestMerchant_Shutdown_Idempotent` | `merchant_test.go` | Double Shutdown() no panic |

### A3. Integration Tests

| Test | File | What it verifies |
|------|------|-----------------|
| `TestIntegration_DynamicRebuild_3To2Mints` | `merchant_degraded_test.go` | 3 mints → block 1 → callback → rebuild with 2 |
| `TestIntegration_DynamicRebuild_2To3Mints` | `merchant_degraded_test.go` | 2 mints → 3rd recovers after hysteresis → callback → rebuild with 3 |
| `TestIntegration_DynamicRebuild_FullToDegraded` | `merchant_degraded_test.go` | All mints down → rebuild as degraded |
| `TestIntegration_DynamicRebuild_DegradedToFull` | `merchant_degraded_test.go` | Degraded → mints recover → rebuild as full |
| `TestIntegration_DynamicRebuild_SwapViaProvider` | `merchant_degraded_test.go` | MerchantProvider swap is thread-safe during rebuild |

### A4. E2E Tests (real BoltDB)

| Test | File | What it verifies |
|------|------|-----------------|
| `TestE2E_DynamicRebuild_BoltDB_3To2To3` | `merchant_degraded_test.go` | Real BoltDB: create 3-mint wallet → block 1 → shutdown → reopen with 2 → unblock → reopen with 3 |

### A5. Hardware Test

| Test | Makefile target | What it verifies |
|------|----------------|-----------------|
| `r-smoke-dynamic-mint-recovery` | `mint-health/Makefile` | 1 mint → block → immediate downgrade → unblock → 3-check hysteresis upgrade |

## Phase B: Pin Upstream After Payment

### B1. Production Code Changes

| File | Change |
|------|--------|
| `src/wireless_gateway_manager/upstream_manager.go` | Add PinUpstream(ssid, duration), isPinned(). Skip scans when pinned (unless signal below floor). |
| `src/upstream_session_manager/upstream_session_manager.go` | Accept UpstreamPinner interface. |
| `src/upstream_session_manager/session.go` | Call PinUpstream after successful payment. Extend on renewal. |
| `src/main.go` | Wire upstreamManager as pinner to session manager. |

### B2. Unit Tests

| Test | File | What it verifies |
|------|------|-----------------|
| `TestUpstreamManager_PinUpstream_SetsPin` | `upstream_manager_test.go` | Pin SSID → isPinned returns true |
| `TestUpstreamManager_PinUpstream_Expires` | `upstream_manager_test.go` | Pin with short duration → expires |
| `TestUpstreamManager_PinUpstream_DifferentSSID` | `upstream_manager_test.go` | Pin A → isPinned(B) is false |
| `TestUpstreamManager_PinUpstream_ExtendsOnRenewal` | `upstream_manager_test.go` | Re-pin extends duration |

### B3. Integration Tests

| Test | File | What it verifies |
|------|------|-----------------|
| `TestUpstreamManager_Pin_SuppressesScheduledScan` | `upstream_manager_test.go` | Pinned → scheduled scan skipped |
| `TestUpstreamManager_Pin_SuppressesEmergencyScan` | `upstream_manager_test.go` | Pinned → connectivity loss → no emergency scan |
| `TestUpstreamManager_Pin_Expired_ScanResumes` | `upstream_manager_test.go` | Pin expires → scan resumes |
| `TestUpstreamManager_Pin_LowSignal_StillSwitches` | `upstream_manager_test.go` | Pinned but signal < floor → scan runs |

### B4. E2E Test

| Test | File | What it verifies |
|------|------|-----------------|
| `TestE2E_PinUpstream_AfterPayment` | `upstream_session_manager_test.go` | Mock payment → verify PinUpstream called with correct args |

### B5. Hardware Test

| Test | Makefile target | What it verifies |
|------|----------------|-----------------|
| `r-smoke-degraded-upstream-stay` | `mint-health/Makefile` | Pay for upstream → stay on TollGate AP → verify internet + no STA switch |

## Phase C: Additional Hardware Tests

| Test | Makefile target | What it verifies |
|------|----------------|-----------------|
| `r-smoke-degraded-upstream-preblock` | `mint-health/Makefile` | Block mint first → connect to beta → pay from degraded mode |
| `r-smoke-degraded-upstream-stay` | `mint-health/Makefile` | (same as B5) Pay → stay → verify internet delivery |

## Execution Checklist

- [ ] A1: Production code (3 files)
- [ ] A2: Unit tests (6 tests)
- [ ] A3: Integration tests (5 tests)
- [ ] A4: E2E BoltDB test (1 test)
- [ ] Run all Go tests — verify no regressions
- [ ] B1: Production code (4 files)
- [ ] B2: Unit tests (4 tests)
- [ ] B3: Integration tests (4 tests)
- [ ] B4: E2E test (1 test)
- [ ] Run all Go tests — verify no regressions
- [ ] Cross-compile, deploy to alpha
- [ ] A5 + C: Hardware tests on alpha
- [ ] Commit both repos
