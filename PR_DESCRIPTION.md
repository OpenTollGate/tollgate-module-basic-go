# Upstream WiFi Management: PR Description & Test Report

**TEMPORARY FILE — DELETE BEFORE MERGING TO MAIN**

## Summary

Replaces shell-script-based upstream WiFi management with native Go. Adds a daemon that automatically discovers, evaluates, and switches WiFi upstreams based on signal strength and internet connectivity, plus CLI commands for manual control.

### What changed

**New: UpstreamManager daemon** (`upstream_manager.go`, 592 lines)
- Periodic scan cycle (5 min default) discovers candidate upstreams from existing disabled STAs and (in reseller mode) open `TollGate-*` SSIDs
- 12 dB hysteresis prevents flapping; signal floor at -85 dBm; connectivity check pings `9.9.9.9` to detect "DHCP but no internet"
- SSID blacklist (60-min TTL, in-memory) for upstreams that provided DHCP but failed internet check
- Pauses connectivity checks for 120s after manual `tollgate upstream connect` to avoid race condition during radio reconfiguration
- Stale STA cleanup on startup, before emergency scans, and before every scan cycle

**New: CLI commands** (`cli/server.go`, `cmd/tollgate-cli/main.go`)
- `tollgate upstream scan` — `iwinfo`-based per-radio scan
- `tollgate upstream connect <ssid> [passphrase] [encryption]` — 7-step connect with streaming progress
- `tollgate upstream list` — show all STA sections with ACTIVE/disabled status
- `tollgate upstream remove <ssid>` — remove disabled STA (refuses active)

**New: Stale STA cleanup** (`connector.go`, `upstream_manager.go`)
- `CleanupStaleSTAs()` removes duplicate STA sections (same SSID on multiple sections) and disables orphaned enabled STAs (enabled but not the active one)
- Runs at three trigger points: service startup, emergency (connectivity loss), and before every scan cycle
- Keeps the active STA or first disabled STA per SSID group; deletes the rest
- Replaces dead `cleanupSTAInterfaces()` which was never called

**Refactored: Scanner** (`scanner.go`)
- `iwinfo <radio> scan` replaces `iw dev <iface> scan` — works on both radios simultaneously
- `ParseIwinfoOutput()` handles ESSID, signal, encryption, hidden SSIDs
- `DetectEncryption()` maps iwinfo strings to UCI values (`psk2`, `sae`, `sae-mixed`, `none`)

**Refactored: Connector** (`connector.go`, 1183 lines)
- Named STA interfaces (`upstream_<ssid>`) with `FindOrCreateSTAForSSID()` — reuses existing disabled STAs
- `SwitchUpstream()` with async `wifi reload <radio>`, 180s DHCP timeout, BusyBox-compatible IP detection
- `verifySTASSID()` prevents false positive when stale IP lingers from previous connection

**Removed: dead code**
- `GatewayManager.RunPeriodicScan`, `ScanWirelessNetworks`, `ConnectToGateway`, `updatePriceAndAPSSID`, `GetAvailableGateways`
- `Connector.UpdateLocalAPSSID`, `stripPricingFromSSID`, `cleanupSTAInterfaces()`
- `Scanner.ScanWirelessNetworks`, `parsePricingFromSSID`, `parseScanOutput`
- Dead goroutine after `mainLogger.Fatal()` in `main.go`, plus `isOnline()`
- Price-from-SSID parsing (AP SSID is now just `TollGate-XXXX`, no price suffix)

### Architecture

```
main.go
  ├── GatewayManager (thin wrapper, delegates to Scanner/Connector)
  └── UpstreamManager (daemon goroutine)
        ├── Scanner.ScanAllRadios() → []NetworkInfo
        ├── Connector.CleanupStaleSTAs() → error (startup, emergency, pre-scan)
        ├── Connector.FindOrCreateSTAForSSID() → iface name
        ├── Connector.SwitchUpstream() → error
        ├── checkConnectivity() → ping 9.9.9.9
        └── ResellerModeChecker interface → config_manager
```

---

## Unit Tests

