# PR #99 — Remaining Work

This PR prevents the tollgate-wrt service from crash-looping when all configured mints are unreachable. The degraded merchant implementation is complete and all 80 automated tests pass across 3 packages (merchant, upstream_session_manager, cli). The two must-fix bugs from review have been resolved.

## Must-Fix Bugs — RESOLVED

- ~~**KICKSTART_DEADLOCK.md**~~ — **Fixed**. `MerchantDegraded` now loads the BoltDB wallet from disk in degraded mode using a `WalletFactory` for testability. `GetAcceptedMints()` returns all configured mints (not just reachable), breaking the chicken-and-egg deadlock. First boot (no wallet on disk) falls back to stubs gracefully. 18 new tests (43-60).
- ~~**STALE_MERCHANT_REFERENCE.md**~~ — **Fixed**. Introduced `MerchantProvider` interface with `MutexMerchantProvider` (RWMutex-backed). USM and CLI server now receive `MerchantProvider` and resolve the current merchant via `GetMerchant()` at each call site. `swapMerchant()` calls `provider.SetMerchant()`. 20 new tests (61-80) across 3 packages.

## Automated Test Summary

| Package | Tests | Status |
|---------|-------|--------|
| `src/merchant` | 69 | All pass, `-race` clean |
| `src/upstream_session_manager` | 7 | All pass, `-race` clean |
| `src/cli` | 4 | All pass, `-race` clean |
| **Total** | **80** | **All pass** |

## Production Verification Tests (on router)

9 of 14 original production tests are still TODO. Tests 7 and 11 should now pass (degraded mode handles all-mints-down and empty-mints). Tests 14-16 (kickstart + provider propagation) need manual verification on hardware.

See `MINT_TEST_PLAN.md` for the full checklist including 11 manual edge cases.

## Other Open Items

- **AP creation fix** (`AP_CREATION_FIX.md`) — A timing race where the Go binary's `ensureAPInterfacesExist()` never runs because `networkMonitor.IsConnected()` requires a successful ping first. This is tracked separately but is related to the same "router starts without internet" scenario.
