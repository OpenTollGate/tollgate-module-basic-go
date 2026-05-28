# Portal Packaging Plan: TollGate SPA + configurationwizzard

**Date:** 2026-05-27
**Status:** In Progress
**Related:**
- `docs/configuration-ui-merge-plan.md` (Part A/B integration)
- `docs/configurationwizzard-integration.md` (architecture ADR)
- PR #124 (`develop → main`) — config schema, CLI `--json`, test infrastructure
- `OpenTollGate/tollgate-captive-portal-site` — TollGate-branded SPA (React)
- `net4sats/configurationwizzard` — net4sats-branded SPA (Preact)

---

## Overview

Three blockers to merging PRs:

1. **TollGate theme for PR124** — standalone full SPA (captive portal + admin dashboard) in `OpenTollGate/tollgate-captive-portal-site`, no dependency on net4sats
2. **configurationwizzard packaging** — own CI workflow producing `.ipk`/`.apk` with `tollgate-wrt` as a dependency, publishing via Blossom/NIP-94
3. **Hardware testing** — thorough end-to-end testing on physical routers

---

## Router Inventory

| Router | Gateway IP | Workstation IP | Arch | OpenWrt | Current State |
|--------|-----------|----------------|------|---------|---------------|
| A | `10.47.41.1` | `10.47.41.133` (enx00e04c633a90) | `aarch64_cortex-a53` | 24.10.4 | `tollgate-wrt v0.4.0`, no `--json` |
| B | `192.168.244.1` | `192.168.244.106` (enx00e04c683d2d) | `aarch64_cortex-a53` | 24.10.4 | `tollgate-wrt v0.0.0` (dev), has `--json` + rpcd |

Both routers: SSH via `ssh root@<gateway-ip>`, no password. ipk format (not apk).

---

## Worktree Layout

```
/home/c03rad0r/tollgate-module-basic-go/              ← main (clean)
/home/c03rad0r/tollgate-worktrees/portal-packaging/   ← feat/portal-packaging (this work)
/tmp/tollgate-captive-portal-site/                    ← TollGate SPA repo
/tmp/configurationwizzard/                            ← net4sats configwizzard repo
~/physical-router-test-automation/                    ← test automation
```

---

## Phase 1: TollGate-branded Full SPA (`OpenTollGate/tollgate-captive-portal-site`)

### Context

The repo currently has a React captive portal that talks to `:2121` (Cashu + Lightning payments). It needs:
- Admin SPA (login, dashboard, settings, wallet, wifi, devices)
- rpcd plugin for admin auth via ubus
- Dual Vite build (portal + admin)
- Packaging (Makefile, build-ipk.sh, etc.)
- CI workflow with Blossom/NIP-94 publishing

**Framework:** React (keep existing, add admin in React)
**Backend dependency:** PR #124 (`tollgate --json config schema/get/set/save`)

### Phase 1a: Admin SPA Source

New files in `src/admin/`:

| File | Purpose |
|------|---------|
| `src/admin/index.jsx` | Entry point |
| `src/admin/App.jsx` | Router with login guard |
| `src/admin/pages/Login.jsx` | ubus session auth |
| `src/admin/pages/Dashboard.jsx` | Status, health, uptime |
| `src/admin/pages/Settings.jsx` | Schema-driven form via rpcd `config_schema` |
| `src/admin/pages/Wallet.jsx` | Balance, fund, drain |
| `src/admin/pages/Wifi.jsx` | Upstream scan/connect/remove |
| `src/admin/pages/Devices.jsx` | DHCP leases, connected clients |
| `src/admin/lib/ubus.js` | JSON-RPC client (ubus over HTTP) |
| `src/admin/lib/schema-form.jsx` | Dynamic form renderer from FieldSchema[] |
| `src/admin/components/Layout.jsx` | Sidebar nav, header |
| `src/admin/index.scss` | Admin styles |

### Phase 1b: rpcd Plugin + ACL

