# Configuration UI Integration Plan

**Date:** 2026-05-19
**Status:** Part A complete (Steps 1–6 done, ready for hardware testing before Step 7 merge)
**Related:** `docs/configurationwizzard-integration.md` (full architecture analysis)

---

## Worktree Layout

Using git worktrees so `main` stays clean for other agents to work on.

```
/home/c03rad0r/tollgate-module-basic-go/              ← main (clean, untouched)
/home/c03rad0r/tollgate-worktrees/step1-schema/        ← feat/config-schema-dotpath-rebased
/home/c03rad0r/tollgate-worktrees/step2-cli/           ← feat/cli-json-config-rebased
/home/c03rad0r/tollgate-worktrees/develop/             ← develop (integration branch)
~/physical-router-test-automation/                     ← test automation (feature/router-to-router-interaction branch)
```

| Branch name | Worktree path | Base | Purpose |
|-------------|---------------|------|---------|
| `main` | `/home/c03rad0r/tollgate-module-basic-go/` | — | Production, untouched until Step 7 |
| `feat/config-schema-dotpath-rebased` | `/home/c03rad0r/tollgate-worktrees/step1-schema/` | `main` | Schema + dotpath + bug fixes |
| `feat/cli-json-config-rebased` | `/home/c03rad0r/tollgate-worktrees/step2-cli/` | `feat/config-schema-dotpath-rebased` | CLI --json + config commands |
| `develop` | `/home/c03rad0r/tollgate-worktrees/develop/` | `main` | Integration + testing gate |
| `feature/router-to-router-interaction` | `~/physical-router-test-automation/` | — | Test automation with hardware mutex |

---

## What We're Building

A production-ready admin UI and captive portal for TollGate routers, split across two repositories:

1. **tollgate-module-basic-go** — Go backend foundation: config schema system, CLI `--json` config commands
2. **configurationwizzard** — Preact SPA frontend: admin dashboard, captive portal, rpcd plugin

---

## Why This Approach

### Why rebase/cherry-pick instead of merging the PR branches directly?

The three open PR branches (`feat/config-schema-dotpath`, `feat/cli-json-config`, `feat/luci-admin-ui`) all fork from `283b58f`, but main has 2 commits since then (`#109 Upstream WiFi Manager` + `x86_64 build target`). A direct merge would bring in a massive WGM refactor that conflicts with the WGM work already on main. Cherry-picking lets us take only the config/CLI-specific files and skip the WGM changes entirely.

### Why fix the thread safety bug before merging?

`SetDotPath` in PR #112 calls `GetConfig()` (which acquires `RLock`) and then mutates the returned pointer without any write lock. Multiple concurrent config writes could corrupt `config.json`. Since the admin UI will trigger config writes via the CLI, this will be hit in production. Fixing it before merge means `go test -race` catches regressions going forward.

### Why add schema constraint validation now?

`SetDotPath` accepts any string value without checking the schema's `enum`, `min`, `max` constraints. Without this, the UI can persist `"log_level": "INVALID"` or `"margin": 999.0` to config.json and crash the Go service on restart. Validating against the schema at the setter level means every transport (CLI, future HTTP API, LuCI, configurationwizzard) gets validation for free.

### Why put the rpcd plugin in configurationwizzard, not tollgate?

The rpcd plugin is the SPA's backend adapter — it defines the ubus API surface the SPA depends on. It evolves with the SPA, not with the Go binary. The tollgate repo provides a stable CLI contract (`--json` output shape, tested by contract tests). The configurationwizzard repo provides the adapter that translates SPA calls into CLI calls. This separation means:
- Tollgate releases don't need to coordinate with configurationwizzard releases
- The SPA can add new ubus methods without touching the Go codebase
- The CLI contract tests guarantee the adapter won't break

### Why keep LuCI as interim?

PR #114's LuCI `settings.js` (1197 lines) is a working admin UI that uses `fs.exec_direct('/usr/bin/tollgate', [..., '--json'])`. It calls the exact same CLI we're building. Once the Go backend ships on main, that LuCI UI works immediately. Configurationwizzard replaces it later with a richer SPA, but we don't block on the SPA being ready.

### Why merge to `develop` first?