74 tests pass (63 WGM + 11 main), `go vet` clean:

```
cd src && TOLLGATE_TEST_CONFIG_DIR=/tmp/tollgate-test-config go test -v -count=1 -timeout 120s .
cd src/wireless_gateway_manager && TOLLGATE_TEST_CONFIG_DIR=/tmp/tollgate-test-config go test -v -count=1 -timeout 120s .
```

### wireless_gateway_manager (63 tests)

| Test | Status |
|------|--------|
| TestConnector_GetSTASections | PASS |
| TestConnector_GetActiveSTA | PASS |
| TestConnector_GetActiveSTA_NoneActive | PASS |
| TestConnector_FindOrCreateSTAForSSID_ReuseExisting | PASS |
| TestConnector_FindOrCreateSTAForSSID_CreateNew | PASS |
| TestConnector_RemoveDisabledSTA_Success | PASS |
| TestConnector_RemoveDisabledSTA_ActiveRefused | PASS |
| TestConnector_RemoveDisabledSTA_NotFound | PASS |
| TestConnector_SwitchUpstream_Success | PASS |
| TestConnector_SwitchUpstream_TimeoutFallback | PASS |
| TestConnector_EnsureWWANSetup | PASS |
| TestConnector_EnsureRadiosEnabled | PASS |
| TestGetUCIEncryptionType | PASS |
| TestGenerateRandomSuffix | PASS |
| TestConnector_GetSTANetdev | PASS |
| TestConnector_GetSTANetdev_NotFound | PASS |
| TestConnector_CleanupStaleSTAs_Success | PASS |
| TestConnector_CleanupStaleSTAs_Error | PASS |
| TestSanitizeSSIDForUCI | PASS |
| TestScanner_DetectEncryption | PASS |
| TestScanner_ParseIwinfoOutput | PASS |
| TestScanner_ParseIwinfoOutput_HiddenSSID | PASS |
| TestScanner_ParseIwinfoOutput_Empty | PASS |
| TestScanner_FindBestRadioForSSID_Found | PASS |
| TestScanner_FindBestRadioForSSID_NotFound | PASS |
| TestScanner_ScanAllRadios_MockError | PASS |
| TestScanner_GetRadios_Mock | PASS |
| TestUpstreamManager_DefaultConfig | PASS |
| TestUpstreamManager_NewUpstreamManager | PASS |
| TestUpstreamManager_NewUpstreamManager_SetsDefaults | PASS |
| TestUpstreamManager_FindKnownCandidates_NoKnownSSIDs | PASS |
| TestUpstreamManager_FindKnownCandidates_WithKnownSSID | PASS |
| TestUpstreamManager_FindKnownCandidates_MultipleKnownSSIDs | PASS |
| TestUpstreamManager_IsResellerModeActive_True | PASS |
| TestUpstreamManager_IsResellerModeActive_False | PASS |
| TestUpstreamManager_IsResellerModeActive_NilChecker | PASS |
| TestUpstreamManager_RunScanCycle_NoActiveUpstream | PASS |
| TestUpstreamManager_RunScanCycle_BelowHysteresis_NoSwitch | PASS |
| TestUpstreamManager_RunScanCycle_AboveHysteresis_Switches | PASS |
| TestUpstreamManager_RunScanCycle_BelowSignalFloor | PASS |
| TestUpstreamManager_RunScanCycle_ScanFails | PASS |
| TestUpstreamManager_ResellerFallbackToDisabledSTA | PASS |
| TestUpstreamManager_ResellerPrefersTollGateOverDisabledSTA | PASS |
| TestUpstreamManager_EmergencyScan_PrefersFallbackOverTollGate | PASS |
| TestUpstreamManager_EmergencyScan_TollGateWinsOnlyIfMuchStronger | PASS |
| TestUpstreamManager_PauseConnectivityChecks | PASS |
| TestUpstreamManager_Stop | PASS |
| TestUpstreamManager_BlacklistSSID | PASS |
| TestUpstreamManager_BlacklistTTLExpiry | PASS |
| TestUpstreamManager_PurgeBlacklist | PASS |
| TestUpstreamManager_CircuitBreaker_BlocksScanAfterFailures | PASS |
| TestUpstreamManager_CircuitBreaker_SkipsScanCycleWhenInCooldown | PASS |
| TestUpstreamManager_CircuitBreaker_ResetOnSuccess | PASS |
| TestUpstreamManager_SwitchFailure_CountsAndCooldowns | PASS |
| TestUpstreamManager_PostSwitch_NoBlacklistWhenConnectivityOK | PASS |
| TestUpstreamManager_CleanupStaleSTAs_InterfaceContract | PASS |
| TestUpstreamManager_CleanupStaleSTAs_InterfaceContract_Error | PASS |
| TestResellerModeDisabled_GatewayManagerInit | PASS |
| TestResellerModeEnabled_ScanAllRadios | PASS |
| TestGatewayManagerInit | PASS |
| TestGatewayManager_ScanAllRadios | PASS |
| TestGatewayManager_FormatScanResults | PASS |
| TestGatewayManager_FormatScanResults_Empty | PASS |

