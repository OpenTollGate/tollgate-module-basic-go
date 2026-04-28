# Plan: Real-Wallet Integration Test for KICKSTART_DEADLOCK Validation

## Objective

Write a Go integration test suite that validates the critical assumption behind the KICKSTART_DEADLOCK fix:

> **`tollwallet.TollWallet` can load a BoltDB wallet from disk and perform wallet operations when the Cashu mint is unreachable.**

This is the ONE assumption that all 80 existing mock-based tests skip. If this assumption is wrong, the KICKSTART_DEADLOCK fix does not actually solve the problem.

## Background

### The Bug

When a TollGate router boots without internet and has existing e-cash in its BoltDB wallet, the degraded merchant must be able to:

1. Load the wallet from disk (even though mints are unreachable)
2. Report balance from cached proofs
3. Create payment tokens to pay an upstream gateway for internet access

Without this, the router is stuck in a chicken-and-egg deadlock: no wallet -> no balance -> no payment -> no internet -> mints never reachable -> stuck forever.

### The Fix (commit `86b02b8`)

`MerchantDegraded` now attempts to load the BoltDB wallet via `DefaultWalletFactory` -> `tollwallet.New()` -> `wallet.LoadWallet()`. If loading succeeds, wallet operations (GetBalance, SendWithOverpayment) are delegated to the loaded wallet.

### The Critical Unknown

`wallet.LoadWallet()` is from `github.com/Origami74/gonuts-tollgate`. The fork claims to support offline loading with cached keysets, but this has never been tested with a real BoltDB. The TODO at `src/tollwallet/tollwallet.go:34-36` explicitly notes this risk:

```go
// TODO: Fix issue where wallet db is not unlocked if it doesn't get a network connection when the tollgate application boots.
```

## Mint Compatibility

### `nofee.testnut.cashu.space` (Nutshell/0.18.2)

- Uses **old keyset ID format** (`"00b4cd27d8861a44"` — 16 hex chars), compatible with `gonuts-tollgate` v0.6.1
- **Zero fees**: `input_fee_ppk: 0`
- **Auto-pays all Lightning invoices** within ~2-3 seconds
- This is the primary mint for integration tests

### `testnut.cashu.space` (Nutshell/0.20.0) — INCOMPATIBLE

- Uses **new keyset ID format** (66 hex chars)
- `gonuts-tollgate` v0.6.1's `DeriveKeysetId` produces old-format IDs and rejects new-format ones
- All `AddMint`/`Receive` operations fail with: `Got invalid keyset. Derived id: '00...' but got '0188...'`

### Self-Hosted Alternative

The `tg-mint-orchestrator` Ansible playbook (`/root/tg-mint-orchestrator`) can deploy CDK mints on a VPS with `fakewallet` backend. Use this if the public nofee mint is unreliable.

## Prerequisites

1. **Go 1.24+** installed
2. **Network access** to `https://nofee.testnut.cashu.space`
3. **All 80 existing tests pass** (`go test -v -count=1 -race ./...` in `src/merchant/`, `src/upstream_session_manager/`, `src/cli/`)

## Architecture

### Code Flow Being Tested

```
MerchantDegraded.NewMerchantDegradedWithWallet()
  -> DefaultWalletFactory(walletPath, mintURLs)
       -> newTollWallet(walletPath, mintURLs)
            -> tollwallet.New(walletPath, mintURLs, false)
                 -> wallet.LoadWallet(config)   <-- THIS IS WHAT WE'RE TESTING
                      |
                      +-- If DB exists + mint unreachable: ???
                      |     +-- Returns wallet loaded from cache -> FIX WORKS
                      |     +-- Returns error -> FIX DOESN'T WORK (gap in gonuts fork)
                      |
                      +-- If DB doesn't exist: returns error (expected, first boot)
```

### Test Strategy

Use a **local HTTP reverse proxy** (`httputil.ReverseProxy`) to the compatible Cashu test mint:

1. Start local proxy -> `https://nofee.testnut.cashu.space`
2. Use gonuts native API (`RequestMint` + `MintTokens`) to fund a wallet through the proxy
3. **Stop the proxy** -> mint is now "unreachable"
4. Try to reload the wallet from disk
5. Verify balance and payment operations work offline

