# Bug: Offline Kickstart Deadlock — Degraded Mode Prevents Wallet Loading

## Problem

When a router boots without internet and has existing e-cash in its wallet from a previous session, the degraded merchant creates a chicken-and-egg deadlock that permanently prevents the router from getting online.

## Deadlock Trace

1. Router boots offline → all mints unreachable → `merchant.New()` returns `MerchantDegraded`
2. `MerchantDegraded` has no wallet — `tollwallet.New()` is never called, BoltDB is never loaded
3. Wireless gateway manager connects to upstream TollGate via WiFi (layer 2, works fine)
4. Upstream detector calls `HandleGatewayConnected()` → `NewUpstreamSession()`
5. `NewUpstreamSession()` calls `selectCompatiblePricingWithFunds()` which calls:
   - `merchant.GetAcceptedMints()` → returns **empty** (degraded, no reachable mints)
   - `merchant.GetBalanceByMint()` → returns **0** (no wallet)
6. Returns `"no compatible mints with sufficient funds found"` → session creation fails
7. No payment sent → no internet → mints never become reachable → `onFirstReachable` never fires
8. **Router is stuck forever**

## Root Cause

`MerchantDegraded` is a hard wall with zero wallet functionality. It returns empty/zero for all wallet-dependent operations. The degraded mode was designed to prevent a crash loop when all mints are unreachable, but it doesn't account for the kickstart flow where the router needs to spend pre-existing wallet balance to buy internet from an upstream gateway.

The underlying gonuts-tollgate fork supports offline wallet loading — `wallet.LoadWallet()` can return with cached keysets and proofs when the mint is unreachable. But `newFullMerchant()` gates wallet initialization behind `len(reachableMints) == 0`, so the wallet is never created in degraded mode.

## Key Code Locations

| File | Line | What |
|------|------|------|
| `src/merchant/merchant.go` | 77 | `if len(reachableMints) == 0` → returns degraded, skips wallet init |
| `src/merchant/merchant_degraded.go` | 29-35 | `CreatePaymentToken*` → `"wallet not initialized"` |
| `src/merchant/merchant_degraded.go` | 41-46 | `GetAcceptedMints()` → empty, `GetBalanceByMint()` → 0 |
| `src/merchant/mint_health_tracker.go` | 163-172 | `probeMint()` — HTTP GET, fails without internet |
| `src/upstream_session_manager/session.go` | 426-471 | `selectCompatiblePricingWithFunds()` — calls merchant interface |
| `src/tollwallet/tollwallet.go` | 34-36 | Existing TODO: wallet DB doesn't unlock without network on first boot |

## Solution Direction

The degraded merchant needs a **semi-functional wallet** that can:

1. **Load the BoltDB wallet from disk** using cached keysets (gonuts already supports this)
2. **Report balance from cached proofs** (wallet.GetBalance() works offline)
3. **Create payment tokens with overpayment** (wallet.SendWithOptions supports offline sends when proofs exist)
4. **Return configured mints** (not just reachable mints) for compatibility checking with upstream gateway pricing

The key insight is that the router doesn't need to talk to its own mint to pay an upstream gateway — it just needs access to its local proofs. The upstream gateway will verify the proofs against the shared mint.

## Constraints

- Must not re-introduce the crash loop from `tollwallet.New()` when the wallet DB doesn't exist yet (first boot)
- Must not call `AddMint()` on an unreachable mint (the HTTP call that causes the crash)
- The gonuts fork's `wallet.LoadWallet()` handles this: if the mint is already known (from a previous session), it uses cached keysets; if the mint is new, it returns an error for non-network errors but continues for network errors
- The existing TODO at `tollwallet.go:34-36` notes a related bug: wallet DB doesn't unlock without network on first boot
