# Progress: feat/set-hostname

## Done

- [x] Create git worktree at `/home/c03rad0r/tollgate-module-basic-go-set-hostname`
- [x] Write PLAN.md
- [x] Implement Go SSL commands in CLI (`src/cmd/tollgate-cli/ssl.go`)
  - `tollgate ssl apply` — self-signed cert via Go crypto/x509 (no openssl/px5g)
  - `tollgate ssl apply <cert> [key]` — real cert with Go PEM parsing
  - `tollgate ssl remove` — revert SSL config from backup
  - `tollgate ssl status` — show cert details and expiry
- [x] Replace shell scripts with thin wrappers (`exec tollgate ssl apply/remove "$@"`)
- [x] Update packaging Makefile to install wrapper scripts
- [x] Fix `hostname` command bug in `99-tollgate-setup` → use `/proc/sys/kernel/hostname`
- [x] **Rewrite ssl.go with all 6 bug fixes**:
  1. `runCommandChecked()` — UCI errors now surface as formatted tollgate errors (was silently swallowed)
  2. `uciSetScalar()` — renamed from `uciSet`, prevents accidental use on UCI list options
  3. `uciGetList()` + `listContains()` — exact element matching, fixes partial match bug (`0.0.0.0:4430` falsely matching `0.0.0.0:443`)
  4. Backup-exists check before re-apply — warns user, requires confirmation (or `--yes`)
  5. `cleanupStaleTempDirs()` — removes leaked temp dirs from killed processes
  6. `--yes` / `-y` flag — non-interactive mode for test automation
- [x] `go build` + `go vet` pass clean (aarch64 static binary, 8.5MB)
- [x] **Add 14 new test targets to `mint-health/Makefile`** (total 21 SSL targets):
  - `r-test-ssl-setup-verify` — verify clean initial state
  - `r-test-ssl-self-signed-yes` — apply with `--yes` flag
  - `r-test-ssl-reapply` — re-apply with existing backup (overwrite warning)
  - `r-test-ssl-remove-no-backup` — error path (no backup)
  - `r-test-ssl-verify-cert` — deep cert validation (CN, SAN, expiry, permissions)
  - `r-test-ssl-verify-nds` — NDS port 443 allow check
  - `r-test-ssl-verify-no-dns` — no dnsmasq domain for self-signed
  - `r-test-ssl-wrappers` — wrapper scripts apply+remove
  - `r-test-ssl-idempotent` — apply twice, verify consistent state
  - `r-test-ssl-comprehensive` — all self-signed tests in sequence (13 phases)
  - `r-test-ssl-real-cert` — real cert via LE staging + Cloudflare DNS-01
  - `r-test-ssl-real-cert-remove` — real cert removal (dnsmasq + NDS revert)
  - `r-test-ssl-real-cert-full` — full real cert lifecycle
  - `r-test-ssl-all` — master orchestrator (self-signed + real cert)
- [x] **Add 15 new top-level targets to `physical-router-test-automation/Makefile`** (total 22 SSL targets)
- [x] Cross-compile CLI for aarch64, deploy to alpha (10.47.41.1)
- [x] Run `test-hostname` on alpha — PASSED
- [x] Run `test-ssl-full` on alpha — PASSED
- [x] **Run `r-test-ssl-comprehensive` on alpha — ALL 13 PHASES PASSED**

## Test Results

### Alpha Router: 2026-05-19 — Comprehensive SSL Test Suite (13 phases)

| Phase | Test | Result |
|-------|------|--------|
| 1 | Clean slate (force remove) | PASSED |
| 2 | Setup verification (clean state) | PASSED — no cert, no backup, uhttpd clean, no port 443 allow |
| 3 | Self-signed apply with `--yes` | PASSED — cert installed, backup created, mode=self-signed |
| 4 | Deep cert verification | PASSED — CN=TollGate.lan, SAN=TollGate.lan+TollGate+10.47.41.1, not expired, key permissions 600 |
| 5 | NDS port 443 verification | PASSED — `allow tcp port 443` present, uhttpd listening on 0.0.0.0:443 |
| 6 | No-DNS verification (self-signed) | PASSED — no dnsmasq .lan domain, status shows self-signed |
| 7 | SSL status command | PASSED — shows mode, domain, cert details, 3649 days remaining |
| 8 | Re-apply with existing backup | PASSED — "backup already exists" warning, cert still installed, uhttpd still configured |
| 9 | SSL remove | PASSED — cert+key removed, backup removed, uhttpd reverted |
| 10 | Remove no-backup error path | PASSED — exit code 1, "no SSL backup found" error message |
| 11 | Wrapper scripts test | PASSED — both `tollgate-apply-ssl --yes` and `tollgate-remove-ssl --yes` work |
| 12 | Idempotent apply (apply twice) | PASSED — different cert (regenerated, expected for self-signed), state consistent |
| 13 | Final cleanup | PASSED |

### Bug #7 Found During Testing
- **`openssl` not available on minimal OpenWrt** — test targets that used `openssl x509` on the router failed
- **Fix**: SCP cert to build machine, validate locally with `openssl` (3 targets fixed: `r-test-ssl-verify-cert`, `r-test-ssl-idempotent`, `r-test-ssl-real-cert`)

### Real Cert Lifecycle Test: 2026-05-19 — ALL 6 PHASES PASSED

| Phase | Test | Result |
|-------|------|--------|
| 1 | Clean slate (force remove) | PASSED |
| 2 | Issue + apply real cert | PASSED — LE staging issued ECDSA cert via Cloudflare DNS-01, applied with dnsmasq+NDS+uhttpd |
| 3 | SSL status check | PASSED — mode=real-cert, issuer=(STAGING) Let's Encrypt, 89 days remaining |
| 4 | Remove real cert | PASSED — dnsmasq entry removed, NDS gatewaydomainname reverted to TollGate.lan:80 |
| 5 | Verify clean state | PASSED — "SSL: not configured" |
| 6 | Cleanup temp files | PASSED |

Key observations:
- Cert: ECDSA P-256 via LE staging, CN=tollgate-test.orangesync.tech
- Issuer: `(STAGING) Baloney Bulgur YE2` (LE staging fake CA)
- dnsmasq domain entry correctly added: `tollgate-test.orangesync.tech → 10.47.41.1`
- nodogsplash gatewaydomainname correctly set to `tollgate-test.orangesync.tech`
- After remove: dnsmasq clean, NDS reverted to `TollGate.lan:80`, backup removed
- acme.sh cached at `/tmp/tollgate-acme-test/` for subsequent runs

### Key `ssl status` Output (from CLI running on router)
```
SSL: configured
  Mode   : self-signed
  Domain : TollGate.lan
  Cert   : /etc/tollgate/ssl/server.crt
  Key    : /etc/tollgate/ssl/server.key
  Subject: CN=TollGate.lan
  Issuer : CN=TollGate.lan
  NotBefore: 2026-05-19 15:05:16
  NotAfter : 2036-05-16 15:05:16
  Days remaining: 3649
  SAN    : TollGate.lan, TollGate
```

## Pending

- [ ] Build proper .ipk package via OpenWrt SDK
- [ ] Production readiness assessment

## Planning Documents

- `IMPLEMENTATION_PLAN.md` — full plan with design decisions, bug details, test target specs, and tracking checklist
