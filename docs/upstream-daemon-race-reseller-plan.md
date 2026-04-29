# Upstream Manager: Daemon Race Condition Fix + Reseller Mode

## Part 1: Daemon Race Condition Fix

### Problem
After `tollgate upstream connect` succeeds, the daemon's 30s connectivity check detects the radio reload disruption and switches back within ~30s.

### Solution
Add a `PauseConnectivityChecks` mechanism. After a successful manual connect, the daemon ignores connectivity loss for 120 seconds.

### Changes

#### `upstream_manager.go`
- Add `pauseUntil time.Time` and `sync.Mutex` to `UpstreamManager` struct
- Add `PauseConnectivityChecks(d time.Duration)` method — sets `pauseUntil = time.Now().Add(d)`
- In daemon loop (line ~143), before incrementing `lostCount`, check `time.Now().Before(pauseUntil)` — if true, skip the loss count

#### `wireless_gateway_manager.go`
- Add `PauseConnectivityChecks(d time.Duration)` delegate method on `GatewayManager` — calls `um.PauseConnectivityChecks(d)`

#### `cli/server.go`
- After `SwitchUpstream` succeeds in `handleUpstreamConnect`, call `s.gatewayManager.PauseConnectivityChecks(120 * time.Second)`
- After `SwitchUpstream` succeeds in `handleUpstreamConnectStreaming`, call `s.gatewayManager.PauseConnectivityChecks(120 * time.Second)`

## Part 2: Reseller Mode in UpstreamManager

### Problem
When `reseller_mode=true`, the daemon currently does nothing (`continue`). It should scan for open `TollGate-*` SSIDs and auto-connect to stronger signals.

### Design
- **Non-reseller mode:** Current behavior — find candidates from disabled STA sections (known SSIDs)
- **Reseller mode:** Find candidates from scan results filtered to open `TollGate-*` SSIDs, create STA sections on-the-fly

### Changes

#### `upstream_manager.go`

1. **Remove the `continue` guard** (line ~109-111):
   ```go
   // OLD:
   if um.isResellerModeActive() { continue }
   
   // NEW: remove this, let the loop proceed with mode-aware logic
   ```

2. **Replace `findStrongestCandidate` with `findCandidates`:**
   - Takes `networks []NetworkInfo` and `isResellerMode bool`
   - Non-reseller: current behavior (disabled STA sections → known SSIDs → match scan)
   - Reseller: filter scan to `TollGate-*` SSIDs with open encryption, create STA sections via `FindOrCreateSTAForSSID` on-the-fly, return strongest

3. **`runScanCycle` passes `isResellerMode` to `findCandidates`:**
   - Same hysteresis/switching logic for both modes
   - Reseller mode: encryption always `none`, password always empty

### Reseller mode candidate selection flow
```
findCandidates(networks, isResellerMode):
  if isResellerMode:
    tollgateNetworks = networks where SSID starts with "TollGate-" and encryption is "none"/"open"
    
    for each network in tollgateNetworks:
      existing = find STA section with matching SSID
      if existing and not disabled:
        continue  # already connected to this one
      if existing:
        candidate = existing
      else:
        radio = FindBestRadioForSSID(ssid, networks)
        iface = FindOrCreateSTAForSSID(ssid, "", "none", radio)
        candidate = {iface, ssid, signal, radio}
      
    return strongest candidate by signal
    
  else:
    # Current behavior: disabled STA sections → known SSIDs → match scan
```

### What stays the same
- Hysteresis threshold (12 dB) — only switch if significantly stronger
- Signal floor (-85 dBm) — don't switch to weak signals
- `SwitchUpstream` — same switching logic for both modes
- Old `GatewayManager.ScanAndConnect` code remains (can remove later)

## Testing

### On Router A (after deploy)

- [ ] `tollgate upstream connect "FRITZ!Box 7490 AS" "Papa-Juliet-Foxtrot-39"` succeeds
- [ ] Wait 2 minutes — verify daemon does NOT switch back (race condition fix)
- [ ] `tollgate upstream list` shows FRITZ!Box as ACTIVE
- [ ] Set `reseller_mode=true` in config, restart service
- [ ] Verify daemon scans for `TollGate-*` SSIDs in logs
- [ ] Verify STA sections created on-the-fly for discovered TollGates
- [ ] Set `reseller_mode=false`, restart service
- [ ] Verify daemon returns to normal behavior
