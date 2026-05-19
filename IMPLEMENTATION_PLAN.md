# SSL Hardening, Bug Fixes & Full Test Coverage Plan

**Branch**: `feat/set-hostname`  
**Worktree**: `/home/c03rad0r/tollgate-module-basic-go-set-hostname`  
**Date**: 2026-05-19  
**Status**: Planning complete, awaiting implementation

---

## Context

The `feat/set-hostname` branch introduces:
1. **Hostname setup** in `99-tollgate-setup` — sets router hostname from "OpenWrt" to "TollGate" on first boot
2. **SSL certificate management** — moved from shell scripts (`tollgate-apply-ssl`, `tollgate-remove-ssl`) into the Go CLI (`tollgate ssl apply`/`remove`/`status`)

### What was already tested (2026-05-19)

- Hostname setup: UCI hostname, kernel hostname, uhttpd commonname — **PASSED**
- Self-signed SSL lifecycle (apply → status → remove → clean state) — **PASSED**
- Cross-compile for aarch64, deploy to alpha (10.47.41.1) — **DONE**

### What was NOT tested

- Real cert mode (`tollgate ssl apply <cert> <key>`) — zero hardware coverage
- Combined PEM splitting (`splitCombinedPEM()`)
- Error paths (invalid file, expired cert, missing key)
- Wrapper scripts (`tollgate-apply-ssl`, `tollgate-remove-ssl`)
- Idempotency (apply twice, remove twice)
- `99-tollgate-setup` verification (uhttpd, DNS, NDS, WiFi)
- Regression (existing `smoke-degraded`, `test-captive-portal`)

### Bugs found during audit

Six bugs were identified in `src/cmd/tollgate-cli/ssl.go` through code review and testing.

---

## Design Decisions

### Decision 1: Rename `uciSet` → `uciSetScalar`

**Why**: `uciSet` uses `uci set key=value` which converts a list to a scalar. We never call it on list options today (`listen_https` uses `uci add_list`), but the name doesn't communicate this constraint. A future developer could accidentally call `uciSetScalar("uhttpd.main.listen_https", ...)` and break things. The rename makes the intent explicit at every call site.

**Alternatives considered**: Add a comment on `uciSet`. Rejected — comments are easier to ignore than a function name.

### Decision 2: Real cert test via Let's Encrypt + Cloudflare DNS-01

**Why**: The user wants real cert testing against the production code path. Self-signed certs skip `configureDnsmasq()`, `configureNodogsplash()`, and the `realCert` branch of `sslRemove()` entirely. Only a real cert exercises all code paths.

**Approach**: Use `acme.sh` (pure shell script, no install needed) with the Cloudflare DNS API to obtain a Let's Encrypt cert for `tollgate-test.orangesync.tech`. Use the **Let's Encrypt staging** environment to avoid rate limits. Staging certs are functionally identical for our purposes — we parse SAN/CN, we don't validate the CA chain.

**Alternatives considered**:
- Generate a fake cert locally with `openssl` — would test cert parsing but not the full workflow. Rejected because it skips the actual certificate obtainment workflow we want to validate.
- Use `lego` (Go ACME client) — install timed out (>2min compile). Rejected for practical reasons.
- Use `certbot` — not installed. Rejected.

**Cloudflare credentials**: Located at `/home/c03rad0r/tollgate-infrastructure-kit/.env` (Token + Zone ID for `orangesync.tech` zone). A wildcard `*.orangesync.tech` A record already exists, so `tollgate-test.orangesync.tech` resolves.

**Self-signed testing**: Self-signed mode uses `<hostname>.lan` (e.g. `TollGate.lan`). No external domain or DNS needed.

### Decision 3: `--yes` flag for non-interactive use

**Why**: All `ssl apply` and `ssl remove` operations require interactive confirmation (`askConfirmation()`). Test targets pipe `echo y` which works but is fragile. A `--yes` / `-y` flag is the standard approach for scripted CLI usage (same pattern as `apt -y`, `pacman --noconfirm`, etc.).

### Decision 4: `runCommandChecked()` for error propagation

**Why**: The current `runCommand()` discards all errors from `uci` commands. If `uci set` fails (permissions, invalid section, etc.), the CLI prints success. This is the highest-severity bug because it affects all UCI mutations silently.

**Approach**: Keep `runCommand()` for read-only/best-effort calls. Add `runCommandChecked()` that returns a formatted tollgate error on non-zero exit. All mutation functions (`uciSetScalar`, `uciCommit`, `configureUhttpd`, etc.) return errors from `runCommandChecked`.

