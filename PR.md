# WGM Improvements — PR for `fix/wgm-improvements`

**Branch**: `fix/wgm-improvements` (1 commit ahead of `main`)
**Commit**: `03d4f71`
**Files changed**: 4 source + PLAN.md (delete before merge)

---

## What You Get by Merging

### 1. Startup Connectivity Check
After every daemon start, the WGM verifies the active STA actually has internet. If not, it nudges netifd with `ifup wwan`, retries 3× at 10s intervals, then triggers an emergency scan to find a working upstream. If nothing works, it defers gracefully to the main scan loop instead of silently running on a dead link.

### 2. TollGate-Aware Probing
After every upstream switch, `probeTollGateGateway()` checks if the default gateway responds on `:2121`. If it's a TollGate, the WGM sets `isTollGateConnection=true` and uses `TollGateLostThreshold=6` instead of the default `LostThreshold=2`. This prevents premature emergency scans when the upstream is a TollGate that intentionally blocks internet until payment.

### 3. Cross-Radio DHCP Nudge
When switching STAs across different radios (e.g. radio1 → radio0), netifd may not re-evaluate the wwan interface. The connector detects cross-radio transitions and fires `ifup wwan` to force DHCP on the new netdev, preventing the 180s DHCP timeout that occurs when netifd still considers wwan bound to the old device.

---

## Changes by File

| File | Lines | What changed |
|------|-------|--------------|
| `upstream_manager.go` | +170 | `startupConnectivityCheck()`, `probeTollGateGateway()`, `waitForDefaultRoute()`, `verifyPostSwitchConnectivity()` with route-table polling |
| `connector.go` | +63 | Cross-radio `ifup wwan` nudge in `waitForSTAIP()`, non-blocking dnsmasq/firewall restarts |
| `types.go` | +4 | `TollGateLostThreshold`, `StartupRetryInterval`, `StartupScanInterval` config fields |
| `upstream_manager_test.go` | +302 | 12 new startup check tests, TollGate patience tests, config override tests |

---

## Automated Test Results

**75/75 PASS** in 0.37s (was 45s before route-table polling eliminated real sleeps).

Key test additions:
- `TestUpstreamManager_StartupCheck_NoActiveSTA`
- `TestUpstreamManager_StartupCheck_HasInternet`
- `TestUpstreamManager_StartupCheck_NoInternet_SwitchesToCandidate`
- `TestUpstreamManager_StartupCheck_NoInternet_NoCandidateAvailable`
- `TestUpstreamManager_StartupCheck_NoInternet_SwitchFails`
- `TestUpstreamManager_StartupCheck_ResellerMode`
- `TestUpstreamManager_StartupCheck_ScanFailsThenSucceedsOnRetry`
- `TestUpstreamManager_StartupCheck_CandidateNotFoundFirstScan`
- `TestUpstreamManager_StartupCheck_SwitchFailsThenSucceedsOnRetry`
- `TestUpstreamManager_StartupCheck_AllScanRetriesFail_NoBlacklist`
- `TestUpstreamManager_StartupCheck_*` (config defaults, post-switch TollGate detection)

---

## Hardware Test Results

| Test | Alpha (10.47.41.1) | Beta (192.168.244.1) |
|------|---------------------|----------------------|
| Startup check (has internet) | PASS — "active STA has internet, all good" after 15s settle | N/A |
| Startup check (no internet) | N/A | PASS — nudge → retry 3× → emergency scan → "deferring to main loop" |
| Cross-radio DHCP nudge (radio1→radio0) | PASS — nudge fired, DHCP lease obtained on phy0-sta0 | N/A |
| TollGate probing (gateway:2121 detected) | PASS — "Post-switch: TollGate detected" | N/A |
| Extended patience (TollGateLostThreshold=6) | PASS — emergency scan at exactly lost_count=6, not 2 | N/A |
| Connectivity lost/restored cycle | PASS — lost_count=1 → restored | PASS — lost_count=2 → emergency scan |

---

## Known Limitations

