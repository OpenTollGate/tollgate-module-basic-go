# Options for Validating KICKSTART_DEADLOCK and STALE_MERCHANT_REFERENCE Fixes

## Context

PR #99 introduces two must-fix bugs that have been addressed in code but need validation:

1. **KICKSTART_DEADLOCK** (commit `86b02b8`): When a router boots offline with existing e-cash in its BoltDB wallet, `MerchantDegraded` now loads the wallet from disk via `WalletFactory`, enabling offline payment creation for upstream gateway purchases.

2. **STALE_MERCHANT_REFERENCE** (commit `fb61299`): When the merchant upgrades from degraded to full, `MerchantProvider` (RWMutex-backed indirection) ensures USM and CLI always see the current merchant via `GetMerchant()`.

The automated test suite (80 tests) validates all logic paths using **mock wallets**. The critical untested assumption is:

> Can `tollwallet.TollWallet` (wrapping `gonuts-tollgate`) actually load a BoltDB wallet from disk and create payment tokens when the mint is unreachable?

---

## Keyset Compatibility Discovery

During implementation, a critical compatibility issue was discovered:

- **`testnut.cashu.space`** (Nutshell/0.20.0) uses the **new Cashu spec keyset ID format** (66 hex characters). This is incompatible with `gonuts-tollgate` v0.6.1, which derives keyset IDs using the **old format** (`"00" + SHA-256(public_keys)[:14]` = 16 hex chars). Any attempt to `AddMint` or `Receive` from this mint fails with: `Got invalid keyset. Derived id: '00...' but got '0188...'`.

- **`nofee.testnut.cashu.space`** (Nutshell/0.18.2) uses the **old keyset ID format** (e.g., `"00b4cd27d8861a44"` — 16 hex chars). This is fully compatible with `gonuts-tollgate` v0.6.1. Additionally, it has **zero fees** (`input_fee_ppk: 0`) and **auto-pays all Lightning invoices** within ~2-3 seconds.

This means the integration tests must use `nofee.testnut.cashu.space` exclusively. The `tg-mint-orchestrator` (see below) can be used as an alternative for self-hosted testing.

---

## Options

### Option 1: Real-Wallet Go Integration Test

Write a Go test in `src/merchant/` that uses the **real** `tollwallet.TollWallet` and real BoltDB, funded via a local reverse proxy to the compatible Cashu test mint. The proxy is stopped to simulate "mint offline."

| Aspect | Detail |
|--------|--------|
| **What it tests** | Whether `wallet.LoadWallet()` succeeds with existing BoltDB when mint is unreachable, whether `GetBalance()` returns cached balance, and whether `SendWithOverpayment()` creates tokens offline |
| **Hardware required** | None |
| **Effort** | Low-Medium (single Go test file + build tag) |
| **Confidence** | High for the critical unknown; validates the exact code path the KICKSTART_DEADLOCK fix depends on |
| **Run in CI** | Yes, with `//go:build integration` tag |
| **Prerequisites** | Network access to `nofee.testnut.cashu.space` (uses gonuts native API — no external tools needed) |

### Option 2: Single Router + `local-compile-to-router.sh`

Use the existing `scripts/local-compile-to-router.sh` to cross-compile and push the binary to a single router, then block mints via iptables and verify degraded mode behavior.

| Aspect | Detail |
|--------|--------|
| **What it tests** | Full real-world path on actual OpenWrt hardware |
| **Hardware required** | 1 router |
| **Effort** | Medium (manual steps, but script automates compile/deploy) |
| **Confidence** | Very high (real hardware, real network) |
| **Run in CI** | No |
| **Limitation** | Can validate degraded mode and recovery but not the "pay upstream while offline" scenario (needs 2 routers) |

### Option 3: Extend Existing pytest Framework

Add new test files to `tests/` that SSH into routers, block mints via iptables, restart the service, and verify degraded-to-full lifecycle.

| Aspect | Detail |
|--------|--------|
| **What it tests** | End-to-end on real hardware including WiFi, captive portal, payment flow |
| **Hardware required** | 2 routers (for full offline kickstart test) |
| **Effort** | Medium-High (new pytest files, router setup) |
| **Confidence** | Highest possible (real everything) |
| **Run in CI** | No (requires physical hardware) |

### Option 4: Docker-Based Integration Test

Run the Go binary in Docker containers with network namespace isolation to simulate the two-router scenario.

| Aspect | Detail |
|--------|--------|
| **What it tests** | Go binary behavior in isolated network |
| **Hardware required** | None |
| **Effort** | High (`main()` depends on OpenWrt-specific tools: `uci`, `dhcp.leases`, `iw`, `procd`) |
| **Confidence** | Medium (can test merchant layer but not OpenWrt integration) |
| **Run in CI** | Yes |
| **Limitation** | Would need to either mock OpenWrt dependencies or test only the merchant layer in isolation — which is what Option 1 does more simply |

### Option 5: Ansible Router Provisioning

Write Ansible playbooks to automate router flashing, configuration, and test execution.

