# Wireless Gateway Manager

Manages upstream Wi-Fi connectivity for tollgate routers running OpenWrt.

## Architecture

```
main.go
  ├── Connector (UCI/STA management)
  ├── Scanner (iw scan operations)
  ├── UpstreamManager (daemon loop)
  ├── VendorElementProcessor (TollGate vendor IEs)
  └── CLIServer (Unix socket CLI)
```

All components share a single `Connector` and `Scanner` instance created at startup.

### Components

- **Connector** (`connector.go`) — Manages OpenWrt UCI configuration for STA interfaces, DHCP, radio setup, upstream switching, and stale STA cleanup.
- **Scanner** (`scanner.go`) — Scans Wi-Fi radios via `iw`, parses results, detects encryption, finds best radio for a given SSID.
- **UpstreamManager** (`upstream_manager.go`) — Background daemon that monitors connectivity, scans for better upstreams, switches automatically on signal degradation or connectivity loss.
- **VendorElementProcessor** (`vendor_element_manager.go`) — Extracts and scores vendor-specific IEs (Bitcoin/Lightning info) from scan results.
- **CLIServer** (`cli/server.go`) — Unix domain socket server exposing `tollgate upstream *` CLI commands.

## Configuration

All upstream Wi-Fi settings are surfaced in `/etc/tollgate/config.json` under the `upstream_wifi` key:

```json
{
  "upstream_wifi": {
    "scan_interval_seconds": 300,
    "fast_check_seconds": 30,
    "lost_threshold": 2,
    "hysteresis_db": 12,
    "signal_floor": -85,
    "blacklist_ttl_minutes": 60,
    "emergency_penalty": 20,
    "max_consecutive_failures": 3,
    "switch_cooldown_minutes": 10,
    "startup_grace_seconds": 90,
    "post_switch_wait_seconds": 5,
    "dhcp_timeout_seconds": 180,
    "manual_pause_seconds": 120
  }
}
```

| Field | Default | Description |
|---|---|---|
| `scan_interval_seconds` | 300 | Full scan cycle interval |
| `fast_check_seconds` | 30 | Connectivity check interval |
| `lost_threshold` | 2 | Consecutive failures before emergency scan |
| `hysteresis_db` | 12 | Minimum signal improvement (dBm) to trigger switch |
| `signal_floor` | -85 | Switch if active signal drops below this |
| `blacklist_ttl_minutes` | 60 | How long to blacklist a non-functional SSID |
| `emergency_penalty` | 20 | Penalty applied to TollGate SSIDs during emergency scans |
| `max_consecutive_failures` | 3 | Switch failures before circuit breaker cooldown |
| `switch_cooldown_minutes` | 10 | Circuit breaker cooldown duration |
| `startup_grace_seconds` | 90 | Skip connectivity checks at startup |
| `post_switch_wait_seconds` | 5 | Wait after switch before verifying connectivity |
| `dhcp_timeout_seconds` | 180 | Timeout for DHCP after switching upstream |
| `manual_pause_seconds` | 120 | Pause duration after manual CLI connect |

## CLI Commands

```
tollgate upstream scan              # Scan for upstream networks
tollgate upstream connect <SSID> [PASS]  # Connect to upstream
tollgate upstream list              # List configured STA sections
tollgate upstream remove <SSID>     # Remove a disabled STA
tollgate status                     # Service status (includes NetworkOK)
```

## Reseller Mode

When `reseller_mode` is enabled in config, the daemon automatically discovers and connects to open `TollGate-*` SSIDs alongside known fallback networks. Non-TollGate SSIDs are only considered if a disabled STA section already exists for them.

## Circuit Breaker

After `max_consecutive_failures` consecutive switch failures, the circuit breaker enters a cooldown period (`switch_cooldown_minutes`). During cooldown, scan cycles are skipped to avoid rapid reconnection attempts.

## StaSTA Cleanup

On startup and before each scan cycle, `CleanupStaleSTAs()` removes STA network interfaces whose underlying wireless section no longer exists, preventing UCI pollution from previous connections.
