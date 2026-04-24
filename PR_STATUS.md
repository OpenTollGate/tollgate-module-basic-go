# PR #99 — Remaining Work

This PR prevents the tollgate-wrt service from crash-looping when all configured mints are unreachable. The degraded merchant implementation is complete and all 42 automated tests pass. Two bugs were discovered during review that must be fixed before the PR can be merged, and several production verification tests remain.

## Must-Fix Bugs

- **KICKSTART_DEADLOCK.md** — The degraded merchant creates a chicken-and-egg deadlock: it skips wallet initialization entirely, but the router needs its existing wallet balance to pay an upstream gateway for internet access. Without internet, mints never become reachable and the router is stuck forever. The fix requires loading the BoltDB wallet from disk in degraded mode using the gonuts fork's cached-keyset support, then allowing offline payment token creation for upstream gateway purchases.
- **STALE_MERCHANT_REFERENCE.md** — `swapMerchant()` only updates the global `merchantInstance` variable. The `UpstreamSessionManager` and `CLIServer` capture the merchant interface at construction time and never see the upgrade from degraded to full. Either a `SetMerchant()` method, a shared indirection pointer, or a `merchantProvider` getter pattern is needed.

## Production Verification Tests (on router)

9 of 14 production tests are still TODO. Tests 7 and 11 are known failures covered by the degraded merchant fix. Test 14 (offline kickstart) is blocked by the kickstart deadlock bug above. See `MINT_TEST_PLAN.md` for the full list.

## Other Open Items

- **AP creation fix** (`AP_CREATION_FIX.md`) — A timing race where the Go binary's `ensureAPInterfacesExist()` never runs because `networkMonitor.IsConnected()` requires a successful ping first. This is tracked separately but is related to the same "router starts without internet" scenario.
