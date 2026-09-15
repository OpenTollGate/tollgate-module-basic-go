# Wallet selection architecture — one contract, several wallets (Phase 2)

**Status:** design 2026-09-14. Goal (operator): *standardise how we interface with
the wallets so we can always choose the wallet best suited to the router we are
targeting.* This is the architecture; the Go changes ship as their own PR in
`tollgate-module-basic-go` (see "Where the code lands").

## 1. The contract stays `WalletPort`

`src/tollwallet/port.go` is the single interface (see
`00-context/walletport-contract.md`). Everything below is about **which
implementation sits behind it**; the contract itself does not change.

## 2. Two kinds of backend

| Kind | Backends | Router cost | Selected by |
|---|---|---|---|
| **In-process** | gonuts (today); future nucula-in-proc if wanted | no second binary | Go build tag |
| **Sidecar** | CDK (`cdk-cli`/daemon), nucula (new daemon) | second process + socket | runtime config |

The sidecar form keeps `CGO_ENABLED=0` and all arches, and is what Phase 1
measured (CDK wallet-only ≈ 4.1 MiB aarch64 / 9.4 MiB mipsel; nucula core = small,
pending link/run).

## 3. Sidecar protocol (new, backend-agnostic)

A tiny versioned RPC so **one** Go client serves every sidecar backend:

- Transport: **AF_UNIX socket** at a path from config (`wallet.socket`), JSON
  lines (length-prefixed or newline-delimited JSON).
- Shape: `{"id":N,"method":"<WalletPort method>","params":{...}}` →
  `{"id":N,"ok":true,"result":{...}}` or `{"id":N,"ok":false,"error":"..."}`.
- Methods mirror `WalletPort` 1:1 (`decode_token`, `receive`, `balance`,
  `send`, `drain`, `melt_to_lightning`, `request_mint_quote`, `mint_quote_state`,
  `mint_tokens`, `request_melt_quote`, `melt`, `shutdown`) plus a `info`
  handshake returning the **capability manifest** (below).
- `Token` crosses as its serialized string; the client owns `Close()` as a no-op
  (no CGO).

This makes CDK and nucula interchangeable without the Go code knowing either.

## 4. Capability manifest (per backend)

Each backend advertises (via `info` and a committed file) what it is:

```yaml
backend: cdk            # gonuts | cdk | nucula
kind: sidecar           # in_process | sidecar
arches: [aarch64_cortex-a53, mipsel_24kc]
size_bytes: { aarch64: 4300000, mipsel: 9900000 }   # stripped, size-optimized
storage: { model: sqlite, writes_per_payment: o(1) } # or whole_blob (nucula today)
contract: { nut_07: true, nut_09: 'partial', cgo: false }
licence: MIT OR Apache-2.0
```

The build/policy tooling reads these; the router reads `info` at handshake.

## 5. Selection: build-time default + runtime override + per-target policy

1. **Build tag (compile-time):** exactly one *in-process* backend is compiled in
   (`gonuts` today). Sidecar backends are always available (no code linked).
2. **Runtime config:** `wallet.backend: gonuts | cdk | nucula`, `wallet.socket`.
   Switching sidecars is a config change + process restart — no rebuild.
3. **Per-target policy** (`wallet-policy.yaml`, used by the build/CI matrix and by
   the router's first-boot setup):

```yaml
# default backend by target (arch + flash tier)
- match: { arch: mipsel_24kc, flash_mb: 16 }
  backend: gonuts            # or nucula once its run/parity proof lands
- match: { arch: aarch64_cortex-a53, flash_mb: 128 }
  backend: cdk               # sidecar, ~4.1 MiB size-optimized
```

Policy is a *default*, never a lock: an operator can override per device.

## 6. Backends to implement

| Backend | Work | Gate |
|---|---|---|
| gonuts (in-proc) | none — reference | — |
| **sidecar client** (Go) | new `WalletPort` impl speaking §3 | Phase 2b PR |
| CDK daemon | wrap `cdk-cli` (or the wallet-only probe) to speak §3 | T15 parity |
| nucula daemon | C ABI + small daemon to speak §3; per-proof storage | Spike N link/run + storage fix |

## 7. One conformance suite (T12) is the selector's safety net

Every backend runs the same suite (`03-baseline/interchangeability.md`): contract,
functional vectors, cross-format vectors, reliability, and **T15 security parity**
(the gonuts fork's HTLC-signature + swap-proof-loss fixes). A backend is only
eligible for selection once it passes.

## 8. Where the code lands

**Status 2026-09-14:** the sidecar client (§3), capability manifest (§4) and the
selection policy (§5) are implemented in **`OpenTollGate/tollgate-module-basic-go`
PR #395** (`src/tollwallet/sidecar.go`, `manifest.go`, `policy.go`,
`manifests/*`; tests green, `gofmt`/`go vet` clean). Still open there: **T16**
(test de-coupling) and the sidecar **daemons + parity suite** (Phase 3).

- **This branch:** the design + capability manifests + the sidecar protocol spec
  + `experiments/` evidence (no shipped code).
- **`tollgate-module-basic-go` (PR #395):** the sidecar `WalletPort` client, the
  capability manifest reader, the runtime `wallet.backend` config, and the
  per-target `wallet-policy.json`. **T16** (de-couple the 9 test files) remains.

## 9. Dependencies / order

1. T16 de-couple (so all backends run one suite) — §8.
2. Sidecar client + protocol (§3) — §8.
3. Parity suite (T15) — Phase 3.
4. Per-target policy + CI matrix — Phase 4.

Net: the router build picks an **in-process default** by tag, can **switch
sidecars by config**, and a **policy table** encodes "best wallet for this
target" — with the conformance suite as the gate.

---

## Status update 2026-09-14 (later)

- **Selection consumer implemented** in PR #395 (`src/tollwallet/select.go`):
  `OpenWallet(cfg)` routes to the in-process backend (gonuts default, via
  `NewWalletPort`, build-tag resolved) or a sidecar daemon, and
  `(*WalletPolicy).OpenFor(arch, flashMB, cfg)` picks the backend from the policy
  table with an operator override. Tests cover routing + policy.
- **T16 first increment** in PR #396: `merchant_token_flow_test.go` no longer
  imports gonuts — token fixtures are centralised in a wallet-agnostic helper
  (`src/merchant/tokenfixture_test.go`, `testenv && !cdk_wallet`). The
  `nut04.State` characterization tests stay (they are the compatibility
  contract). Remaining: the `tollwallet` adapter tests.

**Still open:** the sidecar **daemons** (CDK/nucula) and the **parity suite**
(T15) — the adoption gate; Spike N link+run; and wiring the policy/manifests into
the CI build matrix.
