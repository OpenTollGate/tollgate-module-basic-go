# Nodogsplash Port 2121 — Architecture Decision

## Status: Decided (2026-07-02)

The captive portal / nodogsplash functionality belongs in
`tollgate-module-basic-go` (Go, runs on physical OpenWrt routers), **not** in
`tollgate-rs` (Rust, runs on FIPS mesh peers).

## Background

PR #5 on `OpenTollGate/tollgate-rs` (`feature/persistent-portal-endpoint`)
added a `/portal` SPA endpoint to the Rust HTTP server, intended to serve as
the captive portal page that users see after nodogsplash interception.

## Why the move?

1. **FIPS has no gateway hierarchy** — `tollgate-rs` runs on FIPS mesh peers,
   which operate in a flat peer-to-peer topology. There is no central gateway,
   no DHCP server intercepting traffic, and no concept of a captive portal
   splash page.

2. **FIPS peers peer first, not gateway-first** — nodes discover and route
   through each other directly. There is no single ingress point where
   nodogsplash would intercept HTTP traffic.

3. **Already implemented in Go** — `tollgate-module-basic-go` already has:
   - `packaging/files/etc/config/firewall-tollgate` — UCI firewall rule allowing
     TCP 2121 from LAN
   - `packaging/files/etc/uci-defaults/99-tollgate-setup` — function
     `setup_nodogsplash()` which adds `users_to_router='allow tcp port 2121'`
     and configures the nodogsplash gateway
   - `packaging/files/etc/uci-defaults/90-tollgate-captive-portal-symlink` —
     symlinks the captive portal SPA into nodogsplash's htdocs
   - `packaging/files/tollgate-captive-portal-site/` — the full SPA build

4. **Correct deployment target** — the physical OpenWrt router (GL-MT6000)
   runs nodogsplash at the network edge. The Go backend manages UCI config on
   this device via `connector.go` / `ExecuteUCI()`.

## What was done

- PR #5 on `tollgate-rs` closed with explanation
- New branch `fix/nodogsplash-portal-firewall-rule` created on
  `tollgate-module-basic-go`
- No code changes needed — the Go module already handles port 2121 firewall
  rules and nodogsplash configuration

## Next steps

The remaining work is operational, not code:

1. Install `tollgate-module-basic-go` package on the GL-MT6000 router
2. Run the first-boot setup script (`99-tollgate-setup`) which configures
   nodogsplash + firewall rules + captive portal site
3. Verify port 2121 is reachable and nodogsplash serves the portal

See kanban tasks: ARCH-1, ARCH-3 on the infrastructure board.
