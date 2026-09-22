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

- **tg-mint-rotate** — Fee-charging mint with scriptable keyset rotation
  (`CDK_MINTD_FAKE_WALLET_KEYSET_ROTATIONS`, driven by
  `run-keyset-rotation.sh` from the host): phase A mints proofs on a
  100-ppk keyset, phase B expires it and activates a zero-fee keyset.
  cdk-mintd 0.17.6 refuses swaps on expired keysets outright, so phase B
  pins that pre-rotation proofs are refused — currently *misclassified* as
  `payment-error-mint-unreachable` (#447 lands the dedicated code, filed as
  #440; the inactive-but-swappable
  fee-resolution branch needs a nutshell-style mint — tracked in the
  signet-lane issue)

- **tg-upstream-ext** (profile `external-mints`) — A second upstream
  whose `accepted_mints` also lists a real internet mint
  (`testnut.cashu.exchange`, nutshell main + FakeWallet, free test
  ecash). Used by `run-external-mints.sh`; the default upstream stays
  offline-only. Real-money mints are never probed by this lane..

- **tg-upstream** — The TollGate Go binary, built from source. Uses a
  fake `ndsctl` script instead of NoDogSplash, so all payment/session/
  merchant/mint-health logic runs unmodified. Only packet-level gate
  control is stubbed.

- **tg-reseller** — Same binary in `reseller_mode: true`. Optional,
  started with `--profile two-router`.

- **tg-client** — Test runner with cdk-cli, nak, and pytest.

## Quick Start

```bash
# Build and start the default topology: mint, mint-fees and upstream.
# The fee tests need mint-fees; a bare `mint upstream` start leaves them red.
docker compose up -d

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
| `test_keyset_rotation.py` | Two-phase run via `./run-keyset-rotation.sh` (the phase tests are gated on `ROTATION_LANE=1` and skip in a default `client` run): proofs minted on a pre-rotation keyset are **refused** after it expires (cdk-mintd rejects swaps on expired keysets outright) — currently surfacing *misclassified* as `payment-error-mint-unreachable` while the mint is healthy and the token is permanently dead (#447 lands the dedicated code, filed as #440); sum-then-ceil fee boundary pinned across proof counts (255/1023/2047 sats) |
| `test_external_mints.py` | Opt-in live lane via `./run-external-mints.sh` (`EXTERNAL_MINTS=1` gates it out of default runs; skips with a reason when the mint is down): real-mint keyset fees verified, a 1-sat token terminated by the #409 below-swap-fee pre-check, and a fee-deducted session credit — all against `testnut.cashu.exchange` (nutshell main, FakeWallet; real-money mints never probed) |
| `test_mint_failure.py` | Kill mint mid-session → TollGate degrades gracefully (no crash) → restart mint → TollGate recovers and accepts payments again |
| `test_two_router_autopay.py` | Two-router chain: reseller processes payment without crashing, both TollGates stay alive |
| `conformance/` | Fast-subset conformance lane (#503) via `./conformance/run-conformance.sh`: duplicate/timeout/kill-at-boundary/alias scenarios from the co-owned PRTA matrix, driven through the PRTA fault proxy; emits a per-invariant verdict table. Skips cleanly without docker or a PRTA checkout |

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
        docker compose up -d
        docker compose run --rm client
        docker compose down -v
```

The mint container build (cargo install cdk-mintd) takes ~5-8 minutes
on first run. Docker layer caching makes subsequent runs fast.

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