The proxy URL (e.g., `http://127.0.0.1:PORT`) is used as the "mint URL" throughout. When the proxy stops, the mint becomes unreachable -- without iptables, root, or OS-specific tricks.

### Funding Flow (gonuts native, no cdk-cli)

The nofee mint auto-pays Lightning invoices in ~2-3 seconds. The funding flow:

1. `wallet.LoadWallet(cfg)` — creates/loads the wallet and adds the mint
2. `w.RequestMint(1000, proxyURL)` — creates a mint quote (initially UNPAID)
3. Poll `w.MintQuoteState(quoteId)` until state == PAID (retries every 500ms, timeout 15s)
4. `w.MintTokens(quoteId)` — mints the tokens
5. `w.GetBalance()` — verify balance > 0
6. `w.Shutdown()` — close BoltDB for reload testing

## Implementation

### File: `src/merchant/offline_wallet_integration_test.go`

```go
//go:build integration

package merchant
```

The `//go:build integration` tag ensures this test is NOT run by default with `go test ./...`. It must be explicitly enabled with `go test -tags=integration ./...`.

### Helper Functions

#### `setupReverseProxy(t, targetURL) *httptest.Server`
Creates a local HTTP reverse proxy to the target URL using `httputil.NewSingleHostReverseProxy`.

#### `requireMintReachable(t, mintURL)`
HTTP GET to `/v1/info` with 10s timeout. Skips test if mint is unreachable.

#### `fundWallet(t, proxyURL, walletDir) uint64`
Funds a gonuts wallet using native API with quote polling:
1. `wallet.LoadWallet` → `RequestMint` → poll `MintQuoteState` → `MintTokens` → `Shutdown`
2. Returns the funded balance

### Test Functions

#### Test 1: `TestIntegration_FirstBootOffline`

**Edge case**: No wallet DB exists, no internet.

- Use a random unreachable URL as the mint
- Create `ConfigManager` with the unreachable mint
- Create `MintHealthTracker`, run initial probe → 0 reachable mints
- Create `MerchantDegraded` with `DefaultWalletFactory`
- **Verify**:
  - `WalletLoaded() == false`
  - `GetBalance() == 0`
  - `GetAcceptedMints()` returns all configured mints
  - `CreatePaymentTokenWithOverpayment()` returns error
  - `PurchaseSession()` returns notice event (kind 21023, "service-unavailable")
  - `GetUsage()` returns `"-1/-1"`

#### Test 2: `TestIntegration_OfflineWalletReload`

**Edge case**: Funded wallet, offline reload via raw `wallet.LoadWallet()`.

- Start proxy → fund wallet via gonuts native API → shutdown wallet
- Stop proxy → mint unreachable
- **Verify**:
  - `wallet.LoadWallet(cfg)` succeeds offline
  - `TrustedMints()` includes the proxy URL
  - `GetBalance()` matches the online balance
  - `GetBalanceByMints()` returns correct per-mint balance
  - `SendWithOptions(AllowOverpayment=true)` creates payment tokens offline
  - `WasOffline == true` in the send result

#### Test 3: `TestIntegration_DegradedMerchantOffline`

**Edge case**: Funded wallet, offline `MerchantDegraded` via full production code path.

- Start proxy → fund wallet → stop proxy
- Create `ConfigManager`, `MintHealthTracker`, `MerchantDegraded` with `DefaultWalletFactory`
- **Verify**:
  - `WalletLoaded() == true`
  - `GetBalance()` matches online balance
  - `GetAcceptedMints()` returns all configured mints
  - `CreatePaymentTokenWithOverpayment()` succeeds offline
  - `PurchaseSession()` returns notice event
  - `GetUsage()` returns `"-1/-1"`

#### Test 4: `TestIntegration_RecoveryAndUpgrade`

**Edge case**: Degraded merchant → mint comes back → upgrade to full merchant → `MerchantProvider` swap.

- Start proxy → fund wallet → stop proxy → create degraded merchant
- Set up `OnUpgrade` callback with channel
- Set up `tracker.SetOnFirstReachable` callback (same as `merchant.New` does)
- **Restart proxy on same port** → mint is back
- Call `tracker.RunProactiveCheck()` 3 times (meets `recoveryThreshold` of 3)
- **Verify**:
  - `tracker.GetReachableMintConfigs()` returns the mint
  - `onFirstReachable` callback fired
  - Full merchant creation attempted (may be blocked by BoltDB — see Known Issues)
  - `MerchantProvider` setup works correctly