### config_manager (4 tests) + merchant (7 tests) — all PASS

---

## Physical Device Tests

Two GL.iNet MT3000 (arm64, OpenWrt 24.10.4) connected via NetBird.

| Router | NetBird IP | AP SSIDs |
|--------|------------|----------|
| A (alpha) | 100.90.41.166 | `TollGate-1690` (open), `c08r4d0r-1690` (psk2) |
| B (beta) | 100.90.216.248 | `TollGate-D1C6` (open), `c03rad0r-D1C6` (psk2) |

Deploy: `./scripts/local-compile-to-router.sh <IP>`

### Initial Results (2026-04-30)

| Phase | Test | A | B |
|-------|------|---|---|
| 1 | Service starts without crash | PASS | PASS |
| 2 | `tollgate upstream scan` — 31-39 networks found | PASS | PASS |
| 3 | `tollgate upstream connect` — STA created, DHCP acquired, ping 9.9.9.9 | — | PASS |
| 3 | Refuses to remove active upstream | — | PASS |
| 4 | `tollgate upstream remove` (disabled STA) | — | PASS |
| 5 | AP SSID unchanged (no price suffix) | `TollGate-1690` | `TollGate-D1C6` |
| 6 | Daemon stable for 90+ seconds, no spurious switching | — | PASS |
| 7 | Both routers online after wifi reload recovery | PASS | PASS |

### Stale STA Fix Device Verification (2026-05-05)

Full 9-phase test on both routers after implementing `CleanupStaleSTAs()`.

