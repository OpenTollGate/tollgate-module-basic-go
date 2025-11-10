# Proposed Remote Control Commands

This document tracks potential future commands for the TIP-07 remote control system.

## Configuration Management

### `set_price`
Update pricing for a specific mint or globally.
```json
{
  "cmd": "set_price",
  "args": {
    "mint_url": "https://mint.coinos.io",
    "price_per_step": 2,
    "price_unit": "sats"
  }
}
```

### `update_config`
Modify any configuration value.
```json
{
  "cmd": "update_config",
  "args": {
    "path": "chandler.max_price_per_millisecond",
    "value": 0.003
  }
}
```

### `reload_config`
Reload configuration from disk without restart.
```json
{
  "cmd": "reload_config",
  "args": {}
}
```

### `add_controller`
Add a new authorized controller pubkey dynamically.
```json
{
  "cmd": "add_controller",
  "args": {
    "pubkey": "npub1...",
    "note": "Operations team member"
  }
}
```

### `remove_controller`
Remove an authorized controller pubkey.
```json
{
  "cmd": "remove_controller",
  "args": {
    "pubkey": "npub1..."
  }
}
```

## Diagnostics & Monitoring

### `get_logs`
Retrieve recent system logs.
```json
{
  "cmd": "get_logs",
  "args": {
    "lines": 100,
    "level": "error",
    "module": "chandler"
  }
}
```

### `network_status`
Detailed network interface status.
```json
{
  "cmd": "network_status",
  "args": {
    "interface": "wlan0" // optional, all if omitted
  }
}
```

### `list_sessions`
Show active customer sessions.
```json
{
  "cmd": "list_sessions",
  "args": {
    "active_only": true
  }
}
```

### `wallet_balance`
Check Cashu wallet balances across mints.
```json
{
  "cmd": "wallet_balance",
  "args": {}
}
```

### `relay_status`
Check connection status to configured relays.
```json
{
  "cmd": "relay_status",
  "args": {}
}
```

### `gateway_status`
Check upstream gateway connection and metrics.
```json
{
  "cmd": "gateway_status",
  "args": {}
}
```

## System Management

### `exec`
Execute arbitrary shell command (DANGEROUS - needs careful auth).
```json
{
  "cmd": "exec",
  "args": {
    "command": "df -h",
    "timeout_sec": 30
  }
}
```

### `restart_service`
Restart a specific service without full reboot.
```json
{
  "cmd": "restart_service",
  "args": {
    "service": "tollgate"
  }
}
```

### `clear_cache`
Clear various caches.
```json
{
  "cmd": "clear_cache",
  "args": {
    "type": "dns" // dns, arp, sessions, etc
  }
}
```

### `sync_time`
Force NTP time synchronization.
```json
{
  "cmd": "sync_time",
  "args": {}
}
```

## Session Management

### `terminate_session`
Force-terminate a customer session.
```json
{
  "cmd": "terminate_session",
  "args": {
    "device_identifier": "mac:00:1A:2B:3C:4D:5E"
  }
}
```

### `extend_session`
Grant additional time/data to a session.
```json
{
  "cmd": "extend_session",
  "args": {
    "device_identifier": "mac:00:1A:2B:3C:4D:5E",
    "amount": 600000,
    "metric": "milliseconds"
  }
}
```

## Backup & Recovery

### `backup_config`
Create and send configuration backup.
```json
{
  "cmd": "backup_config",
  "args": {
    "include_identities": false,
    "include_ledger": true
  }
}
```

### `restore_config`
Restore configuration from provided backup.
```json
{
  "cmd": "restore_config",
  "args": {
    "config_json": "{...}",
    "restart": true
  }
}
```

### `factory_reset`
Reset device to factory defaults.
```json
{
  "cmd": "factory_reset",
  "args": {
    "keep_network": true,
    "confirmation": "RESET-CONFIRMED"
  }
}
```

## Updates & Maintenance

### `firmware_update`
Update firmware from URL or built-in.
```json
{
  "cmd": "firmware_update",
  "args": {
    "url": "https://updates.example.com/tollgate-v0.0.5.ipk",
    "verify_signature": true,
    "auto_reboot": true
  }
}
```

### `check_updates`
Check for available firmware/package updates.
```json
{
  "cmd": "check_updates",
  "args": {}
}
```

### `install_package`
Install or update a specific package.
```json
{
  "cmd": "install_package",
  "args": {
    "package": "tollgate-chandler",
    "version": "0.0.5"
  }
}
```

## Wireless Management

### `scan_wifi`
Scan for available WiFi networks.
```json
{
  "cmd": "scan_wifi",
  "args": {
    "interface": "wlan0"
  }
}
```

### `set_wifi_channel`
Change WiFi channel.
```json
{
  "cmd": "set_wifi_channel",
  "args": {
    "interface": "wlan0",
    "channel": 6
  }
}
```

### `set_wifi_power`
Adjust WiFi transmission power.
```json
{
  "cmd": "set_wifi_power",
  "args": {
    "interface": "wlan0",
    "power_dbm": 20
  }
}
```

## Advanced Features

### `benchmark`
Run performance benchmarks.
```json
{
  "cmd": "benchmark",
  "args": {
    "type": "network", // network, cpu, disk
    "duration_sec": 10
  }
}
```

### `generate_report`
Generate and send diagnostic report.
```json
{
  "cmd": "generate_report",
  "args": {
    "include": ["config", "logs", "sessions", "metrics"]
  }
}
```

### `schedule_command`
Schedule a command for future execution.
```json
{
  "cmd": "schedule_command",
  "args": {
    "execute_at": 1731300000,
    "command": "reboot",
    "command_args": {"delay_sec": 60}
  }
}
```

## Security Considerations

**High Risk Commands** (require extra authorization):
- `exec` - Arbitrary command execution
- `factory_reset` - Data loss
- `firmware_update` - Brick risk
- `restore_config` - Config override

**Suggestions:**
1. Multi-signature requirement for high-risk commands
2. Confirmation codes for destructive operations
3. Rate limiting per command type
4. Audit logging for all commands
5. Read-only vs read-write controller roles
