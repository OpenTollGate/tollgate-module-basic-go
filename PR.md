# PR: Mint Health Tracking with Graceful Degradation

## Problem

When all configured Cashu mints become unreachable, the tollgate-wrt service crashes on boot (wallet initialization fails) and enters a procd restart loop. This happens whenever a router boots before its upstream WiFi connects, or when a mint goes down temporarily.

## Solution

### Mint Health Tracker (`src/merchant/mint_health_tracker.go`)

Probes each mint via `GET /v1/info` with a 5-second timeout. If no mints are reachable at startup, the service starts in **degraded mode** instead of crashing. Proactive checks run every 5 minutes. Recovery requires 3 consecutive successful probes (hysteresis against flapping).

### Degraded Merchant (`src/merchant/merchant_degraded.go`)

Loads the BoltDB wallet from disk in read-only mode (cached keysets, no mint communication). Supports:
- `GetBalance()` — returns cached balance
- `CreatePaymentTokenWithOverpayment()` — spends existing e-cash offline
- `PurchaseSession()` — returns a signed nostr notice asking the client to retry

When a mint recovers, the `onFirstReachable` callback creates a full merchant and swaps it in via `MerchantProvider`.

### BoltDB Lock Release for In-Process Upgrade (`src/merchant/merchant_degraded.go`, `src/tollwallet/tollwallet.go`)

The degraded merchant holds the BoltDB file lock open for the offline wallet. When `onFirstReachable` fires and `newFullMerchant()` tries to open the same BoltDB file, the lock conflict caused the upgrade to silently fail — the service stayed in degraded mode until restarted.

Fix: `Shutdown()` method added to `TollWallet`, the `Wallet` interface, and `MerchantDegraded`. The `onFirstReachable` callback calls `deg.Shutdown()` before `newFullMerchant()`, releasing the BoltDB lock so the new wallet can open it.

### Merchant Provider (`src/merchant_types/types.go`, `src/merchant/merchant_provider.go`)

`MutexMerchantProvider` wraps the merchant instance with an RWMutex. USM receives `merchant_types.MerchantProvider` (narrow interface, 1 method) instead of the full `merchant` package. CLI server receives the full `merchant.MerchantProvider` for its broader API surface. Both see the current merchant after a degraded-to-full upgrade via the shared mutex-protected reference.

### Dynamic Advertisement (`src/merchant/merchant.go`)

`GetAdvertisement()` regenerates on each call using live health data (no cached field), so the nostr advertisement always reflects which mints are currently reachable.

### Dynamic Merchant Rebuild on Reachability Changes (`src/merchant/mint_health_tracker.go`, `src/main.go`)

When the reachable mint set changes (mint goes down or recovers), the `onReachableSetChanged` callback fires. If all mints go unreachable while running as a full merchant, the service automatically downgrades to degraded mode — shuts down the wallet, creates a `MerchantDegraded`, and swaps it in via `MerchantProvider`. When a mint recovers, the existing `onFirstReachable` callback upgrades back to full merchant.

### TollGate-Aware WGM (`src/wireless_gateway_manager/upstream_manager.go`)

After each upstream switch, WGM probes `http://gateway:2121/` to detect if the new upstream is another TollGate. If detected:
- Sets `isTollGateConnection = true`
- Uses extended connectivity-loss patience (`TollGateLostThreshold = 6` checks vs default 2)
- Skips ping-based internet blacklist (TollGate may not have internet immediately)

This replaces the old pin mechanism — instead of pinning after payment (cross-module USM→WGM dependency), WGM now independently detects TollGate upstreams and adjusts its behavior accordingly.

### merchant_types Package (`src/merchant_types/`)

New zero-dependency package defining `PaymentMerchant` (4 methods: `CreatePaymentTokenWithOverpayment`, `GetAcceptedMints`, `GetBalanceByMint`, `Fund`) and `MerchantProvider`. USM imports only `merchant_types` instead of the full `merchant` package, eliminating transitive dependencies on btcwallet, lnd, lightning, valve, bbolt, goleveldb etc.

USM go.mod reduced from 111 lines to 49 lines.

## Additional Fixes (on this branch)

### Cross-Radio DHCP Nudge (`src/wireless_gateway_manager/connector.go`)

When switching STAs across different radios, OpenWrt's netifd may not re-evaluate the wwan interface after `wifi reload`. The DHCP client never starts, causing a 180s timeout.

Fix: dual-trigger `ifup wwan` nudge in `waitForSTAIP`:
1. **Cross-radio trigger**: fires immediately once L2 association succeeds on a different radio than the active STA
2. **Timer trigger**: fires after 15s grace period as fallback

### Startup Connectivity Hygiene (`src/wireless_gateway_manager/upstream_manager.go`)

