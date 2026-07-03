# TollGate Roadmap

This roadmap tracks the versioned evolution of TollGate for OpenWrt routers
(`tollgate-module-basic-go`). For the FIPS mesh integration design, see
[tollgate-rs](https://github.com/OpenTollGate/tollgate-rs).

---

## v0.5.0 ✅ Released 2026-07-03

**Resilience & hardening** — a TollGate now degrades gracefully instead of
falling over. Mint fallback, merchant degraded mode, HTTPS, schema-driven
config, .apk packaging for OpenWrt 25.x, security hardening.

Full release notes: https://github.com/OpenTollGate/tollgate-module-basic-go/releases/tag/v0.5.0

---

## v0.6.0 — Mesh & FIPS Integration (In Design)

TollGate routers join FIPS mesh networks as forwarding nodes.

- FIPS peering: cryptographic peer IDs, end-to-end encrypted forwarding
- Per-peer forwarding policy via FIPS control socket
- 802.11s mesh backbone for router-to-router connectivity
- TollGate Review Club alpha testing program begins

---

## v0.7.0 — Internet Exit & Tunneling

FIPS-only TollGate nodes reach the legacy internet through GRE tunnels
to other TollGate nodes with WAN connectivity.

- GRE tunnel bridge: FIPS → legacy internet
- Binary allow/deny on tunnel access (Phase 1)
- TollGate RS pricing on tunnel interfaces (Phase 2)
- Jump host pattern for SSH through FIPS nodes

---

## v0.8.0 — Native Android

Native Android TollGate client built on FIPS.

- Rust core (nostril-native + Cashu NIP-60) via `cargo-ndk`
- Kotlin/Jetpack Compose UI via UniFFI (no Tauri)
- FIPS-based notifications without central servers (no FCM/APNS)
- FIPS solves the Android hotspot VPN bypass problem (access control at
  protocol layer, not IP layer)

---

## Future

- **Full FIPS-only mesh**: Remove all IP internally — only loopback + FIPS + GRE
- **Multiple exit nodes**: Reputation markets for exit access
- **Per-FIPS-instance pricing**: Different rates for heterogeneous peers (LoRa vs fiber)
- **Wi-Fi Direct mobile mesh**: Phone-to-router FIPS connections without hotspot
- **Gamification**: Ham radio style FIPS node "call sign" collection and mapping (opt-in)
- **microFIPS**: ESP32 build target in main FIPS repo via feature flags
- **TTL/Ping proximity proof**: Physical proximity enforcement for pairing and DoS protection