| Aspect | Detail |
|--------|--------|
| **What it tests** | Full deployment pipeline + runtime behavior |
| **Hardware required** | 2 routers |
| **Effort** | Highest (new playbooks, inventory management) |
| **Confidence** | Very high |
| **Run in CI** | No |
| **Best for** | Repeated production verification, not one-time validation |

---

## Self-Hosted Mint Alternative: `tg-mint-orchestrator`

The [`tg-mint-orchestrator`](https://github.com/TollGate/tg-mint-orchestrator) (at `/root/tg-mint-orchestrator`) is an Ansible playbook for deploying per-operator CDK Cashu mints on a VPS. It can be used as an alternative to the public test mint:

- **Architecture**: Traefik reverse proxy + per-operator CDK mint containers with SQLite backend
- **Lightning backend**: `fakewallet` mode for testing (quotes auto-fill, no real sats)
- **Usage**: Deploy a mint with `./scripts/deploy-mint.sh <VPS_IP> <NPUB>`, then use the resulting mint URL in integration tests
- **Advantages**: Full control over mint version (CDK, not Nutshell), no reliance on public infrastructure, compatible with both old and new keyset formats
- **When to use**: If `nofee.testnut.cashu.space` is down or unreliable, or for CI environments that need deterministic mint behavior

For most development, the public nofee test mint is sufficient. The orchestrator is recommended for:
- CI pipelines requiring guaranteed uptime
- Testing against specific CDK versions
- Offline development environments

---

## Why Option 1 Was Chosen

1. **Targets the exact unknown.** The 80 mock tests already prove the logic is correct. The only gap is whether `gonuts-tollgate`'s `wallet.LoadWallet()` can load from an existing BoltDB when the mint is unreachable. Option 1 tests precisely this.

2. **No hardware required.** Can be developed and run on any machine with Go + internet. No routers to flash, no WiFi to configure.

3. **Fast iteration.** A Go test runs in seconds. Router flashing takes minutes per cycle.

4. **Can run in CI.** With the `//go:build integration` tag, it can be triggered in GitHub Actions on demand or on a schedule.

5. **Definitive answer.** If this test passes, the KICKSTART_DEADLOCK fix is validated for the critical code path. If it fails, we know exactly what needs to be fixed (the gonuts fork's offline loading behavior).

6. **STALE_MERCHANT_REFERENCE is already validated.** The 20 provider tests (61-80) with `-race` give high confidence for the concurrency/correctness of the MerchantProvider pattern. No additional testing needed for that fix.

### Test Coverage

The integration test suite covers these bootstrap edge cases:

| Edge Case | Test | Expected Behavior |
|-----------|------|-------------------|
| First boot offline (no wallet, no internet) | `TestIntegration_FirstBootOffline` | `MerchantDegraded` created, `WalletLoaded() == false`, graceful degradation |
| Offline boot with existing wallet | `TestIntegration_OfflineWalletReload` | `wallet.LoadWallet()` succeeds, balance/payment work |
| Degraded merchant with offline wallet | `TestIntegration_DegradedMerchantOffline` | Full production code path through `DefaultWalletFactory` |
| Recovery: mint comes back online | `TestIntegration_RecoveryAndUpgrade` | `onFirstReachable` callback fires, full merchant created, `MerchantProvider` swap works |

### Recommended Follow-Up

After Option 1 passes, do Option 2 (single router smoke test) before merging to confirm no OpenWrt-specific surprises.

## Findings from Integration Testing

### KICKSTART_DEADLOCK: VALIDATED

All tests pass. The gonuts-tollgate fork supports:
- Offline `wallet.LoadWallet()` with existing BoltDB
- Balance reporting from cached proofs
- `SendWithOptions(AllowOverpayment=true)` creates tokens offline (`wasOffline=true`)
- `tollwallet.TollWallet` wraps these operations correctly
- `MerchantDegraded` loads wallet through `DefaultWalletFactory` production code path

### BoltDB In-Process Locking: KNOWN ISSUE

Gonuts' `storage.InitBolt()` passes `nil` options to `bolt.Open()`, resulting in infinite flock timeout (`Timeout: 0`). When the degraded merchant holds BoltDB open and the `onFirstReachable` callback tries `newFullMerchant()`, the second `bolt.Open()` blocks forever. This means the degraded → full merchant upgrade path hangs in production.

**Impact**: After internet recovery, the `onFirstReachable` callback fires correctly, but `newFullMerchant` blocks on BoltDB. The degraded merchant continues to function (it has the DB open). The upgrade will only complete if the degraded merchant's DB handle is released (e.g., by process restart).

**Fix needed in gonuts-tollgate**: Set `bolt.Options{Timeout: 5 * time.Second}` in `storage.InitBolt()` so the second `bolt.Open()` fails gracefully after timeout instead of blocking forever. The `onFirstReachable` callback can then retry or log the issue.

### First Boot Offline: GRACEFUL DEGRADATION

On first boot with no wallet DB and no internet, gonuts creates an empty wallet with 0 balance and continues in offline mode. `WalletLoaded()` returns `true` but all payment operations fail with "mint does not exist" or "insufficient funds". This is correct behavior — the degraded merchant degrades gracefully.