After a power cycle, OpenWrt brings up whatever STAs have `disabled=0` in UCI before tollgate-wrt starts. If a non-internet STA (e.g., another TollGate's AP) is enabled, the router connects to it. The upstream manager's 90-second grace period meant no connectivity check ran, leaving the router without internet for ~3 minutes.

Fix: `startupConnectivityCheck()` runs after startup cleanup but before the grace period:
1. Gets active STA, waits 15s for DHCP/L2 to settle
2. Pings 9.9.9.9 — if internet works, returns immediately
3. If no internet: blacklists the current SSID and triggers an emergency scan+switch

### Reviewer Feedback Fixes

1. **Callback dispatch fix** — Collect all callbacks under lock into a slice, drop lock, fire all. Ensures both `onFirstReachable` and `onReachableSetChanged` fire when first mint recovers after complete outage.
2. **`SetOnFirstReachableForDegraded` rename** — Makes the `hadReachableMint=false` reset coupling explicit and documented.
3. **Removed `advertisement` cache field** — `GetAdvertisement()` always regenerates from live data.
4. **Loud WARN for dev mint** — `log.Printf("WARN: dev build detected...")` when test mint injected on non-main branches.
5. **`ErrTokenAlreadySpent` sentinel** — `tollwallet.Receive` wraps token-spent errors with `fmt.Errorf("%w: %v", ErrTokenAlreadySpent, err)`, merchant uses `errors.Is()`.
6. **Removed diagnostic `log.Printf`** from `tollwallet.Receive`.
7. **`SetOnReachableSetChanged` on `MerchantInterface`** — Clean interface, no `interface{}` shim in main.go.
8. **Removed pin mechanism** — Replaced with TollGate-aware WGM (independent detection, no USM→WGM cross-module dependency).

## Files to Review

| File | What changed |
|------|-------------|
| `src/merchant_types/types.go` | New — `PaymentMerchant` interface, `MerchantProvider`, `MutexMerchantProvider` |
| `src/merchant_types/go.mod` | New — depends only on `config_manager` |
| `src/merchant/mint_health_tracker.go` | New — health probing, `onReachableSetChanged` callback, fixed callback dispatch, `SetOnFirstReachableForDegraded` |
| `src/merchant/mint_health_tracker_test.go` | New — 44 tests |
| `src/merchant/merchant_degraded.go` | New — offline wallet, stub ops, `Shutdown()`, `SetOnReachableSetChanged()` |
| `src/merchant/merchant_degraded_test.go` | New — 42 tests |
| `src/merchant/merchant_provider.go` | New — MutexMerchantProvider (uses MerchantInterface) |
| `src/merchant/merchant.go` | Modified — health-aware startup, dynamic advertisement (no cache), `Shutdown()`, `SetOnReachableSetChanged()`, `errors.Is` for token spent |
| `src/merchant/offline_wallet_integration_test.go` | New — 9 integration tests |
| `src/main.go` | Modified — `merchantTypesProvider` adapter, degraded-to-full upgrade callback, `registerReachableSetChangedCallback()`, removed pin wiring |
| `src/upstream_session_manager/upstream_session_manager.go` | Modified — uses `merchant_types.MerchantProvider` instead of `merchant.MerchantProvider` |
| `src/upstream_session_manager/session.go` | Modified — uses `merchant_types.PaymentMerchant` instead of `merchant.MerchantInterface`, removed pin call |
| `src/upstream_session_manager/token_recovery.go` | Modified — uses `merchant_types.PaymentMerchant` |
| `src/upstream_session_manager/merchant_provider_test.go` | Modified — uses `merchant_types`, simpler mock (4 methods) |
| `src/upstream_session_manager/go.mod` | Modified — replaced `merchant` dep with `merchant_types`, eliminated ~60 lines of transitive deps |
| `src/wireless_gateway_manager/upstream_manager.go` | Modified — startup hygiene, removed all pin code, added `probeTollGateGateway()`, `isTollGateConnection`, TollGate-aware extended patience |
| `src/wireless_gateway_manager/types.go` | Modified — `TollGateLostThreshold`, `isTollGateConnection` |
| `src/wireless_gateway_manager/upstream_manager_test.go` | Modified — removed pin tests, added TollGate-aware tests |
| `src/config_manager/config_manager_config.go` | Modified — loud WARN on dev mint injection |
| `src/tollwallet/tollwallet.go` | Modified — `ErrTokenAlreadySpent` sentinel, `Shutdown()`, removed diagnostic log |
| `src/wireless_gateway_manager/connector.go` | Modified — cross-radio DHCP nudge |

## Test Results

### Automated Tests

| Test Suite | Tests | Result |
|------------|-------|--------|
| `go test` — main package | 27 | PASS |
| `go test` — merchant package | 96 | PASS |
| `go test` — upstream_session_manager | 10 | PASS |
| `go test` — WGM package | 16 | PASS |
| `go test` — config_manager | — | PASS |
| **Total** | **149+** | **PASS** |

### Hardware Tests — Alpha (100.90.41.166) Post-Reviewer-Feedback

| Test | Result |
|------|--------|
| Boot with new binary | PASS — no crashes, no panics |
| Dev mint WARN logged | PASS — `WARN: dev build detected (branch=unknown), injecting test mint: https://nofee.testnut.cashu.space` |
| Degraded mode startup | PASS — `WARNING: No reachable mints detected. Starting in degraded mode.`, balance=20592 sats |
| WGM startup check | PASS — `Startup check: active STA has internet, all good` |
| USM gateway probing | PASS — correctly probes port 2121 on discovered gateways |
| merchant_types decoupling | PASS — no runtime type assertion failures, swapMerchant works |
| TollGate-aware WGM | PASS — `probeTollGateGateway()` compiled and integrated |

Beta (100.90.216.248) unreachable during this round — NetBird down, needs physical rescue.

### Previous Hardware Tests (Pre-Reviewer-Feedback)

All tests passed on both alpha and beta before reviewer feedback:
- Single-router degraded lifecycle: PASS (both routers)
- In-process degraded recovery: PASS (both routers)
- Dynamic merchant rebuild: PASS (alpha)
- Startup hygiene: PASS (alpha)
- Two-router degraded upstream: PASS
- Pin upstream: PASS (now replaced by TollGate-aware WGM)

## Merge Base

Clean patch applied on top of `origin/main` at `289ab87`.

## Known Limitations

- **Degraded `DrainMint`**: Always returns error — admin drain requires full merchant mode.
- **TollGate detection is post-hoc**: WGM detects TollGate upstream after switching, not before. A future improvement could probe candidate SSIDs before switching.
- **Recovery takes 15 minutes**: 3 consecutive probes at 5-minute intervals. Acceptable for the mint-health use case but could be faster with exponential backoff.