## Running the Tests

```bash
# Step 0: Run all existing tests (must all pass before integration tests)
cd src/merchant && go test -v -count=1 -race ./...
cd src/upstream_session_manager && go test -v -count=1 -race ./...
cd src/cli && go test -v -count=1 -race ./...

# Step 1: Run the new integration tests
cd src/merchant && go test -v -count=1 -tags=integration -run TestIntegration -timeout 120s ./...
```

## Expected Results and How to Interpret Them

### Scenario A: All tests pass

The gonuts-tollgate fork supports offline wallet loading, payment creation, recovery, and upgrade. The KICKSTART_DEADLOCK fix is fully validated. Proceed to merge (after hardware smoke test per Option 2).

### Scenario B: `wallet.LoadWallet()` fails when mint is offline

```
CRITICAL: wallet.LoadWallet() failed offline: <error>
```

**Meaning**: The gonuts-tollgate fork's `wallet.LoadWallet()` cannot load from an existing BoltDB without contacting the mint. The KICKSTART_DEADLOCK fix is incomplete.

**Next step**: Fix `wallet.LoadWallet()` in the gonuts-tollgate fork to support offline loading. The fix must:
1. Open the BoltDB file from disk
2. Load cached keysets and proofs
3. NOT make HTTP calls to the mint during loading
4. Return a functional wallet that can report balance and create tokens from cached proofs

### Scenario C: Wallet loads but `SendWithOverpayment()` fails

**Meaning**: Balance reporting works offline, but token creation requires mint interaction. The fix is partially valid -- the degraded merchant can report balance but can't pay upstream gateways.

**Next step**: Investigate whether gonuts-tollgate's `SendWithOptions` can work with cached keysets offline, or if a different approach is needed (e.g., storing pre-created payment tokens).

### Scenario D: Recovery callback doesn't fire

**Meaning**: The `MintHealthTracker` recovery mechanism doesn't work correctly. The degraded merchant can't upgrade to full.

**Next step**: Debug the `runProactiveCheck` logic and the `recoveryThreshold` mechanism.

### Scenario E: Test skipped (network unavailable)

**Meaning**: The test environment doesn't meet prerequisites. Ensure network access and re-run.

## Important Notes

1. **Do NOT modify any existing files** other than the test file and planning documents.

2. **Run all existing tests first** and verify all 80 pass before implementing. If any existing test fails, stop and report.

3. **The test uses `//go:build integration`** so it won't break `go test ./...` for developers who don't have network access.

4. **The proxy approach simulates offline reliably** because:
   - The gonuts wallet uses `http://127.0.0.1:PORT` as the mint URL
   - When the proxy stops, `connection refused` is the same error the router would see when mints are unreachable
   - No iptables, root, or OS-specific tricks needed

5. **Quote polling** is needed because the nofee mint takes ~2-3 seconds to auto-pay. The `fundWallet` helper polls `MintQuoteState` every 500ms with a 15s timeout.

6. **The `wallet.LoadWallet()` offline path** (in gonuts-tollgate) works by:
   - Opening the BoltDB from disk
   - Trying to get the active keyset from the mint → network error
   - `isNetworkError()` catches the error and falls back to cached keysets
   - Returns a wallet with cached data

7. **BoltDB in-process locking (KNOWN ISSUE)**: Gonuts' `storage.InitBolt()` passes `nil` options to `bolt.Open()`, which means the default `Timeout: 0` (infinite) is used. When the degraded merchant holds BoltDB open and the `onFirstReachable` callback tries to create a full merchant via `newFullMerchant()`, the second `bolt.Open()` blocks forever waiting for the `flock()`. In production, this means the degraded → full upgrade path hangs. This is a pre-existing issue in gonuts-tollgate, not related to the KICKSTART_DEADLOCK fix. The test documents this behavior and validates the recovery mechanism (tracker + callback) without requiring the full merchant creation to succeed.

8. **Test isolation**: Each test creates its own temp directories (`t.TempDir()`) for the wallet. Tests don't share state. Temp dirs are automatically cleaned up.
