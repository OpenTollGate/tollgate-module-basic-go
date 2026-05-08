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

### Merchant Provider (`src/merchant/merchant_provider.go`)

`MutexMerchantProvider` wraps the merchant instance with an RWMutex. USM and CLI server receive the provider (not a direct merchant reference) so they always see the current merchant after a degraded-to-full upgrade.

### Dynamic Advertisement (`src/merchant/merchant.go`)

`GetAdvertisement()` regenerates on each call using live health data, so the nostr advertisement always reflects which mints are currently reachable.

### Dynamic Merchant Rebuild on Reachability Changes (`src/merchant/mint_health_tracker.go`, `src/main.go`)

When the reachable mint set changes (mint goes down or recovers), the `onReachableSetChanged` callback fires. If all mints go unreachable while running as a full merchant, the service automatically downgrades to degraded mode — shuts down the wallet, creates a `MerchantDegraded`, and swaps it in via `MerchantProvider`. When a mint recovers, the existing `onFirstReachable` callback upgrades back to full merchant.

Key additions:
- `MintHealthTracker.onReachableSetChanged` — fires when `reachableCount` changes
- `Merchant.Shutdown()` — releases BoltDB lock via `tollwallet.Shutdown()`
- `NewMerchantDegradedFromFull()` — creates degraded merchant from existing tracker
- `registerReachableSetChangedCallback()` in `main.go` — orchestrates downgrade/upgrade cycle

### Upstream Pin After Payment (`src/wireless_gateway_manager/upstream_manager.go`, `src/upstream_session_manager/session.go`)

