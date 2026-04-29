# Upstream WiFi: Test Results

## Unit Tests (all pass)

38 tests pass locally with `go test`:

```
cd src && go test github.com/OpenTollGate/tollgate-module-basic-go/src/wireless_gateway_manager \
  -run "TestConnector_|TestSanitize|TestScanner_|TestUpstreamManager_" -count=1
```

| Test | Status | Notes |
|------|--------|-------|
| TestConnector_GetSTASections | PASS | |
| TestConnector_GetActiveSTA | PASS | |
| TestConnector_GetActiveSTA_NoneActive | PASS | |
| TestConnector_FindOrCreateSTAForSSID_ReuseExisting | PASS | |
| TestConnector_FindOrCreateSTAForSSID_CreateNew | PASS | |
| TestConnector_RemoveDisabledSTA_Success | PASS | |
| TestConnector_RemoveDisabledSTA_ActiveRefused | PASS | |
| TestConnector_RemoveDisabledSTA_NotFound | PASS | |
| TestConnector_SwitchUpstream_Success | PASS | |
| TestConnector_SwitchUpstream_TimeoutFallback | PASS | |
| TestConnector_EnsureWWANSetup | PASS | |
| TestConnector_EnsureRadiosEnabled | PASS | |
| TestSanitizeSSIDForUCI | PASS | |
| TestScanner_DetectEncryption | PASS | |
| TestScanner_ParseIwinfoOutput | PASS | |
| TestScanner_ParseIwinfoOutput_HiddenSSID | PASS | |
| TestScanner_ParseIwinfoOutput_Empty | PASS | |
| TestScanner_FindBestRadioForSSID_Found | PASS | |
| TestScanner_FindBestRadioForSSID_NotFound | PASS | |
| TestScanner_ScanAllRadios_MockError | PASS | |
| TestScanner_GetRadios_Mock | PASS | |
| TestUpstreamManager_DefaultConfig | PASS | |
| TestUpstreamManager_NewUpstreamManager | PASS | |
| TestUpstreamManager_NewUpstreamManager_SetsDefaults | PASS | |
| TestUpstreamManager_FindStrongestCandidate_NoKnownSSIDs | PASS | |
| TestUpstreamManager_FindStrongestCandidate_WithKnownSSID | PASS | |
| TestUpstreamManager_FindStrongestCandidate_MultipleKnownSSIDs | PASS | |
| TestUpstreamManager_IsResellerModeActive_True | PASS | |
| TestUpstreamManager_IsResellerModeActive_False | PASS | |
| TestUpstreamManager_IsResellerModeActive_NilChecker | PASS | |
| TestUpstreamManager_RunScanCycle_NoActiveUpstream | PASS | |
| TestUpstreamManager_RunScanCycle_BelowHysteresis_NoSwitch | PASS | |
| TestUpstreamManager_RunScanCycle_AboveHysteresis_Switches | PASS | |
| TestUpstreamManager_RunScanCycle_BelowSignalFloor | PASS | |
| TestUpstreamManager_RunScanCycle_ScanFails | PASS | |

### Pre-existing failures (not related to upstream WiFi)

| Test | Status | Notes |
|------|--------|-------|
| TestResellerModeEnabled_FilterTollGateNetworks | FAIL | stepSize mismatch (22020096 vs 20000). Broken before our changes. |

## Router Tests

Test environment: two GL.iNet MT3000 routers (arm64) connected via NetBird.

| Router | NetBird IP | Role |
|--------|------------|------|
| A | 100.90.41.166 | Connector + Target (broadcasts `c08r4d0r-1690`, `TollGate-1690`) |
| B | 100.90.216.248 | Connector (broadcasts `c03rad0r-D1C6`, `TollGate-D1C6`) |

Additional SSIDs available for testing:
- `FRITZ!Box 7490 AS` (psk2, password `Papa-Juliet-Foxtrot-39`) — real internet upstream with DHCP
- `c03rad0r2` (sae-mixed, password `c03rad0r123`) — test hotspot (visible from Router B only)

### Phase 1: Service Startup

- [x] Service starts without crash (Router A + B)
- [x] `tollgate version` returns version info
- [x] `Upstream WiFi manager initialized` in logs
- [x] `Starting upstream manager` in logs

### Phase 2: Upstream Scan

- [x] `tollgate upstream scan` completes — found 36-49 networks on both routers
- [x] Router A's SSIDs visible from Router B — `c08r4d0r-1690` at -11 dBm (radio0)
- [x] Router B's SSIDs visible from Router A
- [x] Encryption correctly detected — `WPA2 PSK (CCMP)` mapped to UCI `psk2`
- [x] Hidden SSIDs detected and filtered — shown as `(hidden)`
- [x] Multi-radio scan (radio0 + radio1) — each radio scanned independently
- [x] New SSID `c03rad0r2` visible at -44 dBm on radio0 (from Router B)

### Phase 3: Upstream Connect

- [x] STA created with correct UCI config — SSID, encryption, key, network=wwan all correct
- [x] Radio device set on STA section — `device=radio0` set correctly
- [x] `wifi reload <radio>` (single-radio reload) works
- [x] DHCP IP acquired on new STA — confirmed with `c03rad0r2` (Router B) and `FRITZ!Box 7490 AS` (Router A + B)
- [x] Connectivity through upstream router — `ping 9.9.9.9` 3/3 received, 17-22ms latency
- [x] `tollgate upstream list` shows correct ACTIVE/disabled status
- [x] UCI state verification — active STA disabled=0, old STA disabled=1
- [x] Streaming 7-step progress output works correctly
- [x] Fallback on DHCP timeout — old STA re-enabled, connectivity restored

