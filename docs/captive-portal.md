# Captive Portal Architecture

## Overview

The TollGate captive portal intercepts HTTP traffic from unauthenticated WiFi
clients and presents a splash page where they can pay for internet access. It
uses **NoDogSplash** for traffic interception and client auth, a **React SPA**
for the splash page, and a **Go API** for payment processing.

## Service Layout

```
┌─────────────────────────────────────────────────────────┐
│                    TollGate Router                       │
│                                                         │
│  Port 80   ─ NoDogSplash (captive portal gateway)       │
│  Port 443  ─ uhttpd (LuCI admin, HTTPS)                 │
│  Port 8080 ─ uhttpd (LuCI admin, HTTP)                  │
│  Port 2121 ─ tollgate-basic (Go API, HTTP)              │
│                                                         │
│  dnsmasq   ─ DNS resolver (TollGate.lan → LAN IP)      │
│  iptables  ─ HTTP interception for unauthenticated      │
│              clients on br-lan                          │
└─────────────────────────────────────────────────────────┘
```

| Port | Service | Protocol | Purpose |
|------|---------|----------|---------|
| 80 | NoDogSplash | HTTP | Captive portal gateway — intercepts traffic, serves splash page |
| 443 | uhttpd | HTTPS | LuCI admin interface (when SSL cert is installed) |
| 8080 | uhttpd | HTTP | LuCI admin interface (always available) |
| 2121 | tollgate-basic | HTTP | Go API — payment processing, wallet, config |

## Hostname and DNS

### Default Behavior (first boot)

The `99-tollgate-setup` uci-defaults script (installed by the `tollgate-wrt`
package) configures:

1. **Hostname**: `TollGate` (only if current hostname is `OpenWrt`)
2. **NoDogSplash**: `gatewaydomainname='TollGate.lan'`, `gatewayport='80'`
3. **dnsmasq**: resolves `TollGate.lan` to the LAN IP via the hostname
4. **uhttpd**: `commonname='$hostname'` (used for self-signed cert generation)

The hostname is applied to the running kernel immediately via
`/proc/sys/kernel/hostname` so no reboot is required.

### DNS Resolution

| Client Location | Query | Resolves To | How |
|----------------|-------|-------------|-----|
| WiFi client on LAN | `TollGate.lan` | `<LAN IP>` (e.g. `172.24.193.1`) | dnsmasq on router |
| Device on WAN network | `TollGate.lan` | `<WAN IP>` (e.g. `192.168.x.x`) | Router's DHCP hostname registration |

### WAN-side Access

The router registers its hostname via DHCP on the WAN interface. Any device on
the same upstream network can reach it as `TollGate.lan`. This works for:
- Accessing LuCI admin from the upstream network
- Accessing the Go API from the upstream network

The captive portal itself only applies to WiFi clients on br-lan interfaces.

## Captive Portal Detection (CPD)

NoDogSplash uses **iptables HTTP interception** on port 80. When an
unauthenticated client makes an HTTP request, iptables redirects it to
NoDogSplash which serves the splash page.

### Platform CPD Behavior

| Platform | Detection Method | Works With Our Setup |
|----------|-----------------|---------------------|
| iOS (Apple CNA) | HTTP probe to `captive.apple.com` | Intercepted → splash page |
| Android | HTTP probe to `connectivitycheck.gstatic.com` | Intercepted → splash page |
| Windows (NCSI) | HTTP probe to `www.msftconnecttest.com` | Intercepted → splash page |
| macOS | HTTP probe to `captive.apple.com` | Intercepted → splash page |

All major platforms detect the captive portal via HTTP probes. NoDogSplash
intercepts these probes and redirects to the splash page. This is the legacy
method and works reliably on all current OS versions.

## Splash Page Flow

```
1. Client connects to WiFi (br-lan)
2. Client sends HTTP request (any URL)
3. iptables REDIRECT → NoDogSplash on port 80
4. NoDogSplash checks client MAC:
   - Authenticated → allow traffic through
   - Not authenticated → serve splash page
5. Splash page (React SPA) loads, shows payment options
6. Client pays via Go API (port 2121)
7. Go API calls `ndsctl auth <MAC> <token>`
8. Client is authenticated → internet access granted
```

### Splash Page Location

