# TollGate — OpenWRT Router Payment Gateway

![tollgate-logo](docs/TollGate_Logo-C-black.png)

- Website: [tollgate.me](https://tollgate.me)
- Release manager (firmware + package builds): [releases.tollgate.me](https://releases.tollgate.me)

TollGate turns an OpenWRT router into a Cashu-powered payment gateway for
internet access. Customers pay in sats (time- or data-based); the router
gates network access and sweeps balances to configured Lightning
addresses. The same binary also acts as a *client* to upstream TollGates,
so a router can buy internet from another TollGate and resell it
automatically.

## Core technologies

- **Cashu** — ecash over mints ([cashu.space](https://cashu.space))
- **Nostr** — identities, discovery, advertisements, payments
  ([NIP-01](https://github.com/nostr-protocol/nips/blob/master/01.md))
- **Bitcoin Lightning** — payout settlement
- **NoDogSplash (ndsctl)** — the captive portal / gate that
  authorizes MACs

Wire protocol docs are under
[reference-material/protocol/](reference-material/protocol/).

## Feature highlights

- Accept Cashu tokens for internet access, by time **or** by data.
- Automatic Lightning payouts split across any number of profit-share
  identities.
- Upstream autopay — a TollGate can detect an upstream TollGate and
  purchase access on your behalf while reselling to its own customers
  (reseller mode).
- Auto-update (Janitor) over Nostr, with architecture-specific packages.
- Optional "bragging" announcements of sales on Nostr relays.
- Per-mint pricing, trust allow/blocklists, configurable session
  increments and renewal thresholds.

## Modules

Source lives under [src/](src/). Go tooling runs from there
(`cd src && go build ./...`, `cd src && go test ./...`).

| Module | Role |
|---|---|
| [merchant](src/merchant/) | Prices advertisements, validates incoming Cashu payments, and hands off started sessions to the session manager. Also drives Lightning payouts. See [docs/merchant.md](docs/merchant.md). |
| [upstream_session_manager](src/upstream_session_manager/) | Owns the customer session lifecycle on this router — creates usage trackers (time or bytes), instructs the Valve when to open/close, handles renewal near limit. Formerly the `chandler` package. See [docs/upstream_session_manager.md](docs/upstream_session_manager.md). |
| [upstream_detector](src/upstream_detector/) | Probes WAN interfaces to discover an upstream TollGate, decides whether to buy from it, and coordinates the reseller flow. Formerly `crowsnest`. See [docs/crowsnest.md](docs/crowsnest.md) and [docs/upstream-gateway-flow.md](docs/upstream-gateway-flow.md). |
| [wireless_gateway_manager](src/wireless_gateway_manager/) | Wi-Fi gateway selection, connection/reconnection, scanning, reseller-mode network orchestration. See [docs/wireless_gateway_manager.md](docs/wireless_gateway_manager.md). |
| [valve](src/valve/) | Thin wrapper over `ndsctl` that opens/closes gates and authorizes/deauthorizes MACs. |
| [janitor](src/janitor/) | Listens on Nostr for update events, downloads and verifies architecture-matched packages. |
| [config_manager](src/config_manager/) | Schema, loading, migrations, validation, backups of `/etc/tollgate/config.json`. |
| [tollwallet](src/tollwallet/) | Cashu wallet operations (mint client, balance tracking, melt). |
| [lightning](src/lightning/) | LNURL-p / Lightning address resolution and invoice fetching for payouts. |
| [relay](src/relay/) | Embedded Nostr relay for local pub/sub. |
| [cli](src/cli/) | `tollgate` CLI for `status`, `start`/`stop`/`restart`, `logs`, `version`. Entry point: [src/cmd/tollgate-cli](src/cmd/tollgate-cli/). |
| [tollgate_protocol](src/tollgate_protocol/) | Wire-type definitions shared across modules. |

## Installation

Build artifacts produced by the CI matrix in
[.github/workflows/build-package.yml](.github/workflows/build-package.yml)
target both `apk` (OpenWrt 25.x) and `ipk` (OpenWrt ≤24.10) formats.

On OpenWrt 25.x:
```sh
apk add --allow-untrusted /tmp/tollgate-wrt-<version>.apk
```
On OpenWrt 24.10.x and earlier:
```sh
opkg install /tmp/tollgate-wrt_<version>_<arch>.ipk
```

For local packaging experiments there is a developer helper
[build-sdk-apk.sh](build-sdk-apk.sh) that wraps the `openwrt/sdk` Docker
image.

## Configuration

TollGate writes a default `/etc/tollgate/config.json` on first boot.
The current schema version is **`v0.0.7`**. An abridged example:

```json
{
  "config_version": "v0.0.7",
  "log_level": "info",
  "metric": "bytes",
  "step_size": 22020096,
  "margin": 0.1,
  "relays": [
    "wss://relay.damus.io",
    "wss://nos.lol",
    "wss://nostr.mom"
  ],
  "show_setup": true,
  "reseller_mode": false,
  "accepted_mints": [
    {
      "url": "https://mint.coinos.io",
      "min_balance": 64,
      "balance_tolerance_percent": 10,
      "payout_interval_seconds": 60,
      "min_payout_amount": 128,
      "price_per_step": 1,
      "price_unit": "sats",
      "purchase_min_steps": 0
    }
  ],
  "profit_share": [
    { "factor": 0.79, "identity": "owner" },
    { "factor": 0.21, "identity": "developer" }
  ],
  "upstream_detector": {
    "probe_timeout": "10s",
    "probe_retry_count": 3,
    "probe_retry_delay": "2s",
    "require_valid_signature": true,
    "ignore_interfaces": ["lo", "docker0", "br-lan", "hostap0"],
    "only_interfaces": [],
    "discovery_timeout": "300s"
  },
  "upstream_session_manager": {
    "max_price_per_millisecond": 0.002777777778,
    "max_price_per_byte": 0.00003725782414,
    "trust": {
      "default_policy": "trust_all",
      "allowlist": [],
      "blocklist": []
    },
    "sessions": {
      "preferred_session_increments_milliseconds": 60000,
      "preferred_session_increments_bytes": 131100000,
      "millisecond_renewal_offset": 10000,
      "bytes_renewal_offset": 131100000
    },
    "usage_tracking": {
      "data_monitoring_interval": "500ms"
    }
  }
}
```

Key fields:

- **`metric`** — `"bytes"` sells data, `"milliseconds"` sells time. `step_size` is the unit.
- **`accepted_mints[*]`** — per-mint URL, pricing, and payout thresholds. Multiple mints are supported; the first mint holding sufficient balance wins on payout.
- **`profit_share[*]`** — each entry references an `identity` that maps to an entry in `/etc/tollgate/identities.json`; `factor` values should sum to 1.0.
- **`reseller_mode`** — when true, this router actively purchases from an upstream TollGate and resells to its own customers.
- **`upstream_detector`** / **`upstream_session_manager`** — control the *client* side (buying from an upstream).

`ignore_interfaces` and `only_interfaces` gate which WAN-side interfaces
are probed. `ignore_interfaces` typically needs to list any wireless
interfaces *the router itself serves on* to prevent self-probing.

## Testing

Unit tests, from the [src/](src/) directory:

```sh
cd src && go test ./...
```

A single package:

```sh
cd src && go test ./upstream_session_manager/...
```

End-to-end tests live in [tests/](tests/) and use pytest against real
router hardware:

| File | Purpose |
|---|---|
| [tests/test_copy_images.py](tests/test_copy_images.py) | Transfer firmware to target routers. |
| [tests/test_install_images.py](tests/test_install_images.py) | Flash firmware (destructive). |
| [tests/test_install_packages.py](tests/test_install_packages.py) | Install the `tollgate-wrt` package on a running router. |
| [tests/test_network_configuration.py](tests/test_network_configuration.py) | Verify upstream gateway connectivity. |
| [tests/test_ecash_payment.py](tests/test_ecash_payment.py) | End-to-end buy-internet flow. |
| [tests/test_ecash_functionality.py](tests/test_ecash_functionality.py) | Wallet-level Cashu operations. |
| [tests/test_data_measurement.py](tests/test_data_measurement.py) | Byte accounting across a data-metered session. |
| [tests/test_teardown.py](tests/test_teardown.py) | Reset routers between runs. |

See [tests/README.md](tests/README.md) for how to wire up the test fleet.

## Documentation

Design and protocol docs live under [docs/](docs/):

- [docs/merchant.md](docs/merchant.md)
- [docs/upstream_session_manager.md](docs/upstream_session_manager.md)
- [docs/upstream-gateway-flow.md](docs/upstream-gateway-flow.md)
- [docs/data-session-management.md](docs/data-session-management.md)
- [docs/wireless_gateway_manager.md](docs/wireless_gateway_manager.md)
- [docs/crowsnest.md](docs/crowsnest.md)

Module-level HLDDs/LLDDs (where they exist) sit next to their code:
[src/janitor/HLDD.md](src/janitor/HLDD.md),
[src/config_manager/HLDD.md](src/config_manager/HLDD.md),
[src/config_manager/LLDD.md](src/config_manager/LLDD.md).

Work-in-progress notes are under [docs/work/](docs/work/).

## License

GPL-3.0 — see [LICENSE](LICENSE).
