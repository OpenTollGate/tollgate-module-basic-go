# SSID Blacklist for Non-Internet Upstreams

## Problem

Router connects to a TollGate, gets DHCP, gateway responds to ping — but there's no actual internet (TollGate blocks traffic until payment). Two issues:

1. **Connectivity check is wrong:** `checkConnectivity` pings the default gateway, which responds even without internet. Daemon thinks "connected" and never switches away.
2. **No memory of bad SSIDs:** Even if the daemon switches away, it may reconnect to the same non-internet SSID.

## Solution

### Fix 1: Improve connectivity check

Change `checkConnectivity` to ping an external address (`9.9.9.9`) instead of the default gateway. If the external ping fails, the upstream doesn't have real internet.

- File: `upstream_manager.go`, method `checkConnectivity` (line ~362)
- Keep STA association check as fast-fail
- Remove `getDefaultGateway` method (no longer needed)
- Increase ping timeout from 2s to 3s (external ping needs more time)

### Fix 2: SSID blacklist

When the daemon detects no internet and switches away (emergency scan), blacklist the SSID. Candidate selection skips blacklisted SSIDs.

#### Data structure

```go
// In UpstreamManager struct
blacklist   map[string]time.Time  // SSID → blacklisted-at timestamp
blacklistMu sync.Mutex
```

In-memory only — resets on service restart.

#### Config

```go
// In UpstreamManagerConfig
BlacklistTTL time.Duration  // default: 60 minutes
```

#### Methods

- `blacklistSSID(ssid string)` — add SSID with `time.Now()`
- `isBlacklisted(ssid string) bool` — check if present and not expired
- `purgeBlacklist()` — remove all entries older than `BlacklistTTL`

#### When to blacklist

In `runScanCycle`, when `SwitchUpstream` succeeds with reason "emergency":

```go
if strings.HasPrefix(reason, "emergency") && activeSSID != "" {
    um.blacklistSSID(activeSSID)
}
```

Manual `tollgate upstream connect` does NOT trigger blacklisting (user's explicit choice).

#### When to check blacklist

In `findKnownCandidates` and `findResellerCandidates`:

```go
if um.isBlacklisted(net.SSID) {
    continue
}
```

#### When to purge

Call `purgeBlacklist()` at the start of each `runScanCycle`.

## Files Changed

| File | Change |
|------|--------|
| `types.go` | Add `BlacklistTTL time.Duration` to `UpstreamManagerConfig` |
| `upstream_manager.go` | Add blacklist map + mutex to struct. Add `blacklistSSID`, `isBlacklisted`, `purgeBlacklist` methods. Improve `checkConnectivity` to ping `9.9.9.9`. Remove `getDefaultGateway`. Blacklist SSID on emergency switch. Check blacklist in candidate selection. |
| `upstream_manager_test.go` | Add tests: blacklist add/check, expiry, skip in candidates |

## Default Config

| Setting | Value |
|---------|-------|
| `BlacklistTTL` | 60 minutes |
| Ping target | `9.9.9.9` |
| Ping timeout | 3 seconds |

## Flow After Fix

```
T+0s    Router connected to TollGate-1690 (no internet)
T+30s   Daemon: ping 9.9.9.9 → FAIL (lost_count=1)
T+60s   Daemon: ping 9.9.9.9 → FAIL (lost_count=2)
T+60s   Emergency scan, blacklist "TollGate-1690"
T+60s   Scan candidates, skip blacklisted SSIDs
T+60s   Switch to next best candidate (e.g., FRITZ!Box)
T+60m   Blacklist entry expires, "TollGate-1690" eligible again
```
