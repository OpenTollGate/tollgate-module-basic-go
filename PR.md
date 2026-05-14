# Mint Health Tracking with Graceful Degradation

## What You Get by Merging This Branch

Merging `94-mint-health-rebase-clean` into `main` gives the tollgate router the ability to **boot and operate when all Cashu mints are unreachable**, and **automatically recover when mints come back online**. It also fixes a captive portal payment hang caused by TLS incompatibility between the router and the mint.

**New capabilities:**
- Router boots into **degraded mode** instead of crashing when mints are unreachable at startup
- **Offline wallet** loads from disk (no mint communication needed) — balance queries and cached keysets work without internet
- **Automatic recovery**: when a mint comes back, the service upgrades from degraded to full merchant in-process (no restart)
- **Dynamic downgrade**: if all mints go offline mid-operation, the service drops to degraded mode automatically
- **Health probing**: periodic `GET /v1/info` probes every 5 minutes with hysteresis (3 consecutive successes required for recovery)
- **Captive portal payments work reliably**: TLS 1.2 forced for router compatibility, 30s backend timeout, 60s frontend AbortController timeout
- **TollGate-aware upstream detection**: WGM probes `gateway:2121` after every upstream switch, adjusts patience for TollGate upstreams
- **`merchant_types` decoupling**: USM imports a 4-method interface instead of the full merchant package, eliminating ~60 lines of transitive deps (bbolt, btcwallet, lnd, etc.)
- **Cross-radio DHCP fix**: dual-trigger `ifup wwan` nudge prevents 180s timeout when switching STAs across radios
- **Startup connectivity hygiene**: emergency scan+switch if non-internet STA is active after power cycle

**Tested at commit `a701c2a` on two physical GL.iNet MT3000 routers — 22/22 hardware tests pass.**

---

## PR Size Overview

After rebasing onto `main` + `fix/wgm-improvements` (WGM PR merged), the final reviewable diff breaks down as:

| Category | Files | Net Lines | Notes |
|---|---|---|---|
| **Source code** (*.go, non-test) | 18 | +1,120 / -151 | Core feature logic |
| **Test code** (*_test.go) | 11 | +4,397 / -0 | Unit + integration tests |
| **Frontend assets** (js/css/html) | 14 | ~0 net | Bundle rebuild (renames) |
| **Stray docs** (delete before merge) | 4 | +730 | Will be removed |
| **WGM overlap** (vanishes on rebase) | 4 | ~-530 | Subsumed by `fix/wgm-improvements` |

**After rebase + cleanup, the reviewer sees:**
- ~1,100 lines of source logic (merchant health, degraded mode, TLS fixes, merchant_types decoupling)
- ~4,400 lines of tests (test:code ratio ~4:1)
- Frontend bundle swap is zero-net (1 rebuilt JS file replaces another)
- No WGM changes (already merged via `fix/wgm-improvements`)

**Before this PR can merge, rebase onto `main` after `fix/wgm-improvements` lands.** The rebase removes ~530 lines of WGM overlap and eliminates 4 conflicting files. No manual conflict resolution expected — the WGM branch is a clean superset of what this branch carries in those files.

---

## Happy Path Test Plan

### Setup

Set all variables before starting. These are used throughout the test plan.
**You must replace the `<...>` placeholders with actual values from your hardware setup.**
Check `routers.env` in the test automation repo for your SSID names and passwords.

```sh
# Router IPs (LAN interfaces on your dev machine)
alpha="10.47.41.1"       # Alpha router LAN IP
beta="192.168.244.1"     # Beta router LAN IP

# SSIDs and passwords — REPLACE THESE with values from your routers.env
beta_ssid="<other-router-5ghz-ssid>"    # Other router's TollGate AP (test 7, 8, 9)
beta_ssid_24="<other-router-2.4ghz-ssid>" # Other router's 2.4GHz AP (test 10 cross-radio)
internet_ssid="<ssid-with-internet>"    # An internet-providing SSID (test 10)
internet_pass="<password>"              # Password for internet SSID (leave empty if open)

# Verify variables are filled in (not still <...> placeholders)
echo "alpha=$alpha  beta=$beta"
echo "beta_ssid=$beta_ssid  beta_ssid_24=$beta_ssid_24"
echo "internet_ssid=$internet_ssid  internet_pass=$internet_pass"
# If any line still shows <...>, edit the values above before continuing.
```

