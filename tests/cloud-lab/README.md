# TollGate Cloud Lab

Docker-based integration test environment for TollGate. Runs the real
TollGate binary (compiled from source) against a self-hosted Cashu mint
with FakeWallet backend — no physical routers required.

## Architecture

```
┌──────────────────── Docker network: 172.28.0.0/16 ────────────────────┐
│                                                                       │
│  ┌────────────┐     ┌──────────────┐     ┌──────────────┐            │
│  │  tg-mint   │◄────│  tg-upstream │◄────│  tg-client   │            │
│  │ (cdk-mintd)│     │ (tollgate)   │     │ (pytest +    │            │
│  │ FakeWallet │     │ port 2121    │     │  cdk-cli +   │            │
│  │ port 8085  │     │              │     │  nak)        │            │
│  └────────────┘     └──────────────┘     └──────────────┘            │
│                          ▲                                            │
│                          │ optional                                   │
│                    ┌─────┴───────┐                                    │
│                    │ tg-reseller │                                    │
│                    │ (tollgate)  │                                    │
│                    │ port 2121   │                                    │
│                    └─────────────┘                                    │
└───────────────────────────────────────────────────────────────────────┘
```

- **tg-mint** — Cashu mint ([cdk-mintd](https://github.com/cashubtc/cdk))
  with FakeWallet backend. Automatically settles Lightning quotes.
  Killable for failure/degraded-mode tests.

- **tg-upstream** — The TollGate Go binary, built from source. Uses a
  fake `ndsctl` script instead of NoDogSplash, so all payment/session/
  merchant/mint-health logic runs unmodified. Only packet-level gate
  control is stubbed.

- **tg-reseller** — Same binary in `reseller_mode: true`. Optional,
  started with `--profile two-router`.

- **tg-client** — Test runner with cdk-cli, nak, and pytest.

## Quick Start

```bash
# Build and start the mint + upstream TollGate
docker compose up -d mint upstream

# Wait for health checks to pass
docker compose ps

# Run the smoke tests
docker compose run --rm client

# Run a specific test file
docker compose run --rm client pytest -sv test_smoke_payment.py

# Run the two-router tests (starts reseller too)
docker compose --profile two-router up -d
docker compose run --rm client pytest -sv test_two_router_autopay.py

# Teardown
docker compose down
```

## Test Suite

| File | What it validates |
|---|---|
| `test_smoke_payment.py` | Mint reachable, TollGate reachable, wallet funded, payment returns session event (kind 1022), gate opened, balance endpoint works |
| `test_mint_failure.py` | Kill mint mid-session → TollGate degrades gracefully (no crash) → restart mint → TollGate recovers and accepts payments again |
| `test_two_router_autopay.py` | Two-router chain: reseller processes payment without crashing, both TollGates stay alive |

## What This Tests vs What It Doesn't

### Tested (logic-level)
- Cashu token minting, sending, verification
- Payment event processing (Nostr kind 21000)
- Session event generation (kind 1022)
- Profit-share math
- Mint health tracking and degraded mode
- Config loading and migration
- Two-router autopay payment logic
- Valve timer logic (open/extend/close via fake ndsctl)

### Not tested (needs QEMU/real hardware)
- Packet-level gate control (actual NoDogSplash/iptables)
- WiFi scanning, SSID detection, WPA connection
- DHCP lease assignment
- ARP table MAC resolution
- Upstream TollGate discovery via WiFi probe

## Files

| File | Purpose |
|---|---|
| `docker-compose.yml` | Service definitions, networking, health checks |
| `Dockerfile.tollgate` | Multi-stage Go build → Debian + fake ndsctl |
| `Dockerfile.mint` | cdk-mintd with FakeWallet (cargo install) |
| `Dockerfile.client` | Python + cdk-cli + nak test runner |
| `fake-ndsctl.sh` | Drop-in ndsctl replacement that logs auth/deauth |
| `configs/upstream-config.json` | TollGate config for upstream container |
| `configs/reseller-config.json` | TollGate config for reseller container |
| `configs/*-identities.json` | Nostr identities for signing |
| `configs/install.json` | Minimal install metadata |
| `configs/dhcp.leases` | Fake DHCP lease table (maps client IP → MAC) |
| `conftest.py` | Shared pytest fixtures |
| `test_*.py` | Test suites |

## CI Integration

Add to `.github/workflows/`:

```yaml
cloud-lab-tests:
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - working-directory: tests/cloud-lab
      run: |
        docker compose up -d mint upstream
        docker compose run --rm client
        docker compose down -v
```

The mint container build (cargo install cdk-mintd) takes ~5-8 minutes
on first run. Docker layer caching makes subsequent runs fast.
