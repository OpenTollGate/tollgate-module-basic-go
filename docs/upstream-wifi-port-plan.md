# Upstream WiFi Port Plan: Shell Scripts to Go

## Overview

Port `wifiscan.sh` and `upstream-daemon.sh` into the Go codebase. The upstream daemon logic goes into `src/wireless_gateway_manager/` as a new subsystem. The CLI commands get wired through the existing `tollgate` CLI via the Unix socket protocol. The shell scripts and the `tollgate-upstream` init script are removed.

## Design Decisions

- **Always on by default** — no feature flag needed
- **Coexists with ResellerMode** — upstream manager only initiates connections when reseller mode is NOT active; when reseller mode is active, upstream manager must not interfere
- **Named per-SSID STA interfaces** — e.g. `upstream_mynet` where 'mynet' is a sanitized SSID
- **GatewayManager passed to CLIServer** — CLI accesses connector/scanner through the GatewayManager

## Architecture

### Shell Scripts Being Replaced
- `wifiscan.sh` (root, 532 lines) — CLI: scan, connect, list-upstream, remove-upstream
- `files/usr/bin/upstream-daemon.sh` (471 lines) — Daemon: connectivity monitoring, signal switching
- `files/etc/init.d/tollgate-upstream` (28 lines) — procd init script

### Go Integration Points
- `src/wireless_gateway_manager/` — new `upstream_manager.go`; extend `connector.go` and `scanner.go`
- `src/cli/server.go` — new `handleUpstreamCommand`
- `src/cmd/tollgate-cli/main.go` — new `upstream` cobra subcommands

## Phase 1: Extend Connector with Upstream STA Management

### File: `src/wireless_gateway_manager/types.go`

Add:
```go
type STASection struct {
    Name       string // UCI section name (e.g. "upstream_mynet")
    SSID       string
    Device     string // radio0, radio1
    Encryption string
    Disabled   bool
}

type UpstreamManagerConfig struct {
    ScanInterval time.Duration // default: 300s
    FastCheck    time.Duration // default: 30s
    LostThreshold int          // default: 2
    HysteresisDB  int          // default: 12
    SignalFloor   int          // default: -85
}

type UpstreamManager struct {
    connector ConnectorInterface
    scanner   ScannerInterface
    config    UpstreamManagerConfig
    stopChan  chan struct{}
}
```

### File: `src/wireless_gateway_manager/interfaces.go`

Add to `ConnectorInterface`:
```go
GetSTASections() ([]STASection, error)
GetActiveSTA() (*STASection, error)
FindOrCreateSTAForSSID(ssid, passphrase, encryption string) (string, error)
RemoveDisabledSTA(ssid string) error
SwitchUpstream(activeIface, candidateIface, candidateSSID string) error
EnsureWWANSetup() error
EnsureRadiosEnabled() error
```

Add to `ScannerInterface`:
```go
ScanAllRadios() ([]NetworkInfo, error)
GetRadios() ([]string, error)
DetectEncryption(encryptionStr string) string
FindBestRadioForSSID(ssid string, networks []NetworkInfo) (string, error)
```

### File: `src/wireless_gateway_manager/connector.go` — New Methods

1. **`GetSTASections()`** — Parse `uci show wireless` for STA mode interfaces, return struct slice
2. **`GetActiveSTA()`** — Find enabled STA section
3. **`FindOrCreateSTAForSSID(ssid, passphrase, encryption)`** — Reuse existing disabled STA or create new named `upstream_<sanitized_ssid>`
4. **`RemoveDisabledSTA(ssid)`** — Delete disabled STA matching SSID, refuse if active
5. **`SwitchUpstream(activeIface, candidateIface, candidateSSID)`** — Disable old, enable new, wait for DHCP, rollback on timeout
6. **`EnsureWWANSetup()`** — Create network.wwan + firewall zone if missing
7. **`EnsureRadiosEnabled()`** — Enable disabled radios, wifi up

## Phase 2: Extend Scanner for Multi-Radio iwinfo Scanning

### File: `src/wireless_gateway_manager/scanner.go` — New Methods

1. **`ScanAllRadios()`** — Get radios, run `iwinfo <radio> scan` per radio (3 retries), parse combined results
2. **`ParseIwinfoOutput(output []byte, radio string)`** — Parse iwinfo format (ESSID:, Signal:, Encryption:, Channel:, Address:)
3. **`GetRadios()`** — Parse `/etc/config/wireless` for `config wifi-device 'radioN'`
4. **`DetectEncryption(encryptionStr)`** — Map iwinfo strings to UCI types (none, psk, psk2, sae, sae-mixed, wpa2-eap), default psk2
5. **`FindBestRadioForSSID(ssid, networks)`** — Strongest signal radio for given SSID

## Phase 3: Upstream Manager (Daemon Logic)

### New file: `src/wireless_gateway_manager/upstream_manager.go`

Main loop (port of `upstream-daemon.sh` main function):
1. On start: `EnsureRadiosEnabled()`, `EnsureWWANSetup()`, sleep 10s
2. Every `FastCheck` seconds:
   - `EnsureRadiosEnabled()`
   - Get active STA, radio, signal
   - **If reseller mode is active**: skip switching (only monitor connectivity for informational purposes)
   - **If reseller mode is NOT active**: check connectivity, run scan cycle
