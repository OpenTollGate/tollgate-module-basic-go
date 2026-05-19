# Plan: feat/set-hostname — SSL Management via Go CLI

## Goal

Move all SSL certificate management from shell scripts (`tollgate-apply-ssl`, `tollgate-remove-ssl`) into the Go CLI (`tollgate ssl apply`/`tollgate ssl remove`), eliminating the openssl and px5g dependencies entirely.

## Why

- OpenWrt routers have ~16MB flash. openssl-util adds ~2MB. Unacceptable.
- px5g-mbedtls can't parse certs (no x509 inspection), can't output SAN, needs openssl for DER→PEM.
- Go's `crypto/x509` and `crypto/rsa` handle everything natively. The CLI binary is already deployed.
- Shell scripts have `set -euo pipefail` which is incompatible with busybox ash.

## Design

### CLI Commands

```
tollgate ssl apply                  # Self-signed cert for <hostname>.lan
tollgate ssl apply <cert> [key]     # Real cert (combined PEM or separate files)
tollgate ssl remove                 # Revert SSL config
tollgate ssl status                 # Show current SSL state
```

These are direct CLI operations (like `start`/`stop`/`restart`), NOT daemon commands.

### Self-signed mode (`tollgate ssl apply`)

1. Read hostname from UCI (`system.@system[0].hostname`)
2. Generate 2048-bit RSA key + x509 cert using Go `crypto/x509` + `crypto/rsa`
3. CN = `<hostname>.lan`, SAN = `<hostname>.lan` + `<hostname>`
4. Valid 10 years
5. Install cert+key to `/etc/tollgate/ssl/`
6. Configure UCI: uhttpd cert/key/listen_https, nodogsplash port 443 allow
7. Backup pre-SSL UCI state to `/etc/tollgate/ssl/backup/`
8. Reload uhttpd + nodogsplash

### Real cert mode (`tollgate ssl apply <cert> [key]`)

1. Parse cert with Go `x509.ParseCertificate`
2. Validate: valid PEM, not expired
3. Extract domain from SAN DNSNames, fallback to CN
4. Install cert+key to `/etc/tollgate/ssl/`
5. Configure UCI: uhttpd, dnsmasq (domain→LAN IP), nodogsplash (gatewaydomainname + port 443)
6. Backup pre-SSL UCI state
7. Reload uhttpd + dnsmasq + nodogsplash

### Remove mode (`tollgate ssl remove`)

1. Read backup from `/etc/tollgate/ssl/backup/`
2. Remove cert+key files
3. Restore UCI from backup (uhttpd, dnsmasq, nodogsplash)
4. Remove port 443 allow rule
5. Reload services
6. Clean up backup

### What stays as shell

- `99-tollgate-setup` — uci-defaults, runs at first boot before Go binary available. Fix: `set -euo pipefail` → `set -eu`
- `tollgate-apply-ssl` — thin wrapper: `exec tollgate ssl apply "$@"`
- `tollgate-remove-ssl` — thin wrapper: `exec tollgate ssl remove "$@"`

### Dependencies eliminated

| Before | After |
|--------|-------|
| px5g-mbedtls for self-signed | Go crypto/x509 (in binary) |
| openssl-util for real cert parsing | Go crypto/x509 (in binary) |
| ~2MB flash for openssl | 0 extra bytes |

### Testing

Physical hardware tests on alpha router (10.47.41.1):
- Hostname setup verification
- Self-signed cert apply → verify HTTPS → remove → verify cleanup
- Real cert apply (Let's Encrypt via Cloudflare DNS-01) → verify → remove → verify cleanup
- Regression: existing smoke-degraded, test-captive-portal still pass

## Architecture Compatibility

- Reuses Cloudflare API token from `/home/c03rad0r/tollgate-infrastructure-kit/.env`
- DNS-01 challenge for real certs (same approach as infrastructure kit's Caddy)
- Test make targets follow existing `mint-health/Makefile` conventions
