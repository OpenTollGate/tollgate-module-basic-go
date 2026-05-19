# PR: SSL Management Rewrite in Go + Hostname Setup

## Summary

Rewrites the SSL certificate management subsystem in Go, replacing the previous shell-based approach with a native Go implementation using `crypto/x509`. Also adds hostname configuration to first-boot setup and fixes 7 bugs found during integration testing on physical hardware (GL.iNet MT3000, aarch64, OpenWrt 24.10.4).

## Files Changed

| File | Change | Lines |
|------|--------|-------|
| `src/cmd/tollgate-cli/ssl.go` | **New** — Go SSL management (850 lines, 36 functions) | +850 |
| `packaging/files/etc/uci-defaults/99-tollgate-setup` | Hostname setup, uhttpd crash fix, idempotent NDS rules | +46/-5 |
| `packaging/Makefile` | Install SSL wrapper scripts alongside `check_package_path` | +4/-1 |
| `packaging/files/usr/bin/tollgate-apply-ssl` | **New** — thin wrapper: `exec tollgate ssl apply "$@"` | +2 |
| `packaging/files/usr/bin/tollgate-remove-ssl` | **New** — thin wrapper: `exec tollgate ssl remove "$@"` | +2 |

**Total: 6 files, +904/-6** (excluding deleted working docs)

## What's New

### `tollgate ssl apply` (self-signed)
Generates a self-signed TLS certificate using Go's `crypto/x509` — no `openssl` or `px5g` dependency. The certificate includes:
- CN = `<hostname>.lan`
- SAN = `<hostname>.lan`, `<hostname>`, `<LAN IP>`
- 10-year validity
- 2048-bit RSA key

Configures uhttpd for HTTPS on port 443, adds NDS firewall allow rule, creates backup of original config.

### `tollgate ssl apply <cert> [key]` (real cert)
Accepts a real certificate (PEM, separate or combined cert+key). For real certs on public domains:
- Adds dnsmasq DNS entry (domain → LAN IP)
- Sets nodogsplash `gatewaydomainname` to the public domain
- Configures uhttpd for HTTPS with the real cert

### `tollgate ssl remove`
Reverts all SSL configuration from backup:
- Removes cert/key files and backup
- Restores original uhttpd config (HTTP only)
- Removes NDS port 443 allow rule
- For real certs: removes dnsmasq entry, reverts NDS gatewaydomainname

### `tollgate ssl status`
Shows current SSL state: mode (self-signed/real-cert), domain, cert details, expiry, SAN entries.

### `--yes` / `-y` flag
Non-interactive mode for all SSL commands. Required for test automation.

### Hostname setup (`99-tollgate-setup`)
New `setup_hostname()` function:
- Changes hostname from `OpenWrt` default to `TollGate`
- Preserves custom hostnames
- Sets `uhttpd.main.commonname` to match
- Applies to running kernel immediately via `/proc/sys/kernel/hostname`

## Bug Fixes

| # | Bug | Fix |
|---|-----|-----|
| 1 | UCI errors silently swallowed | `runCommandChecked()` returns formatted tollgate errors |
| 2 | `uciSet` could corrupt UCI list options | Renamed to `uciSetScalar()` — prevents accidental use on lists |
| 3 | Partial match in UCI list lookup (`0.0.0.0:4430` matched `0.0.0.0:443`) | `uciGetList()` + `listContains()` for exact element matching |
| 4 | Re-apply overwrote backup without warning | Backup-exists check with user confirmation (or `--yes`) |
| 5 | Killed processes leaked temp dirs | `cleanupStaleTempDirs()` on every apply |
| 6 | NDS firewall rules duplicated on re-provision | Idempotent grep-before-add in `99-tollgate-setup` |
| 7 | `openssl` not available on minimal OpenWrt | Go `crypto/x509` replaces all external crypto dependencies |

## Key Design Decisions

- **Go `crypto/x509`** — no `openssl`, `px5g`, or any external crypto dependency
- **SSL commands are direct CLI operations** — not daemon commands via Unix socket
- **Backup-based restore** — `ssl apply` backs up original config, `ssl remove` restores from backup
- **Self-signed only for `.lan`** — real certs for public domains, no dnsmasq entries for self-signed
- **Cert validation via SCP** — router lacks `openssl`; test targets SCP cert to build machine for local validation

## Testing

All tests run on **alpha router** (GL.iNet MT3000, aarch64, OpenWrt 24.10.4, IP 10.47.41.1).

### Test Targets

Tests live in the `physical-router-test-automation` repo. All targets require a hardware mutex lock (`make lock PHASE='ssl testing'`).

