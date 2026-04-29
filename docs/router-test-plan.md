# Router Test Plan: Degraded Merchant Mode on Physical Hardware

## Overview

End-to-end test of the degraded merchant mode on two GL.iNet MT3000 routers
connected via NetBird. The tests verify that a router boots gracefully when
mints are unreachable, continues serving cached-wallet operations, and
recovers automatically when internet returns.

## Test Environment

| Role  | Label | NetBird IP       | Notes                              |
|-------|-------|------------------|------------------------------------|
| Alpha | alpha | `100.90.41.166`  | Primary test target                |
| Beta  | beta  | `100.90.216.248` | Secondary / upstream TollGate      |

- **Architecture**: arm64 (aarch64_cortex-a53), built with `GOOS=linux GOARCH=arm64`
- **Deploy script**: `scripts/local-compile-to-router.sh <IP>`
- **Test runner**: `make -f Makefile.test r-<target> ROUTER=alpha`

## How We Simulate "Offline"

We cannot physically disconnect the router (we'd lose SSH via NetBird). Instead,
we use **iptables** to block traffic to the mint's IP while preserving NetBird
connectivity:

```sh
# Block mint
iptables -A OUTPUT -d <mint-ip> -j DROP

# Unblock mint
iptables -D OUTPUT -d <mint-ip> -j DROP
```

The Makefile.test targets resolve the mint hostname to an IP automatically.

## Mint Configuration

The test mint is `https://nofee.testnut.cashu.space` (Nutshell 0.18.2, old
keyset format compatible with gonuts v0.6.1, zero fees, auto-pays in ~2-3s).

**For "unreachable mint" tests**, we configure a TEST-NET address
(`http://192.0.2.1:12345`) that is guaranteed to never respond.

## Key Log Messages to Watch

| Event                          | Log message                                                    | Source              |
|--------------------------------|----------------------------------------------------------------|---------------------|
| Degraded mode boot             | `WARNING: No reachable mints detected. Starting in degraded mode.` | `merchant.go`       |
|                                | `Merchant started in degraded mode`                            | `main.go`           |
| Offline wallet loaded          | `Degraded mode: offline wallet loaded successfully, balance=N sats` | `merchant_degraded.go` |
| First boot (no wallet DB)      | `Degraded mode: offline wallet load failed (first boot...)`    | `merchant_degraded.go` |
| Recovery detected              | `Mint became reachable — attempting to upgrade from degraded mode` | `merchant.go`       |
| Upgrade complete               | `Upgrading from degraded to full merchant`                     | `main.go`           |
|                                | `=== Merchant ready ===`                                       | `merchant.go`       |

## What's Already Covered by Automated Tests

These scenarios are fully validated by the 6 integration tests in
`src/merchant/offline_wallet_integration_test.go` (all PASS):

1. **Offline wallet reload** — wallet loads from BoltDB when mint unreachable
2. **Offline balance reporting** — `GetBalance()` returns cached value
3. **Offline payment creation** — `SendWithOverpayment` works offline
4. **Health tracker recovery detection** — tracker detects mint coming back
5. **`onFirstReachable` callback fires** — callback mechanism validated
6. **TollWallet offline reload** — `TollWallet.New()` after `Shutdown()`
7. **Full lifecycle (online -> offline -> online)** — single wallet instance

## What Needs Manual Router Testing

The automated tests use a localhost proxy to simulate offline. On real hardware,
the service interacts with procd, hotplug, and real network interfaces. The
following scenarios can only be validated on physical routers.

---

## Part A: Basic Flow (Makefile.test phases 1-8)

These are the core scenarios with direct Makefile.test targets.

### Phase 1: Deploy and Verify Online Boot

```sh
make -f Makefile.test r-deploy ROUTER=alpha
make -f Makefile.test r-status ROUTER=alpha
make -f Makefile.test r-check-merchant ROUTER=alpha
```

**Pass criteria**:
- `tollgate version` returns version info
- Logs contain `=== Merchant ready ===` (full merchant, not degraded)
- `tollgate wallet balance` returns a balance (may be 0 if never funded)

### Phase 2: Record Baseline

```sh
make -f Makefile.test r-record-baseline ROUTER=alpha
```

**Pass criteria**:
- Balance printed and saved to `/tmp/tollgate-baseline-<router>.txt`

### Phase 3: Block Mint and Restart (Offline Boot)

```sh
make -f Makefile.test r-block-mint ROUTER=alpha
make -f Makefile.test r-restart-service ROUTER=alpha
sleep 5
make -f Makefile.test r-check-degraded ROUTER=alpha
```

**Pass criteria**:
- Logs contain `WARNING: No reachable mints detected. Starting in degraded mode.`
- Logs contain `Degraded mode: offline wallet loaded successfully, balance=N sats`
- `tollgate wallet balance` returns the **same** balance as Phase 2
- Service did NOT crash or enter a boot loop

### Phase 4: Verify Offline Operations

```sh
make -f Makefile.test r-test-offline-ops ROUTER=alpha
```

**Pass criteria**:
- `tollgate wallet balance` works (returns cached balance)
- `tollgate status` shows `running: true`, `wallet_ok: true`
- Service logs do NOT show any crash or panic

### Phase 5: Unblock Mint and Verify Recovery

```sh
make -f Makefile.test r-unblock-mint ROUTER=alpha
make -f Makefile.test r-wait-recovery ROUTER=alpha
```

**Pass criteria**:
- Logs eventually contain `Mint became reachable — attempting to upgrade from degraded mode`
- Logs contain `Upgrading from degraded to full merchant` OR `ERROR: Failed to upgrade from degraded mode` (BoltDB lock)

**NOTE on BoltDB locking**: Due to the BoltDB locking issue (documented in
`BOLTDB_LOCKING_FIX.md`), the in-process upgrade may fail with a timeout
error. This is a **known limitation** — the degraded merchant holds the
BoltDB file open, preventing the full merchant from opening it. The recovery
*mechanism* (tracker -> callback -> upgrade attempt) is still validated.
On real hardware, the hotplug script (`95-tollgate-restart`) restarts the
entire service when WAN comes up, which sidesteps the BoltDB issue entirely.

### Phase 6: Edge Case — First Boot Offline (No Wallet DB)

On Router B (beta), configure an unreachable mint and a fresh config dir.
Verify graceful degradation without crash.

```sh
make -f Makefile.test r-test-first-boot-offline ROUTER=beta
```

**Pass criteria**:
- Service starts without crashing
- Logs contain `Degraded mode: offline wallet load failed (first boot or no cached data)`
- `tollgate status` shows `running: true`
- Service stays running for 30+ seconds without restart (no boot loop)

### Phase 7: Edge Case — No Configured Mints

Remove all mints from config and restart. Verify the service doesn't crash.

```sh
make -f Makefile.test r-test-no-mints ROUTER=alpha
```

**Pass criteria**:
- Service starts without crashing
- Logs contain `Degraded mode: no configured mints, wallet not loaded`
- Service stays running for 30+ seconds

### Phase 8: Cleanup

```sh
make -f Makefile.test r-cleanup ROUTER=alpha
```

---

## Part B: Real-World Boot Sequence (Manual, Critical)

These tests validate the actual production scenario: router boots before WAN
is connected, then internet comes up later. This is the primary use case for
the degraded merchant feature and cannot be simulated by the automated tests.

### Test B1: Cold Boot with Upstream WiFi Still Connecting

This is the most important real-world test. The router's procd starts
`tollgate-wrt` at `START=95` before the upstream WiFi STA has connected.

**Steps** (manual — requires physical access or NetBird persistent across reboot):
1. SSH to Alpha: `reboot`
2. Wait ~60s for router to come back online
3. SSH to Alpha: check logs

```sh
ssh root@<alpha-ip> "logread -e tollgate | head -30"
ssh root@<alpha-ip> "tollgate status"
ssh root@<alpha-ip> "tollgate wallet balance"
```

**Pass criteria**:
- One of two outcomes is acceptable:
  - **(A) Full merchant**: If upstream WiFi connected fast enough, service
    starts in full merchant mode with `=== Merchant ready ===` in logs
  - **(B) Degraded -> recovery**: If upstream WiFi was slow, service starts
    in degraded mode, then either:
    - Hotplug script (`95-tollgate-restart`) restarts service when WAN comes
      up -> full merchant on second boot
    - OR proactive health checks detect recovery within 15 min -> in-process
      upgrade attempt
- No crash loop regardless of outcome

**Key insight**: The hotplug script `/etc/hotplug.d/iface/95-tollgate-restart`
triggers a full service restart when the WAN interface comes up. This means
the BoltDB locking issue is sidestepped on real hardware because the degraded
merchant process is killed and a new one starts with the mint reachable.

### Test B2: Cold Boot with WAN Already Up

Verify the normal case — service starts with internet already available.

**Steps**:
1. Ensure Alpha has upstream connectivity (check `ping -c 1 8.8.8.8`)
2. Restart service: `/etc/init.d/tollgate-wrt restart`
3. Wait 5s, check logs

```sh
ssh root@<alpha-ip> "/etc/init.d/tollgate-wrt restart"
sleep 5
ssh root@<alpha-ip> "logread -e tollgate | grep 'Merchant ready'"
```

**Pass criteria**:
- Logs contain `=== Merchant ready ===`
- Full merchant mode, wallet loaded

### Test B3: Service Restart While Offline

Already covered by Phase 3 (Makefile.test), but worth confirming with a
full `procd` cycle rather than just init.d restart.

**Steps**:
```sh
ssh root@<alpha-ip> "iptables -A OUTPUT -d <mint-ip> -j DROP"
ssh root@<alpha-ip> "/etc/init.d/tollgate-wrt stop; sleep 2; /etc/init.d/tollgate-wrt start"
sleep 5
ssh root@<alpha-ip> "logread -e tollgate | grep -E 'degraded|wallet loaded|panic'"
```

**Pass criteria**:
- Degraded mode, wallet loaded from disk, no crash

### Test B4: Hotplug Restart (WAN Up Event)

Verify the hotplug script triggers a service restart when WAN comes up,
recovering from degraded mode.

**Steps**:
1. Block mint, restart service (degraded mode)
2. Remove `/tmp/tollgate_initial_restart_done` flag (so hotplug will trigger)
3. Unblock mint
4. Simulate WAN interface up event

```sh
ssh root@<alpha-ip> "iptables -A OUTPUT -d <mint-ip> -j DROP"
ssh root@<alpha-ip> "/etc/init.d/tollgate-wrt restart"
sleep 3
ssh root@<alpha-ip> "rm -f /tmp/tollgate_initial_restart_done"
ssh root@<alpha-ip> "iptables -D OUTPUT -d <mint-ip> -j DROP"
ssh root@<alpha-ip> ". /etc/hotplug.d/iface/95-tollgate-restart"
sleep 5
ssh root@<alpha-ip> "logread -e tollgate | grep -E 'Merchant ready|degraded'"
```

**Pass criteria**:
- Hotplug script restarts the service
- Service starts in full merchant mode (mint is now reachable)
- Logs contain `=== Merchant ready ===`

---

## Part C: BoltDB Locking Behavior on Real Hardware (Validates Known Issue)

This test confirms the documented BoltDB limitation and verifies that the
real-world recovery path (hotplug restart) works around it.

### Test C1: In-Process Recovery (No Service Restart)

The proactive health checks detect the mint is back and attempt an in-process
upgrade. The BoltDB lock prevents this from succeeding, but the attempt should
not hang the goroutine forever (or if it does, the hotplug restart provides
the actual recovery path).

**Steps**:
1. Block mint, restart service (degraded mode)
2. Unblock mint (do NOT restart service)
3. Wait up to 15 min for proactive checks to detect recovery
4. Observe the upgrade attempt in logs

```sh
ssh root@<alpha-ip> "iptables -A OUTPUT -d <mint-ip> -j DROP"
ssh root@<alpha-ip> "/etc/init.d/tollgate-wrt restart"
sleep 3
ssh root@<alpha-ip> "iptables -D OUTPUT -d <mint-ip> -j DROP"
# Wait and watch — up to 15 min
ssh root@<alpha-ip> "timeout 960 logread -e tollgate -f" | grep -i "reachable\|upgrade\|BoltDB"
```

**Expected outcomes** (either is acceptable):
- **Best case**: `Mint became reachable` followed by `Upgrading from degraded
  to full merchant` — BoltDB lock was released in time, upgrade succeeded
- **Known limitation**: `Mint became reachable` followed by `ERROR: Failed to
  upgrade from degraded mode: timeout` — BoltDB lock blocked the upgrade.
  This is documented and the fix is upstream (gonuts-tollgate).
  The recovery still works via hotplug restart (Test B4).

---

## Part D: Multi-Mint Scenarios (Not Tested in Integration)

The integration tests use a single mint. Real routers may have multiple mints.

### Test D1: One Mint Reachable, One Unreachable

Configure 2 mints where only one is reachable. Service should start in full
merchant mode (at least 1 mint reachable).

**Steps**:
```sh
ssh root@<alpha-ip> "cp /etc/tollgate/config.json /etc/tollgate/config.json.bak"
ssh root@<alpha-ip> "cat /etc/tollgate/config.json | jq '.accepted_mints = [
  {\"url\": \"https://nofee.testnut.cashu.space\", \"price_per_step\": 1, \"price_unit\": \"sats\"},
  {\"url\": \"http://192.0.2.1:12345\", \"price_per_step\": 1, \"price_unit\": \"sats\"}
]' > /tmp/config.json && mv /tmp/config.json /etc/tollgate/config.json"
ssh root@<alpha-ip> "/etc/init.d/tollgate-wrt restart"
sleep 5
ssh root@<alpha-ip> "logread -e tollgate | grep -E 'Merchant ready|degraded'"
```

**Pass criteria**:
- Full merchant mode (at least 1 mint reachable)
- Wallet loads with the reachable mint
- Payout routine starts for reachable mint only

### Test D2: Both Mints Down, One Comes Back

Both mints unreachable -> degraded -> one mint recovers.

**Steps**:
```sh
# Block the real mint, keep the fake one
ssh root@<alpha-ip> "iptables -A OUTPUT -d <nofee-mint-ip> -j DROP"
ssh root@<alpha-ip> "/etc/init.d/tollgate-wrt restart"
sleep 5
# Verify degraded
ssh root@<alpha-ip> "logread -e tollgate | grep 'degraded'"
# Unblock
ssh root@<alpha-ip> "iptables -D OUTPUT -d <nofee-mint-ip> -j DROP"
# Wait for recovery
ssh root@<alpha-ip> "timeout 960 logread -e tollgate -f" | grep -i "reachable\|upgrade"
```

**Pass criteria**:
- Degraded mode when both mints unreachable
- Recovery detected when real mint comes back

### Cleanup after D1/D2

```sh
ssh root@<alpha-ip> "mv /etc/tollgate/config.json.bak /etc/tollgate/config.json"
ssh root@<alpha-ip> "iptables -D OUTPUT -d <nofee-mint-ip> -j DROP 2>/dev/null"
ssh root@<alpha-ip> "/etc/init.d/tollgate-wrt restart"
```

---

## Part E: USM + Degraded Merchant Integration

The upstream session manager calls `merchant.GetMerchant().CreatePaymentTokenWithOverpayment()`
for upstream payments. This tests the critical path where a degraded router
connects to an upstream TollGate and needs to pay for service.

### Test E1: USM Payment While Degraded (Wallet Loaded)

Alpha is in degraded mode with a funded wallet. Alpha connects to Beta
(which is running as an upstream TollGate). USM tries to pay.

**Preconditions**:
- Alpha has a funded wallet (funded in Phase 1)
- Beta is running as a TollGate with a reachable mint
- Alpha can reach Beta via network (they can see each other's TollGate APs or are on the same LAN)

**Steps**:
1. Block Alpha's mint (degraded mode, wallet loaded)
2. Alpha discovers Beta as upstream TollGate
3. USM on Alpha attempts to create a session and pay

```sh
# On alpha: block mint, restart degraded
ssh root@<alpha-ip> "iptables -A OUTPUT -d <mint-ip> -j DROP"
ssh root@<alpha-ip> "/etc/init.d/tollgate-wrt restart"
sleep 5
# Verify degraded
ssh root@<alpha-ip> "logread -e tollgate | grep 'degraded'"
# Watch USM logs for session creation attempts
ssh root@<alpha-ip> "logread -e tollgate -f" | grep -i "upstream\|session\|payment"
```

**Pass criteria**:
- `CreatePaymentTokenWithOverpayment` on degraded merchant works (uses cached wallet)
- USM successfully creates a session with Beta
- OR if the upstream probe/advertisement fetch fails for other reasons, the
  USM logs show a clear error (not a crash)

### Test E2: USM Payment After Recovery

After Alpha recovers to full merchant, verify USM payments work normally.

**Steps**:
```sh
ssh root@<alpha-ip> "iptables -D OUTPUT -d <mint-ip> -j DROP"
# Wait for recovery or restart
ssh root@<alpha-ip> "/etc/init.d/tollgate-wrt restart"
sleep 5
ssh root@<alpha-ip> "logread -e tollgate | grep -E 'Merchant ready|upstream.*session'"
```

**Pass criteria**:
- Full merchant mode
- USM session creation works normally

---

## Part F: Procd and Resource Constraints on Embedded Hardware

### Test F1: Memory Usage in Degraded Mode

Verify no memory leak when running in degraded mode.

**Steps**:
```sh
ssh root@<alpha-ip> "iptables -A OUTPUT -d <mint-ip> -j DROP"
ssh root@<alpha-ip> "/etc/init.d/tollgate-wrt restart"
sleep 5
ssh root@<alpha-ip> "top -b -n1 | grep tollgate"
# Wait 5 minutes
sleep 300
ssh root@<alpha-ip> "top -b -n1 | grep tollgate"
```

**Pass criteria**:
- RSS stays stable (no more than ~10% growth over 5 min)
- Comparable to full merchant mode memory usage

### Test F2: Procd Auto-Restart

Kill the process and verify procd restarts it into degraded mode.

**Steps**:
```sh
ssh root@<alpha-ip> "kill -9 \$(pidof tollgate-wrt)"
sleep 10
ssh root@<alpha-ip> "pidof tollgate-wrt && echo OK_PROCESS_RUNNING || echo FAIL_NOT_RUNNING"
ssh root@<alpha-ip> "logread -e tollgate | grep -E 'degraded|Merchant ready' | tail -3"
```

**Pass criteria**:
- Procd restarts the service within ~10s
- Service enters degraded mode again (mint still blocked)
- No crash loop

### Test F3: Repeated Restart Cycles

Stress test: restart the service 5 times in a row while offline.

**Steps**:
```sh
for i in 1 2 3 4 5; do
  ssh root@<alpha-ip> "/etc/init.d/tollgate-wrt restart"
  sleep 10
  ssh root@<alpha-ip> "pidof tollgate-wrt && echo \"Cycle \$i: OK\" || echo \"Cycle \$i: FAIL\""
done
ssh root@<alpha-ip> "top -b -n1 | grep tollgate"
```

**Pass criteria**:
- Service consistently enters degraded mode each time
- No crash loop on any cycle
- Memory doesn't grow across restarts

---

## Proposed Execution Order

| Phase | Tests | Router | Duration | Depends On |
|-------|-------|--------|----------|------------|
| **1** | A1-A4: Basic flow | Alpha + Beta | ~25 min | Deploy |
| **2** | B1-B4: Boot sequence | Alpha | ~20 min | Phase 1 |
| **3** | C1: BoltDB behavior | Alpha | ~20 min | Phase 1 |
| **4** | D1-D2: Multi-mint | Alpha | ~15 min | Phase 1 |
| **5** | E1-E2: USM integration | Alpha + Beta | ~20 min | Phase 1, Beta running as TollGate |
| **6** | F1-F3: Resource constraints | Alpha | ~15 min | Phase 1 |

**Total estimated time: ~2 hours** across both routers.

## Pre-Test Setup

1. **Fund wallet on Alpha** — needs some sats for USM payment tests (E1)
2. **Ensure Beta is running a compatible TollGate** — Alpha needs to detect it as upstream
3. **Back up configs** — `ssh root@<ip> "cp /etc/tollgate/config.json /etc/tollgate/config.json.original"`
4. **Resolve mint IP** — `nslookup nofee.testnut.cashu.space` to get the IP for iptables rules

## Quick Smoke Tests

### Offline smoke (Part A phases 2-4)

```sh
make -f Makefile.test r-smoke-offline ROUTER=alpha
```

### Recovery smoke (Part A phase 5)

```sh
make -f Makefile.test r-smoke-recovery ROUTER=alpha
```

### Full Part A suite

```sh
make -f Makefile.test r-full ROUTER=alpha
```

## Recovery Procedures

| Scenario                                  | Recovery Command                                                                  |
|-------------------------------------------|-----------------------------------------------------------------------------------|
| Service crashed / boot loop               | `ssh root@<ip> "/etc/init.d/tollgate-wrt stop; sleep 2; /etc/init.d/tollgate-wrt start"` |
| Leftover iptables rules                   | `ssh root@<ip> "iptables -F OUTPUT"` (flush all — use with caution on production) |
| Corrupted config                          | `ssh root@<ip> "rm /etc/tollgate/config.json; /etc/init.d/tollgate-wrt restart"`   |
| Need to restore original config           | `ssh root@<ip> "mv /etc/tollgate/config.json.bak /etc/tollgate/config.json"`       |
| Total connectivity loss (can't SSH)       | Use router's LAN IP (192.168.1.1) from local network                              |

## Differences from wifiscan Branch Tests

| Aspect              | wifiscan (`feature/wifiscan`)         | This PR (`94-degraded-merchant`)          |
|---------------------|---------------------------------------|--------------------------------------------|
| What's tested       | WiFi scanning, STA switching          | Mint health, wallet degradation/recovery   |
| Offline simulation  | Physical WiFi disconnect               | iptables blocking mint IP                  |
| Recovery trigger    | Manual reconnect                       | Automatic (5-min proactive checks) + hotplug |
| Recovery time       | Immediate (WiFi reconnect)             | Up to 15 min (proactive) or instant (hotplug) |
| Key risk            | Router loses upstream permanently      | BoltDB lock prevents in-process upgrade    |
| Uses Router B       | As upstream WiFi AP                    | As upstream TollGate for USM tests         |
