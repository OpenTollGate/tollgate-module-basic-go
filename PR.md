# Mint Health Tracking — Graceful Degradation When Mints Are Unreachable

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

### Merchant Provider (`src/merchant/merchant_provider.go`)

`MutexMerchantProvider` wraps the merchant instance with an RWMutex. USM and CLI server receive the provider (not a direct merchant reference) so they always see the current merchant after a degraded-to-full upgrade.

### Dynamic Advertisement (`src/merchant/merchant.go`)

`GetAdvertisement()` regenerates on each call using live health data, so the nostr advertisement always reflects which mints are currently reachable.

## Files to Review

| File | What changed |
|------|-------------|
| `src/merchant/mint_health_tracker.go` | New — health probing with lock-free HTTP calls |
| `src/merchant/mint_health_tracker_test.go` | New — 36 tests (unit, integration, concurrent) |
| `src/merchant/merchant_degraded.go` | New — offline wallet, stub operations, notice events |
| `src/merchant/merchant_degraded_test.go` | New — 30 tests covering all stubs + kickstart |
| `src/merchant/merchant_provider.go` | New — MutexMerchantProvider |
| `src/merchant/merchant_provider_test.go` | New — 10 tests (concurrent get/set, swap propagation) |
| `src/merchant/merchant.go` | Modified — health-aware startup, dynamic advertisement, skip unreachable mints in payout |
| `src/main.go` | Modified — MerchantProvider wrapper, degraded-to-full upgrade callback |
| `src/cli/server.go` | Modified — accepts MerchantProvider instead of MerchantInterface |
| `src/upstream_session_manager/` | Modified — uses MerchantProvider |
| `src/000_main_test_env.go` | Modified — added `//go:build testenv` build tag (was leaking test config path to production) |
| `src/config_manager/buildinfo.go` | New — `GitBranch` var for conditional dev-only test mint |
| `src/config_manager/config_manager_config.go` | Modified — test mint auto-appended on non-main branches |
| `src/tollwallet/tollwallet.go` | Modified — diagnostic logging for accepted mints on rejection |

## Test Results

### Unit tests — all pass, `go vet` clean

```
src/    — 23 tests (config path integration, E2E HTTP, main)
src/merchant/ — 70 tests (health tracker, degraded merchant, provider, offline wallet)
src/cli/ — 12 tests (server, MerchantProvider)
```

### Hardware tests — both routers pass full degraded lifecycle

```
make r-smoke-degraded ROUTER=alpha   # PASS
make r-smoke-degraded ROUTER=beta    # PASS
```

Steps verified on each router: deploy → fund wallet → block mint → degraded mode with offline balance → unblock → recovery to full merchant → restore config.

## Merge Base

Clean patch applied on top of `origin/main` at `289ab87`.

## Known Limitations

- **BoltDB locking**: In-process degraded-to-full upgrade may timeout if BoltDB flock is held. Production recovery works via hotplug restart (kills old process, starts fresh). Fix pending upstream in gonuts-tollgate.
- **Degraded `DrainMint`**: Always returns error — admin drain requires full merchant mode.
