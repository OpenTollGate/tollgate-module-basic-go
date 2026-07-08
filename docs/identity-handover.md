# Device Identity & Secret Handover

Every TollGate router derives its entire network identity — LAN IP, MAC
addresses, root password, WiFi password — from a single source of entropy:
the Nostr merchant private key. This document describes how operators learn
these secrets when deploying a new router.

## Derivation tree

```
Nostr private key (32 bytes, in /etc/tollgate/identities.json)
├── npub (bech32 public key, broadcast to Nostr relays)
├── LAN IPv4 = SHA256("tollgate-ipv4-v1:" || pubkey_hex) → 100.64/10 CGNAT
├── br-lan MAC = SHA256("tollgate-mac-v1:br-lan:" || pubkey_hex)
├── wlan0 MAC = SHA256("tollgate-mac-v1:wlan0:" || pubkey_hex)
├── wlan1 MAC = SHA256("tollgate-mac-v1:wlan1:" || pubkey_hex)
├── root password = SHA256("tollgate-root-pw-v1:" || pubkey_hex) → Word-Word-Word-NN
├── WiFi password = SHA256("tollgate-wifi-pw-v1:private:" || pubkey_hex) → Word-Word-NNNN
└── BIP39 mnemonic (backup of the private key)
```

All values are deterministic: the same key always produces the same output.

## Secret handover modes

Operators learn the secrets through one of four layered mechanisms, depending
on the deployment scenario:

### 1. conwrt pre-provisioning (fleet deployments)

conwrt generates the mnemonic at flash time and derives all secrets locally.
The operator knows everything before the router boots:

```
$ conwrt flash --model cf-wr632ax
  Mnemonic:     abandon abandon abandon abandon abandon abandon ...
  Root password: Alpha-Bravo-Charlie-42
  WiFi password: Delta-Echo-1234
  LAN IP:       100.118.131.1
  npub:         npub1...
```

No discovery needed — the router boots pre-configured.

### 2. First-boot ethernet share (standalone deployments)

When the router boots without pre-provisioned secrets, a temporary endpoint
is available on the LAN:

```
GET http://192.168.1.1:2121/first-boot-setup
→ 200 OK (JSON: mnemonic, passwords, IP, MACs)
```

The operator connects via ethernet, opens this URL in a browser, and sees all
secrets. Once saved:

```
POST http://192.168.1.1:2121/first-boot-setup/complete
→ 204 No Content
```

This creates `/etc/tollgate/.first_boot_complete` and permanently disables the
GET endpoint (subsequent requests return `410 Gone`). The secrets are never
exposed again.

**Security**: the GET endpoint is accessible from the LAN during the setup
window. The operator should complete setup immediately after first boot.
Connected WiFi customers could theoretically access it during this window —
mitigate by completing setup over a direct ethernet connection before
enabling WiFi.

### 3. Physical wizard (Endo onboarding)

The Endo onboarding wizard (browser at `http://192.168.1.1`) provides a
guided setup flow with a visual QR code for the mnemonic. This is the
GL.iNet-style flow for single-device setup.

### 4. NIP-44 encrypted DM (remote monitoring)

On first boot, the router sends an encrypted Nostr direct message (NIP-44)
to the fleet operator's npub containing the root password and mnemonic. The
operator receives it in their Nostr client. Requires the operator's npub to
be configured in the firmware and a relay to be reachable at boot time.

## Security model

| Endpoint | Auth | Method | Exposes |
|---|---|---|---|
| `GET /identity` | none (public data) | GET | npub, IPv4, MACs (all public) |
| `POST /identity/reveal-seed` | loopback only | POST | mnemonic, private key, passwords |
| `GET /first-boot-setup` | flag-file gated | GET | full identity (one-time, then 410) |
| `POST /first-boot-setup/complete` | flag-file gated | POST | nothing (creates flag) |

The `/identity/reveal-seed` endpoint requires loopback (127.0.0.1) access.
Non-local requests receive 403 Forbidden. This prevents WiFi customers from
stealing the private key.

The `/first-boot-setup` endpoint is intentionally accessible from the LAN
during the setup window — the operator connects via ethernet. The window
closes permanently after `POST /first-boot-setup/complete`.