```bash
# Setup
make lock PHASE='ssl testing'

# Deploy CLI to router
make deploy-cli ROUTER=alpha

# Quick tests (~2 min each)
make test-hostname ROUTER=alpha              # verify hostname is set
make test-ssl-self-signed ROUTER=alpha       # apply self-signed cert
make test-ssl-remove ROUTER=alpha            # remove SSL config
make test-ssl-full ROUTER=alpha              # full lifecycle: apply → verify → remove

# Individual verification tests
make test-ssl-setup-verify ROUTER=alpha      # verify clean initial state
make test-ssl-verify-cert ROUTER=alpha       # deep cert validation (CN, SAN, expiry)
make test-ssl-verify-nds ROUTER=alpha        # NDS port 443 allow rule
make test-ssl-verify-no-dns ROUTER=alpha     # no dnsmasq domain for self-signed
make test-ssl-status ROUTER=alpha            # tollgate ssl status command
make test-ssl-self-signed-yes ROUTER=alpha   # --yes flag (non-interactive)
make test-ssl-reapply ROUTER=alpha           # re-apply with existing backup
make test-ssl-remove-no-backup ROUTER=alpha  # error path (no backup)
make test-ssl-wrappers ROUTER=alpha          # tollgate-apply-ssl / tollgate-remove-ssl
make test-ssl-idempotent ROUTER=alpha        # apply twice, verify consistent state

# Comprehensive suites
make test-ssl-comprehensive ROUTER=alpha     # all self-signed tests (~10 min)
make test-ssl-real-cert ROUTER=alpha         # LE staging + Cloudflare DNS-01
make test-ssl-real-cert-remove ROUTER=alpha  # real cert removal
make test-ssl-real-cert-full ROUTER=alpha    # full real cert lifecycle (~5 min)
make test-ssl-all ROUTER=alpha               # everything (~20 min)

# Cleanup
make ssl-status ROUTER=alpha                 # read-only status check (no lock)
make ssl-remove-force ROUTER=alpha           # force-remove SSL config
make unlock                                  # release hardware lock
```

### Test Results: Comprehensive Self-Signed (13 phases)

**Date**: 2026-05-19 | **Router**: alpha (10.47.41.1) | **CLI commit**: `5b2f671`

| Phase | Test | Result |
|-------|------|--------|
| 1 | Clean slate (force remove) | PASSED |
| 2 | Setup verification (clean state) | PASSED — no cert, no backup, uhttpd clean, no port 443 allow |
| 3 | Self-signed apply with `--yes` | PASSED — cert installed, backup created, mode=self-signed |
| 4 | Deep cert verification | PASSED — CN=TollGate.lan, SAN=TollGate.lan+TollGate+10.47.41.1, not expired, key permissions 600 |
| 5 | NDS port 443 verification | PASSED — `allow tcp port 443` present, uhttpd listening on 0.0.0.0:443 |
| 6 | No-DNS verification (self-signed) | PASSED — no dnsmasq .lan domain, status shows self-signed |
| 7 | SSL status command | PASSED — shows mode, domain, cert details, 3649 days remaining |
| 8 | Re-apply with existing backup | PASSED — "backup already exists" warning, cert still installed |
| 9 | SSL remove | PASSED — cert+key removed, backup removed, uhttpd reverted |
| 10 | Remove no-backup error path | PASSED — exit code 1, "no SSL backup found" error message |
| 11 | Wrapper scripts test | PASSED — both `tollgate-apply-ssl --yes` and `tollgate-remove-ssl --yes` work |
| 12 | Idempotent apply (apply twice) | PASSED — different cert (regenerated), state consistent |
| 13 | Final cleanup | PASSED |

### Test Results: Real Cert Lifecycle (6 phases)

**Date**: 2026-05-19 | **Router**: alpha | **Domain**: `tollgate-test.orangesync.tech`

| Phase | Test | Result |
|-------|------|--------|
| 1 | Clean slate (force remove) | PASSED |
| 2 | Issue + apply real cert | PASSED — LE staging ECDSA P-256 via Cloudflare DNS-01 |
| 3 | SSL status check | PASSED — mode=real-cert, issuer=(STAGING) Let's Encrypt, 89 days |
| 4 | Remove real cert | PASSED — dnsmasq entry removed, NDS reverted to TollGate.lan:80 |
| 5 | Verify clean state | PASSED — "SSL: not configured" |
| 6 | Cleanup temp files | PASSED |

## Pre-Merge Checklist

- [x] `go build` passes (aarch64 static binary)
- [x] `go vet` passes clean
- [x] All 13 self-signed SSL phases passed on alpha
- [x] All 6 real cert phases passed on alpha
- [x] No secrets in diff
- [x] `check_package_path` install preserved in `packaging/Makefile`
- [x] Wrapper scripts installed by `.ipk` packaging
- [ ] CI green (triggers after push to GitHub)
- [ ] Built `.ipk` tested on hardware