#### Network Topology

```
                         ┌─────────────┐
                         │  Internet   │
                         │  (9.9.9.9)  │
                         └──────┬──────┘
                                │
                    ┌───────────┴───────────┐
                    │   Working upstream    │
                    │  ($internet_ssid)     │
                    └───────────┬───────────┘
                                │ WiFi (radio1)
                ┌───────────────┼───────────────┐
                │               │               │
           ┌────┴─────┐   ┌────┴─────┐   ┌─────┴────┐
           │  Alpha   │◄──┤  TollGate │──►│   Beta   │
           │          │   │  AP WiFi  │   │          │
           │  $alpha  │   │ ($beta_   │   │  $beta   │
           │          │   │  ssid)    │   │          │
           └────┬─────┘   └──────────┘   └─────┬────┘
                │                              │
         USB-LAN (enx...)                USB-LAN (enx...)
                │                              │
           ┌────┴──────────────────────────────┴────┐
           │            Dev Machine                 │
           │   (test automation + CLI + browser)     │
           └────────────────────────────────────────┘
```

**Three connection modes used across the tests:**
- **Upstream internet** — router connects to a working WiFi upstream (tests 1-6, 10)
- **TollGate-to-TollGate** — one router connects to the other's TollGate AP (tests 7, 8)
- **LAN control** — dev machine talks to both routers via USB-LAN for `make` targets, SSH, and SCP

#### Build & Deploy (from the Go repo)

```sh
# Cross-compile for arm64 (do NOT use -tags testenv — that overrides config to /tmp)
cd src/
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o tollgate-wrt .

# Stop service on both routers (binary is locked while running — "Text file busy")
ssh root@$alpha "/etc/init.d/tollgate-wrt stop"
ssh root@$beta "/etc/init.d/tollgate-wrt stop"

# Deploy to both routers (scp -O required — OpenWrt ash lacks sftp-server)
scp -O tollgate-wrt root@$alpha:/usr/bin/tollgate-wrt
scp -O tollgate-wrt root@$beta:/usr/bin/tollgate-wrt

# Restart service on both routers
ssh root@$alpha "/etc/init.d/tollgate-wrt start"
ssh root@$beta "/etc/init.d/tollgate-wrt start"
```

#### Test Automation (from the test automation repo)

