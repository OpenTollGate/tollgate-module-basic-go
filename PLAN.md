# fix/wgm-improvements Branch Plan

## DELETE THIS FILE BEFORE MERGING

## Branch Purpose
Extract WGM (Wireless Gateway Manager) improvements from the mint-health branch into a standalone PR for focused review.

## Changes (4 files in src/wireless_gateway_manager/)

### 1. connector.go — Cross-radio DHCP nudge (+63 lines)
- After `SwitchUpstream`, if the new STA is on a different radio, issue `ifup wwan` on the new radio's netdev
- Prevents the 180s DHCP timeout that occurs when netifd doesn't trigger DHCP on the new interface
- Dual-trigger approach: first `ifup wwan` via STA netdev mapping, second via radio name

### 2. upstream_manager.go — Startup hygiene + TollGate-aware probing (+162 lines)
- **`startupConnectivityCheck()`**: Runs once at daemon start. If an active STA has no internet after settling, nudges with `ifup wwan`, retries connectivity 3x, then triggers emergency scan to find a working upstream
- **`probeTollGateGateway()`**: After every upstream switch, probes `gateway:2121` to verify TollGate is reachable. If reachable, extends patience (uses `TollGateLostThreshold` = 6 failures instead of default 2)
- **Configurable timing**: `StartupSettle`, `StartupRetryInterval`, `StartupScanInterval` all configurable via `UpstreamManagerConfig` with sensible defaults

### 3. types.go — New config fields (+2 fields)
- `TollGateLostThreshold int` — Lost threshold when TollGate is detected (default: 6)
- `StartupSettle time.Duration` — Initial settle time after boot (default: 15s)
- `StartupRetryInterval time.Duration` — Between connectivity retries (default: 10s)
- `StartupScanInterval time.Duration` — Between scan retries (default: 10s)

### 4. upstream_manager_test.go — 302 lines of new tests
- Tests for `startupConnectivityCheck`: no active STA, has internet, no internet + switch, no candidate, switch fails, reseller mode, scan fails then succeeds, candidate not found first scan, switch fails then succeeds, all retries fail
- Tests for TollGate-aware probing: TollGate detected extends patience, non-TollGate uses default threshold
- Config override tests for new fields

## Adaptations from mint-health branch
The `startupConnectivityCheck` function originally had hardcoded `10 * time.Second` sleep intervals that caused test timeouts. On this branch, these were made configurable via `StartupRetryInterval` and `StartupScanInterval` config fields so tests can run with 1ms intervals. This is a test-quality improvement not present on the mint-health branch.

After this branch merges to main, the mint-health branch should be rebased. The rebase will pick up the configurable intervals automatically (they were already `StartupSettle`-aware).

## Constructor Signature
**Unchanged.** `NewUpstreamManager(connector, scanner, reseller, config)` — same 4 params. New config fields have zero-value defaults, so `main.go` on main needs no changes.

## Hardware Test Plan
Both routers (Alpha + Beta) need:
1. Deploy cross-compiled arm64 binary
2. Reboot router, verify `startupConnectivityCheck` logs appear
3. Disconnect upstream WiFi, verify emergency scan triggers
4. Reconnect, verify cross-radio DHCP nudge works if switching between radios

## Post-Merge Actions
1. Rebase `94-mint-health-rebase-clean` onto updated main
2. The mint-health branch's WGM changes will be subsumed by this branch's changes
3. After rebase, mint-health diff shrinks by ~520 lines (all WGM)
4. Delete this file before merging