| File | Purpose |
|------|---------|
| `openwrt/rpcd/tollgate` | Shell script calling `tollgate --json` (same pattern as configwizzard) |
| `openwrt/rpcd/tollgate_acl.json` | ACL policy: read (schema, get, balance, info, status, health, upstream_scan/list) / write (set, save, fund, drain, connect, remove) |

### Phase 1c: Dual Vite Build + Packaging

| File | Purpose |
|------|---------|
| `vite.config.js` | Updated: dual entry (portal base `/` + admin base `/tollgate/`) |
| `scripts/build-all.mjs` | Builds both portal + admin |
| `packaging/Makefile` | OpenWrt SDK package definition |
| `packaging/build-ipk.sh` | ar + tar ipk builder (from tollgate-module-basic-go) |
| `packaging/normalize-apk-version.sh` | Version normalization (from tollgate-module-basic-go) |
| `packaging/postinst` | Restart rpcd, nodogsplash, uhttpd |
| `packaging/prerm` | Clean up on removal |
| `packaging/files/etc/uci-defaults/91-tollgate-portal-setup` | One-time setup |
| `packaging/files/etc/config/uhttpd_tollgate` | uhttpd: admin on :80, LuCI on :8080 |

Package metadata:
```
PKG_NAME: tollgate-captive-portal
DEPENDS: +tollgate-wrt +nodogsplash +luci +jq
PROVIDES: tollgate-captive-portal-site
REPLACES: tollgate-wrt (for portal files)
```

Install paths:
```
dist/portal/*      → /etc/tollgate/tollgate-captive-portal-site/
dist/admin/*       → /www/tollgate/
openwrt/rpcd/*     → /usr/libexec/rpcd/ + /usr/share/rpcd/acl.d/
packaging/files/*  → /etc/config/ + /etc/uci-defaults/
```

### Phase 1d: CI Workflow

`.github/workflows/build-package.yml`:

```
Jobs:
1. build-spa        — Node 22, npm ci, npm run build (dual: admin + portal)
2. define-matrix    — Same arch/sdk matrix as tollgate-module-basic-go
3. determine-version — Same versioning logic
4. package-ipk      — Stage SPA assets + rpcd + ACL, build via build-ipk.sh
5. package-apk      — Stage assets + SDK container build
6. publish-metadata — Blossom upload + NIP-94 events
```

Requires secrets: `NSEC_HEX` (same Blossom/relay setup as tollgate-module-basic-go)

---

## Phase 2: configurationwizzard Packaging (`net4sats/configurationwizzard`)

The SPA + rpcd plugin already exist. Just needs packaging + CI.

### Files to Create

| File | Purpose |
|------|---------|
| `packaging/Makefile` | `PKG_NAME:=configurationwizzard`, `DEPENDS:=+tollgate-wrt +nodogsplash +luci +jq` |
| `packaging/build-ipk.sh` | From tollgate-module-basic-go |
| `packaging/normalize-apk-version.sh` | From tollgate-module-basic-go |
| `packaging/postinst` | Restart rpcd, nodogsplash |
| `packaging/prerm` | Clean up |
| `packaging/files/etc/uci-defaults/91-configurationwizzard-setup` | One-time setup |
| `.github/workflows/build-package.yml` | Same pattern as TollGate portal CI |

PR description must reference `OpenTollGate/tollgate-module-basic-go#124`.

---

## Phase 3: Fresh Deploy PR124 to Both Routers

Using `physical-router-test-automation` framework with mutex.

### Router A (`10.47.41.1`)

```bash
export TOLLGATE_SSH_HOST=10.47.41.1
export TOLLGATE_ROUTER_ARCH=aarch64_cortex-a53

# Acquire mutex, factory reset, deploy PR124 develop branch
pytest --tollgate-branch=develop --tollgate-factory-reset \
  --lock-phase="pr124-deploy-router-a" --tollgate-arch=aarch64_cortex-a53 \
  -k "test_smoke"
```

### Router B (`192.168.244.1`)

```bash
export TOLLGATE_SSH_HOST=192.168.244.1
export TOLLGATE_ROUTER_ARCH=aarch64_cortex-a53

# Same treatment
pytest --tollgate-branch=develop --tollgate-factory-reset \
  --lock-phase="pr124-deploy-router-b" --tollgate-arch=aarch64_cortex-a53 \
  -k "test_smoke"
```