### Decision 5: `uciGetList()` for exact list matching

**Why**: UCI returns list values as space-separated quoted strings (e.g. `'0.0.0.0:443' '[::]:443'`). The current code uses `strings.Contains()` which has a partial match bug: `0.0.0.0:4430` matches `0.0.0.0:443`. This could cause `configureUhttpd()` to skip adding the `0.0.0.0:443` listener if a similar-but-different value existed.

**Approach**: Parse the space-separated list into individual elements and check for exact match. Add `uciGetList(key)` and `listContains(list, value)` helpers.

### Decision 6: Use Let's Encrypt staging, not production

**Why**: Production Let's Encrypt has strict rate limits (5 identical certs per week, 50 certs per registered domain per week). The staging environment has much higher limits and is designed for testing. Staging certs use a different root CA but our code doesn't validate the chain — it only parses SAN/CN from the leaf cert.

---

## Bug Fixes

### Bug 1: `runCommand()` silently swallows errors — **HIGH**

**File**: `ssl.go:700`

Every `runCommand("uci", ...)` discards the error. If `uci` fails, the CLI reports success.

**Fix**: Add `runCommandChecked()`. All mutation functions return its error.

### Bug 2: `configureDnsmasq()` / `removeDnsmasqDomain()` broken index parsing — **HIGH**

**File**: `ssl.go:559-569` and `ssl.go:601-611`

`strings.SplitN("dhcp.@domain[0]=domain", ".", 3)` produces `["dhcp", "@domain[0]=domain"]`. The code uses `parts[1]` as the UCI index, but it includes `=domain`. The resulting `uci get dhcp.@domain[0]=domain.name` is invalid — it will never find the domain name, so the existing-entry cleanup silently fails.

**Fix**: After splitting on `.`, split `parts[1]` on `=` to extract just the index: `@domain[0]`.

### Bug 3: `strings.Contains` on UCI lists — partial match — **MEDIUM**

**File**: `ssl.go:546`, `ssl.go:549`, `ssl.go:587`

`strings.Contains("0.0.0.0:4430", "0.0.0.0:443")` is true. If someone had `0.0.0.0:4430` in `listen_https`, the code would skip adding `0.0.0.0:443`. Same issue in `allowPort443()` with `users_to_router`.

**Fix**: `uciGetList()` + `listContains()` with exact element matching.

### Bug 4: Re-apply without remove overwrites backup — **MEDIUM**

**File**: `ssl.go:155` (called in both `sslApplySelfSigned` and `sslApplyRealCert`)

Running `tollgate ssl apply` twice creates a backup the first time (capturing the pre-SSL state). The second run overwrites that backup with the already-modified state. The original pre-SSL config is lost — `tollgate ssl remove` can no longer fully revert.

**Fix**: Check for existing backup at start of `sslApply()`. If found, warn and require confirmation to overwrite.

### Bug 5: Temp files leak on process kill — **LOW**

**File**: `ssl.go:117`, `ssl.go:479`

`os.MkdirTemp("", "tollgate-ssl-*")` creates temp dirs. If the process is killed (OOM, signal), they're never cleaned. Over time, these accumulate in `/tmp/`.

**Fix**: At the start of `sslApply()`, glob and remove any stale `tollgate-ssl-*` temp dirs from previous interrupted runs.

### Bug 6: No `--yes` flag — **MEDIUM**

All `ssl apply` and `ssl remove` operations require interactive confirmation. Tests pipe `echo y` which is fragile.

**Fix**: Add `--yes` / `-y` flag to both commands. Add `confirmOrYes()` helper.

---

## Test Targets

All targets use the hardware mutex:
- In `mint-health/Makefile`: `$(CHECK_LOCK)` at the start
- In top-level `Makefile`: `$(call require_hardware_lock)`
- This prevents collisions with other LLM sessions that might access the physical hardware

### `99-tollgate-setup` verification

These verify the state left behind by the first-boot setup script (which already ran on alpha).

| Target | What it verifies |
|--------|-----------------|
| `r-test-setup-uhttpd` | Listen on 8080, conditional HTTPS (only if cert exists), no crash-loop |
| `r-test-setup-dns` | DNS forwarders (1.1.1.1), domain=lan, dnsmasq config |
| `r-test-setup-nodogsplash` | NDS enabled, gatewayport=80, idempotent users_to_router rules |
| `r-test-setup-wifi` | Public SSIDs match `TollGate-XXXX` pattern, radios enabled |
| `r-test-setup-full` | All of the above in sequence |

### SSL self-signed (enhance existing)