After a successful upstream session payment, the current WiFi upstream is "pinned" to prevent the upstream manager from scanning and switching away. Scheduled scans are suppressed entirely while pinned; emergency scans are suppressed unless signal drops below `SignalFloor` (can't serve clients on a dying link).

Key additions:
- `UpstreamManager.PinUpstream(ssid, duration)` — sets pin, resolves empty SSID to current active STA
- `UpstreamManager.isPinned()` / `getPinnedSSID()` — pin state queries
- `UpstreamPinner` interface — decouples session manager from upstream manager
- Session manager calls `PinUpstream("")` after each successful payment/renewal with duration based on allotment (milliseconds metric) or 5 minutes (bytes metric)

## Additional Fixes (on this branch)

### Cross-Radio DHCP Nudge (`src/wireless_gateway_manager/connector.go`)

When switching STAs across different radios, OpenWrt's netifd may not re-evaluate the wwan interface after `wifi reload`. The DHCP client never starts, causing a 180s timeout.

Fix: dual-trigger `ifup wwan` nudge in `waitForSTAIP`:
1. **Cross-radio trigger**: fires immediately once L2 association succeeds on a different radio than the active STA
2. **Timer trigger**: fires after 15s grace period as fallback

Both set the same `nudged` flag so the nudge fires at most once per switch.

### Startup Connectivity Hygiene (`src/wireless_gateway_manager/upstream_manager.go`)

After a power cycle, OpenWrt brings up whatever STAs have `disabled=0` in UCI before tollgate-wrt starts. If a non-internet STA (e.g., another TollGate's AP) is enabled, the router connects to it. The upstream manager's 90-second grace period meant no connectivity check ran, leaving the router without internet for ~3 minutes.

Fix: `startupConnectivityCheck()` runs after startup cleanup but before the grace period:
1. Gets active STA, waits 15s for DHCP/L2 to settle
2. Pings 9.9.9.9 — if internet works, returns immediately
3. If no internet: blacklists the current SSID and triggers an emergency scan+switch

## Files to Review

| File | What changed |
|------|-------------|
| `src/merchant/mint_health_tracker.go` | New — health probing with lock-free HTTP calls, `onReachableSetChanged` callback, `reachableCount` tracking |
| `src/merchant/mint_health_tracker_test.go` | New — 44 tests (unit, integration, concurrent, set-changed callback, reachable count) |
| `src/merchant/merchant_degraded.go` | New — offline wallet, stub operations, notice events, `Shutdown()` for BoltDB lock, `NewMerchantDegradedFromFull()`, `SetOnReachableSetChanged()`, `GetMintHealthTracker()` |
| `src/merchant/merchant_degraded_test.go` | New — 42 tests covering all stubs, kickstart, shutdown lifecycle, BoltDB lock E2E, degraded-from-full, pinner/tracker accessors |
| `src/merchant/merchant_provider.go` | New — MutexMerchantProvider |
| `src/merchant/merchant_provider_test.go` | New — 10 tests (concurrent get/set, swap propagation) |
| `src/merchant/merchant.go` | Modified — health-aware startup, dynamic advertisement, `Shutdown()`, `SetOnReachableSetChanged()`, `GetMintHealthTracker()` |
| `src/merchant/offline_wallet_integration_test.go` | New — 9 integration tests including full-merchant-downgrade, degraded-recovery with set-changed, BoltDB lock release E2E |
| `src/main.go` | Modified — MerchantProvider wrapper, degraded-to-full upgrade callback, `registerReachableSetChangedCallback()`, upstream pinner wiring |
| `src/cli/server.go` | Modified — accepts MerchantProvider instead of MerchantInterface |
| `src/upstream_session_manager/` | Modified — uses MerchantProvider, `UpstreamPinner` interface, `SetUpstreamPinner()`, calls `PinUpstream()` after payment |
| `src/wireless_gateway_manager/upstream_manager.go` | Modified — startup connectivity hygiene, `PinUpstream()`, `isPinned()`, `getPinnedSSID()`, pin-aware scan suppression |
| `src/wireless_gateway_manager/upstream_manager_test.go` | Modified — 16 tests (startup check, scan retry, switch retry, deferred blacklist, pin upstream) |
| `src/000_main_test_env.go` | Modified — added `//go:build testenv` build tag (was leaking test config path to production) |
| `src/config_manager/buildinfo.go` | New — `GitBranch` var for conditional dev-only test mint |
| `src/config_manager/config_manager_config.go` | Modified — test mint auto-appended on non-main branches |
| `src/tollwallet/tollwallet.go` | Modified — diagnostic logging, `Shutdown() error` for BoltDB lock release |
| `src/wireless_gateway_manager/connector.go` | Modified — dual-trigger `ifup wwan` nudge in `waitForSTAIP` for cross-radio DHCP |

## Test Results

### Automated Tests

| Test Suite | Tests | Result |
|------------|-------|--------|
| `go vet -tags testenv ./...` | — | CLEAN |
| `go test` — main package | 27 (config path, E2E HTTP, main) | PASS |
| `go test` — merchant package | 96 (health tracker, degraded, provider, offline wallet, BoltDB lock, set-changed callback, reachable count, degraded-from-full) | PASS |
| `go test` — cli package | 12 (server, MerchantProvider) | PASS |
| `go test` — upstream_session_manager | 10 (provider, swap, pinner) | PASS |
| `go test` — WGM package | 16 (startup check, scan retry, switch retry, deferred blacklist, circuit breaker, pin upstream) | PASS |
| **Total** | **161** | **PASS** |

### Hardware Tests — GL.iNet MT3000 (arm64, OpenWrt 24.10.4)

All hardware tests run against `nofee.testnut.cashu.space` on both alpha and beta routers.

#### Single-Router Degraded Lifecycle (`r-smoke-degraded`)

Verifies: service boots in degraded mode when mint is blocked, offline wallet preserves balance, service recovers to full merchant when mint is unblocked.

Steps: setup test mint → fund wallet (1013 sats) → block mint via /etc/hosts → restart → verify degraded mode with offline balance → unblock → restart → verify full merchant → restore production config.

| Router | Result |
|--------|--------|
| Alpha (100.90.41.166) | PASS — degraded mode loaded 16626 sats offline, recovered to full |
| Beta (100.90.216.248) | PASS — degraded mode loaded 11116 sats offline, recovered to full |

#### In-Process Degraded Recovery (`r-smoke-degraded-recovery`)

Verifies: degraded-to-full merchant upgrade works **without service restart**. Tests that `Shutdown()` releases the BoltDB lock so `newFullMerchant()` can reopen it.

Steps: setup test mint → fund wallet → block mint → restart → verify degraded → **unblock mint (NO restart)** → wait for proactive recovery (3 × 5min ticks) → verify full merchant.

| Router | Result |
|--------|--------|
| Alpha (100.90.41.166) | PASS — recovered at 21:52:33 (3rd proactive tick), balance 16642 sats preserved, same PID |

Recovery log sequence (alpha, PID 29581):
```
21:37:30 WARNING: No reachable mints detected. Starting in degraded mode.
21:37:30 Degraded mode: offline wallet loaded successfully, balance=16642 sats
21:42:31 Proactive check: consecutive=1/3
21:47:31 Proactive check: consecutive=2/3
21:52:33 Proactive check: consecutive=3/3 → reachable=true
21:52:33 Mint became reachable — attempting to upgrade from degraded mode
21:52:33 Setting up wallet...
21:52:33 === Merchant ready ===  (balance=16642, same PID)
21:52:33 Upgrading from degraded to full merchant
```

#### Two-Router Degraded Upstream (`r-smoke-degraded-upstream`)

Verifies: alpha connects to beta's open AP, alpha's USM detects beta as TollGate gateway, alpha pays for upstream access. Then mint is blocked on alpha, verifying the degraded merchant can renew the upstream session using offline e-cash.

**Status: PASS**

Alpha connected to beta's TollGate-D1C6 AP via radio0, got DHCP within 5s (cross-radio nudge working), detected beta as gateway. Degraded mode payment attempts confirmed. NetBird recovered after switching back to TP-Link_97E6.

#### Config Path Verification (`r-diagnose-config-path`)

Verifies: service reads config from `/etc/tollgate/config.json` (not from `/tmp/` as the pre-fix `000_main_test_env.go` caused).

| Router | Result |
|--------|--------|
| Alpha | PASS — `config=/etc/tollgate/config.json` |
| Beta | PASS — `config=/etc/tollgate/config.json` |

#### STA Health Check (`r-check-sta-health`)

Verifies: no stale or duplicate STA sections in UCI wireless config.

| Router | Result |
|--------|--------|
| Alpha | PASS — 1 active STA, no duplicates |
| Beta | PASS — 1 active STA, no duplicates |

#### Dead-Only Boot Recovery (`r-test-startup-hygiene-dead-only`)

Verifies: boot with ONLY a dead STA enabled (other router's open AP with its upstream disconnected), startup check detects no internet, triggers emergency scan, switches to a working candidate.

Setup: disconnect beta's upstream → enable only TollGate-D1C6 on alpha (beta's open AP, no internet) → disable all other STAs → reboot alpha. Verify: startup logs show "no internet" detection, emergency scan, candidate found, switch succeeded, internet recovered. Cleanup: restore alpha STAs + wallet, reconnect beta's upstream (with 5-min safety-net auto-restore on beta).

**Status: PASS (Alpha)**

Startup check correctly detected no internet on TollGate-D1C6 after settle period, triggered emergency scan on attempt 1, found candidate c03rad0r-D1C6 (signal=-20), switched successfully, blacklisted dead SSID. Internet recovered.

### Additional Hardware Test Results (Post Power Cycle)

Both routers power cycled, rescued (DNS fix + upstream restore), then full suite run.

#### Degraded Mode Lifecycle (`r-smoke-degraded`)

| Router | Result |
|--------|--------|
| Alpha (100.90.41.166) | PASS — degraded mode loaded 19591 sats, offline ops OK, recovery to full merchant after restart |
| Beta (100.90.216.248) | PASS — degraded mode loaded 11517 sats, offline ops OK, recovery to full merchant after restart |

#### In-Process Degraded Recovery (`r-smoke-degraded-recovery`)

| Router | Result |
|--------|--------|
| Alpha (100.90.41.166) | PASS — recovered in-process without restart, balance preserved |
| Beta (100.90.216.248) | PASS — recovered in-process without restart, balance preserved |

#### Dynamic Merchant Rebuild (`r-smoke-dynamic-rebuild`)

| Router | Result |
|--------|--------|
| Alpha (100.90.41.166) | PASS — auto-downgrade detected at 30s via proactive check, in-process recovery confirmed, balance 19591 sats preserved through full cycle |

#### Startup Hygiene (`r-test-startup-hygiene`)

| Router | Result |
|--------|--------|
| Alpha (100.90.41.166) | PASS — startup check triggered, emergency scan found TP-Link_97E6, internet recovered, NetBird up |

#### Two-Router Degraded Upstream (`r-smoke-degraded-upstream`)

| Result |
|--------|
| PASS — alpha connected to beta's TollGate-D1C6 AP, initial payment succeeded (2 sats), pin set (4m55s), session renewal working, both routers restored after test |

#### Pin Upstream (`r-smoke-pin-upstream`)

| Result |
|--------|
| PASS — alpha paid beta, pin set on TollGate-D1C6 (4m55s), alpha stayed pinned throughout session, pin prevented scan-away |

### Tests Not Yet Run on This Branch

| Test | What it covers | Risk |
|------|---------------|------|
| `r-test-first-boot-offline` | First boot with no wallet on disk | Low — covered by unit test |
| `r-full` | Full ~20min test suite | Low — exercises same paths as smoke tests |

## Merge Base

Clean patch applied on top of `origin/main` at `289ab87`.

## Known Limitations

- **Degraded `DrainMint`**: Always returns error — admin drain requires full merchant mode.
- **Upstream pin only fires after payment**: The pin mechanism only activates after a successful session payment/renewal. If no upstream session is active, the upstream manager scans freely.
- **Pin duration for bytes metric**: Hardcoded 5 minutes since bytes-based allotment doesn't map naturally to time. Could be improved with usage-rate estimation.
