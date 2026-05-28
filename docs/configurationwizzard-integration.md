# Configurationwizzard Integration: Architecture Decision Record

**Date:** 2026-05-19
**Status:** Decided
**Decision makers:** c03rad0r

## Context

The [configurationwizzard](https://github.com/net4sats/configurationwizzard) repository provides a standalone Preact SPA that serves as both a router admin dashboard and a captive portal for net4sats TollGate devices. It needs to be integrated with the tollgate-module-basic-go backend and packaged into the tollgate IPK for production deployment.

This document records the analysis of the existing codebase, the architectural options considered, and the decision made.

## Repositories Involved

| Repo | Role |
|------|------|
| `tollgate-module-basic-go` | Go backend: payment processing, session management, wallet, WiFi control. Exposes HTTP API on `:2121` and CLI over Unix socket at `/var/run/tollgate.sock` |
| `configurationwizzard` | Preact SPA (Vite + TypeScript): admin dashboard + captive portal. Served by uhttpd on port 80 |
| `physical-router-test-automation` | Playwright E2E tests running on physical hardware |

## Background: What Exists Today

### tollgate-module-basic-go (`:2121` HTTP API)

The Go service listens on port 2121 with the following endpoints:

| Endpoint | Auth | Purpose |
|----------|------|---------|
| `GET /` | None | Returns pricing advertisement (Nostr kind 10021) |
| `POST /` | None | Cashu token payment, grants internet access |
| `GET /whoami` | None | Returns client MAC address |
| `POST /ln-invoice` | None | Creates Lightning invoice for payment |
| `GET /ln-invoice?quote=X` | None | Polls Lightning invoice status |
| `GET /balance` | None | Returns session balance/usage |

These are intentionally unauthenticated — WiFi clients must reach them to pay.

The Go service also has a CLI server listening on a Unix domain socket that handles: `wallet balance/info/fund/drain`, `network private status/enable/disable/rename/set-password`, `status`, `version`, `health`, and (pending PR merge) `config get/set/schema/save`.

### tollgate-module-basic-go (LuCI admin — pending PRs)

Three open PRs add a LuCI-based admin UI:

| PR | Branch | What it adds |
|----|--------|--------------|
| #112 | `feat/config-schema-dotpath` | Schema system (`config_schema.go`) and dot-path setter (`config_dotpath.go`) — pure Go, zero frontend dependency |
| #113 | `feat/cli-json-config` | `--json` flag on CLI, config handlers over Unix socket (`config get/set/schema/save/save-identities`), health endpoint |
| #114 | `feat/luci-admin-ui` | LuCI settings.js (5-tab admin UI) using `fs.exec_direct('/usr/bin/tollgate', [..., '--json'])` |

**None of these PRs are merged yet.** They have known issues (thread safety in SetDotPath, schema constraints not enforced by dot-path setter) that need resolution before merge.

### configurationwizzard (main branch)

A Preact SPA with:

- **Admin dashboard**: login, dashboard, WiFi management, devices, settings, wallet
- **ubus JSON-RPC client** (`src/lib/ubus.ts`) with session auth
- **Mock ubus layer** (`VITE_MOCK=true`) for standalone demos
- **rpcd shell plugin** (`openwrt/rpcd/tollgate`) bridging ubus to `tollgate` CLI
- **Dual uhttpd config**: port 80 = SPA, port 8080 = LuCI
- **PWA**: manifest + service worker
- **CI**: GitHub Pages demo deployment

### configurationwizzard (`captive-portal` branch)

One commit ahead of main, adds the captive portal (user-facing payment page):
- Lightning and Cashu payment tabs
- Size selection (time or data)
- Success screen with redirect
- **Payment logic is mock-only** — fake invoices, auto-succeed after timeout

### Two Config Systems

The router manages configuration through two separate systems:

**UCI (Unified Configuration Interface)** — OpenWrt standard:
- WiFi SSID/password (`wireless.*`)
- Hostname (`system.*`)
- Firewall rules (`firewall.*`)
- DHCP, DNS, uhttpd settings
- Read/write via `uci` CLI or `ubus uci.get/uci.set/uci.commit`

**config.json** — Go application config:
- Pricing (metric, step_size, price_per_step)
- Accepted mints, profit share
- Upstream detector/session manager
- Identities (Nostr keys, Lightning addresses)
- Read/write via `ConfigManager` in Go (or `tollgate config` CLI)

UCI knows nothing about config.json. The Go ConfigManager knows nothing about UCI. Any admin UI must handle both.

### Existing LuCI Branch Patterns Analyzed

Three LuCI approaches were evaluated:

| Branch | Pattern | Transport | Config writes | Auth |
|--------|---------|-----------|---------------|------|
| `feature/luci-payments-poc` | CGI shell script + code generator | `fetch('/cgi-bin/tollgate-api')` | Shell writes config.json directly | None |
| `feature/luci-admin-ui` | Direct file I/O + socket helper | `fs.read/write` + `nc -U` socket | JS writes config.json directly | ubus session |
| `feat/luci-admin-ui` (PR #114) | CLI `--json` via LuCI `fs.exec_direct` | LuCI `fs` module → CLI → socket | Go `SetDotPath` + `SaveConfig` | ubus session |

PR #114 is the best pattern — schema-driven, Go-validated, contract-tested. But it depends on LuCI's `fs.exec_direct` which is unavailable to a standalone SPA.

## Options Considered

### Option A: Admin HTTP API on Go Service (`:2121`)

Add admin endpoints to the existing HTTP server:

```
SPA → fetch(:2121/admin/config/schema)   → Go HTTP handler → ConfigManager.GetConfigSchema()
SPA → fetch(:2121/admin/config/save)     → Go HTTP handler → ConfigManager.SaveConfig()
SPA → fetch(:2121/admin/wallet/drain)    → Go HTTP handler → merchant.DrainMint()
SPA → fetch(:2121/admin/network/rename)  → Go HTTP handler → exec.Command("uci", "set", ...)
```

**Pros:**
- One unified transport for both admin and payments
- No shell wrapping of JSON
- Reuses Go business logic directly
- Captive portal already talks to `:2121`

**Cons:**
- Must build authentication from scratch (session management, password validation against `/etc/shadow`, token storage, expiry)
- New auth code is attack surface on a payment gateway
- Two auth systems if LuCI also used (ubus sessions + Go tokens)
- ~200-300 lines of security-critical Go code to write and audit

### Option B: rpcd Plugin Calling CLI

SPA uses ubus JSON-RPC (already implemented in `ubus.ts`) to call a custom rpcd plugin, which calls the CLI binary:

```
SPA → POST /ubus {session, "tollgate", "config_schema"}
        → rpcd validates session against ACL
        → rpcd executes /usr/libexec/rpcd/tollgate
              → shell script runs /usr/bin/tollgate config schema --json
                    → CLI → Unix socket → CLIServer.handleConfigSchema()
```

**Pros:**
- Authentication is free (ubus session system, validates against `/etc/shadow`)
- ACL enforcement is free (rpcd ACL policy)
- Session management is free (rpcd manages tokens, expiry, revocation)
- Battle-tested auth code (rpcd is the standard OpenWrt auth mechanism)
- configurationwizzard's existing `ubus.ts` already handles login/sessions
- Same auth used by LuCI — one password, one session system

**Cons:**
- Extra process hops (rpcd → shell → CLI → socket) for config.json operations
- Need to write/maintain a shell rpcd plugin (~100 lines)
- Two transports in the SPA (ubus for admin, HTTP for payments)

### Hybrid (Chosen)

Admin config via rpcd (with ubus auth). Captive portal payments via HTTP on `:2121` (no auth needed).

## Decision

**We chose the hybrid approach.**

### Rationale

The deciding factor was **security**. The admin endpoints control pricing, wallet funds, profit share destinations, and service lifecycle. Exposing these without robust authentication on a payment gateway is unacceptable:

| Attack vector if admin endpoints are unauthenticated | Impact |
|---|---|
| Modify `profit_share` to attacker's Lightning address | Redirect all revenue |
| `wallet drain` | Steal all funds as Cashu tokens |
| Change `accepted_mints` to attacker's mint | MITM all payments |
| `service stop` | DoS all connected users |
| Modify `upstream_detector` config | Route traffic through attacker |

The rpcd/ubus session system provides battle-tested authentication, session management, and ACL enforcement with zero custom security code. Building a new auth system in Go for Option A would introduce attack surface on a payment gateway — the risk/reward is wrong.

The captive portal payment endpoints on `:2121` remain unauthenticated because they are deposit-only (you can only pay, not withdraw). MAC resolution is server-side. This is already the production design.

### Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                    configurationwizzard SPA                      │
│                                                                  │
│  ┌──────────────────┐              ┌──────────────────────────┐ │
│  │   Admin Dashboard │              │    Captive Portal         │ │
│  │  (login, settings,│              │  (payment flow)           │ │
│  │   wallet, wifi,   │              │                           │ │
│  │   devices)        │              │                           │ │
│  └────────┬──────────┘              └────────────┬──────────────┘ │
│           │                                      │               │
└───────────┼──────────────────────────────────────┼───────────────┘
            │ ubus JSON-RPC                         │ HTTP
            │ (with session token)                  │ (no auth)
            ▼                                      ▼
┌───────────────────────┐              ┌───────────────────────────┐
│     rpcd / ubus       │              │  Go HTTP API (:2121)       │
│                       │              │                           │
│ Session auth against  │              │ POST /       → pay       │
│ /etc/shadow           │              │ POST /ln-invoice         │
│                       │              │ GET  /ln-invoice?quote=  │
│ ACL policy controls   │              │ GET  /balance             │
│ which ubus objects    │              │ GET  /whoami              │
│ each session can call │              │ GET  / → pricing ad       │
│                       │              │                           │
└───────────┬───────────┘              └───────────────────────────┘
            │
            ▼
┌───────────────────────────┐
│ rpcd plugin (shell)       │
│ /usr/libexec/rpcd/tollgate│
│                           │
│ Calls:                    │
│  tollgate config schema   │
│  tollgate config get      │
│  tollgate config set      │
│  tollgate config save     │
│  tollgate wallet balance  │
│  tollgate wallet drain    │
│  tollgate wallet fund     │
│  tollgate status          │
│  tollgate health          │
│  tollgate network priv... │
│  tollgate version         │
│                           │
└───────────┬───────────────┘
            │
            ▼
┌───────────────────────────────────────────────────────────────┐
│              tollgate-wrt (Go service)                         │
│                                                                │
│  CLIServer listening on /var/run/tollgate.sock                 │
│                                                                │
│  ConfigManager ← config_schema.go, config_dotpath.go          │
│  Merchant      ← wallet, payments, sessions                   │
│  Valve         ← ndsctl gate control                          │
│  WiFi Manager  ← uci commands for network operations          │
└───────────────────────────────────────────────────────────────┘
```

### Auth Flow

```
1. User opens http://router/ → SPA loads
2. SPA shows login page
3. User enters username + password
4. SPA → POST /ubus {method: "call", params: ["000...", "session", "login", {username, password}]}
5. rpcd validates against /etc/shadow → returns ubus_rpc_session token
6. SPA stores token in localStorage (existing ubus.ts already does this)
7. All admin calls include session token:
   SPA → POST /ubus {params: [session_token, "tollgate", "config_schema", {}]}
8. rpcd checks session validity + ACL policy before executing plugin
9. Expired/invalid sessions → SPA redirects to login
```

### Transport Mapping

| SPA operation | Transport | Backend path | Auth |
|---|---|---|---|
| Login/logout | ubus | rpcd `session.login/logout` | None (login IS auth) |
| Get config schema | ubus | rpcd plugin → `tollgate config schema --json` | ubus session |
| Get/set config | ubus | rpcd plugin → `tollgate config get/set/save --json` | ubus session |
| Wallet balance/info | ubus | rpcd plugin → `tollgate wallet balance/info --json` | ubus session |
| Wallet fund/drain | ubus | rpcd plugin → `tollgate wallet fund/drain --json` | ubus session |
| Private WiFi manage | ubus | rpcd plugin → `tollgate network private ...` | ubus session |
| Service status/health | ubus | rpcd plugin → `tollgate status/health --json` | ubus session |
| Service start/stop/restart | ubus | rpcd plugin → `/etc/init.d/tollgate-wrt ...` | ubus session |
| WiFi status (read) | ubus | Built-in `wireless.status` | ubus session |
| DHCP leases (read) | ubus | Built-in `dhcp.ipv4leases` | ubus session |
| Hostname change | ubus | Built-in `uci.set/commit` for system | ubus session |
| Password change | ubus | Built-in `system.password_set` | ubus session |
| View logs | ubus | rpcd plugin → `logread -e tollgate-wrt` | ubus session |
| **Pay with Cashu** | HTTP `:2121` | `POST /` | None (deposit-only) |
| **Pay with Lightning** | HTTP `:2121` | `POST /ln-invoice` + `GET /ln-invoice?quote=` | None (deposit-only) |
| **Get MAC** | HTTP `:2121` | `GET /whoami` | None |
| **Get pricing** | HTTP `:2121` | `GET /` | None |
| **Check balance** | HTTP `:2121` | `GET /balance` | None |

### Schema-Driven Config UI

The SPA will fetch the config schema at runtime via `ubusCall('tollgate', 'config_schema')` and render forms dynamically — the same pattern PR #114 established for LuCI:

```js
// SPA fetches schema from Go business logic (via rpcd → CLI → socket)
const schemaResp = await ubusCall('tollgate', 'config_schema');
const configSchema = schemaResp.config;   // FieldSchema[] from config_schema.go
const identSchema = schemaResp.identities; // FieldSchema[] from config_schema.go

// Render forms dynamically from schema
configSchema.forEach(field => {
    if (!field.editable) return;
    if (field.enum) renderDropdown(field);
    else if (field.type === 'bool') renderToggle(field);
    else if (field.type === 'uint64' || field.type === 'float64') renderNumberInput(field);
    else if (field.type === 'array' && field.json_key === 'accepted_mints') renderMintCards(field);
    else if (field.type === 'array' && field.json_key === 'profit_share') renderProfitShareSliders(field);
    else if (field.type === 'object') renderCollapsibleSection(field);
    else renderTextInput(field);
});
```

Adding a new Go config field + schema entry makes it appear in the SPA with zero frontend code changes.

## Implementation Roadmap

### Phase 1: Captive Portal — Wire to Real Payment API

Replace mock payment logic in the `captive-portal` branch with real calls to `:2121`:

- Replace mock pricing → `fetch('http://gateway:2121/')` → parse Nostr advertisement
- Replace mock MAC → `fetch('http://gateway:2121/whoami')`
- Replace mock Lightning → `fetch('http://gateway:2121/ln-invoice', {POST})` + poll
- Replace mock Cashu → `fetch('http://gateway:2121/', {POST, body: token})`
- Add QR code library for invoice display
- Fix hardcoded `https://net4sats.cash/` asset URLs → local relative paths

### Phase 2: Admin Dashboard — rpcd Plugin + Schema

Build the rpcd plugin that bridges ubus to CLI:

- Write `/usr/libexec/rpcd/tollgate` — shell script calling `tollgate ... --json`
- Write `/usr/share/rpcd/acl.d/tollgate.json` — ACL policy
- Wire SPA settings page to use schema-driven rendering
- Wire wallet page to real fund/drain/balance operations

### Phase 3: Build & Packaging

- Dual Vite build: admin SPA (served by uhttpd on :80) + captive portal (served by NoDogSplash)
- Add built assets to tollgate IPK
- Include rpcd plugin, ACL, uhttpd config in IPK
- CI pipeline to build configurationwizzard and stage assets

### Phase 4: Testing

- Contract tests (schema drift, ACL lint, CLI response shapes) — follow PR #114 pattern
- E2E tests on physical hardware
- Security review of rpcd plugin (input sanitization)

### Prerequisites (blocking)

PRs #112 and #113 need to merge first (with thread safety fix and schema constraint enforcement). The Go config schema and CLI `--json` infrastructure is the foundation everything else builds on.

## Open Issues

1. **PR #112 thread safety**: `SetDotPath` uses `RLock` then mutates shared state without write lock. Must fix before merge.
2. **PR #112 schema constraints not enforced**: `SetDotPath` accepts invalid enum/min/max values. Should validate against schema before persisting.
3. **rpcd plugin input sanitization**: The current configurationwizzard rpcd plugin passes user input directly to `uci set` and `passwd` — must sanitize or route all writes through `tollgate ... --json` instead.
4. **Dual build config**: Vite needs separate build outputs for admin SPA (base: `/net4sats/`) and captive portal (base: `/`). Currently on separate branches.
5. **Captive portal asset URLs**: Hardcoded `https://net4sats.cash/assets/...` won't work offline. Must use local assets.
6. **Wallet page**: Currently a "Coming Soon" placeholder. Needs fund, drain, per-mint breakdown.

## File Inventory

### configurationwizzard files that need changes

| File | Change needed |
|------|--------------|
| `src/lib/ubus.ts` | Keep as-is (already handles ubus auth correctly) |
| `src/lib/ubus.mock.ts` | Add `tollgate.activate` mock, fix pricing shape to match Go advertisement format |
| `src/routes/captive-portal.tsx` | Rewrite to call `:2121` payment API instead of mock ubus; add QR library; fix asset URLs |
| `src/routes/settings.tsx` | Rewrite to use schema-driven rendering via `ubusCall('tollgate', 'config_schema')` |
| `src/routes/wallet.tsx` | Implement fund/drain/balance via ubus calls |
| `src/routes/dashboard.tsx` | Replace `ubusCall('tollgate', 'status')` with rpcd-backed call |
| `src/routes/wifi.tsx` | Keep ubus `wireless.status` + `uci.set/commit` (already works) |
| `openwrt/rpcd/tollgate` | Rewrite to call `tollgate ... --json` instead of raw `uci set`/`passwd` |
| `openwrt/rpcd/tollgate_acl.json` | Expand to cover all new operations |
| `vite.config.ts` | Configure dual build outputs |

### tollgate-module-basic-go files involved

| File | Role |
|------|------|
| `src/config_manager/config_schema.go` | Schema source of truth (PR #112) |
| `src/config_manager/config_dotpath.go` | Dot-path setter with validation (PR #112) |
| `src/cli/config.go` | Config handlers over Unix socket (PR #113) |
| `src/cli/server.go` | CLI server dispatch (PR #113) |
| `src/main.go` | HTTP server on :2121, payment endpoints |
| `src/merchant/` | Payment processing, wallet, sessions |
| `packaging/Makefile` | IPK packaging — needs configurationwizzard assets added |
| `packaging/files/tollgate-captive-portal-site/` | Current captive portal assets — to be replaced |