- Source: [OpenTollGate/tollgate-captive-portal-site](https://github.com/OpenTollGate/tollgate-captive-portal-site)
- Installed to: `/etc/tollgate/tollgate-captive-portal-site/`
- Symlinked from: `/etc/nodogsplash/htdocs/` (via `90-tollgate-captive-portal-symlink`)

The splash page uses `window.location.hostname` to discover the router
address and communicates with the Go API at `http://<hostname>:2121`.

## SSL/HTTPS

### Architecture Decision: Captive Portal Stays HTTP

NoDogSplash **cannot serve HTTPS**. It is built on libmicrohttpd without
`MHD_USE_TLS` support. This is not a limitation we can work around without
forking NoDogSplash.

Additionally, captive portal detection on all platforms uses **plain HTTP
probes**. iOS, Android, and Windows all send HTTP (not HTTPS) requests to
detect the portal. Switching the captive portal to HTTPS would break CPD.

**Port 443 is served by uhttpd for LuCI admin only.**

### Implementation

SSL management is implemented in Go (`src/cmd/tollgate-cli/ssl.go`) using the
standard library's `crypto/x509` package. This eliminates the dependency on
`openssl` or `px5g` which are not available on minimal OpenWrt installations.

The `tollgate-apply-ssl` and `tollgate-remove-ssl` shell scripts in
`/usr/bin/` are thin wrappers that delegate to `tollgate ssl apply` and
`tollgate ssl remove` respectively.

### Default State (no SSL)

On a fresh install, the router serves everything over HTTP:

- `http://TollGate.lan/` → captive portal (NoDogSplash)
- `http://TollGate.lan:8080/` → LuCI admin (uhttpd)
- `http://TollGate.lan:2121/` → Go API

The `99-tollgate-setup` script only enables HTTPS on uhttpd if cert and key
files already exist at `/etc/uhttpd.crt` and `/etc/uhttpd.key`. On a fresh
firmware flash, these files don't exist, so uhttpd runs HTTP only on port 8080.

### Enabling SSL (Self-Signed)

```
tollgate ssl apply
# or via wrapper:
tollgate-apply-ssl
```

When called with no arguments, generates a self-signed certificate for the
router's hostname (e.g. `TollGate.lan`) using Go's `crypto/x509`. This mode:

- Generates a 2048-bit RSA key and X.509 certificate valid for 10 years
- Sets CN = `<hostname>.lan`
- Includes SAN entries for `<hostname>.lan`, `<hostname>`, and the LAN IP
- Installs cert+key to `/etc/tollgate/ssl/`
- Configures uhttpd to serve HTTPS on port 443
- Allows port 443 through NoDogSplash firewall
- Does **not** change dnsmasq or NoDogSplash domain (hostname already resolves)
- Creates a backup of pre-SSL UCI configuration for later removal

Self-signed HTTPS is intended for encrypted LuCI admin access on the local
network. Browsers will display a certificate warning that users must accept.

**Self-signed certs do NOT provide RFC 8908 compliance.** See
[Standards Compliance](#standards-compliance) below.

### Enabling SSL (Real Certificate)

```
tollgate ssl apply <cert-file> [key-file]
# or via wrapper:
tollgate-apply-ssl <cert-file> [key-file]
```

Accepts a real certificate in PEM format (separate cert+key files, or a single
combined PEM). This mode:

1. **Validates** the certificate (PEM format, checks expiry — warns but
   continues if expired)
2. **Extracts domain** from SAN DNS names, falling back to CN
3. **Installs** cert+key to `/etc/tollgate/ssl/`
4. **Configures uhttpd** to serve HTTPS on port 443 using the cert
5. **Adds dnsmasq entry** so the cert's domain resolves to the LAN IP
6. **Updates NoDogSplash** `gatewaydomainname` to the cert's domain (portal
   stays on HTTP port 80)
7. **Allows port 443** through NoDogSplash's `users_to_router` firewall so
   clients can reach uhttpd HTTPS
8. **Reloads** all services (uhttpd, dnsmasq, nodogsplash)

After applying SSL with a real cert:
- `http://example.com/` → captive portal (NoDogSplash, HTTP)
- `https://example.com/` → LuCI admin (uhttpd, HTTPS)
- `http://example.com:8080/` → LuCI admin (uhttpd, HTTP, still available)

### Disabling SSL

```
tollgate ssl remove
# or via wrapper:
tollgate-remove-ssl
```

Reverts all changes made by `tollgate ssl apply`, restoring from the backup
created during apply:

- Removes installed cert+key from `/etc/tollgate/ssl/`
- Restores uhttpd to previous cert configuration
- Removes dnsmasq domain entry (real-cert mode only)
- Reverts NoDogSplash `gatewaydomainname` and `gatewayport` (real-cert mode only)
- Removes port 443 firewall allow rule
- Reloads all services

### Checking SSL Status

```
tollgate ssl status
```

Shows current SSL state: mode (self-signed/real-cert), domain, cert file
paths, subject, issuer, validity dates, days remaining, and SAN entries.

### Non-Interactive Mode

All SSL commands accept `--yes` / `-y` to skip confirmation prompts. Required
for scripted and test automation:

```
tollgate ssl apply --yes
tollgate ssl apply --yes <cert> <key>
tollgate ssl remove --yes
```

### Re-apply Protection

Running `tollgate ssl apply` when an SSL backup already exists produces a
warning and requires confirmation before overwriting the backup. This prevents
accidental loss of the original pre-SSL configuration.

### Script Interaction with First-Boot Setup

`99-tollgate-setup` runs once on first boot. `tollgate ssl apply` runs
manually afterward. They don't conflict because:

- Setup configures uhttpd HTTPS only if `/etc/uhttpd.crt` + `/etc/uhttpd.key`
  already exist (firmware-provided certs)
- `tollgate ssl apply` overwrites cert/key paths and backs up current values
- `tollgate ssl remove` restores from backup — reverts to pre-SSL state
- Re-running setup (removing setup flag) resets everything to defaults

## Standards Compliance

### RFC 8910 — DHCP Captive Portal Option

**Status: Not implemented**

RFC 8910 defines DHCP Option 114 which advertises a captive portal URI via
DHCP or Router Advertisements. Modern clients (iOS 14+, Android 11+,
Windows 11+) can use this for faster portal detection without relying on
HTTP interception.

Implementation would require:
- `dhcp-option=114,<API-URI>` in dnsmasq config
- An RFC 8908 compliant API endpoint (see below)
- Only advertised when a valid HTTPS cert is installed

**Why not yet**: Option 114 should point to an RFC 8908 API endpoint served
over HTTPS with a publicly trusted certificate. `.lan` domains cannot get
public CA certificates. This requires a real domain + cert.

### RFC 8908 — Captive Portal API

**Status: Not implemented**

RFC 8908 defines a JSON API for captive portal state. The endpoint would be:
```
GET /.well-known/captive-portal
Accept: application/captive+json

Response: { "captive": true, "user-portal-url": "https://..." }
```

Requirements for compliance:
- **HTTPS with validated certificate** — client MUST validate cert per RFC 6125
- Content-Type: `application/captive+json`
- Cache-Control: `no-store` or `private`
- Per-client state (must know if the requesting client is authenticated)
- OCSP stapling SHOULD be supported
- Network SHOULD allow access to OCSP/CRL/NTP servers

#### Self-Signed Certificates and RFC 8908

The RFC does **not** explicitly require a publicly trusted CA certificate. It
requires that clients successfully validate the certificate. However, in
practice:

- A plain self-signed cert will **fail validation** on unmanaged devices
  (consumers) because it's not in their trust store
- A private CA root works if you **control the client devices** (enterprise)
- **Best interoperability**: real domain + publicly trusted cert

RFC 8908 Section 4.1 includes a **graceful degradation clause**:

> *"If the client is unable to validate the certificate presented by the API
> server, it MUST NOT proceed with any of the behavior for API interaction
> described in this document. The client will proceed to interact with the
> captive network as if the API capabilities were not present."*

This means clients fall back to legacy HTTP interception when cert validation
fails. Our current captive portal (NoDogSplash on port 80) works correctly as
this fallback.

**Why not yet**: Requires a publicly trusted cert on a real domain + integration
with NoDogSplash's client auth state. Self-signed certs provide HTTPS for LuCI
admin but cannot serve as an RFC 8908 endpoint for unmanaged devices.

### RFC 8952 — CAPPORT Architecture

**Status: Partially compliant**

RFC 8952 establishes that captive portal solutions:
- MUST NOT require forging DNS responses — we use HTTP interception, not DNS forgery
- MUST allow DNSSEC validation — dnsmasq passes through DNS queries without modification
- SHOULD support incremental deployment — our HTTP interception is backward-compatible
- SHOULD provide a HTTPS API for portal state — not yet implemented (RFC 8908)

### Compliance Summary

| Standard | Status | Requirement | Blocker |
|----------|--------|-------------|---------|
| RFC 8910 | Not implemented | DHCP Option 114 | Needs RFC 8908 API first |
| RFC 8908 | Not implemented | Captive Portal API | Needs public cert + client state |
| RFC 8952 | Partial | CAPPORT Architecture | Needs RFC 8908 for full compliance |

## Configuration Files

| File | Purpose |
|------|---------|
| `/etc/config/nodogsplash` | NoDogSplash config — `gatewaydomainname`, `gatewayport`, `users_to_router` |
| `/etc/config/uhttpd` | LuCI web server — `cert`, `key`, `listen_https`, `commonname` |
| `/etc/config/dhcp` | dnsmasq config — domain entries for cert domain → LAN IP |
| `/etc/tollgate/ssl/` | Installed SSL certificates (by `tollgate ssl apply`) |
| `/etc/tollgate/ssl/backup/` | Backup of pre-SSL UCI values (for `tollgate ssl remove`) |
| `/etc/nodogsplash/htdocs/` | Splash page files (symlink to `/etc/tollgate/tollgate-captive-portal-site/`) |

## Key Commands

| Command | Purpose |
|---------|---------|
| `tollgate ssl apply` | Generate self-signed cert and enable HTTPS |
| `tollgate ssl apply <cert> [key]` | Install real cert and enable HTTPS |
| `tollgate ssl remove` | Revert SSL configuration back to HTTP |
| `tollgate ssl status` | Show current SSL state and cert details |
| `tollgate-apply-ssl` | Wrapper for `tollgate ssl apply` |
| `tollgate-remove-ssl` | Wrapper for `tollgate ssl remove` |
| `99-tollgate-setup` | First-boot setup: hostname, uhttpd, DNS, NoDogSplash |
| `90-tollgate-captive-portal-symlink` | Symlink splash page on first boot |
