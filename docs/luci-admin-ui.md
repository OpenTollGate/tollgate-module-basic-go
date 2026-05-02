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
  ├── fs.exec_direct('/usr/bin/tollgate', [...])                ← display-only CLI
  ├── fs.exec_direct('/usr/libexec/tollgate-luci-helper', [...]) ← socket operations
  ├── fs.read_direct('/etc/tollgate/config.json')                ← config reads
  ├── fs.write('/etc/tollgate/...', data)                        ← config saves
  └── fs.exec_direct('/sbin/logread', [...])                     ← log output
```

The view uses standard OpenWrt LuCI modules (`require view`, `require fs`,
`require poll`, `require ui`) following the same pattern as luci-app-adblock
and other mainstream LuCI applications.

### Backend split

| Component | Path | Purpose |
|-----------|------|---------|
| `tollgate` CLI | `/usr/bin/tollgate` | Display-only text output: `wallet balance`, `wallet info`, `status`, `version` |
| Socket helper | `/usr/libexec/tollgate-luci-helper` | Structured JSON socket operations: wallet fund/drain, WiFi management, network status |
| rpcd ACL | `/usr/share/rpcd/acl.d/luci-app-tollgate-payments.json` | File/exec permission grants for LuCI `fs` module |

The helper script proxies commands to the running `tollgate-wrt` service over
the Unix domain socket at `/var/run/tollgate.sock`, using the same JSON
`CLIMessage` protocol as the CLI. It returns JSON responses that the JS view
parses directly.

## Tabs

### Overview

- Wallet balance (auto-refreshes every 5 s via `poll.add()`)
- Service status badge (running / stopped)
- Version info
- Start / Stop / Restart buttons

### Wallet

- Total balance and per-mint breakdown
- **Fund** — paste a Cashu ecash token to add funds (via socket helper)
- **Drain** — convert all wallet funds to Cashu tokens (displayed on-screen;
  copy them to a safe place). Confirmation dialog via `ui.showModal()`.

### Network

- Private WiFi status (enabled/disabled, SSID, password) loaded via helper
- Enable / Disable private WiFi (with `ui.showModal()` confirmation)
- Rename SSID
- Change password (leave empty to auto-generate a memorable password)

### Configuration

- Read-only pricing display (price per step, step size, metric) via `fs.read_direct()`
- Accepted mints list
- Profit share configuration

### Logs

- Live tollgate-wrt log output via `fs.exec_direct('/sbin/logread', [...])`
- Auto-refreshes while the tab is active via `poll.add()`

### Advanced

- Raw JSON editors for `config.json` and `identities.json` via `fs.read_direct()`
- Validate (JSON.parse check) and Save buttons
- Save creates timestamped backup via `fs.exec_direct('/bin/cp', [...])`
  then writes via `fs.write()`

## RPCd ACL

The file `/usr/share/rpcd/acl.d/luci-app-tollgate-payments.json` grants the
LuCI session the following permissions:

| Permission | Path | Access |
|------------|------|--------|
| Config files | `/etc/tollgate/` | read + write |
| CLI binary | `/usr/bin/tollgate` | exec |
| Helper script | `/usr/libexec/tollgate-luci-helper` | exec |
| Backup copy | `/bin/cp` | exec |
| Log reader | `/sbin/logread` | exec |
| Service scripts | `/etc/init.d/tollgate-wrt` | exec |
| NoDogSplash | `/etc/init.d/nodogsplash` | exec |
| Package path check | `/usr/bin/check_package_path` | exec |

## Security model

- All operations run through LuCI's `fs` module, which is gated by rpcd ACLs.
- Access is protected by LuCI's HTTP basic auth (the router admin password).
- Wallet and network commands are sent over the Unix socket to the running
  service — the same path the CLI uses.
- User-supplied strings (tokens, SSIDs, passwords) are escaped in the helper
  script via `json_str_esc()` to prevent JSON injection.
- Service start/stop/restart call init.d scripts through `fs.exec_direct()`.

## Files

| File | Purpose |
|------|---------|
| `files/www/luci-static/resources/view/tollgate-payments/settings.js` | LuCI JavaScript view (6-tab admin UI) |
| `files/usr/libexec/tollgate-luci-helper` | Socket proxy for structured JSON operations |
| `files/usr/share/rpcd/acl.d/luci-app-tollgate-payments.json` | rpcd ACL permissions |
| `files/usr/share/luci/menu.d/luci-app-tollgate-payments.json` | Sidebar menu registration |

## Testing

An end-to-end Playwright smoke test covers the admin UI:
[tests/e2e/luci-minimal-smoke.mjs](../tests/e2e/luci-minimal-smoke.mjs)

```sh
TOLLGATE_ROUTER=192.168.13.112:8080 node tests/e2e/luci-minimal-smoke.mjs
```