| Target | What it tests | New? |
|--------|--------------|------|
| `r-test-ssl-self-signed` | Apply, verify files+UCI+listener | Existing |
| `r-test-ssl-self-signed-https` | `curl -k https://<ip>` and verify cert CN matches | **NEW** |
| `r-test-ssl-reapply` | Apply twice without remove, verify backup warning | **NEW** |
| `r-test-ssl-apply-no` | Pipe 'n', verify abort | **NEW** |

### SSL real cert via Let's Encrypt + Cloudflare DNS-01

| Target | What it tests | New? |
|--------|--------------|------|
| `r-test-ssl-real-cert` | Obtain real cert via acme.sh + Cloudflare DNS-01, apply with 2 args, verify dnsmasq+NDS+uhttpd, remove, verify cleanup | **NEW** |
| `r-test-ssl-real-cert-combined` | Same cert, create combined PEM, apply with 1 arg | **NEW** |
| `r-test-ssl-real-cert-expired` | Generate expired cert locally, apply, verify warning | **NEW** |
| `r-test-ssl-real-cert-invalid` | Apply non-PEM file, verify error | **NEW** |

**Real cert test flow**:
1. Download `acme.sh` to `/tmp/acme.sh/` (pure shell script, no install)
2. Load Cloudflare token from `/home/c03rad0r/tollgate-infrastructure-kit/.env`
3. Issue cert via `acme.sh --issue --dns dns_cf -d tollgate-test.orangesync.tech --staging`
4. SCP cert+key to router
5. `tollgate ssl apply --yes /tmp/cert.pem /tmp/key.pem`
6. Verify: dnsmasq resolves `tollgate-test.orangesync.tech` → LAN IP, NDS gatewaydomainname updated, uhttpd on 443
7. `tollgate ssl remove --yes`
8. Verify cleanup: no dnsmasq domain entry, NDS restored, no port 443 listener
9. Cleanup: `acme.sh --remove`, delete DNS TXT record, delete local files

### SSL error paths

| Target | What it tests | New? |
|--------|--------------|------|
| `r-test-ssl-error-nokey` | Apply cert without key file, verify error | **NEW** |
| `r-test-ssl-error-nofile` | Apply nonexistent file, verify error | **NEW** |
| `r-test-ssl-error-remove-nobackup` | Remove with no backup, verify error | **NEW** |

### Wrapper scripts

| Target | What it tests | New? |
|--------|--------------|------|
| `r-test-ssl-wrapper-apply` | Call `/usr/bin/tollgate-apply-ssl` with `--yes`, verify it invokes CLI | **NEW** |
| `r-test-ssl-wrapper-remove` | Call `/usr/bin/tollgate-remove-ssl` with `--yes`, verify it invokes CLI | **NEW** |

### Idempotency

| Target | What it tests | New? |
|--------|--------------|------|
| `r-test-ssl-idempotent-apply` | Apply twice, verify second detects backup | **NEW** |
| `r-test-ssl-idempotent-remove` | Remove twice, verify second is clean | **NEW** |
| `r-test-ssl-idempotent-allow443` | Apply, check port 443 rule appears exactly once | **NEW** |

### Comprehensive suite

| Target | What it runs | New? |
|--------|-------------|------|
| `r-test-ssl-all` | Self-signed lifecycle → real cert lifecycle → error paths → wrappers → idempotency → cleanup | **NEW** |

### Top-level targets

All mirror the `mint-health` targets with `$(call require_hardware_lock)`:
- `test-setup-full ROUTER=alpha`
- `test-ssl-all ROUTER=alpha`
- `test-ssl-real-cert ROUTER=alpha`
- `test-ssl-real-cert-combined ROUTER=alpha`
- `test-ssl-self-signed ROUTER=alpha`
- `test-ssl-error-paths ROUTER=alpha`
- `test-ssl-wrappers ROUTER=alpha`
- `test-ssl-idempotency ROUTER=alpha`

---

## Implementation Order

1. Fix `ssl.go` bugs + add `--yes` flag + rename `uciSet` → `uciSetScalar`
2. Add test make targets to `mint-health/Makefile`
3. Add top-level targets to `Makefile`
4. Cross-compile, deploy to alpha
5. Run `test-ssl-all` on alpha
6. Update `PROGRESS.md`

---

## Files Changed