---

## Phase 4: Hardware Testing

### 4a: Install Portal Packages

**Router A — TollGate portal:**
```bash
ssh root@10.47.41.1 "opkg install /tmp/tollgate-captive-portal_*.ipk"
```

**Router B — configurationwizzard:**
```bash
ssh root@192.168.244.1 "opkg install /tmp/configurationwizzard_*.ipk"
```

### 4b: E2E Test Script (`test-configwizzard-e2e.sh`)

Run on both routers (with mutex):

```
Phase 1: tollgate --json CLI backend (schema, config get/set, wallet, health, status)
Phase 2: RPCD plugin — ubus methods (config_schema, config_get, config_set, wallet_balance)
Phase 3: :2121 payment API (pricing, whoami, balance)
Phase 4: SPA deployment files (admin at :80, portal files, rpcd plugin)
Phase 5: Integration (rpcd ↔ tollgate --json data matches)
```

### 4c: Full Pytest Suite

```bash
# Router A
export TOLLGATE_SSH_HOST=10.47.41.1
pytest tests/api/ --lock-phase="portal-e2e-router-a" \
  --tollgate-branch=develop --tollgate-arch=aarch64_cortex-a53

# Router B
export TOLLGATE_SSH_HOST=192.168.244.1
pytest tests/api/ --lock-phase="portal-e2e-router-b" \
  --tollgate-branch=develop --tollgate-arch=aarch64_cortex-a53
```

### 4d: Manual Verification Checklist

For each router:

- [ ] `tollgate --json config schema` returns valid schema
- [ ] `tollgate --json config get` returns full config
- [ ] `tollgate --json config set log_level debug` → success, persisted
- [ ] `tollgate --json config set log_level INVALID` → error (enum validation)
- [ ] `tollgate --json wallet balance` responds
- [ ] `tollgate --json health` → ok
- [ ] `ubus call tollgate config_schema` returns schema via rpcd
- [ ] `ubus call tollgate config_set '{"key":"log_level","value":"warn"}'` → success
- [ ] Admin SPA loads at `http://<router>/`
- [ ] Admin login works (ubus session auth)
- [ ] Settings page renders from schema
- [ ] Config save round-trip works
- [ ] Wallet fund/drain works
- [ ] Captive portal: Cashu payment → access granted
- [ ] Captive portal: Lightning payment → access granted
- [ ] `:2121` payment API still works
- [ ] No crash loops in `tollgate-wrt` or `nodogsplash`

### 4e: Upgrade / Rollback Test

- [ ] `opkg remove configurationwizzard` (or `tollgate-captive-portal`) → default TollGate portal restored
- [ ] `tollgate-wrt` still running and healthy after portal package removal
- [ ] Re-install portal package → everything works again

---

## Phase 5: Final Verification

- [ ] Cross-reference PR #124 in TollGate portal repo PR
- [ ] Cross-reference PR #124 in configurationwizzard repo PR
- [ ] All three blockers resolved:
  - [ ] Blocker 1: TollGate theme for PR124 — standalone SPA
  - [ ] Blocker 2: Packaging workflow — both repos have CI + Blossom/NIP-94
  - [ ] Blocker 3: Hardware testing — both routers pass full test suite
- [ ] No regressions in existing `tollgate-wrt` functionality

---

## Dependency Graph

```
PR #124 (develop → main)
  │
  ├─→ Phase 1: TollGate SPA (tollgate-captive-portal-site)
  │     ├─ 1a: Admin SPA source
  │     ├─ 1b: rpcd plugin + ACL
  │     ├─ 1c: Dual build + packaging
  │     └─ 1d: CI workflow
  │
  ├─→ Phase 2: configwizzard packaging (net4sats/configurationwizzard)
  │     ├─ packaging files
  │     └─ CI workflow
  │
  ├─→ Phase 3: Fresh deploy PR124 to both routers
  │
  ├─→ Phase 4: Hardware testing
  │     ├─ 4a: Install portal packages
  │     ├─ 4b: E2E test script
  │     ├─ 4c: Full pytest suite
  │     ├─ 4d: Manual verification
  │     └─ 4e: Upgrade/rollback
  │
  └─→ Phase 5: Final verification + PR cross-references
```

