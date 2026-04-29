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
| A | 100.90.41.166 | Target (broadcasts `c08r4d0r-1690`, `TollGate-1690`) |
| B | 100.90.216.248 | Connector (deployed new binaries) |

### Phase 1: Service Startup

| Test | Status | Notes |
|------|--------|-------|
| Service starts without crash | PASS (B) | Required adding accepted_mints to config.json (pre-existing issue on A) |
| `tollgate version` returns info | PASS (B) | |
| `Upstream WiFi manager initialized` in logs | PASS (B) | |
| `Starting upstream manager` in logs | PASS (B) | |

### Phase 2: Upstream Scan

| Test | Status | Notes |
|------|--------|-------|
| `tollgate upstream scan` completes | PASS (A+B) | Found 30-36 networks on both routers |
| Router A's SSIDs visible from Router B | PASS | `c08r4d0r-1690` at -9 dBm (radio0), -21 dBm (radio1) |
| Router B's SSIDs visible from Router A | PASS | `c03rad0r-D1C6` at -25 dBm (radio1), -31 dBm (radio0) |
| Encryption correctly detected | PASS | `WPA2 PSK (CCMP)` mapped to UCI `psk2` |
| Hidden SSIDs detected and filtered | PASS | Shown as `(hidden)` |
| Multi-radio scan (radio0 + radio1) | PASS | Each radio scanned independently |

### Phase 3: Upstream Connect

| Test | Status | Notes |
|------|--------|-------|
| STA created with correct UCI config | PASS | SSID, encryption, key, network=wwan all correct |
| Radio device set on STA section | PASS (after fix) | Initially missing — bug found and fixed |
| `wifi reload <radio>` (single-radio reload) | PASS | New `reloadRadio()` function works |
| Cross-radio switching preserves old STA | PASS (after fix) | Old STA on radio0 stays up while radio1 STA activates |
| DHCP IP acquired on new STA | **NOT YET TESTED** | Cross-radio + alternate-radio fix deployed but not yet tested on hardware |
| Connectivity through upstream router | **NOT YET TESTED** | |
| Fallback on DHCP timeout | PASS | Old STA re-enabled, connectivity restored |

### Bugs Found During Router Testing

| # | Bug | Fix | Commit |
|---|-----|-----|--------|
| 1 | `FindOrCreateSTAForSSID` never sets `device` (radio) on new STA | Added `device` UCI set, added `radio` parameter to signature | this commit |
| 2 | `SwitchUpstream` disables old STA before verifying new one, `wifi reload` takes down all radios causing total connectivity loss | Cross-radio detection: reload only the candidate radio, verify DHCP, then disable old STA | this commit |
| 3 | `FindBestRadioForSSID` picks the same radio as active STA, forcing same-radio (unsafe) path | Added `FindAlternateRadioForSSID` to prefer alternate radio when active STA is on best radio | this commit |
| 4 | `waitForSTAIP` only uses `ifconfig` (may not work on modern OpenWrt) | Added `ip -4 addr show -brief` as primary, `ifconfig` as fallback | this commit |
| 5 | DHCP timeout too short (30s) | Increased to 45s | this commit |

### Remaining Router Tests

These need to be run after the latest fixes are deployed:

- [ ] Connect upstream with cross-radio switching (radio1 STA while radio0 STA stays up)
- [ ] Verify DHCP IP acquired on new STA
- [ ] Verify connectivity through upstream router (`ping 8.8.8.8`)
- [ ] `tollgate upstream list` — verify ACTIVE/disabled status
- [ ] UCI state verification (upstream STA, wwan, disabled states)
- [ ] Edge cases: unknown SSID, remove unknown, existing commands
- [ ] Old shell script cleanup verification
- [ ] Daemon scan cycle observation (5-minute interval)
- [ ] Reseller mode guard test