| File | Location | Changes |
|------|----------|---------|
| `src/cmd/tollgate-cli/ssl.go` | tollgate-module-basic-go | 6 bug fixes, `--yes` flag, `uciSetScalar` rename, `uciGetList`, `runCommandChecked`, `confirmOrYes` |
| `mint-health/Makefile` | physical-router-test-automation | ~20 new test targets with `$(CHECK_LOCK)` |
| `Makefile` | physical-router-test-automation | ~15 new top-level targets with `require_hardware_lock` |

---

## Checklist

### Phase 1: Bug fixes in `ssl.go`

- [ ] Add `runCommandChecked()` — returns formatted tollgate errors on non-zero exit
- [ ] Rename `uciSet` → `uciSetScalar` — prevent accidental use on list options
- [ ] Fix `configureDnsmasq()` index parsing — strip `=domain` suffix from UCI show output
- [ ] Fix `removeDnsmasqDomain()` index parsing — same bug, same fix
- [ ] Add `uciGetList()` and `listContains()` — exact list element matching
- [ ] Fix `configureUhttpd()` — use `listContains()` instead of `strings.Contains`
- [ ] Fix `allowPort443()` — use `listContains()` instead of `strings.Contains`
- [ ] Add backup-exists check in `sslApply()` — warn + confirm before overwrite
- [ ] Add stale temp dir cleanup at start of `sslApply()`
- [ ] Add `--yes` / `-y` flag to `ssl apply` command
- [ ] Add `--yes` / `-y` flag to `ssl remove` command
- [ ] Add `confirmOrYes()` helper — checks flag before prompting
- [ ] Make all mutation functions return errors (not just `nil`)
- [ ] Verify `go build` and `go vet` pass

### Phase 2: Test targets in `mint-health/Makefile`

- [ ] `r-test-setup-uhttpd` — with `$(CHECK_LOCK)`
- [ ] `r-test-setup-dns` — with `$(CHECK_LOCK)`
- [ ] `r-test-setup-nodogsplash` — with `$(CHECK_LOCK)`
- [ ] `r-test-setup-wifi` — with `$(CHECK_LOCK)`
- [ ] `r-test-setup-full` — with `$(CHECK_LOCK)`
- [ ] `r-test-ssl-self-signed-https` — curl HTTPS and check cert CN
- [ ] `r-test-ssl-reapply` — apply twice, verify backup warning
- [ ] `r-test-ssl-apply-no` — pipe 'n', verify abort
- [ ] `r-test-ssl-real-cert` — Let's Encrypt staging + Cloudflare DNS-01
- [ ] `r-test-ssl-real-cert-combined` — combined PEM, 1 arg
- [ ] `r-test-ssl-real-cert-expired` — expired cert, verify warning
- [ ] `r-test-ssl-real-cert-invalid` — non-PEM file, verify error
- [ ] `r-test-ssl-error-nokey` — cert without key
- [ ] `r-test-ssl-error-nofile` — nonexistent file
- [ ] `r-test-ssl-error-remove-nobackup` — remove with no backup
- [ ] `r-test-ssl-wrapper-apply` — call `/usr/bin/tollgate-apply-ssl`
- [ ] `r-test-ssl-wrapper-remove` — call `/usr/bin/tollgate-remove-ssl`
- [ ] `r-test-ssl-idempotent-apply` — apply twice
- [ ] `r-test-ssl-idempotent-remove` — remove twice
- [ ] `r-test-ssl-idempotent-allow443` — port 443 exactly once
- [ ] `r-test-ssl-all` — comprehensive suite

### Phase 3: Top-level targets in `Makefile`

- [ ] Add `.PHONY` entries for all new targets
- [ ] Add help text for SSL test section
- [ ] `test-setup-full` — with `require_hardware_lock`
- [ ] `test-ssl-all` — with `require_hardware_lock`
- [ ] `test-ssl-real-cert` — with `require_hardware_lock`
- [ ] `test-ssl-real-cert-combined` — with `require_hardware_lock`
- [ ] `test-ssl-self-signed` — with `require_hardware_lock`
- [ ] `test-ssl-error-paths` — with `require_hardware_lock`
- [ ] `test-ssl-wrappers` — with `require_hardware_lock`
- [ ] `test-ssl-idempotency` — with `require_hardware_lock`

### Phase 4: Deploy and test

- [ ] Cross-compile CLI for aarch64
- [ ] Deploy to alpha (10.47.41.1)
- [ ] Acquire hardware lock
- [ ] Run `r-test-setup-full`
- [ ] Run `r-test-ssl-all`
- [ ] Release hardware lock
- [ ] Update PROGRESS.md with results

### Phase 5: Documentation

- [ ] Update PROGRESS.md with test results
- [ ] Update IMPLEMENTATION_PLAN.md checklist
