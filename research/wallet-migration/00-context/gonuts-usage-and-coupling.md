# Context: how the module consumes gonuts today

## Dependency

`src/go.mod` pins `github.com/OpenTollGate/gonuts-tollgate` (a fork of
`github.com/elnosh/gonuts`) as a *direct* dependency, with the version varying by
branch (v0.7.1 / v0.10.0 / v0.11.1 observed across branches). Local research
clones exist at `~/repos/gonuts-tollgate`, `~/repos/gonuts-pr23-red`,
`~/repos/gonuts-pr23-build-2`.

## The seam already exists

`src/tollwallet/gonuts_wallet.go` documents itself as:

> Wraps `*TollWallet` (gonuts-tollgate) and translates between primitive port
> types (`Token`, `MintQuoteState`, `MintQuote`) and gonuts types.

That is the good news for this migration: there is a **port/adapter boundary**.
A replacement should implement `WalletPort` and leave the rest of the module
untouched. Research task: enumerate the full `WalletPort` surface and record
which NUTs each method implies (see `TASKS.md` T1b), because that surface *is*
the acceptance contract for any replacement.

## Coupling to record (task T1b)

- [ ] Every exported method on `WalletPort` and its callers
- [ ] Types that leak gonuts notions into the module (e.g. `MintQuoteState` values)
- [ ] Storage: where the wallet's proofs/quotes live today (sqlite? files?) and who owns that file
- [ ] Which NUTs are actually exercised (mint quote, mint, melt, swap, check-state, …)
- [ ] Concurrency expectations (what calls the wallet concurrently on a router)
- [ ] Binary-size and RSS contribution of the gonuts dependency (task T4)

## PRIOR ART FOUND (2026-09-13, on this branch's base)

The seam is not just a plan — a **CDK adapter already exists in the module**:

- `src/tollwallet/port.go` — `WalletPort`, documented as *"the library-agnostic
  Cashu wallet interface … the seam between merchant/lightning code and the
  underlying Cashu library (gonuts-tollgate by default, **cdk-go behind the
  `cdk_wallet` build tag**)"*.
- `src/tollwallet/cdk_wallet.go` — 443 lines, `//go:build cdk_wallet`, importing
  **`github.com/cashubtc/cdk-go/bindings/cdkffi`**. Holds a mnemonic file
  (`cdk-wallet-mnemonic.txt` — *"losing it forfeits every derived balance"*),
  a per-mint wallet map, `quoteToMint`, `acceptedMints`, `allowUntrusted`.
- `src/tollwallet/WIREFORMAT.md` — documents the NUT-04 state wire format and
  the deliberate dual-format JSON (uppercase strings on marshal, ints **and**
  strings on unmarshal for legacy gonuts data).
- Supporting tests already present: `cdk_wallet_test.go`,
  `compatibility_matrix_test.go` (gonuts v0.7.x keyset/round-trip matrix),
  `cross_vectors_test.go`, `bench_test.go`, `sentinel_test.go`,
  `spending_conditions_test.go`.

**Consequence for this research:** the CDK question is *not* "can we integrate
Rust into a Go router" — a cdk-go FFI binding path is already written. The real
questions become: does `cdk-go` exist as a maintained, buildable dependency; does
it cross-compile to the router's musl arches; what does it cost at runtime; and
is the existing adapter complete or partial (T5a/T5b). This materially raises
CDK's standing versus nucula, which has no comparable integration path at all.
