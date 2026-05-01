# LuCI Admin UI

TollGate ships a LuCI admin page at **Services → TollGate** that provides a
structured dashboard for managing the payment gateway without SSH.

## Access

Navigate to `http://<router-ip>/cgi-bin/luci/admin/services/tollgate-payments`
or click **Services → TollGate** in the LuCI sidebar.

The page is served by the `tollgate-wrt` package itself (not a separate
`luci-app-*` package). It depends on the `luci` meta-package and `jq`.

## Architecture

```
LuCI browser (settings.js)
  │
  │  fetch('/cgi-bin/tollgate-api?action=...')
  ▼
CGI shell script (/www/cgi-bin/tollgate-api)
  │
  ├── CLI commands (/usr/bin/tollgate wallet/status/version)
  ├── Unix socket (/var/run/tollgate.sock) ──▶ running tollgate-wrt service
  ├── init.d scripts (/etc/init.d/tollgate-wrt, nodogsplash)
  └── JSON files (/etc/tollgate/config.json, identities.json)
```

The CGI script runs as root under uhttpd. Wallet and network operations are
proxied to the running `tollgate-wrt` service over the Unix domain socket using
the same JSON protocol as the `tollgate` CLI.

## Tabs

### Overview

- Wallet balance (auto-refreshes every 5 s)
- Service status badge (running / stopped)
- Version info
- Start / Stop / Restart buttons

### Wallet

- Total balance and per-mint breakdown
- **Fund** — paste a Cashu ecash token to add funds
- **Drain** — convert all wallet funds to Cashu tokens (displayed on-screen;
  copy them to a safe place)

### Network

- Private WiFi status (enabled/disabled, SSID, password)
- Enable / Disable private WiFi (with confirmation)
- Rename SSID
- Change password (leave empty to auto-generate a memorable password)

### Configuration

- Read-only pricing display (price per step, step size, metric)
- Accepted mints list
- Profit share configuration

### Logs

- Live tollgate-wrt log output (auto-refreshes while the tab is active)

### Advanced

- Raw JSON editors for `config.json` and `identities.json`
- Validate and save buttons with timestamped backups

## CGI API Reference

All actions are called via `GET /cgi-bin/tollgate-api?action=<action>`.
Actions that accept a body expect `Content-Type: application/json`.

| Action | Method | Body | Description |
|--------|--------|------|-------------|
| `dashboard` | GET | — | Wallet balance, version, status, logs |
| `wallet_info` | GET | — | Per-mint balance breakdown |
| `wallet_fund` | POST | `{"token":"..."}` | Fund wallet with a Cashu token |
| `wallet_drain` | POST | — | Drain all wallet funds to Cashu tokens |
| `wifi_status` | GET | — | Private WiFi SSID, password, enabled state |
| `wifi_enable` | POST | — | Enable private WiFi on both radios |
| `wifi_disable` | POST | — | Disable private WiFi |
| `wifi_rename` | POST | `{"ssid":"..."}` | Rename private SSID |
| `wifi_password` | POST | `{"password":"..."}` | Change WiFi password (omit to auto-generate) |
| `service_start` | POST | — | Start TollGate + NoDogSplash |
| `service_stop` | POST | — | Stop services |
| `service_restart` | POST | — | Restart services |
| `clients` | GET | — | DHCP leases and ndsctl authenticated clients |
| `files` | GET | — | Contents of config.json and identities.json |
| `validate_config` | POST | raw JSON | Validate JSON syntax for config |
| `validate_identities` | POST | raw JSON | Validate JSON syntax for identities |
| `save_config` | POST | raw JSON | Atomic save with timestamped backup |
| `save_identities` | POST | raw JSON | Atomic save with timestamped backup |

### Response format

All responses are `application/json` with at least an `ok` field:

```json
{"ok": true, "wallet_balance": "1234 sats", "version": "version: 0.7.0", ...}
```

Error responses:

```json
{"ok": false, "error": "Description of what went wrong."}
```

## Security model

- The CGI script runs as **root** under uhttpd (standard for OpenWrt LuCI).
- Access is protected by LuCI's HTTP basic auth (the router admin password).
- Wallet and network commands are sent over the Unix socket to the running
  service — the same path the CLI uses.
- User-supplied strings (tokens, SSIDs, passwords) are escaped before JSON
  embedding via `json_str_esc()` to prevent injection.
- Service start/stop/restart call init.d scripts directly.

## Files

| File | Purpose |
|------|---------|
| `files/www/luci-static/resources/view/tollgate-payments/settings.js` | LuCI JavaScript view |
| `files/www/cgi-bin/tollgate-api` | CGI REST API (shell script) |
| `files/usr/share/luci/menu.d/luci-app-tollgate-payments.json` | Sidebar menu registration |

## Testing

An end-to-end Playwright smoke test covers the admin UI:
[tests/e2e/luci-minimal-smoke.mjs](../tests/e2e/luci-minimal-smoke.mjs)

```sh
TOLLGATE_ROUTER=192.168.13.112:8080 node tests/e2e/luci-minimal-smoke.mjs
```