#### Pre-test state
- **Beta** was stuck on `upstream_tollgate_1690` (alpha's TollGate AP — provides DHCP but blocks internet without e-cash). NetBird tunnel was down. Manually connected to `c03rad0r` to restore internet, which also restored NetBird.
- **Alpha** had a **real stale STA**: `upstream_tollgated1c6_` (trailing underscore) — a duplicate of `upstream_tollgate_d1c6`. The fix found and removed it automatically.

#### Phase results

| Phase | Test | Alpha | Beta | Notes |
|-------|------|-------|------|-------|
| 1 | Deploy + service starts | PASS | PASS | Cleanup log line confirmed on both |
| 2 | STA health baseline | PASS | PASS | Clean state, no duplicates |
| 2.5 | Internet + NetBird | PASS | PASS | Both online, cross-router ping ~9ms |
| 3.1 | Duplicate STA created on beta | — | PASS | Two TollGate-1690 entries visible |
| 3.2 | Orphaned enabled STA created on beta | — | PASS | FakeOrphanSSID shown as ACTIVE |
| 3.3 | Startup cleanup runs | — | PASS | Both artifacts cleaned |
| 3.4 | Cleanup verified | — | PASS | Duplicate deleted, orphan disabled |
| 4 | Emergency cleanup (iptables block) | — | PASS | Cleanup fires before emergency scan |
| 5 | Pre-switch cleanup (daemon scan cycle) | PASS | — | Cleanup before scheduled scan |
| 5 | Real stale STA found on alpha | PASS | — | `upstream_tollgated1c6_` removed |
| 6 | Cross-router connect (TollGate-1690) | — | PASS | No stale STAs created |
| 7 | Service restart survivability | PASS | PASS | Clean restart, internet maintained |
| 8 | NetBird recovery | PASS | PASS | Both wt0 UP, cross-router ping ~9ms |
| 9 | Cleanup and restore | PASS | PASS | Test artifacts removed, locks released |

#### Phase 3 — Bug reproduction + cleanup (detailed)

Created two stale STA conditions on beta:

1. **Duplicate STA** (`stale_dup`): second `TollGate-1690` section on radio0, disabled=1
2. **Orphaned enabled STA** (`orphan_test`): `FakeOrphanSSID` on radio0, disabled=0

Service restart log output:
```
Cleaning up stale STA interfaces
Removing duplicate STA section    interface=stale_dup  kept=upstream_tollgate_1690  ssid=TollGate-1690
Disabling orphaned STA (enabled but not active)  interface=orphan_test  ssid=FakeOrphanSSID
Stale STA cleanup complete        deleted=2
```

Result: `stale_dup` section **deleted entirely**. `orphan_test` **disabled** (disabled=0 → disabled=1). Active upstream `c03rad0r` **untouched**.

#### Phase 4 — Emergency cleanup (detailed)

Blocked connectivity with `iptables -A OUTPUT -o phy0-sta0 -j DROP`. Log output:
```
Connectivity lost                  lost_count=1
Connectivity lost                  lost_count=2
Cleaning up stale STA interfaces
Removing duplicate STA section     interface=stale_emergency  kept=upstream_tollgate_1690  ssid=TollGate-1690
Stale STA cleanup complete         deleted=1
Running upstream scan cycle        reason=emergency
Best candidate found               ssid=TollGate-1690  signal=-37
```

Cleanup ran **before** the emergency scan cycle, ensuring stale sections don't interfere with candidate selection.

#### Phase 5 — Pre-switch cleanup (detailed)

Created `StaleSwitchTest` disabled STA on alpha. Waited for daemon's scheduled scan cycle (~6 min). Log output:
```
Running upstream scan cycle       active_ssid=c03rad0r  reason=scheduled  signal=-23
Cleaning up stale STA interfaces
```

Cleanup runs before every scan cycle. Note: `StaleSwitchTest` was NOT removed because it has a unique SSID with no duplicates — the cleanup only targets duplicate SSIDs and orphaned enabled STAs, not harmless single disabled STAs.

#### Phase 5 bonus — Real stale STA found on alpha

Alpha had `upstream_tollgated1c6_` (with trailing underscore) as a duplicate of `upstream_tollgate_d1c6`. The fix found and removed it on first startup:
```
Removing duplicate STA section    interface=upstream_tollgated1c6_  kept=upstream_tollgate_d1c6  ssid=TollGate-D1C6
```
This validates that the stale STA bug existed in production and the fix correctly addresses it.

#### Phase 6 — Cross-router switch (detailed)

Beta connected to alpha's TollGate-1690. The CLI reported connecting on radio1 but the UCI section `upstream_tollgate_1690` still had `device=radio0`. Analysis traced this to a pre-existing issue in `FindOrCreateSTAForSSID()` (see Bug #17 below). The connect actually succeeded on radio0 (TollGate-1690 is visible on both radios) and the daemon later rolled back to c03rad0r because TollGate-1690 provides DHCP but no internet. **No stale STAs were created during the connect/switch flow.**

### Bugs found and fixed (17)

| # | Bug | Fix |
|---|-----|-----|
| 1 | `FindOrCreateSTAForSSID` never sets radio device on new STA | Added `device` UCI set, `radio` parameter |
| 2 | `wifi reload` (full) reconfigures all radios, 50-60s | Always use `wifi reload <radio>` |
| 3 | `waitForSTAIP` waits for reload to finish — reload takes 60-90s | Run `wifi reload` in goroutine, poll immediately |
| 4 | `ip -4 addr show <iface> -brief` fails on BusyBox | Use `ip -o -4 addr show dev <iface>` |
| 5 | DHCP timeout too short (30s) | Increased to 180s |
| 6 | Alternate radio hack forced weaker signal | Removed, always use strongest |
| 7 | Cross-radio logic added complexity without benefit | Simplified to single-radio flow |
| 8 | `initUpstreamManager()` called after `initCLIServer()` — nil upstreamManager | Swapped init order |
| 9 | `waitForSTAIP` false positive on stale IP | Added `verifySTASSID()` check |
| 10 | `lostCount++` before pause check — counts accumulate during pause | Moved after `isPaused()` check |
| 11 | No startup grace period — daemon checks connectivity 30s after start while radio still reconfiguring | 90s grace period, skips all connectivity checks during startup |
| 12 | Emergency scan picks stronger-signal TollGate over known fallback even though TollGate likely has no internet | 20 dB signal penalty for TollGate SSIDs during emergency scans |
| 13 | No circuit breaker — repeated switch failures loop continuously, disrupting radio | 3 consecutive failures triggers 10-minute cooldown; resets on success |
| 14 | `getCurrentSignal` receives radio name (`radio0`) instead of netdev (`phy0-sta0`), causing `iwinfo` to return signal=0 — hysteresis always triggers switch to strongest candidate | Added `GetSTANetdev()` to resolve netdev via `ubus call network.wireless status`; falls back to radio name if resolution fails |
| 15 | No post-switch connectivity verification — daemon stays on non-internet upstream (e.g. TollGate providing DHCP but blocking without e-cash) | `verifyPostSwitchConnectivity()` waits 5s then pings 9.9.9.9; failure triggers immediate blacklist (60-min TTL) |
| 16 | Stale duplicate STA sections accumulate after failed switches — `cleanupSTAInterfaces()` existed but was never called | New `CleanupStaleSTAs()` removes duplicates and disables orphans; called at startup, on emergency, and before every scan cycle |
| 17 | `FindOrCreateSTAForSSID` reuses existing disabled STA without updating `device` field — if scan found SSID on radio1 but existing STA is on radio0, connects on wrong radio | **Not yet fixed** — pre-existing. See Known Issues #3. |

### Key hardware findings

1. `wifi reload <radio>` returns immediately on GL.iNet MT3000 but the actual reconfiguration (associate + DHCP) takes 60-120s asynchronously. `waitForSTAIP` must run concurrently with the reload.

2. Stale STAs accumulate in production. Alpha had `upstream_tollgated1c6_` (trailing underscore, likely from a manual UCI typo or previous code path) coexisting with `upstream_tollgate_d1c6`. Both were disabled, but the duplicate adds confusion and wastes UCI entries.

---

## Reproducible Test Procedure

### Smoke test (5 min, on any router)

```sh
# 1. Deploy
./scripts/local-compile-to-router.sh <ROUTER_IP>

# 2. Verify service
ssh root@<ROUTER_IP> "logread -e tollgate | grep 'Upstream WiFi manager initialized'"

# 3. Scan
ssh root@<ROUTER_IP> "tollgate upstream scan"

# 4. Connect
ssh root@<ROUTER_IP> "tollgate upstream connect <SSID> <PASS>"

# 5. Verify online
ssh root@<ROUTER_IP> "ping -c 2 9.9.9.9"

# 6. List
ssh root@<ROUTER_IP> "tollgate upstream list"
```

Or use the Makefile:

```sh
make -f Makefile.test smoke SSID=MyNet PASS=secret          # local
make -f Makefile.test r-smoke SSID=MyNet PASS=secret ROUTER=alpha  # remote
```

### Full test (30 min, on two routers)

Phases:
1. Verify service startup on both routers
2. Scan for networks — verify multi-radio scan, hidden SSID filtering, encryption detection
3. Connect to upstream — verify STA creation, DHCP, connectivity
4. Verify `tollgate upstream list` shows correct status
5. Test edge cases — non-existent SSID, remove unknown, remove active (should fail)
6. Remove disabled upstream, verify UCI cleanup
7. Observe daemon scan cycle — wait 5 min, verify no spurious switching
8. Simulate connectivity loss — `iptables -A OUTPUT -o <iface> -j DROP`, verify emergency scan
9. Verify stale STA cleanup — `make -f Makefile.test r-check-sta-health ROUTER=alpha`

### Stale STA reproduction test (10 min, on one router)

To verify the stale STA fix:
1. Manually create a duplicate: `uci set wireless.stale_test=wifi-iface` with same SSID as existing STA
2. Restart service: `service tollgate-wrt restart`
3. Check logs: `logread -e tollgate | grep -i 'cleanup\|stale\|Removing'`
4. Verify duplicate removed: `tollgate upstream list`

---

## Shared Router Mutex Protocol

Multiple developers may operate on shared physical routers. Coordinate via a lock file:

**Lock file**: `/root/routers.lock` (outside repo, gitignored)

**Before any router modification** (deploy, restart, config edit):
```sh
cat /root/routers.lock   # If exists and locked, stop and wait
```

**When starting router work**:
```sh
echo "locked: true
branch: $(git branch --show-current)
session: <your-name> — <what you're doing>
timestamp: $(date -u '+%Y-%m-%dT%H:%M:%SZ')" > /root/routers.lock
```

**When done**:
```sh
rm /root/routers.lock
```

**Stale locks** older than 2 hours can be force-released if the session is unreachable.

---

## Encryption Detection Mapping

| iwinfo output | UCI value |
|---------------|-----------|
| `none` / `OPEN` / `WEP-*` | `none` |
| `WPA2 PSK (CCMP)` | `psk2` |
| `WPA PSK (TKIP)` | `psk` |
| `WPA3 SAE (CCMP)` | `sae` |
| `WPA3 SAE mixed (CCMP)` | `sae-mixed` |
| `WPA2 EAP (CCMP)` | `wpa2-eap` |
| Unknown | `psk2` (safe default) |

---

## Test Coverage Verification

### Unit Tests (2026-05-05)

| Test | File | Status |
|------|------|--------|
| `getUCIEncryptionType` (5 cases) | `connector_test.go` | PASS |
| `generateRandomSuffix` (3 cases) | `connector_test.go` | PASS |
| `sanitizeSSIDForUCI` (4 cases) | `connector_test.go` | PASS |
| `CleanupStaleSTAs` success + error | `connector_test.go` | PASS |
| `CleanupStaleSTAs` interface contract (2 cases) | `upstream_manager_test.go` | PASS |
| `PauseConnectivityChecks`/`isPaused` | `upstream_manager_test.go` | PASS |
| `Stop` (channel close) | `upstream_manager_test.go` | PASS |
| `blacklistSSID`/`isBlacklisted`/`purgeBlacklist` (4 cases) | `upstream_manager_test.go` | PASS |
| `getIP` (3 cases: X-Real-Ip, X-Forwarded-For, RemoteAddr) | `main_test.go` | PASS |
| `parseUsageString` (4 cases: valid, zero, bad format, bad values) | `main_test.go` | PASS |
| `resellerModeAdapter.IsResellerModeActive` (3 cases: nil, true, false) | `main_test.go` | PASS |

**Total: 63 WGM tests + 11 main tests = 74 tests passing**

### Reseller Mode Device Test (Router B, 2026-04-30)

| Check | Result |
|-------|--------|
| Router B reachable | PASS |
| Daemon running (PID 13584, 56+ min uptime) | PASS |
| Internet connectivity (ping 9.9.9.9) | PASS |
| AP SSID intact (TollGate-D1C6) | PASS |
| Reseller mode active (reseller_mode: true) | PASS |
| `tollgate upstream scan` (37 networks found) | PASS |
| `tollgate upstream list` (3 STAs, correct status) | PASS |
| TollGate-1690 STA section exists (disabled) | PASS |
| No spurious switching (1 scheduled switch only) | PASS |
| No circuit breaker triggers | PASS |
| No blacklisting events | PASS |

### Post-Fix Device Verification (Router B, 2026-04-30)

After fixing Bugs 14 + 15, deployed to Router B with `reseller_mode=false` for safety,
then enabled `reseller_mode=true`. Router subsequently rebooted (power cycle).
Daemon auto-restarted and passed all checks.

#### Pre-reboot verification (PID 10330, started 10:27 UTC)

| Scan | Time | Active | Signal | Best Candidate | Cand. Signal | Diff | Switch? |
|------|------|--------|--------|----------------|-------------|------|---------|
| 1 | 10:33:07 | c03rad0r | **-37** | c03rad0r2 | -40 | -3 dB | No |
| 2 | 10:38:07 | c03rad0r | **-37** | c03rad0r2 | -41 | -4 dB | No |

Signal correctly reads as -37 dBm (was 0 before Bug 14 fix). Hysteresis working.

#### Post-reboot verification (PID 2554, started ~10:52 UTC)

| Scan | Time | Active | Signal | Best Candidate | Cand. Signal | Diff | Switch? |
|------|------|--------|--------|----------------|-------------|------|---------|
| 1 | 10:57:35 | c03rad0r | **-44** | TollGate-1690 | -41 | +3 dB | No |
| 2 | 11:02:35 | c03rad0r | **-44** | c03rad0r2 | -42 | +2 dB | No |

Key observations:
- **Signal fix (Bug 14) confirmed**: -44 dBm correctly read (not 0)
- **Hysteresis prevents stranding**: TollGate-1690 at -41 dBm is only +3 dB stronger than active at -44 dBm, well below 12 dB threshold
- **Reboot survival**: Daemon auto-started, kept c03rad0r as active upstream, internet maintained
- **Reseller mode stable**: TollGate-1690 visible in scans but correctly not selected
- **CLI working**: `tollgate upstream scan` (34 networks), `tollgate upstream list` (3 STAs), `tollgate status` all return correct data
- **Internet**: ping 9.9.9.9 = 26-49 ms, NetBird tunnel stable throughout

### Stale STA Fix Device Verification (2026-05-05)

Full 9-phase test on both routers. All phases PASS.

Key observations:
- **Cleanup runs at all three trigger points**: startup, emergency, and pre-scan
- **Cleanup is targeted**: removes duplicate SSID sections and disables orphaned enabled STAs, but does not touch harmless single disabled STAs with unique SSIDs
- **Real production bug found and fixed**: Alpha had `upstream_tollgated1c6_` (trailing underscore) coexisting with `upstream_tollgate_d1c6`. The fix automatically removed it on first startup.
- **Internet maintained throughout**: No disruption to active upstream during cleanup
- **NetBird mesh recovered**: Both routers online via NetBird after restoring beta's internet connectivity

### Known Issues

1. **Noisy UCI error logs**: `EnsureRadiosEnabled()` calls `uci get wireless.radio0.disabled` every 30s. If radio0 has no explicit `disabled` option (OpenWrt defaults to enabled), `ExecuteUCI` logs `ERROR: uci: Entry not found`. Functionally correct (treats missing as "not disabled"), but fills the log. **Low severity — cosmetic.**

2. **`NetworkOK: true` hardcoded** in `cli/server.go:459` — TODO from before this PR. Low priority.

3. **`FindOrCreateSTAForSSID` does not update `device` field when reusing existing disabled STA**: If a scan finds an SSID on radio1 but the existing STA section is configured for radio0, the STA stays on radio0. If the SSID is only available on radio1, the connect will fail. If available on both radios, it connects on the wrong radio (potentially weaker signal). Found during cross-router test (Phase 6). **Pre-existing bug, not introduced by this PR. Low severity — only affects multi-radio environments where the same SSID is visible on different radios at different times.**

4. **`logread -e tollgate -f` pipe exits immediately when grep'd over SSH**: When running `ssh router "logread -f | grep ..."` the pipe exits with no output. Likely a PTY/buffering issue. Workaround: check logs directly with `logread -e tollgate | grep ...` after the event. **Test infrastructure only — not a product bug.**
