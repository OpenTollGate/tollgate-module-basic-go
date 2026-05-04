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
  ├── fs.exec_direct('/usr/bin/tollgate', [..., '--json'])  ← all operations
  ├── fs.exec_direct('/sbin/logread', [...])                 ← log output
  └── fs.exec_direct('/etc/init.d/...', [...])               ← start/stop/restart
```

All backend interaction goes through the `tollgate` CLI binary with the
persistent `--json` flag. The CLI connects to the running service over the Unix
domain socket at `/var/run/tollgate.sock` and returns structured `CLIResponse`
JSON that the JS view reads directly. There is no separate helper script.

The view uses standard OpenWrt LuCI modules (`require view`, `require fs`,
`require poll`, `require ui`) following the same pattern as luci-app-adblock
and other mainstream LuCI applications.

### Single-backend design

| Component | Path | Purpose |
|-----------|------|---------|
| `tollgate` CLI | `/usr/bin/tollgate` | All operations: status, wallet, config, network, health, service control |
| rpcd ACL | `/usr/share/rpcd/acl.d/luci-app-tollgate-payments.json` | File/exec permission grants for LuCI `fs` module |

The CLI sends JSON `CLIMessage` payloads over the socket and receives
`CLIResponse{success, message, data, error, timestamp}` back. With `--json`,
all read commands return structured data (no text parsing); write commands
bypass interactive confirmation prompts.

### Schema-driven configuration

The Configuration tab is driven entirely by a schema fetched at runtime:

1. `tollgate config schema --json` returns `{config: [...], identities: [...]}`
2. Each schema entry has `{name, type, json_key, editable, description, enum, min, max, children}`
3. JS renders fields dynamically: enum fields get dropdowns, bools get toggle selects, simple types get text inputs
4. Complex arrays (mints, profit shares, public identities) render as editable tables
5. "Save All Changes" assembles the full config+identities JSON and calls `tollgate config save` and `tollgate config save-identities`

If a new field is added to the Go Config struct and registered in the schema,
it appears in the UI automatically with no JS changes.

## Tabs

### Overview

- Wallet balance (auto-refreshes every 5 s via `poll.add()`)
- Service status badge (running / stopped)
- Version info
- Start / Stop / Restart buttons

### Wallet

- Total balance and per-mint breakdown
- **Fund** — paste a Cashu ecash token to add funds
- **Drain** — convert all wallet funds to Cashu tokens (displayed on-screen;
  copy them to a safe place). Confirmation dialog via `ui.showModal()`.

### Network

- Private WiFi status (enabled/disabled, SSID, password) loaded via CLI
- Enable / Disable private WiFi (with `ui.showModal()` confirmation)
- Rename SSID
- Change password (leave empty to auto-generate a memorable password)

### Configuration

- Schema-driven structured editor for all editable config fields
- Pricing: metric, step size, margin
- Mints table: add/remove mints, edit URL, price per step, min balance, etc.
- Profit share table: factor + identity dropdown (sourced from public_identities)
- Public identities table: name, pubkey, lightning address
- Upstream detector/session manager shown as collapsible read-only JSON
- "Save All Changes" persists both config.json and identities.json

### Logs

- Live tollgate-wrt log output via `logread -e tollgate-wrt -l 300`
- Auto-refreshes while the tab is active via `poll.add()`

### Advanced

- Raw JSON editors for `config.json` and `identities.json` loaded via `tollgate config get --json`
- Validate (JSON.parse check) and Save buttons
- Save via `tollgate config save` / `tollgate config save-identities`

## CLI JSON commands used by the UI

| Command | `--json` output shape |
|---------|----------------------|
| `wallet balance` | `{data: {balance_sats, address, drain_target}}` |
| `wallet info` | `{data: {total_balance, mint_count, mint_balances}}` |
| `status` | `{data: {running, version, uptime, config_ok, wallet_ok, network_ok}}` |
| `version` | `{data: {version, commit, build_time, go_version, openwrt_version}}` |
| `network private status` | `{data: {ssid, password, enabled}}` |
| `config get` | `{data: {config: {...}, identities: {...}}}` |
| `config schema` | `{data: {config: [FieldSchema...], identities: [FieldSchema...]}}` |
| `config set <key> <value>` | `{data: {key, value}}` |
| `config save <json>` | `{message: "Configuration saved"}` |
| `config save-identities <json>` | `{message: "Identities saved"}` |
| `health` | `{data: {running, socket_ok, healthy, uptime, version, config_ok, wallet_ok, ...}}` |

## RPCd ACL

The file `/usr/share/rpcd/acl.d/luci-app-tollgate-payments.json` grants the
LuCI session the following permissions:

| Permission | Path | Access |
|------------|------|--------|
| Config files | `/etc/tollgate/` | read + write |
| CLI binary | `/usr/bin/tollgate` | exec |
| Log reader | `/sbin/logread` | exec |
| Service scripts | `/etc/init.d/tollgate-wrt` | exec |
| NoDogSplash | `/etc/init.d/nodogsplash` | exec |
| Package path check | `/usr/bin/check_package_path` | exec |

## Security model

- All operations run through LuCI's `fs` module, which is gated by rpcd ACLs.
- Access is protected by LuCI's HTTP basic auth (the router admin password).
- Wallet and network commands are sent over the Unix socket to the running
  service — the same path the CLI uses.
- User-supplied strings (tokens, SSIDs, passwords) are passed as CLI arguments;
  the CLI encodes them into JSON `CLIMessage` payloads for the socket — no
  shell injection risk.
- Service start/stop/restart call init.d scripts through `fs.exec_direct()`.

## Contract tests

Three layers of automated tests catch drift between the Go backend and the JS frontend:

| Test | Location | What it catches |
|------|----------|----------------|
| Schema↔struct drift | `src/config_manager/config_schema_drift_test.go` | New Go struct field with no schema entry (won't show in UI), or orphan schema entry |
| Dotpath round-trip | `src/config_manager/config_schema_drift_test.go` | Every editable schema field can be set + persisted via `config set` |
| CLI response shapes | `src/cmd/tollgate-cli/contract_test.go` | `CLIResponse`, `CLIMessage`, and all `data` shapes match what JS reads |
| ACL lint | `tests/contract/acl-lint.mjs` | Every binary path called by JS is granted exec in the ACL |

These run in CI as the `contract-tests` job before the build.

## Files

| File | Purpose |
|------|---------|
| `files/www/luci-static/resources/view/tollgate-payments/settings.js` | LuCI JavaScript view (6-tab admin UI) |
| `files/usr/share/rpcd/acl.d/luci-app-tollgate-payments.json` | rpcd ACL permissions |
| `files/usr/share/luci/menu.d/luci-app-tollgate-payments.json` | Sidebar menu registration |
| `src/config_manager/config_schema.go` | Schema source of truth for config + identities |
| `src/config_manager/config_dotpath.go` | Dot-path setter for `config set` |
| `src/cli/config.go` | Socket handlers for config get/set/schema/save |
| `src/cmd/tollgate-cli/main.go` | CLI with `--json` flag for all commands |

## Testing

An end-to-end Playwright smoke test covers the admin UI:
[tests/e2e/luci-minimal-smoke.mjs](../tests/e2e/luci-minimal-smoke.mjs)

```sh
TOLLGATE_ROUTER=192.168.13.112:8080 node tests/e2e/luci-minimal-smoke.mjs
```

Contract tests (no router needed):

```sh
cd src/config_manager && go test ./... -v
cd src/cmd/tollgate-cli && go test ./... -v
node tests/contract/acl-lint.mjs
```
