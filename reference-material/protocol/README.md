# TIPs

TIPs stand for **TollGate Implementation Possibilities**.

They exist to document what may be implemented by TollGate compatible devices.

---

- [List](#list)
- [License](#license)

---

## List

### Protocol — *what* is the data?

Data model, event kinds, tags, semantics, payment asset types. Interface-agnostic.

| # | Description |
|---|-------------|
| [TIP-01](TIP-01.md) | Base Events (Advertisement, Session, Notice) |
| [TIP-02](TIP-02.md) | Cashu payments |

### Interface — *how* do customer and TollGate talk?

Communication method used for negotiation (Advertisement, payment, session management).

| # | Description |
|---|-------------|
| [HTTP-01](HTTP-01.md) | HTTP server |
| [HTTP-02](HTTP-02.md) | Restrictive OS compatibility |
| [HTTP-03](HTTP-03.md) | Usage endpoint |
| [NOSTR-01](NOSTR-01.md) | Nostr relay |

### Medium — *what physical link* carries the sold data?

| # | Description |
|---|-------------|
| [WIFI-01](WIFI-01.md) | Discovery through Beacon Frames |

## Recipes

Example combinations of Protocol + Interface + Medium specs.

| Name | Protocol | Interface | Medium | Description |
|------|----------|-----------|--------|-------------|
| WiFi hotspot (full) | TIP-01, TIP-02 | HTTP-01, HTTP-02 | WIFI-01 | Adds `/whoami` for restrictive OS support and beacon frame discovery. |
| WiFi hotspot (basic) | TIP-01, TIP-02 | HTTP-01 | — | Captive portal with HTTP payment. Device ID from MAC. |
| WiFi hotspot (stealth) | TIP-01, TIP-02 | HTTP-01 | — | No Captive portal, No beacon frame advertisement, no `/whoami`. Only customers who know the network can pay. |
| Ethernet port | TIP-01, TIP-02 | HTTP-01 | — | Wired access sold via HTTP. Same as WiFi hotspot but over Ethernet. |
| WiFi + Nostr relay | TIP-01, TIP-02 | HTTP-01, NOSTR-01 | WIFI-01 | HTTP for in-band payments, Nostr relay for out-of-band or remote management. |
| Nostr-only | TIP-01, TIP-02 | NOSTR-01 | — | Fully out-of-band. Customer pays via relay, device identity via pubkey/tags. |
| BLE tethering | TIP-01, TIP-02 | *(future BLE-01)* | — | Bluetooth internet sharing. Payment over GATT characteristics. |
| LoRa mesh node | TIP-01, TIP-02 | *(future LORA-01)* | — | Mesh radio access. Custom negotiation over LoRa frames. |

## Breaking changes
| When | What |
|------|------|
| June 2025 | Cashu payments mint now is added to `<price_per_step>` tag, removing `<mint>` tag |
| June 2025 | Changed Discovery to no longer be ephemeral |
| March 2026 | Payment accepts pure bearer asset tokens instead of `kind=21000` Nostr event wrapper. Specs restructured into Protocol (TIP), Interface (HTTP, NOSTR), and Medium (WIFI) categories. |

## License

All TIPs are public domain.