The `develop` branch lets us run CI + hardware tests before touching `main`. If the cherry-picks introduce regressions, we catch them on `develop` without destabilizing the production branch. This is especially important because the PR branches haven't been tested against the current main (they're 2 commits behind).

### Why thorough testing at the gate (Step 6)?

We're merging three PRs worth of changes (config schema, dot-path setter, CLI config commands, contract tests, CI workflow) into a payment gateway. A bad config write could crash the service and cut off paying users. A thread safety bug could corrupt config.json. A schema drift could make the admin UI show wrong fields. The multi-layer testing (unit + race + drift + contract + hardware + Playwright) catches each class of problem.

### Why hardware mutex for testing?

The `physical-router-test-automation` repo is shared between multiple LLM sessions. The hardware lock (`make lock PHASE="description"`) prevents two sessions from deploying to the same router simultaneously, which would cause flaky tests and binary conflicts. All `deploy-develop` and `test-develop-smoke` targets require the lock to be held.

### Why compile from the develop worktree, not from the test-automation sibling?

The existing `make deploy` compiles from `~/tollgate-module-basic-go/src` (the main branch). Our changes are on the `develop` branch at `~/tollgate-worktrees/develop/src`. The new `deploy-develop` target compiles from the correct worktree so we test exactly what's on develop, not what's on main.

---

## Progress Checklist

### Part A: tollgate-module-basic-go (`github.com/OpenTollGate/tollgate-module-basic-go`)

#### Step 1: Rebase PR #112 — Schema + Dotpath + Bug Fixes

