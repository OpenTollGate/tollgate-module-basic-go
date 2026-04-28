# Options for Validating KICKSTART_DEADLOCK and STALE_MERCHANT_REFERENCE Fixes

## Context

PR #99 introduces two must-fix bugs that have been addressed in code but need validation:

1. **KICKSTART_DEADLOCK** (commit `86b02b8`): When a router boots offline with existing e-cash in its BoltDB wallet, `MerchantDegraded` now loads the wallet from disk via `WalletFactory`, enabling offline payment creation for upstream gateway purchases.

2. **STALE_MERCHANT_REFERENCE** (commit `fb61299`): When the merchant upgrades from degraded to full, `MerchantProvider` (RWMutex-backed indirection) ensures USM and CLI always see the current merchant via `GetMerchant()`.

The automated test suite (80 tests) validates all logic paths using **mock wallets**. The critical untested assumption is:

> Can `tollwallet.TollWallet` (wrapping `gonuts-tollgate`) actually load a BoltDB wallet from disk and create payment tokens when the mint is unreachable?

---

## Options

### Option 1: Real-Wallet Go Integration Test

Write a Go test in `src/merchant/` that uses the **real** `tollwallet.TollWallet` and real BoltDB, funded via a local reverse proxy to a Cashu test mint. The proxy is stopped to simulate "mint offline."

| Aspect | Detail |
|--------|--------|
| **What it tests** | Whether `wallet.LoadWallet()` succeeds with existing BoltDB when mint is unreachable, whether `GetBalance()` returns cached balance, and whether `SendWithOverpayment()` creates tokens offline |
| **Hardware required** | None |
| **Effort** | Low-Medium (single Go test file + build tag) |
| **Confidence** | High for the critical unknown; validates the exact code path the KICKSTART_DEADLOCK fix depends on |
| **Run in CI** | Yes, with `//go:build integration` tag |
| **Prerequisites** | `cdk-cli` installed (already provisioned by `tests/setup_cdk_testing.yml`), network access to `testnut.cashu.space` |

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

## Why Option 1 Was Chosen

1. **Targets the exact unknown.** The 80 mock tests already prove the logic is correct. The only gap is whether `gonuts-tollgate`'s `wallet.LoadWallet()` can load from an existing BoltDB when the mint is unreachable. Option 1 tests precisely this.

2. **No hardware required.** Can be developed and run on any machine with Go + cdk-cli + internet. No routers to flash, no WiFi to configure.

3. **Fast iteration.** A Go test runs in seconds. Router flashing takes minutes per cycle.

4. **Can run in CI.** With the `//go:build integration` tag, it can be triggered in GitHub Actions on demand or on a schedule.

5. **Definitive answer.** If this test passes, the KICKSTART_DEADLOCK fix is validated for the critical code path. If it fails, we know exactly what needs to be fixed (the gonuts fork's offline loading behavior).

6. **STALE_MERCHANT_REFERENCE is already validated.** The 20 provider tests (61-80) with `-race` give high confidence for the concurrency/correctness of the MerchantProvider pattern. No additional testing needed for that fix.

### Recommended Follow-Up

After Option 1 passes, do Option 2 (single router smoke test) before merging to confirm no OpenWrt-specific surprises.