All `make` commands below assume you're in the `mint-health/` directory of the
[test automation repo](https://github.com/OpenTollGate/physical-router-test-automation/tree/feature/router-to-router-interaction):

```sh
cd ~/physical-router-test-automation/mint-health/
```

### Pre-conditions

```sh
# 1. Both routers reachable
ssh root@$alpha ping -c1 8.8.8.8
ssh root@$beta ping -c1 8.8.8.8

# 2. Test mint reachable from both routers
ssh root@$alpha "wget -qO- https://nofee.testnut.cashu.space/v1/info"
ssh root@$beta "wget -qO- https://nofee.testnut.cashu.space/v1/info"

# 3. Fund wallet on Alpha (for payment tests)
make r-fund-wallet ROUTER=alpha
```

---

### 1. Service Boots Normally When Mints Are Reachable

**Feature**: Merchant starts in full mode, wallet loads, advertisement is served.

```sh
# 1.0 Safety cleanup — removes any leftover /etc/hosts blocks from previous tests
make r-cleanup ROUTER=alpha 2>/dev/null; true

# 1.1 Restart service (binary already deployed from Build & Deploy step)
make r-restart-service ROUTER=alpha
# Expected: OK_RESTARTED, "Waiting 3s for startup..."

# 1.2 Check logs for full merchant
make r-check-merchant ROUTER=alpha
# Expected: OK_MERCHANT_READY_VIA_STATUS, wallet balance shown

# 1.3 Verify wallet loaded
ssh root@$alpha "tollgate wallet balance"
# Expected: "Total wallet balance: <N> sats"

# 1.4 Verify API advertisement
curl -s http://$alpha:2121/ | jq .kind
# Expected: 10021

# 1.5 Verify dev-mint WARN (non-main branch)
ssh root@$alpha "logread -e tollgate-wrt | grep 'dev build detected'"
# Expected: "dev build detected (branch=unknown), injecting test mint: ..."
# This is normal: binary was built without version ldflags, so it auto-injects the test mint.
```

**Automated coverage**: Go `TestNew_ReturnsFullMerchant`, `TestUpstreamManager_DefaultConfig`.

---

### 2. Service Boots into Degraded Mode When All Mints Are Unreachable

**Feature**: If no mints respond to the initial probe, the service starts in degraded mode with an offline wallet instead of crashing.

```sh
# 2.1 Record baseline balance
make r-record-baseline ROUTER=alpha
# Expected: "Saved to /tmp/tollgate-baseline-alpha.txt", balance shown

# 2.2 Block mint
make r-block-mint ROUTER=alpha
# Expected: "OK: Mint unreachable"

# 2.3 Restart into degraded mode
make r-restart-service ROUTER=alpha
# Expected: OK_RESTARTED

# 2.4 Verify degraded mode
make r-check-degraded ROUTER=alpha
# Expected: OK_DEGRADED, "Degraded mode: offline wallet loaded successfully, balance=<N> sats"

# 2.5 Verify cached balance matches baseline
ssh root@$alpha "tollgate wallet balance"
# Expected: same balance as step 2.1

# 2.6 Verify service stable
ssh root@$alpha "tollgate status"
# Expected: running: true, config_ok: true, wallet_ok: true

# 2.7 Verify API returns notice (not advertisement)
curl -s http://$alpha:2121/ | jq .kind
# Expected: 21023 (notice event)
```

**Automated coverage**: Go `TestNew_ReturnsDegradedWhenNoMintsReachable`, `TestKickstart_WalletLoaded_OfflineBalanceAvailable`.

---

### 3. Automatic Recovery from Degraded to Full Merchant

**Feature**: When a mint comes back online, proactive health checks detect it, and the service upgrades in-process. Continues from test 2 (mint is blocked).

```sh
# 3.1 Unblock mint
make r-unblock-mint ROUTER=alpha
# Expected: "OK: Mint reachable again"

# 3.2 Wait for recovery (up to 15 min — 3 probes at 5-min intervals)
make r-wait-recovery ROUTER=alpha
# Expected: "Mint became reachable", "Upgrading from degraded to full merchant"

# 3.3 Verify full merchant
make r-check-merchant ROUTER=alpha
# Expected: OK_MERCHANT_READY_VIA_STATUS

# 3.4 Verify API back to normal
curl -s http://$alpha:2121/ | jq .kind
# Expected: 10021
```

**Note**: On hardware, the hotplug script (`95-tollgate-restart`) may trigger a full service restart before the proactive check fires. Either outcome is acceptable.

**Automated coverage**: Go `TestOnFirstReachable_FiredOnce`, `TestIntegration_RecoveryAndUpgrade`, `TestE2E_BoltDBLock_DegradedShutdownThenReopen`.

---

### 4. Captive Portal Happy Path — Payment End-to-End

**Feature**: Client connects, portal loads, Cashu token submitted, payment processed, internet granted. These tests require mints to be reachable (API returns `kind: 10021`).

```sh
# 4.0 Detect which router has internet for portal tests
alpha="10.47.41.1"; beta="192.168.244.1"
cd ~/physical-router-test-automation/tests
online=""
for r in $alpha $beta; do
  if ssh -o ConnectTimeout=5 root@$r "wget -qO- https://nofee.testnut.cashu.space/v1/info" >/dev/null 2>&1; then
    online=$r; break
  fi
done
if [ -z "$online" ]; then echo "!!! NEITHER ROUTER CAN REACH THE MINT — cannot run portal tests !!!"; else echo "Using $online for portal tests"; fi

# 4.1 Safety cleanup — removes any leftover /etc/hosts blocks from previous tests
cd ~/physical-router-test-automation/mint-health/
make r-cleanup ROUTER=alpha 2>/dev/null; true
cd ~/physical-router-test-automation/tests/

# 4.2 Portal UI loads — check for cashu token input
TOLLGATE_CAPTIVE_PORTAL_HOST=$online npx playwright test -g "cashu token input"
# Expected: test passes, portal shows cashu token input

# 4.3 Mint selection buttons visible
TOLLGATE_CAPTIVE_PORTAL_HOST=$online npx playwright test -g "mint selection pricing"
# Expected: at least one mint option button present

# 4.4 No bare "0" literals
TOLLGATE_CAPTIVE_PORTAL_HOST=$online npx playwright test -g "no bare"
# Expected: both cashu and lightning tabs pass

# 4.5 Full e2e payment — checkmark shown
TOLLGATE_CAPTIVE_PORTAL_HOST=$online npx playwright test -g "checkmark"
# Expected: token minted, submitted, checkmark shown, allotment displayed

# 4.6 Verify session created in router logs
ssh root@$online "logread -e tollgate-wrt | grep 'PurchaseSession'"
# Expected: "Payment successful, session created"
```

---

### 5. Captive Portal Degraded-Mode UI

**Feature**: When mints are unreachable, portal shows error, hides payment inputs, displays retry indicator.

```sh
# 5.1 Block mint and restart (same as test 2.2–2.3)
cd ~/physical-router-test-automation/mint-health/
make r-block-mint ROUTER=alpha
# Expected: "OK: Mint unreachable"
make r-restart-service ROUTER=alpha
# Expected: OK_RESTARTED

# 5.2 Error message shown
cd ~/physical-router-test-automation/tests
TOLLGATE_CAPTIVE_PORTAL_HOST=$alpha npx playwright test -g "unreachable"
# Expected: .status.error visible with "unreachable"/"initializing"/"unavailable" text

# 5.3 Retry indicator visible
TOLLGATE_CAPTIVE_PORTAL_HOST=$alpha npx playwright test -g "retrying"
# Expected: .tollgate-captive-portal-retrying visible with "retry" text

# 5.4 Payment tabs hidden
TOLLGATE_CAPTIVE_PORTAL_HOST=$alpha npx playwright test -g "hides payment"
# Expected: #cashu-token not visible

# 5.5 Unblock mint for subsequent tests
cd ~/physical-router-test-automation/mint-health/
make r-unblock-mint ROUTER=alpha
# Expected: "OK: Mint reachable again"
```

---

### 6. Dynamic Downgrade When All Mints Go Offline Mid-Operation

**Feature**: If all mints go offline while running in full mode, service automatically downgrades.

```sh
# 6.1 Start in full merchant mode (run test 1 first)

# 6.2 Block mint
make r-block-mint ROUTER=alpha
# Expected: "OK: Mint unreachable"

# 6.3 Wait for proactive check to fire (up to 5 min)
ssh root@$alpha "timeout 360 logread -e tollgate-wrt -f" | grep -i "reachable\|downgrad"
# Expected: "Reachable mint set changed — rebuilding merchant"
#           "All mints unreachable — downgrading to degraded mode"

# 6.4 Verify degraded
make r-check-degraded ROUTER=alpha
# Expected: OK_DEGRADED

# 6.5 Unblock mint
make r-unblock-mint ROUTER=alpha
# Expected: "OK: Mint reachable again"
```

---

### 7. TollGate-Aware Upstream Detection

**Feature**: WGM detects TollGate upstreams by probing `gateway:2121`, applies extended patience (6 lost checks vs default 2).

```sh
# 7.0 Safety cleanup — removes any leftover /etc/hosts blocks from previous tests
cd ~/physical-router-test-automation/mint-health/
make r-cleanup ROUTER=alpha 2>/dev/null; true

# 7.1 Both routers in full merchant mode
make r-check-merchant ROUTER=alpha
# Expected: OK_MERCHANT_READY_VIA_STATUS
make r-check-merchant ROUTER=beta
# Expected: OK_MERCHANT_READY_VIA_STATUS

# 7.2 Alpha connects to Beta's TollGate AP (open network, no password)
make r-connect SSID="$beta_ssid" ROUTER=alpha
# Expected: STA switched successfully

# 7.3 Wait for post-switch detection
sleep 10 && ssh root@$alpha "logread -e tollgate-wrt | grep -i 'TollGate'"
# Expected: "TollGate detected" or probeTollGateGateway activity

# 7.4 Verify extended patience active
ssh root@$alpha "logread -e tollgate-wrt | grep 'lost_count'"
# Expected: losses counted up to 6 before emergency scan
```

---

### 8. Two-Router Upstream Session Payment (Degraded Merchant)

**Feature**: Alpha (degraded, cached wallet) connects to Beta (full merchant, TollGate AP), discovers Beta's ad, pays from offline wallet.

```sh
# 8.1 Fund Alpha's wallet
make r-fund-wallet ROUTER=alpha
# Expected: "Successfully funded wallet with <N> sats"

# 8.2 Block Alpha's mint and restart into degraded mode
make r-block-mint ROUTER=alpha
# Expected: "OK: Mint unreachable"
make r-restart-service ROUTER=alpha
# Expected: OK_RESTARTED

# 8.3 Verify Alpha is degraded
make r-check-degraded ROUTER=alpha
# Expected: OK_DEGRADED, "offline wallet loaded successfully"

# 8.4 Verify Beta is full merchant
make r-check-merchant ROUTER=beta
# Expected: OK_MERCHANT_READY_VIA_STATUS

# 8.5 Run two-router smoke test
make r-smoke-degraded-upstream
# Expected: 13-step lifecycle completes — Alpha discovers Beta, creates session, pays from cached wallet

# 8.6 Cleanup
make r-unblock-mint ROUTER=alpha
# Expected: "OK: Mint reachable again"
make r-cleanup ROUTER=alpha
# Expected: "Cleanup done"
```

---

### 9. Startup Connectivity Hygiene

**Feature**: After power cycle with non-internet STA active, WGM detects and performs emergency scan+switch.

**Prerequisites**: The setup target creates a disabled STA pointing at the other router's TollGate AP and enables it before reboot. For this to work:
- Beta's TollGate AP (`TollGate-24A6-5GHz`) must be broadcasting and visible from Alpha
- Alpha must have internet on its current upstream (so the startup check can detect "no internet" on the dead STA and switch away)
- Alpha must have a funded wallet (setup backs it up and verify restores it)

```sh
# 9.0 Verify prerequisites
cd ~/physical-router-test-automation/mint-health/

# Beta's TollGate AP must be visible from Alpha's scan
ssh root@$alpha "iwinfo wl-sta0 scan 2>/dev/null | grep -c 'TollGate-24A6'"
# Expected: count >= 1 (Beta's AP is visible)

# Alpha must have internet on current upstream
ssh root@$alpha "ping -c1 -W3 9.9.9.9"
# Expected: "0% packet loss"

# Alpha must have wallet balance (run `make r-fund-wallet ROUTER=alpha` if needed)
ssh root@$alpha "tollgate wallet balance"
# Expected: balance > 0

# 9.1 Setup: enable a non-internet STA
make r-test-startup-hygiene-setup ROUTER=alpha
# Expected: dead STA enabled in UCI, ecash removed, state saved

# 9.2 Reboot and verify auto-switch
make r-test-startup-hygiene ROUTER=alpha
# Expected: "Startup check: no internet", emergency scan, switch to working SSID

# 9.3 Verify connectivity restored
ssh root@$alpha "ping -c1 9.9.9.9"
# Expected: "1 packets transmitted, 1 packets received, 0% packet loss"

# 9.4 Cleanup
make r-test-startup-hygiene-verify ROUTER=alpha
# Expected: config restored, wallet restored
```

---

### 10. Cross-Radio DHCP Nudge

**Feature**: Cross-radio STA switch triggers `ifup wwan` nudge, preventing 180s DHCP timeout.

**Prerequisite**: Alpha must be connected to an upstream on one radio (e.g., radio0) with a second SSID available on a different radio (e.g., radio1). The variables `internet_ssid`/`internet_pass` and `beta_ssid_24` from the Setup section are used here.

```sh
# 10.1 Check which radio Alpha's current upstream is on
cd ~/physical-router-test-automation/mint-health/
ssh root@$alpha "uci show wireless | grep 'upstream_.*disabled=0' -A5 | grep device"
# Expected: shows current radio (e.g. "radio0")

# 10.2 Switch to an SSID on the OTHER radio (if currently radio0, use radio1; vice versa)
# If current is radio0 → switch to EnterSSID-5GHz (radio1, requires password):
make r-connect SSID="$internet_ssid" PASS="$internet_pass" ROUTER=alpha
# If current is radio1 → switch to TollGate-24A6-2.4GHz (radio0, open):
# make r-connect SSID="$beta_ssid_24" ROUTER=alpha
# Expected: switch completes in <30s (not 180s)

# 10.3 Verify nudge in logs
ssh root@$alpha "logread -e tollgate-wrt | grep -i 'nudge'"
# Expected: "Nudging netifd with ifup wwan after cross-radio STA transition"

# 10.4 Verify IP obtained on the new interface
ssh root@$alpha "ip addr show | grep -A2 'wwan\|wl-sta'"
# Expected: "inet <ip-address>/<prefix> scope global"
```

---

### Quick Regression

```sh
# Go tests — all packages (from the Go repo src/ directory)
cd src/merchant && go test ./... -count=1 -v                        # 96 tests
cd ../wireless_gateway_manager && go test ./... -count=1 -v          # 16 tests
cd ../upstream_session_manager && go test ./... -count=1 -v          # 10 tests
cd .. && go test ./... -count=1 -v                                   # 27 tests (main)

# Playwright tests (from the test automation repo — uses $online from test 4.0)
cd ~/physical-router-test-automation/tests
TOLLGATE_CAPTIVE_PORTAL_HOST=$online npx playwright test              # 9 tests
```

All 149+ Go tests and 9 Playwright tests pass at `a701c2a`.

---

## How It Works

### Mint Health Tracker (`src/merchant/mint_health_tracker.go`)

Probes each mint via `GET /v1/info` with a 5-second timeout. At startup, if no mints are reachable, the service starts in **degraded mode** instead of crashing. Proactive checks run every 5 minutes. Recovery requires 3 consecutive successful probes (hysteresis against flapping). When the reachable set changes, `onReachableSetChanged` fires — this handles both upgrade (degraded→full) and downgrade (full→degraded) transitions.

### Degraded Merchant (`src/merchant/merchant_degraded.go`)

Loads the BoltDB wallet from disk in read-only mode (cached keysets, no mint communication). Supports:
- `GetBalance()` — returns cached balance
- `CreatePaymentTokenWithOverpayment()` — spends existing e-cash offline
- `PurchaseSession()` — returns a signed nostr notice asking the client to retry
- `Shutdown()` — releases the BoltDB file lock so a full merchant can open it

When a mint recovers, `onFirstReachable` creates a full merchant and swaps it in via `MerchantProvider`.

### BoltDB Lock Release for In-Process Upgrade

The degraded merchant holds the BoltDB file lock open. When `onFirstReachable` fires and `newFullMerchant()` tries to open the same file, `deg.Shutdown()` releases the lock first. Without this, the upgrade silently fails and the service stays degraded until restarted.

### Merchant Provider (`src/merchant_types/types.go`, `src/merchant/merchant_provider.go`)

`MutexMerchantProvider` wraps the merchant instance with an RWMutex. USM receives `merchant_types.MerchantProvider` (narrow interface, 1 method) instead of the full `merchant` package. CLI server receives the full `merchant.MerchantProvider` for its broader API surface. Both see the current merchant after a degraded-to-full upgrade via the shared mutex-protected reference.

### Dynamic Advertisement (`src/merchant/merchant.go`)

`GetAdvertisement()` regenerates on each call using live health data (no cached field), so the nostr advertisement always reflects which mints are currently reachable.

### TollGate-Aware WGM (`src/wireless_gateway_manager/upstream_manager.go`)

After each upstream switch, WGM probes `http://gateway:2121/` to detect if the new upstream is another TollGate. If detected:
- Sets `isTollGateConnection = true`
- Uses extended connectivity-loss patience (`TollGateLostThreshold = 6` checks vs default 2)
- Skips ping-based internet blacklist (TollGate may not have internet immediately)

This replaces the old pin mechanism — no USM→WGM cross-module dependency.

### `merchant_types` Package (`src/merchant_types/`)

New zero-dependency package defining `PaymentMerchant` (4 methods: `CreatePaymentTokenWithOverpayment`, `GetAcceptedMints`, `GetBalanceByMint`, `Fund`) and `MerchantProvider`. USM go.mod reduced from 111 lines to 49 lines.

---

## Captive Portal Payment Resilience

The tip commit (`a701c2a`) fixes a payment hang caused by TLS incompatibility:

- **TLS 1.2 forced globally**: Go's pure TLS 1.3 ClientHello times out on the router's network path. `MaxVersion: tls.VersionTLS12` in `http.DefaultTransport` fixes this (mint responds in ~300ms).
- **Backend timeouts**: `ResponseHeaderTimeout: 30s`, `WriteTimeout: 120s`, `PurchaseSession` wraps `tollwallet.Receive` in a 30s goroutine timeout.
- **Frontend AbortController**: `submitToken()` in `cashu.js` uses a 60s AbortController with cleanup on unmount.
- **`ErrTokenAlreadySpent` sentinel**: `tollwallet.Receive` wraps token-spent errors, merchant uses `errors.Is()`.

---

## Cross-Radio DHCP Nudge (`src/wireless_gateway_manager/connector.go`)

When switching STAs across different radios, OpenWrt's netifd may not re-evaluate the wwan interface after `wifi reload`. Fix: dual-trigger `ifup wwan` nudge in `waitForSTAIP`:
1. **Cross-radio trigger**: fires immediately once L2 association succeeds on a different radio
2. **Timer trigger**: fires after 15s grace period as fallback

## Startup Connectivity Hygiene (`src/wireless_gateway_manager/upstream_manager.go`)

After a power cycle, OpenWrt brings up whatever STAs have `disabled=0` before tollgate-wrt starts. Fix: `startupConnectivityCheck()` runs after cleanup but before the grace period:
1. Gets active STA, waits 15s for DHCP/L2 to settle
2. Pings 9.9.9.9 — if internet works, returns immediately
3. If no internet: blacklists the current SSID and triggers emergency scan+switch

---

## Test Results

**Tested at commit `a701c2a` on two physical GL.iNet MT3000 routers (Alpha + Beta), 2026-05-13.**

### Automated Tests

| Test Suite | Tests | Result |
|------------|-------|--------|
| `go test` — main package | 27 | PASS |
| `go test` — merchant package | 96 | PASS |
| `go test` — upstream_session_manager | 10 | PASS |
| `go test` — WGM package | 16 | PASS |
| `go test` — config_manager | — | PASS |
| **Total** | **149+** | **PASS** |

### Hardware Tests — 22/22 PASS

Both routers freshly flashed with OpenWrt 24.10.4, custom `tollgate-wrt` binary deployed, test mint `nofee.testnut.cashu.space`.

#### Phase 2 & 3: Non-destructive — Both Routers

| Test | Alpha | Beta | Notes |
|------|-------|------|-------|
| r-check-merchant | PASS | PASS | Merchant mode confirmed via `tollgate status` |
| r-test-captive-portal | PASS (7/7) | PASS (7/7) | 3 degraded-mode tests correctly skipped (mints reachable) |
| r-test-cashu-payment | PASS (3.0s) | PASS (2.6s) | Token minted → portal → submit → checkmark |
| r-smoke-degraded | PASS | PASS | Full lifecycle: setup → fund → block → degraded → unblock → recover |

#### Phase 4: Upstream WiFi — Both Routers

| Test | Alpha | Beta |
|------|-------|------|
| r-scan | PASS (21 networks) | PASS (30 networks) |
| r-list | PASS | PASS |
| r-test-edge-cases | PASS | PASS |
| r-test-cleanup | PASS | PASS |

#### Phase 5: Two-Router Test

| Test | Result |
|------|--------|
| r-smoke-degraded-upstream | PASS — full 13-step lifecycle (both routers funded, upstream switch, config restore) |

#### Phase 6: Destructive Tests — Both Routers

| Test | Alpha | Beta |
|------|-------|------|
| r-test-first-boot-offline | PASS — `OK_DEGRADED`, `OK_WALLET_LOADED`, `OK_SERVICE_UP` | PASS — same |
| r-test-no-mints | PASS — `OK_NO_MINTS`, `OK_SERVICE_UP` | PASS — same |
| Post-test recovery | PASS — `running: true`, `network_ok: true` | PASS — same |

---

## Files to Review

| File | What changed |
|------|-------------|
| `src/merchant_types/types.go` | New — `PaymentMerchant` interface, `MerchantProvider`, `MutexMerchantProvider` |
| `src/merchant_types/go.mod` | New — depends only on `config_manager` |
| `src/merchant/mint_health_tracker.go` | New — health probing, callbacks, fixed dispatch |
| `src/merchant/mint_health_tracker_test.go` | New — 44 tests |
| `src/merchant/merchant_degraded.go` | New — offline wallet, stub ops, `Shutdown()` |
| `src/merchant/merchant_degraded_test.go` | New — 42 tests |
| `src/merchant/merchant_provider.go` | New — MutexMerchantProvider |
| `src/merchant/merchant.go` | Modified — health-aware startup, dynamic advertisement, `Shutdown()`, `errors.Is` for token spent |
| `src/merchant/offline_wallet_integration_test.go` | New — 9 integration tests |
| `src/main.go` | Modified — TLS 1.2, backend timeouts, `merchantTypesProvider` adapter, degraded↔full callbacks, removed pin wiring |
| `src/upstream_session_manager/upstream_session_manager.go` | Modified — uses `merchant_types.MerchantProvider` |
| `src/upstream_session_manager/session.go` | Modified — uses `merchant_types.PaymentMerchant`, removed pin call |
| `src/upstream_session_manager/token_recovery.go` | Modified — uses `merchant_types.PaymentMerchant` |
| `src/upstream_session_manager/merchant_provider_test.go` | Modified — simpler mock (4 methods) |
| `src/upstream_session_manager/go.mod` | Modified — replaced `merchant` dep with `merchant_types` |
| `src/wireless_gateway_manager/upstream_manager.go` | Modified — startup hygiene, removed pin code, TollGate-aware probing |
| `src/wireless_gateway_manager/types.go` | Modified — `TollGateLostThreshold`, `isTollGateConnection` |
| `src/wireless_gateway_manager/upstream_manager_test.go` | Modified — removed pin tests, added TollGate-aware tests |
| `src/wireless_gateway_manager/connector.go` | Modified — cross-radio DHCP nudge |
| `src/config_manager/config_manager_config.go` | Modified — loud WARN on dev mint injection |
| `src/tollwallet/tollwallet.go` | Modified — `ErrTokenAlreadySpent` sentinel, `Shutdown()` |

---

## Known Limitations

- **Degraded `DrainMint`**: Always returns error — admin drain requires full merchant mode.
- **TollGate detection is post-hoc**: WGM detects TollGate upstream after switching, not before.
- **Recovery takes 15 minutes**: 3 consecutive probes at 5-minute intervals. Acceptable for the mint-health use case.
