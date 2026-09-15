# T16 — de-coupling the tests from the concrete wallet (implementation plan)

**Status:** scoped 2026-09-14. This is the recommended optionality work
("~1–2 days, regardless of the final choice"): convert the three un-fork paths
into a *choice* by removing incidental `gonuts` coupling from tests.

## What the coupling actually is (measured)

Only **2 non-test files** import the concrete library
(`src/tollwallet/gonuts_wallet.go`, `src/tollwallet/tollwallet.go`). **9 test
files** do. Reference counts (gonuts/`cashu.`/`nut04.` occurrences):

| refs | file | nature | action |
|---|---|---|---|
| 42 | `src/merchant/lightning_state_test.go` | fixtures + state types | **decouple** |
| 15 | `src/merchant/quotes_wireformat_test.go` | wire-format fixtures | **decouple** (reuse shared vectors) |
| 13 | `src/merchant/merchant_token_flow_test.go` | fixtures (`cashu.NewTokenV3/V4`) | **decouple** |
| 9 | `src/merchant/offline_wallet_integration_test.go` | fixtures | **decouple** |
| 15 | `src/tollwallet/compatibility_matrix_test.go` | **gonuts keyset compat by design** | keep (adapter-specific) |
| 13 | `src/tollwallet/tollwallet_test.go` | mixed: adapter + primitives | split |
| 8 | `src/tollwallet/bench_test.go` | adapter bench | keep-ish (adapter bench) |
| 5 | `src/tollwallet/spending_conditions_test.go` | primitives + fixtures | decouple |
| 1 | `src/tollwallet/cross_vectors_test.go` | portable vectors | decouple (1 line) |

**Key finding:** the merchant tests already talk to `tollwallet.WalletPort` (a
`stubReceiveWallet` embeds it). The residual coupling is almost entirely
**building token fixtures** with gonuts (`cashu.NewTokenV4(proofs, mint, cashu.Sat, false)`).
So this is not a deep refactor — it is fixture plumbing.

## Strategy

1. **Add a library-agnostic token-fixture helper** (exported, test-support): build
   a canonical `cashuA…`/`cashuB…` string from primitives
   (amount, mint URL, unit) *without* importing gonuts — emit the wire format
   directly (it is a documented, stable format; see `WIREFORMAT.md`).
   Proposed home: `src/tollwallet/tokentest/` (a tiny non-`_test.go` package so the
   merchant tests can import it) or an `internal/` equivalent.
2. **Migrate the 4 merchant tests** to the helper. They already use
   `WalletPort`, so only fixture construction changes.
3. **Split `tollwallet_test.go` / `spending_conditions_test.go`:** keep
   adapter-specific assertions in gonuts-build-tagged tests; move primitive
   assertions onto the helper + `WalletPort`.
4. **Keep `compatibility_matrix_test.go` as the gonuts compatibility suite** — it
   exists to pin gonuts behaviour (and to prove the port preserves it). It becomes
   the *gonuts arm* of the shared conformance suite.
5. **Wire the shared suite** (`03-baseline/interchangeability.md` §conformance):
   one table-driven test parameterised by adapter, so gonuts / CDK-sidecar run the
   same vectors.

## Acceptance for T16

- `grep -rl gonuts src/merchant --include="*_test.go"` → **empty**.
- `go test ./...` green with the default build (gonuts) and with the helper.
- No change to `WalletPort` or non-test code.

## Why it matters

- Makes the wallet a **reversible build/config choice** rather than a fate.
- Turns the parity work (T15) into "point the suite at adapter B" instead of a
  parallel test effort.
- Shrinks the blast radius of any eventual swap (327 branches carry a `replace`).

## Not done here

This plan is committed but the refactor is **not implemented**: it is a code
change in `tollgate-module-basic-go` and needs its own branch + PR with the
module's CI (`go test ./...`), not a docs commit on the research branch. It is the
next executable step after the parity suite skeleton.
