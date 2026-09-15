# Interchangeability: one contract, several wallets (T12 + T16)

**Status:** design 2026-09-14. Goal (operator policy): the ability to use
**nucula and CDK interchangeably** behind one interface, so the wallet becomes a
reversible build/config choice rather than a one-way migration.

## The seam already exists

`src/tollwallet/port.go` defines `WalletPort`; adapters are selected by build tag:

- `gonuts_wallet.go` — `//go:build !cdk_wallet` (the current, default wallet)
- `cdk_wallet.go`    — `//go:build cdk_wallet`  (the existing CDK adapter)

Plus shared wire-format vectors (`WIREFORMAT.md`, `cross_vectors_test.go`,
`compatibility_matrix_test.go`). So coexistence is *architecturally* solved; the
open question is the **selection mechanism** and the **shared conformance suite**.

## Selection mechanism

| Mechanism | Reversible at | CGO | mipsel | Verdict |
|---|---|---|---|---|
| **Build tag** (`-tags cdk_wallet`) | build time | yes (in-proc) | ✗ (T5c) | keep for gonuts/CDK in-proc, but CDK-in-proc is dead on musl |
| **Runtime config** (pick adapter by config) | run time | yes | ✗ | needs both linked; impossible when CDK can't link |
| **Process sidecar** (wallet daemon over a local socket/CLI) | run time, swap the process | **no** (`CGO_ENABLED=0`) | pending (T5c: full cdk-cli ✗ on mipsel) | **recommended for CDK** |

**Recommendation:** keep the compile-time seam for gonuts (default) and for a
future nucula adapter, and make **CDK a sidecar** (`experiments/cdk-sidecar/`).
The `WalletPort` implementation for the sidecar is a thin RPC client, so the
merchant code is unchanged and the wallet is swappable by pointing at a different
sidecar binary/socket.

## The single conformance suite (both candidates must pass it)

One table-driven suite, run against every adapter, asserting the `WalletPort`
contract (`00-context/walletport-contract.md`):

1. **Type/serialization** — `MintQuoteState` dual-format round-trip
   (int → marshal → uppercase string; string/int → unmarshal); `Token.Close()`
   idempotency; no library types leak.
2. **Functional vectors** — decode→receive→balance; send→receive; drain;
   mint-quote→mint→swap→melt; fee reserve handling.
3. **Cross-format vectors** — reuse `cross_vectors_test.go` (V3/V4 tokens,
   keyset v1/v2 ids) against each adapter.
4. **Reliability** — mint unreachable/5xx/malformed; kill mid-swap; disk full;
   corrupt store; spent-proof disagreement + self-recovery (NUT-07).
5. **Security parity (T15)** — the two fork fixes (below).
6. **Non-functional** — binary size, RSS, threads, startup, flash bytes/payment.

## Security / funds-safety parity (T15)

Non-negotiable acceptance tests, one failing test per case, run against the
candidate before any removal of gonuts:

- **`296c7bf` HTLC signature-enforcement bypass** — craft a melt/receive path
  that the pre-fix wallet accepted; the post-fix (fork) wallet must reject it;
  the candidate must reject it too.
- **`7dc430b` swap proof-loss** — interrupt/fault-inject a swap; the fork
  preserves proofs; the candidate must not lose value.

Design + skeleton: `experiments/parity/`.

## T16 — de-couple the tests (optionality, recommended regardless)

Only **2 non-test files** import the concrete wallet library
(`src/tollwallet/gonuts_wallet.go`, `src/tollwallet/tollwallet.go`), but **9 test
files** do. De-coupling those 9 tests behind `WalletPort` (or a test double)
converts the three un-fork paths into a *choice* without a big-bang port.

- Estimated effort: **~1–2 days**.
- Value: unblocks evaluating CDK/nucula behind the same suite **without** first
  finishing a migration, and reduces the blast radius of any wallet swap.
- Blast radius to keep in mind: **327 branches carry a `replace`**, and **50
  already point at our own fork-of-the-fork** — renaming/re-pointing the
  dependency is not a small edit across the estate.