---

## Checklist

### Phase 1: TollGate SPA

- [ ] 1a: Create `src/admin/` directory structure
- [ ] 1a: Implement `src/admin/lib/ubus.js` — JSON-RPC client
- [ ] 1a: Implement `src/admin/lib/schema-form.jsx` — dynamic form from FieldSchema[]
- [ ] 1a: Implement `src/admin/pages/Login.jsx`
- [ ] 1a: Implement `src/admin/pages/Dashboard.jsx`
- [ ] 1a: Implement `src/admin/pages/Settings.jsx` — schema-driven
- [ ] 1a: Implement `src/admin/pages/Wallet.jsx`
- [ ] 1a: Implement `src/admin/pages/Wifi.jsx`
- [ ] 1a: Implement `src/admin/pages/Devices.jsx`
- [ ] 1a: Implement `src/admin/components/Layout.jsx`
- [ ] 1a: Create `admin.html` entry point
- [ ] 1a: Update `vite.config.js` for dual entry
- [ ] 1a: Create `scripts/build-all.mjs`
- [ ] 1a: Test: `npm run build` produces `dist/portal/` and `dist/admin/`
- [ ] 1b: Create `openwrt/rpcd/tollgate` — shell plugin
- [ ] 1b: Create `openwrt/rpcd/tollgate_acl.json`
- [ ] 1c: Create `packaging/Makefile`
- [ ] 1c: Create `packaging/build-ipk.sh`
- [ ] 1c: Create `packaging/normalize-apk-version.sh`
- [ ] 1c: Create `packaging/postinst`
- [ ] 1c: Create `packaging/prerm`
- [ ] 1c: Create `packaging/files/etc/uci-defaults/91-tollgate-portal-setup`
- [ ] 1c: Create `packaging/files/etc/config/uhttpd_tollgate`
- [ ] 1c: Test: build ipk locally and verify contents
- [ ] 1d: Create `.github/workflows/build-package.yml`
- [ ] 1d: Push branch, verify CI passes

### Phase 2: configurationwizzard Packaging

- [ ] 2: Create `packaging/Makefile`
- [ ] 2: Create `packaging/build-ipk.sh`
- [ ] 2: Create `packaging/normalize-apk-version.sh`
- [ ] 2: Create `packaging/postinst`
- [ ] 2: Create `packaging/prerm`
- [ ] 2: Create `packaging/files/etc/uci-defaults/91-configurationwizzard-setup`
- [ ] 2: Create `.github/workflows/build-package.yml`
- [ ] 2: PR description references PR #124
- [ ] 2: Push branch, verify CI passes

### Phase 3: Fresh Deploy

- [ ] 3: Deploy PR124 to Router A (10.47.41.1) with mutex
- [ ] 3: Deploy PR124 to Router B (192.168.244.1) with mutex
- [ ] 3: Verify `tollgate --json config schema` works on both

### Phase 4: Hardware Testing

- [ ] 4a: Install TollGate portal package on Router A
- [ ] 4a: Install configurationwizzard package on Router B
- [ ] 4b: Run `test-configwizzard-e2e.sh` on Router A
- [ ] 4b: Run `test-configwizzard-e2e.sh` on Router B
- [ ] 4c: Run pytest API tests on Router A
- [ ] 4c: Run pytest API tests on Router B
- [ ] 4d: Manual verification on Router A (all items)
- [ ] 4d: Manual verification on Router B (all items)
- [ ] 4e: Rollback test on Router A
- [ ] 4e: Rollback test on Router B

### Phase 5: Final Verification

- [ ] 5: Cross-reference PR #124 in TollGate portal PR
- [ ] 5: Cross-reference PR #124 in configurationwizzard PR
- [ ] 5: Confirm all three blockers resolved
- [ ] 5: Confirm no regressions in tollgate-wrt
