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
| `src/merchant/mint_health_tracker.go` | New — health probing with lock-free HTTP calls |
| `src/merchant/mint_health_tracker_test.go` | New — 36 tests (unit, integration, concurrent) |
| `src/merchant/merchant_degraded.go` | New — offline wallet, stub operations, notice events, `Shutdown()` for BoltDB lock release |
| `src/merchant/merchant_degraded_test.go` | New — 39 tests covering all stubs, kickstart, shutdown lifecycle, BoltDB lock E2E |
| `src/merchant/merchant_provider.go` | New — MutexMerchantProvider |
| `src/merchant/merchant_provider_test.go` | New — 10 tests (concurrent get/set, swap propagation) |
| `src/merchant/merchant.go` | Modified — health-aware startup, dynamic advertisement, `deg.Shutdown()` before upgrade |
| `src/main.go` | Modified — MerchantProvider wrapper, degraded-to-full upgrade callback |
| `src/cli/server.go` | Modified — accepts MerchantProvider instead of MerchantInterface |
| `src/upstream_session_manager/` | Modified — uses MerchantProvider |
| `src/000_main_test_env.go` | Modified — added `//go:build testenv` build tag (was leaking test config path to production) |
| `src/config_manager/buildinfo.go` | New — `GitBranch` var for conditional dev-only test mint |
| `src/config_manager/config_manager_config.go` | Modified — test mint auto-appended on non-main branches |
| `src/tollwallet/tollwallet.go` | Modified — diagnostic logging, `Shutdown() error` for BoltDB lock release |
| `src/wireless_gateway_manager/connector.go` | Modified — dual-trigger `ifup wwan` nudge in `waitForSTAIP` for cross-radio DHCP |
| `src/wireless_gateway_manager/upstream_manager.go` | Modified — startup connectivity hygiene with 3-retry scan loop, deferred blacklisting, injectable check for testing |
| `src/wireless_gateway_manager/upstream_manager_test.go` | Modified — 11 new tests for startup connectivity check (including scan retry, switch retry, deferred blacklist) |

## Test Results

### Automated Tests

| Test Suite | Tests | Result |
|------------|-------|--------|
| `go vet -tags testenv ./...` | — | CLEAN |
| `go test` — main package | 23 (config path, E2E HTTP, main) | PASS |
| `go test` — merchant package | 85 (health tracker, degraded, provider, offline wallet, BoltDB lock) | PASS |
| `go test` — cli package | 12 (server, MerchantProvider) | PASS |
| `go test` — WGM package | 11 (startup check, scan retry, switch retry, deferred blacklist, circuit breaker) | PASS |
| **Total** | **131** | **PASS** |

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

### Tests Not Yet Run on This Branch

| Test | What it covers | Risk |
|------|---------------|------|
| `r-smoke-degraded-connect` | Connect to upstream while already degraded (risky) | High — may strand router, needs physical access |
| `r-test-first-boot-offline` | First boot with no wallet on disk | Low — covered by unit test `TestKickstart_WalletNotLoaded_FirstBoot_NoPanic` |
| `r-test-no-mints` | Zero configured mints | Low — covered by unit test `TestKickstart_WalletNotLoaded_NoConfiguredMints` |
| `r-full` | Full ~20min test suite | Low — exercises same paths as smoke tests |

## Merge Base

Clean patch applied on top of `origin/main` at `289ab87`.

## Known Limitations

- **Degraded `DrainMint`**: Always returns error — admin drain requires full merchant mode.