### Phase 4: Edge Cases

- [x] Connect to non-existent SSID — returns `SSID 'NonExistentSSID' not found in scan`
- [x] Remove unknown SSID — returns `no disabled upstream found with SSID 'UnknownSSID'`
- [x] `tollgate version` still works after upstream operations
- [x] `tollgate status` still works after upstream operations

### Phase 5: Cleanup Verification

- [x] `tollgate upstream remove c03rad0r` — removed disabled STA from UCI (Router B)
- [x] `tollgate upstream remove "FRITZ!Box 7490 AS"` — removed disabled STA from UCI (Router A)
- [x] Old shell scripts absent — `/usr/bin/wifiscan.sh`, `/usr/bin/upstream-daemon.sh`, `/etc/init.d/tollgate-upstream` all gone

### Phase 6: Daemon Scan Cycle

- [x] Daemon runs scheduled scan cycles (observed at 5-minute interval on Router B)
- [x] Daemon maintains stable connection — no spurious switching when connected to `c03rad0r2`
- [x] Connectivity restored after brief loss — daemon detected lost_count=1 then restored
- [ ] **Known issue**: Daemon undoes manual connect within 25-30s — see below

### Phase 7: Reseller Mode Guard

- [ ] Reseller mode=false → upstream manager active (not explicitly tested with reseller_mode=true)

### Not Yet Tested

- [ ] Reseller mode=true blocks upstream operations
- [ ] Daemon auto-switch: observe daemon automatically switching to stronger signal over time

## Known Issue: Daemon vs Manual Connect Race

When `tollgate upstream connect` succeeds, the daemon's connectivity check (every 30s) detects the brief radio reload disruption and triggers an emergency switch back to the previous STA. Timeline observed on both routers:

```
T+0s   - Manual connect succeeds (FRITZ!Box 7490 AS)
T+25s  - Daemon detects connectivity lost (count 1)
T+55s  - Daemon detects connectivity lost (count 2 = LostThreshold)
T+58s  - Daemon switches back to c03rad0r (emergency scan)
T+68s  - Daemon successfully switches back
```

The connect itself works perfectly — DHCP, IP, connectivity all succeed. The daemon just undoes it.

**Mitigations** (not yet implemented):
- Pause daemon scanning for N seconds after a manual connect
- Increase `LostThreshold` from 2 to 3-4 (more tolerant of brief disruptions)
- Add a grace period after connects where connectivity loss is ignored
- Have the daemon update its internal active STA tracking to match the manual switch

## Bugs Found and Fixed During Router Testing

| # | Bug | Fix | Status |
|---|-----|-----|--------|
| 1 | `FindOrCreateSTAForSSID` never sets `device` (radio) on new STA | Added `device` UCI set, added `radio` parameter | Fixed |
| 2 | `wifi reload` (full) reconfigures all radios, takes 50-60s | Always use `wifi reload <radio>` (single-radio reload) | Fixed |
| 3 | `waitForSTAIP` starts after `wifi reload` completes — reload takes 60-90s, DHCP happens during reload | Run `wifi reload` in goroutine, start IP polling immediately after UCI commit | Fixed |
| 4 | `ip -4 addr show <iface> -brief` fails on BusyBox (no `-brief` flag) | Use `ip -o -4 addr show dev <iface>` (BusyBox compatible) | Fixed |
| 5 | DHCP timeout too short (30s → 45s → 60s) | Increased to 180s, async reload means polling starts immediately | Fixed |
| 6 | Alternate radio hack forced weaker signal, caused DHCP timeouts | Removed `FindAlternateRadioForSSID` entirely, always use strongest signal | Fixed |
| 7 | Cross-radio logic added complexity without preventing NetBird loss | Removed cross-radio branching, simplified to straight-line flow | Fixed |

## Key Findings

### wifi reload timing on GL.iNet MT3000

- `wifi reload radio0` returns immediately (0.01s) — it triggers async netifd reconfiguration
- The actual reconfiguration (tear down interfaces, reconfigure, bring up, associate, DHCP) takes 60-120s
- `waitForSTAIP` must run concurrently with the reload, not after it

### Same-radio switching always disrupts NetBird

When the active STA providing NetBird is on the same radio as the new STA:
1. Old STA disabled, new STA enabled, UCI committed
2. `wifi reload <radio>` triggers reconfiguration
3. Radio0 interfaces torn down (including NetBird tunnel)
4. New STA associates, gets DHCP
5. NetBird tunnel eventually re-establishes through new upstream

This is inherent to same-radio switching. The brief outage (30-120s) is unavoidable.

### Daemon vs manual connect race condition

After a manual `tollgate upstream connect`, the daemon's 30-second connectivity check may detect the brief disruption from the radio reload and trigger an emergency scan. This can undo the manual switch within 60-90 seconds.

Possible mitigations (not yet implemented):
- Pause daemon scanning for N seconds after a manual connect
- Increase `LostThreshold` or add a grace period after connects
- Have the daemon respect the manual switch by updating its internal state

### Encryption detection

| iwinfo output | UCI value | Notes |
|---------------|-----------|-------|
| `WPA2 PSK (CCMP)` | `psk2` | |
| `mixed WPA2/WPA3 PSK/SAE (CCMP)` | `sae-mixed` | Detected as-is from iwinfo |
| `none` | `none` | Open network |
| Unknown | `psk2` | Safe default |