- [ ] Create `feat/config-schema-dotpath-rebased` branch from `main`
- [ ] Cherry-pick `951215e` (schema + dotpath commit)
- [ ] Resolve conflict in `src/config_manager/config_manager_config.go` (keep both sides)
- [ ] Resolve conflict in `src/config_manager/config_manager_test.go` (keep both sides)
- [ ] Skip WGM file changes (keep main's versions of all `wireless_gateway_manager/*`)
- [ ] **Fix thread safety bug** in `SetDotPath`: change from `GetConfig()` (RLock) to `cm.mu.Lock()` + direct field access
- [ ] **Add schema constraint validation**: enum, min/max, type checking in `SetDotPath`
- [ ] Run `go test ./src/config_manager/... -v -count=1 -race` — all pass
- [ ] Verify new files present: `config_schema.go`, `config_dotpath.go`, `config_schema_test.go`, `config_schema_drift_test.go`, `config_drift_test.go`

#### Step 2: Rebase PR #113 — CLI --json + Config Commands

- [ ] Create `feat/cli-json-config-rebased` from `feat/config-schema-dotpath-rebased`
- [ ] Cherry-pick `f0ab1be` (CLI --json + config handlers)
- [ ] Resolve conflict in `src/cli/server.go` (keep main's handlers + add config dispatch)
- [ ] Resolve conflict in `src/cmd/tollgate-cli/main.go` (keep main's commands + add config subcommand)
- [ ] Resolve conflict in `src/main.go` (keep main's HTTP setup + add config registration)
- [ ] Verify new files present: `src/cli/config.go`, `src/cmd/tollgate-cli/contract_test.go`
- [ ] Run `go test ./src/config_manager/... -v -count=1 -race` — all pass
- [ ] Run `go test ./src/cmd/tollgate-cli/... -v -count=1 -race` — all pass

#### Step 3: Extract CI Test Workflow from PR #114

- [ ] Create `.github/workflows/test.yml` — Go test matrix + contract lint
- [ ] Create `tests/contract/js-schema-lint.mjs` — validates schema consistency
- [ ] Do NOT add LuCI JS files, LuCI ACL, Playwright tests, Python test removals
- [ ] Run `node tests/contract/js-schema-lint.mjs` — passes

#### Step 4: Final Bug Fixes

- [ ] Add array bounds checking in `SetDotPath` for `accepted_mints.N.*`, `profit_share.N.*`
- [ ] Improve error messages in `SetDotPath` (include key path + value)
- [ ] Run all Go tests with `-race` — all pass
- [ ] Run contract lint — passes

#### Step 5: Push to `develop`

- [ ] Create `develop` branch from `main`
- [ ] Merge `feat/cli-json-config-rebased` into `develop`
- [ ] Push `develop` to `origin`
- [ ] Verify CI workflow runs and passes on `develop`

#### Step 6: Thorough Testing (GATE — do not skip any item)

**6a. Automated unit + race tests:**
- [ ] `go test ./src/config_manager/... -v -count=1 -race` — all pass
- [ ] `go test ./src/cmd/tollgate-cli/... -v -count=1 -race` — all pass
- [ ] `go test ./src/cli/... -v -count=1 -race` — all pass
- [ ] `node tests/contract/js-schema-lint.mjs` — passes

**6b. Schema drift tests pass:**
- [ ] `TestConfigSchemaDrift` — every JSON tag in Config struct has schema entry
- [ ] `TestIdentitiesSchemaDrift` — every JSON tag in IdentitiesConfig struct has schema entry
- [ ] `TestConfigJSONFields` — marshaled config has all fields UI expects

**6c. CLI contract tests pass:**
- [ ] `TestCLIResponseJSONShape` — success response has correct fields
- [ ] `TestCLIResponseErrorShape` — error response has `error` field
- [ ] Config subcommand shape tests (`config schema`, `config get`, `config set`, `config save`)

**6d. Hardware smoke test (manual):**
- [ ] Build IPK from `develop` branch
- [ ] Install on test router
- [ ] `tollgate --json config schema` → valid JSON array of FieldSchema
- [ ] `tollgate --json config get` → full config JSON
- [ ] `tollgate --json config set log_level debug` → success
- [ ] `tollgate --json config save` → success
- [ ] Restart service, verify `log_level` persisted
- [ ] `tollgate --json config set log_level INVALID` → error (enum validation)
- [ ] `tollgate --json config set margin 5.0` → error (max 1.0)
- [ ] Concurrent config operations complete without race

**6e. Existing Playwright tests pass on hardware:**
- [ ] `tests/tollgate.spec.mjs` — LuCI admin UI tests (wallet, config, network)
- [ ] `tests/captive-portal.spec.mjs` — payment flow tests (Cashu, Lightning)

**6f. No regressions:**
- [ ] `:2121` payment API still works (Cashu + Lightning)
- [ ] `tollgate --json wallet balance/info/fund/drain` still works
- [ ] `tollgate --json status/health/version` still works

#### Step 7: Merge to Main

- [ ] All Step 6 items checked off
- [ ] `git checkout main && git merge develop`
- [ ] Push to `origin/main`
- [ ] Verify CI passes on `main`

---

### Part B: configurationwizzard (`github.com/net4sats/configurationwizzard`)

**Blocked until Part A is complete (tollgate `main` has schema + CLI config commands).**

#### Step 8: Set Up Repo

- [ ] Clone `git@github.com:net4sats/configurationwizzard.git`
- [ ] Create `develop` branch from `main`
- [ ] Merge `captive-portal` branch into `develop` (1 commit, should be clean)

#### Step 9: Wire Captive Portal to Real Payment API (`:2121`)

- [ ] Replace mock pricing → `fetch('http://<gateway>:2121/')` → parse Nostr kind 10021
- [ ] Replace mock MAC → `fetch('http://<gateway>:2121/whoami')`
- [ ] Replace mock Lightning invoice → `POST /ln-invoice` + `GET /ln-invoice?quote=` polling
- [ ] Replace mock Cashu payment → `POST /` with token body
- [ ] Add QR code library for Lightning invoice display
- [ ] Replace hardcoded `https://net4sats.cash/assets/...` with local relative paths
- [ ] Test Cashu payment flow end-to-end on hardware
- [ ] Test Lightning payment flow end-to-end on hardware

#### Step 10: Write the rpcd Plugin

- [ ] Create `openwrt/rpcd/tollgate` — shell script calling `tollgate ... --json`
- [ ] Create `openwrt/rpcd/tollgate_acl.json` — ACL policy (read/write split)
- [ ] Test on router: `ubus call tollgate config_schema` returns schema
- [ ] Test on router: `ubus call tollgate config_set '{"key":"log_level","value":"debug"}'` works
- [ ] Test on router: unauthenticated access blocked by ACL

#### Step 11: Schema-Driven Settings Page

- [ ] Rewrite `src/routes/settings.tsx` to fetch schema via `ubusCall('tollgate', 'config_schema')`
- [ ] Render forms dynamically from `FieldSchema[]` (dropdowns, toggles, inputs, cards)
- [ ] Save changes via `config_set` + `config_save` ubus calls
- [ ] Test: all schema fields render correctly
- [ ] Test: config save round-trip works

#### Step 12: Implement Wallet Page

- [ ] Balance display: `ubusCall('tollgate', 'wallet_balance')` + per-mint breakdown
- [ ] Fund form: Cashu token input → `ubusCall('tollgate', 'wallet_fund', {token})`
- [ ] Drain form: `ubusCall('tollgate', 'wallet_drain', {method: 'cashu'})`

#### Step 13: Dual Vite Build + Packaging

- [ ] Configure `vite.config.ts` for dual output (admin SPA + captive portal)
- [ ] Admin SPA → `dist/admin/` (uhttpd :80)
- [ ] Captive portal → `dist/portal/` (NoDogSplash :2050)
- [ ] Update `openwrt/` packaging to include built assets + rpcd plugin + ACL

#### Step 14: End-to-End Testing

- [ ] Install both IPKs (tollgate-wrt + configurationwizzard) on test router
- [ ] Admin login works (ubus session auth)
- [ ] Settings page loads, config save round-trip
- [ ] Wallet fund/drain lifecycle
- [ ] Captive portal: Cashu payment → access granted
- [ ] Captive portal: Lightning payment → access granted
- [ ] Degraded mode: error message shown

---

## Dependency Graph

```
Part A (tollgate-module-basic-go):
═══════════════════════════════════

Step 1 (schema + dotpath + fixes)
  └─→ Step 2 (CLI --json + config)
        └─→ Step 3 (CI workflow)
              └─→ Step 4 (final fixes)
                    └─→ Step 5 (push develop)
                          └─→ Step 6 (testing gate)
                                └─→ Step 7 (merge main)


Part B (configurationwizzard) — starts after Step 7:
════════════════════════════════════════════════════════

Step 8 (set up repo)
  ├─→ Step 9  (captive portal wiring)     ──┐
  ├─→ Step 10 (rpcd plugin)                ├──  parallelizable
  └─→ Step 11 (schema-driven settings)    ──┘
        └─→ Step 12 (wallet page)
              └─→ Step 13 (build + packaging)
                    └─→ Step 14 (E2E testing)
```

## Files Involved

### tollgate-module-basic-go — files being added/modified

| File | Action | Source |
|------|--------|--------|
| `src/config_manager/config_schema.go` | NEW | PR #112 |
| `src/config_manager/config_dotpath.go` | NEW (with bug fixes) | PR #112 + our fixes |
| `src/config_manager/config_schema_test.go` | NEW | PR #112 |
| `src/config_manager/config_schema_drift_test.go` | NEW | PR #112 |
| `src/config_manager/config_drift_test.go` | NEW | PR #112 |
| `src/config_manager/config_manager_config.go` | MODIFIED | Merge resolution |
| `src/config_manager/config_manager_test.go` | MODIFIED | Merge resolution |
| `src/cli/config.go` | NEW | PR #113 |
| `src/cli/server.go` | MODIFIED | Add config command dispatch |
| `src/cmd/tollgate-cli/main.go` | MODIFIED | Add config subcommand + `--json` |
| `src/cmd/tollgate-cli/contract_test.go` | NEW | PR #113 |
| `src/main.go` | MODIFIED | Add config command registration |
| `.github/workflows/test.yml` | NEW | PR #114 (extracted) |
| `tests/contract/js-schema-lint.mjs` | NEW | PR #114 (adapted) |

### configurationwizzard — files being added/modified

| File | Action | Step |
|------|--------|------|
| `src/routes/captive-portal.tsx` | REWRITE | Step 9 |
| `src/routes/settings.tsx` | REWRITE | Step 11 |
| `src/routes/wallet.tsx` | REWRITE | Step 12 |
| `openwrt/rpcd/tollgate` | NEW (replaces existing) | Step 10 |
| `openwrt/rpcd/tollgate_acl.json` | NEW (replaces existing) | Step 10 |
| `vite.config.ts` | MODIFY | Step 13 |
