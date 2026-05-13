# Test Plan — Cashu Payment E2E Validation

**Branch**: `94-mint-health-rebase-clean`
**Date**: 2026-05-13
**Commit under test**: `a701c2a` (tollgate-module-basic-go), `37140b6` (physical-router-test-automation)

## Router Setup
- **Alpha**: `10.47.41.1` (LAN via enx00e04c683d2d), upstream `EnterSSID-5GHz` via radio1
- **Beta**: `192.168.244.1` (LAN via enx00e04c633a90), upstream `StarGate` via radio1
- Both flashed with fresh image + our `tollgate-wrt` binary + captive portal + test mint config
- Mint: `https://nofee.testnut.cashu.space`

## Test Execution Order

### Phase 1: Setup
- [x] Update `upstream-wifi/routers.env` with new IPs
- [x] Build & deploy tollgate CLI from `cmd/tollgate-cli` to both routers
- [x] Verify both routers: internet, merchant mode, captive portal

### Phase 2: Non-destructive tests — Alpha
- [x] `r-check-merchant ROUTER=alpha` — PASS
- [x] `r-test-captive-portal ROUTER=alpha` — PASS (7/7, 3 skipped)
- [x] `r-test-cashu-payment ROUTER=alpha` — PASS (3.0s)
- [x] `r-smoke-degraded ROUTER=alpha` — PASS (full lifecycle)

### Phase 3: Non-destructive tests — Beta
- [x] `r-check-merchant ROUTER=beta` — PASS
- [x] `r-test-captive-portal ROUTER=beta` — PASS (7/7, 3 skipped)
- [x] `r-test-cashu-payment ROUTER=beta` — PASS (2.6s)
- [x] `r-smoke-degraded ROUTER=beta` — PASS (full lifecycle)

### Phase 4: Upstream WiFi tests — Both
- [x] `r-scan ROUTER=alpha` — PASS (21 networks)
- [x] `r-list ROUTER=alpha` — PASS (EnterSSID-5GHz ACTIVE)
- [x] `r-test-edge-cases ROUTER=alpha` — PASS
- [x] `r-test-cleanup ROUTER=alpha` — PASS
- [x] Same for Beta — all PASS

### Phase 5: Two-router tests
- [x] `r-smoke-degraded-upstream` — PASS (full 13-step lifecycle)

### Phase 6: Destructive tests — Both routers
- [x] `r-test-first-boot-offline ROUTER=alpha` — PARTIAL PASS (expected: no prior wallet to load)
- [x] `r-test-no-mints ROUTER=alpha` — PASS
- [x] `r-test-first-boot-offline ROUTER=beta` — PARTIAL PASS (timing issue, service was actually fine)
- [x] `r-test-no-mints ROUTER=beta` — PASS
- [x] Verify full recovery after destructive tests — PASS

### Tests skipped (require physical interaction)
- `r-test-startup-hygiene` — requires physical power cycle
- `r-test-startup-hygiene-dead-only` — requires physical power cycle
- Serial tests (`s-*`) — no serial adapter connected

## Decisions
- Destructive tests on both routers (user confirmed)
- Routers NOT directly connected — WiFi bridge needed for two-router tests
- Full degraded lifecycle (including recovery wait)
- New tollgate CLI from `cmd/tollgate-cli` deployed (original lacked `upstream` subcommand)
- Beta switched from `c03rad0r` to `StarGate` upstream (c03rad0r lost internet mid-test)

## Makefile Fixes Applied
1. `logread -e tollgate` → `logread -e tollgate-wrt` in both Makefiles
2. Added `MK_DIR`/`REPO_ROOT` variables, fixed script/test paths
3. New targets: `r-deploy-cli`, `r-setup-fresh`
4. `r-check-merchant` fallback to `tollgate status`
5. Removed `--project=alpha` from Playwright
6. Added `-include routers.env` to upstream-wifi Makefile
7. Updated routers.env SSIDs to match current router identities