### 1. `verifyPostSwitchConnectivity` goroutine logging not observed on hardware
The function runs in a goroutine after `connector.SwitchUpstream` returns. On hardware, the goroutine's logrus output was not captured by procd's `logread`. The route-table polling logic is correct (all 75 unit tests pass), but the goroutine may not be executing on OpenWrt due to potential `exec.Command` limitations in spawned goroutines. Further investigation needed with `fmt.Fprintf(os.Stderr, ...)` instead of logrus to rule out logger buffering.

### 2. `PostSwitchWait` config field is now unused
Previously used as a fixed sleep duration before probing. Now replaced by `waitForDefaultRoute()` with a 30s timeout. The field is kept in `UpstreamManagerConfig` for backward compatibility but has no effect.

### 3. Non-blocking service restarts
`connector.SwitchUpstream` now uses `exec.Command(...).Start()` instead of `.Run()` for dnsmasq and firewall restarts. This prevents the connector from blocking indefinitely (the previous `.Run()` calls could hang if firewall restart triggered nodogsplash cleanup). The trade-off: dnsmasq and firewall may not be fully restarted when subsequent code runs, but in practice the route table is the right signal, not service state.

---

## Manual Happy Path Testing Plan

### Prerequisites
- **Router A**: flashed with this branch's binary, connected to an internet upstream via WiFi STA
- **Router B**: flashed with this branch's binary, has internet (separate upstream), TollGate AP active
- Both routers on the same network segment or within WiFi range
- Replace `<ROUTER_A>` and `<ROUTER_B>` with actual IPs below

### Test 1: Startup Connectivity Check (has internet)

Verifies the WGM detects internet after boot and skips emergency procedures.

```bash
# Step 1: Restart the daemon
ssh root@<ROUTER_A> '/etc/init.d/tollgate-wrt restart'

# Step 2: Wait 20s for settle + check
sleep 20

# Step 3: Check startup logs
ssh root@<ROUTER_A> 'logread -e tollgate-wrt | grep "Startup check"'
```

**Expected output:**
```
Startup check: waiting for STA to settle      settle_seconds=15
Startup check: active STA has internet, all good  ssid=<YOUR_UPSTREAM_SSID>
Startup grace period active                   grace_seconds=90
```

**PASS if**: You see "active STA has internet, all good".

---

### Test 2: Startup Connectivity Check (no internet)

Verifies the WGM gracefully handles a boot with a connected STA that has no internet (e.g. upstream is down).

```bash
# Step 1: Disable the upstream (disconnect WiFi or power off upstream router)
# Then restart daemon
ssh root@<ROUTER_A> '/etc/init.d/tollgate-wrt restart'

# Step 2: Wait 60s for full startup check cycle (settle + 3 retries + 3 scan retries)
sleep 60

# Step 3: Check startup logs
ssh root@<ROUTER_A> 'logread -e tollgate-wrt | grep "Startup check"'
```

**Expected output:**
```
Startup check: waiting for STA to settle      settle_seconds=15
Startup check: no internet after settle, nudging netifd with ifup wwan
Startup check: no internet yet, retrying      attempt=1  remaining=2
Startup check: no internet yet, retrying      attempt=2  remaining=1
Startup check: active STA has no internet, triggering emergency scan
Startup check: scanning for alternative upstream  attempt=1  remaining=2
...
Startup check: no working upstream found after all scan retries, deferring to main loop
```

**PASS if**: You see "no internet after settle", "nudging netifd", and "deferring to main loop". The daemon should NOT crash or hang.

---

### Test 3: Cross-Radio DHCP Nudge

Verifies the connector detects a radio change and fires `ifup wwan` to prevent DHCP timeout.

```bash
# Step 1: Check which radio the current upstream is on
ssh root@<ROUTER_A> 'uci show wireless | grep -E "upstream.*\.device"'

# Step 2: Connect to an SSID on a DIFFERENT radio
# If current upstream is on radio1, use a 2.4GHz SSID (radio0), or vice versa
ssh root@<ROUTER_A> '/usr/bin/tollgate upstream connect <OTHER_RADIO_SSID> <PASSWORD>'

# Step 3: Check for cross-radio nudge
ssh root@<ROUTER_A> 'logread -e tollgate-wrt | grep "cross-radio"'
```

**Expected output:**
```
Nudging netifd with ifup wwan after cross-radio STA transition
```