3. Scan cycle:
   - Scan all radios
   - Find strongest candidate among known upstreams (disabled STAs whose SSIDs appear in scan)
   - Switch decision: no active / not associated / below signal floor / hysteresis exceeded
   - If switching: `SwitchUpstream()` with safe fallback

### Configurable via UpstreamManagerConfig (env vars mapped in main.go):
| Env Variable | Default | Field |
|---|---|---|
| UPSTREAM_SCAN_INTERVAL | 300s | ScanInterval |
| UPSTREAM_FAST_CHECK | 30s | FastCheck |
| UPSTREAM_LOST_THRESHOLD | 2 | LostThreshold |
| UPSTREAM_HYSTERESIS_DB | 12 | HysteresisDB |
| UPSTREAM_SIGNAL_FLOOR | -85 | SignalFloor |

## Phase 4: CLI Commands

### Server-side: `src/cli/server.go`

New `handleUpstreamCommand` dispatching:

| Args | Action |
|---|---|
| `["scan"]` | `scanner.ScanAllRadios()` → formatted network list |
| `["connect", ssid, pass]` | Full connect flow: scan → find best radio → detect encryption → ensure wwan → create/reuse STA → wifi reload → wait for IP |
| `["connect", ssid]` | Same but interactive passphrase prompt |
| `["list-upstream"]` | `connector.GetSTASections()` → formatted STA list |
| `["remove-upstream", ssid]` | `connector.RemoveDisabledSTA(ssid)` |

### New types in `src/cli/types.go`:
```go
type UpstreamNetwork struct {
    SSID, BSSID, Encryption, Channel, Radio string
    Signal int
}
type UpstreamSTA struct {
    SSID, Status, Radio, Encryption string
}
```

### Client-side: `src/cmd/tollgate-cli/main.go`

New cobra commands:
- `tollgate upstream scan` — scan available networks
- `tollgate upstream connect <SSID> [PASSPHRASE]` — connect
- `tollgate upstream list` — list configured upstreams
- `tollgate upstream remove <SSID>` — remove disabled upstream

## Phase 5: Integration

### File: `src/main.go`

- Create `UpstreamManager` with `Connector`, `Scanner`, config from env vars
- Pass `gatewayManager` to `NewCLIServer`
- Start `UpstreamManager` goroutine

### File: `src/cli/server.go`

- `NewCLIServer` accepts `*wireless_gateway_manager.GatewayManager`
- Store as field, access connector/scanner for upstream commands

## Phase 6: Cleanup

1. Delete `wifiscan.sh`
2. Delete `files/usr/bin/upstream-daemon.sh`
3. Delete `files/etc/init.d/tollgate-upstream`
4. Update `Makefile`: remove shell script install lines and file references

## Phase 7: Testing

### Unit Tests

#### `src/wireless_gateway_manager/upstream_manager_test.go`
- Scheduled scan switches to stronger candidate
- No switch below hysteresis threshold
- Emergency rescan on connectivity loss
- Connectivity restored resets lost count
- No active upstream triggers scan
- Signal below floor forces switch
- Safe fallback on DHCP timeout
- Stop cleanly shuts down
- Upstream manager skips switching when reseller mode is active

#### `src/wireless_gateway_manager/connector_test.go`
- GetSTASections parses correctly
- GetActiveSTA returns enabled STA
- FindOrCreateSTAForSSID reuses existing
- FindOrCreateSTAForSSID creates new
- RemoveDisabledSTA success
- RemoveDisabledSTA active refused
- RemoveDisabledSTA not found
- SwitchUpstream success
- SwitchUpstream timeout fallback
- EnsureWWANSetup creates when missing
- EnsureWWANSetup skips when existing
- EnsureRadiosEnabled enables disabled radios

#### `src/wireless_gateway_manager/scanner_test.go`
- ParseIwinfoOutput realistic output
- ParseIwinfoOutput skips hidden SSIDs
- DetectEncryption all cases
- FindBestRadioForSSID strongest signal
- GetRadios parses wireless config
- ScanAllRadios combines results

#### `src/cli/server_test.go`
- Scan returns networks
- Connect success
- Connect SSID not found
- List upstream
- Remove upstream success
- Remove active refused
- Remove not found
- Unknown subcommand error

### Integration/E2E Tests (on real hardware)

#### `tests/test_upstream_wifi.py`
- Scan returns networks
- Connect and list
- Connect preserves previous
- Remove disabled
- Remove active refused
- Daemon auto-switch on disconnect
- Daemon signal-based switch

## Behavioral Invariants to Preserve

| Shell Behavior | Go Port Must |
|---|---|
| Previous connection preserved as disabled STA | FindOrCreateSTAForSSID reuses matching disabled STA |
| Hidden SSIDs skipped | Scanner skips empty SSIDs |
| 12 dB hysteresis | RunScanCycle enforces threshold |
| Emergency rescan after 2 failures | Track consecutive failures |
| Safe fallback on DHCP timeout | SwitchUpstream implements rollback |
| Signal floor -85 dBm forces switch | Check floor before hysteresis |
| Scan cycles = interval / fast_check | Compute cycles identically |
| iwinfo scan (not iw dev scan) | Multi-radio scanner uses iwinfo |
| Unknown encryption defaults to psk2 | DetectEncryption defaults to psk2 |
| Upstream manager doesn't interfere with reseller mode | Skip switching when reseller mode active |
