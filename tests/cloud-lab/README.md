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

- **tg-mint-fees** — Second cdk-mintd FakeWallet mint with
  `CDK_MINTD_INPUT_FEE_PPK=100`, mirroring fee-charging real-world mints
  (e.g. mint.coinos.io): a single-proof swap costs 1 sat, so a 1-sat token
  is entirely consumed by the fee. Used by `test_swap_fees.py`.

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
docker compose run --rm client -sv test_smoke_payment.py

# Run the two-router tests (starts reseller too)
docker compose --profile two-router up -d
docker compose run --rm client -sv test_two_router_autopay.py

# Teardown
docker compose down
```

## Test Suite

| File | What it validates |
|---|---|
| `test_smoke_payment.py` | Mint reachable, TollGate reachable, wallet funded, payment returns session event (kind 1022), gate opened, balance endpoint works |
| `test_swap_fees.py` | Fee-charging mint: fee visible in keysets, below-fee token refused before the swap (`payment-error-below-swap-fee`, token stays unspent), above-fee payment credited net of fee, free-mint path unchanged |
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

<<<<<<< HEAD
## Per-checkout project isolation

Every checkout of this repo resolves the same default compose project
(`cloud-lab`, from the top-level `name:` in `docker-compose.yml`), so
`compose up` from worktree B silently reuses worktree A's built images —
the binary inside belongs to A's code. To isolate a checkout, set
`COMPOSE_PROJECT_NAME` before any `docker compose` call, or copy
`.env.example` to an untracked `.env` in this directory (auto-loaded):

```bash
cp tests/cloud-lab/.env.example tests/cloud-lab/.env
echo "COMPOSE_PROJECT_NAME=cloud-lab-$(git branch --show-current)" >> tests/cloud-lab/.env
```

Isolated projects build their own images (docker layer cache is still
shared, so rebuilds are fast) and own their containers, volumes and
network. Note the compose file pins the `172.28.0.0/16` subnet, so two
labs still cannot run simultaneously on one host — tear the other down
first (`docker compose down -v`).
=======
## Gotchas from multi-branch lab runs (2026-09-20)

Two environmental traps manufactured phantom test failures during
multi-agent, multi-branch lab sessions. Both are environmental, not code
bugs — but they cost hours if you don't know them.

### 1. Stale images across worktrees (shared compose project name)

`docker compose` derives the project (and therefore the image tags,
e.g. `cloud-lab-upstream`) from the **directory name** — and every
checkout of this repo has a `tests/cloud-lab/`. Run the lab from a
worktree of branch A, later `compose up` from a worktree of branch B,
and B silently **reuses A's built images** (the `tollgate` binary inside
is A's code). Symptoms seen in the wild: a crash referencing
`config.json.tmpl` (removed from the tree weeks earlier) and a wallet
running with a months-old one-mint config — both vanishing after a
rebuild.

**Fix:** build explicitly when switching branches, or use a per-branch
project name:

```bash
docker compose build upstream reseller   # force rebuild at this branch
docker compose -p cl416 up -d mint upstream   # per-branch project (own images, own network)
```

Note `-p` also creates a **second network** — the compose file pins the
`172.28.0.0/16` subnet, so two labs cannot run simultaneously on one
host; tear one down first (`docker compose down -v`).

### 2. Host port conflicts (8085 / 2121)

The compose file publishes `8085` (mint) and `2121` (upstream/reseller)
on the host for operator convenience — tests talk to `mint:8085` /
`upstream:2121` **on the docker network**, never via the host ports. On
a shared host where something else already binds those ports, `compose
up` fails (`address already in use`) and — worse — a lab script that
recreates containers mid-run can half-fail.

**Fix:** drop a `docker-compose.override.yml` next to the compose file
(host-specific; gitignored — see the root `.gitignore`):

```yaml
services:
  mint:
    ports: !override
      - "28085:8085"
  upstream:
    ports: !override
      - "21212:2121"
  reseller:
    ports: !override
      - "21213:2121"
```

`ports: !override` is a Compose-specific YAML tag introduced in
**v2.24.4** — on older Compose the override file fails to parse instead
of overriding, so check `docker compose version` first.

Container-to-container traffic is unaffected; only your browser access
to the mint/upstream moves to the high ports.
>>>>>>> 54f8615 (docs(cloud-lab): document the two environmental traps from multi-branch lab runs)
