# Mint Health Tracker — Test Plan

## Checklist

### Automated Tests
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

### Easy Production Verification Tests
- [x] 1. Reachable mint included in payouts
- [x] 2. Unreachable mint excluded from payouts
- [x] 3. Service starts without error with 4 mints configured
- [ ] 7. All mints down (no panic, no goroutine leak) - fails [fix plan here](https://github.com/OpenTollGate/tollgate-module-basic-go/blob/e8133bb09a1f528fbfcadec4d768500bc8bb0182/DEGRADED_MERCHANT_TASK.md)
- [x] 9. "Token already spent" does NOT mark unreachable
- [ ] 11. Empty accepted_mints list - fails [fix plan here](https://github.com/OpenTollGate/tollgate-module-basic-go/blob/e8133bb09a1f528fbfcadec4d768500bc8bb0182/DEGRADED_MERCHANT_TASK.md)


### Involved Production Verification Tests
- [ ] 4. Mint goes down mid-session
- [ ] 5. Recovery after outage (hysteresis)
- [ ] 6. Service restart recovery (initial probe bypasses threshold)
- [ ] 8. PurchaseSession failure marks unreachable
- [ ] 10. Flaky mint stability (no flapping)
- [ ] 12. Mint returns 2xx with invalid JSON
- [ ] 13. Mint timeout (slow response)
- [ ] 14. Offline kickstart with existing wallet balance

---

## Automated Tests (passing)

All tests in `src/merchant/mint_health_tracker_test.go` — **21 tests, all passing**.

Run with: `cd src && go test ./merchant/ -run "Test" -v`

### Unit Tests

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

### Integration Tests

| # | Test | What it verifies |
|---|------|-----------------|
| 18 | `TestEndToEnd_FullLifecycle` | Initial probe → reactive removal → recovery (3 probes) → proactive removal of another mint |
| 19 | `TestEndToEnd_AllMintsDown_NoReachableConfigs` | All mints down returns empty, no panic |
| 20 | `TestEndToEnd_MintGoesDownThenRecoversWithInterruption` | Recovery counter resets on interruption, full recovery after 3 clean probes |

### Concurrency Tests

| # | Test | What it verifies |
|---|------|-----------------|
| 21 | `TestConcurrentAccess` | 10 concurrent readers + 5 concurrent writers, no race conditions |

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
| 9 | "Token already spent" does NOT mark unreachable | 1. Trigger a double-spend scenario (same token twice). 2. Verify the mint is NOT marked unreachable. | Mint remains reachable | TODO |
| 10 | Flaky mint stability | 1. Set up a mint that alternates between reachable/unreachable. 2. Monitor for ~30 min. | Mint does not flap in/out of accepted set due to 3-success hysteresis | TODO |

### Low Priority

| # | Test | Steps | Expected Result | Status |
|---|------|-------|-----------------|--------|
| 11 | Empty accepted_mints list | 1. Set `"accepted_mints": []` in config. 2. Restart service. | No crash, no panic, payouts silently skip | FAIL — crash-loops with FATAL "No mints provided. Wallet requires at least 1 accepted mint, none were provided". Covered by degraded merchant fix. |
| 12 | Mint returns 2xx with invalid JSON | 1. Point a mint URL to a server returning 200 with non-JSON body. | Treated as unreachable (response body parsing fails gracefully) | TODO |
| 13 | Mint timeout (slow response) | 1. Point a mint URL to a server that hangs. 2. Verify timeout behavior. | Request times out, mint marked unreachable on that probe cycle | TODO |

### Critical

| # | Test | Steps | Expected Result | Status |
|---|------|-------|-----------------|--------|
| 14 | Offline kickstart with existing wallet balance | 1. Fund router wallet with e-cash while online. 2. Disconnect internet (block WAN). 3. Reboot router. 4. Place another TollGate router running as gateway nearby. 5. Verify customer router connects to upstream gateway via WiFi. 6. Verify customer router pays upstream gateway using existing wallet balance. 7. Verify customer router gets internet after payment. | Service starts without crash, router connects to upstream gateway at layer 2, payment succeeds using cached wallet balance, internet access established | FAIL — MerchantDegraded creates chicken-and-egg deadlock: no wallet loaded from disk, GetAcceptedMints() returns empty, USM fails with "no compatible mints with sufficient funds". Also: swapMerchant() doesn't update USM's stale merchant reference. See [KICKSTART_DEADLOCK.md](KICKSTART_DEADLOCK.md) and [STALE_MERCHANT_REFERENCE.md](STALE_MERCHANT_REFERENCE.md). |