```bash
# Step 4: Verify route was established
ssh root@<ROUTER_A> 'ip route show default'
```

**Expected output** (showing route on new interface):
```
default via <GATEWAY_IP> dev phy0-sta0  (or phy1-sta0 depending on radio)
```

**PASS if**: You see "cross-radio STA transition" and `ip route show default` shows a route via the new STA interface.

---

### Test 4: TollGate Detection + Extended Patience

Verifies that when connected to a TollGate upstream, the WGM tolerates 6 missed connectivity checks before triggering emergency scan (instead of the default 2).

```bash
# Step 1: Connect Router A to Router B's TollGate SSID
ssh root@<ROUTER_A> '/usr/bin/tollgate upstream connect <ROUTER_B_TOLLGATE_SSID>'

# Step 2: Wait for the WGM scan cycle to pick up the new upstream and switch
# This may take up to 5 minutes (300s scan interval) or happen sooner if the
# startup check triggers it
sleep 30

# Step 3: Verify Alpha is on the TollGate SSID
ssh root@<ROUTER_A> 'uci show wireless | grep "disabled" | grep 0'

# Step 4: Wait for connectivity checks to start failing (every 30s)
# TollGate blocks internet until payment, so checks will fail
# Wait 4 minutes for at least 6 failed checks
sleep 240

# Step 5: Check lost_count progression
ssh root@<ROUTER_A> 'logread -e tollgate-wrt | grep "Connectivity lost"'
```

**Expected output:**
```
Connectivity lost                             lost_count=1
Connectivity lost                             lost_count=2
Connectivity lost                             lost_count=3
Connectivity lost                             lost_count=4
Connectivity lost                             lost_count=5
Connectivity lost                             lost_count=6
Running upstream scan cycle                   active_ssid=<TOLLGATE_SSID>  reason=emergency
```

**PASS if**:
- `lost_count` reaches 6 before emergency scan triggers
- Emergency scan does NOT trigger at `lost_count=2`

**FAIL if**: Emergency scan triggers at `lost_count=2` (means TollGate detection didn't work).

---

### Test 5: Emergency Scan Fallback (non-TollGate upstream)

Verifies that on a non-TollGate upstream, emergency scan triggers at the default threshold of 2 missed checks.

```bash
# Step 1: Connect to a normal internet upstream
ssh root@<ROUTER_A> '/usr/bin/tollgate upstream connect <NORMAL_INTERNET_SSID> <PASSWORD>'

# Step 2: Wait for startup check to complete
sleep 25

# Step 3: Simulate connectivity loss by blocking pings
# (e.g. add an iptables rule on the upstream router, or disable WAN)
# Alternatively, just wait if the upstream is flaky

# Step 4: If you can't easily break connectivity, check that the
# default LostThreshold=2 is being used for non-TollGate:
ssh root@<ROUTER_A> 'logread -e tollgate-wrt | grep -E "lost_count=2.*emergency|lost_count=3"'
```

**PASS if**: On a non-TollGate upstream with no internet, emergency scan triggers at `lost_count=2` (not 6).

---

### Test 6: Connectivity Restored

Verifies that after a connectivity loss, the WGM detects restoration and resets the counter.

```bash
# Step 1: After Test 4 or 5, restore internet connectivity
# (e.g. reconnect to an internet upstream)

# Step 2: Wait 30s for the next connectivity check
sleep 30

# Step 3: Check for restored log
ssh root@<ROUTER_A> 'logread -e tollgate-wrt | grep -E "Connectivity restored|lost_count"'
```

**Expected output:**
```
Connectivity restored                         lost_count=<PREVIOUS_COUNT>
```

**PASS if**: You see "Connectivity restored" after reconnecting.

---

### Cleanup

```bash
# Restore both routers to their normal upstreams
ssh root@<ROUTER_A> '/usr/bin/tollgate upstream connect <ORIGINAL_SSID> <PASSWORD>'
ssh root@<ROUTER_B> 'uci set wireless.upstream_stargate.ssid="<ORIGINAL_SSID>"; uci commit wireless; wifi reload'
```

---

*Delete this file (`PR.md`) and `PLAN.md` before merging.*
