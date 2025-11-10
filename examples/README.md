# TIP-07 Remote Control Examples

This directory contains example tools for sending TIP-07 remote control commands to TollGate routers.

## Prerequisites

You need:
1. **Controller private key** (nsec or hex) - This must be in the `allowed_pubkeys` list in the TollGate's config
2. **TollGate public key** (npub or hex) - The router you want to control
3. The TollGate must have `control.enabled: true` in its config

## Quick Start

### 1. Send an uptime command

```bash
go run send_command.go \
  -controller-key nsec1... \
  -tollgate-pubkey npub1... \
  -command uptime
```

### 2. Send a reboot command

```bash
go run send_command.go \
  -controller-key nsec1... \
  -tollgate-pubkey npub1... \
  -command reboot \
  -args '{"delay_sec":60}'
```

### 3. Get device status

```bash
go run send_command.go \
  -controller-key nsec1... \
  -tollgate-pubkey npub1... \
  -command status
```

## Command Line Options

**Note**: The `-device-id` flag is optional. Leave it empty unless you're managing a fleet with shared keypairs.

```
-controller-key <key>    Your controller private key (nsec or hex) [REQUIRED]
-tollgate-pubkey <key>   Target TollGate public key (npub or hex) [REQUIRED]
-command <cmd>           Command to execute (default: "uptime")
-args <json>             Command arguments as JSON (default: "{}")
-device-id <id>          (Optional) Device identifier for fleet management
-relays <urls>           Comma-separated relay URLs
-timeout <sec>           Response timeout in seconds (default: 30)
```

## Available Commands

### uptime
Get system uptime and load average.

```bash
go run send_command.go \
  -controller-key nsec1... \
  -tollgate-pubkey npub1... \
  -command uptime
```

**Response:**
```json
{
  "status": "ok",
  "timestamp": 1731294012,
  "data": {
    "uptime_sec": 86400,
    "load_avg": ["0.5", "0.6", "0.7"]
  }
}
```

### reboot
Schedule a system reboot.

```bash
go run send_command.go \
  -controller-key nsec1... \
  -tollgate-pubkey npub1... \
  -command reboot \
  -args '{"delay_sec":60}'
```

**Response:**
```json
{
  "status": "ok",
  "timestamp": 1731294012,
  "message": "Reboot scheduled in 60 seconds",
  "data": {
    "delay_sec": 60
  }
}
```

### status
Get comprehensive device status.

```bash
go run send_command.go \
  -controller-key nsec1... \
  -tollgate-pubkey npub1... \
  -command status
```

**Response:**
```json
{
  "status": "ok",
  "timestamp": 1731294012,
  "data": {
    "version": "v0.0.6",
    "uptime_sec": 86400,
    "memory_free_kb": 131072,
    "device_id": "tollgate-01"
  }
}
```

## Setting Up Your TollGate

### 1. Generate controller keys

```bash
# Using nak or nostril CLI tool
nak key generate

# Or in Go
go run -
package main
import (
    "fmt"
    "github.com/nbd-wtf/go-nostr"
    "github.com/nbd-wtf/go-nostr/nip19"
)
func main() {
    sk := nostr.GeneratePrivateKey()
    pk, _ := nostr.GetPublicKey(sk)
    nsec, _ := nip19.EncodePrivateKey(sk)
    npub, _ := nip19.EncodePublicKey(pk)
    fmt.Printf("Private key (nsec): %s\n", nsec)
    fmt.Printf("Public key (npub): %s\n", npub)
}
```

### 2. Configure TollGate

Edit `/etc/tollgate/config.json` on your TollGate:

```json
{
  "control": {
    "enabled": true,
    "allowed_pubkeys": [
      "<your_controller_npub_or_hex>"
    ],
    "device_id": "router-01",
    "reboot_min_interval_sec": 600,
    "command_timeout_sec": 300,
    "ledger_path": "/etc/tollgate/command_ledger.json"
  }
}
```

### 3. Restart TollGate

```bash
/etc/init.d/tollgate restart
```

### 4. Verify it's listening

Check logs:
```bash
logread | grep Commander
```

You should see:
```
Commander: Starting TIP-07 remote control listener for device router-01
Commander: Authorized controllers: [<your_pubkey>]
Commander: Connected to relay wss://...
```

## Troubleshooting

### No response received

**Problem:** Command sent but no response received.

**Possible causes:**
1. Controller pubkey not in `allowed_pubkeys` list
2. Remote control disabled (`control.enabled: false`)
3. TollGate not connected to relays
4. Command too old (>5 minutes)

**Solution:**
- Check TollGate logs: `logread | grep Commander`
- Verify config: `cat /etc/tollgate/config.json | grep -A 10 control`
- Check relay connectivity

### "unauthorized" error

**Problem:** Response says "unauthorized".

**Solution:**
- Verify your controller pubkey is in the `allowed_pubkeys` array
- Make sure you're using the correct format (npub or hex)

### "replay_detected" error

**Problem:** Response says "replay_detected".

**Solution:**
- This command was already executed
- Each command event ID can only be processed once
- Send a new command (it will have a different timestamp/nonce/ID)

### "reboot_too_soon" error

**Problem:** Reboot rejected due to rate limiting.

**Solution:**
- Default minimum interval is 600 seconds (10 minutes)
- Wait for the interval to pass
- Or adjust `reboot_min_interval_sec` in config

## Security Notes

1. **Keep your controller private key secure** - Anyone with this key can control your routers
2. **Use multiple controller keys** - Different keys for different operators/purposes
3. **Monitor command execution** - Check `/etc/tollgate/command_ledger.json` for audit trail
4. **Use private relays** - Consider using your own relay for sensitive commands
5. **Implement key rotation** - Periodically update authorized keys

## Advanced Usage

### Using a specific relay

```bash
go run send_command.go \
  -controller-key nsec1... \
  -tollgate-pubkey npub1... \
  -command status \
  -relays wss://your-private-relay.com
```

### Targeting a specific device in multi-router setup

```bash
go run send_command.go \
  -controller-key nsec1... \
  -tollgate-pubkey npub1... \
  -command uptime \
  -device-id router-office-1
```

### Shorter timeout for quick checks

```bash
go run send_command.go \
  -controller-key nsec1... \
  -tollgate-pubkey npub1... \
  -command uptime \
  -timeout 10
```

## Integration Examples

### Bash script for monitoring

```bash
#!/bin/bash
# monitor-routers.sh

CONTROLLER_KEY="nsec1..."
ROUTERS=(
  "npub1router1..."
  "npub1router2..."
  "npub1router3..."
)

for router in "${ROUTERS[@]}"; do
  echo "Checking $router..."
  go run send_command.go \
    -controller-key "$CONTROLLER_KEY" \
    -tollgate-pubkey "$router" \
    -command status \
    -timeout 10 || echo "Failed to reach $router"
done
```

### Scheduled reboot via cron

```bash
# Reboot all routers at 3 AM daily
0 3 * * * /path/to/reboot-routers.sh
```

```bash
#!/bin/bash
# reboot-routers.sh
for router in $(cat routers.txt); do
  go run send_command.go \
    -controller-key "$CONTROLLER_KEY" \
    -tollgate-pubkey "$router" \
    -command reboot \
    -args '{"delay_sec":60}'
  sleep 5
done
```

## See Also

- [TIP-07 Specification](../protocol/07.md)
- [TollGate Configuration Guide](../docs/)
- [Nostr Protocol](https://github.com/nostr-protocol/nostr)
