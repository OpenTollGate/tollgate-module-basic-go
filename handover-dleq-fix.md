# Handover Document — DLEQ Proof Verification Fix

## Date

2026-07-17

## Summary

When a Cashu mint rotates its keysets, tokens minted under a now-inactive
keyset fail DLEQ verification in the gonuts-tollgate library. The error
`"invalid DLEQ proof"` is returned, blocking all Cashu payments from
wallets that embed DLEQ proofs (Minibits, Enno, most modern Cashu wallets).

## Root Cause

In `gonuts-tollgate`, `wallet.Receive()` calls
`nut12.VerifyProofsDLEQ(proofsToSwap, *keyset)` where `keyset` is obtained
from `w.getActiveKeyset(tokenMint)` — **only the mint's current active
keyset**. The function does not consult `proof.Id` (the keyset ID the proof
was actually minted under). When the mint has rotated keysets since the
token was minted, the proof's keyset ID differs from the active keyset ID,
the public keys don't match, and verification fails — even though the proof
is perfectly valid.

### Affected Code Path

```
merchant.go:437  cashu.DecodeToken(token)
merchant.go:455  m.tollwallet.Receive(paymentCashuToken)
    └─ tollwallet.go:127  w.wallet.Receive(token, swapToTrusted)
        └─ wallet.go:590   w.getActiveKeyset(tokenMint)  ← only active keyset
        └─ wallet.go:596   nut12.VerifyProofsDLEQ(proofsToSwap, *keyset)  ← FAILS
```

The same bug exists in `ReceiveHTLC` (wallet.go:679–685).

### Key Observation

`GetMintInactiveKeysets` (keyset.go:43–64) fetches inactive keysets but
**does not fetch their public keys** — it creates `WalletKeyset` structs
with empty `PublicKeys` maps. So even if we passed inactive keysets to
`VerifyProofsDLEQ`, they wouldn't have the keys needed for verification.
`AddMint` (wallet.go:173–210) does fetch keys for inactive keysets and saves
them to the DB, but clears the in-memory `PublicKeys` for inactive keysets
(line 204).

## Current go.mod State

The repo already has a `replace` directive redirecting `Origami74/gonuts-tollgate`
to `OpenTollGate/gonuts-tollgate v0.7.1`:

```
replace (
    ...
    github.com/Origami74/gonuts-tollgate => github.com/OpenTollGate/gonuts-tollgate v0.7.1
)
```

This means the upstream is already forked under `OpenTollGate/gonuts-tollgate`.
A patched version can be published as a new tag (e.g., `v0.7.2`) and the
replace directive updated accordingly.

**Important:** The DLEQ-FIX-PLAN.md references `v0.6.1` line numbers. The
actual go.mod requires `v0.6.1` as the original dependency but replaces it
with `v0.7.1`. Line numbers in the upstream library may have shifted in
`v0.7.1` — always verify against the actual source.

## Fix Approaches

### Approach A — Proper Fix: Verify DLEQ Against Correct Keyset (Recommended)

Patch `gonuts-tollgate` so `VerifyProofsDLEQ` (or a new
`VerifyProofsDLEQWithKeysets`) looks up the keyset by `proof.Id` instead of
blindly using the active keyset.

**Upstream files to patch:**

1. **`cashu/nuts/nut12/nut12.go`** — Add `VerifyProofsDLEQWithKeysets()` that
   accepts a map of keysets keyed by ID, and for each proof, selects the
   correct keyset using `proof.Id`.

2. **`wallet/wallet.go`** — Update `Receive()` and `ReceiveHTLC()` to:
   - Build a map of keysets keyed by ID (active + inactive).
   - For any proof with `DLEQ != nil` whose `proof.Id` isn't in the map,
     fetch keys via `GetKeysetKeys(mintURL, proof.Id)`.
   - Call the new `VerifyProofsDLEQWithKeysets()` instead of
     `VerifyProofsDLEQ()`.

3. **`wallet/keyset.go`** — Optionally update `GetMintInactiveKeysets` to
   also fetch public keys, so they're available without on-demand HTTP calls.

**tollgate-module-basic-go changes:**

- Update `src/go.mod` replace directive to point to the new patched version
  (e.g., `v0.7.2`).
- Run `go mod tidy` from `src/`.

### Approach B — Phase 1 Workaround: Strip DLEQ Proofs Before Receive

If patching the upstream library proves too complex or time-consuming, the
backend can strip DLEQ proofs before calling `wallet.Receive()`.

**How:** In `src/tollwallet/tollwallet.go`, before calling
`w.wallet.Receive(token, swapToTrusted)`:
1. Extract proofs from the token via `token.Proofs()`.
2. Set `proof.DLEQ = nil` for each proof.
3. Reconstruct the token via `cashu.NewTokenV4(proofs, token.Mint(), cashu.Sat, false)`.
4. Pass the reconstructed token to `w.wallet.Receive()`.

Since `VerifyProofsDLEQ` skips proofs with `DLEQ == nil` (nut12.go:15–16),
verification passes. The mint still validates proof validity during the
swap operation server-side.

**Trade-off:** This loses the DLEQ cryptographic guarantee (proof that the
mint signed the blinded message without seeing the unblinded message). For
a tollgate that already trusts its configured mints, this is acceptable as
an interim measure.

**Files to modify (Approach B only):**
- `src/tollwallet/tollwallet.go` — strip DLEQ in `Receive()`
- `src/tollwallet/tollwallet_test.go` — add test for DLEQ stripping

## Build & Test Commands

From `src/` directory:

```bash
gofmt -l .          # must print nothing
go vet ./...
go build ./...
go test -race -count=1 -tags testenv ./...
```

If the change touches the config schema or captive-portal contract, also run
from the repo root:

```bash
node tests/contract/js-schema-lint.mjs
bash tests/contract/build-purity.sh
```

## CHANGELOG Entry

Every user-visible change needs a CHANGELOG.md entry under `[Unreleased]`.
Use the `Fixed` category for the DLEQ bug fix. Match the existing entries'
wrapped style with a bold lead-in phrase and PR link:

```markdown
- **DLEQ proof verification across keyset rotations.** When a mint
  rotates its keysets, tokens minted under a now-inactive keyset
  correctly verify their DLEQ proofs against the original keyset's
  public keys rather than the active keyset
  ([#N](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/N)).
```

## PR Requirements

From AGENTS.md:
- **One logical change per PR**, targeting `main`.
- **No coding-assistant attribution** in commits or PR bodies.
- **Do NOT commit planning documents** (`*-plan.md`, scratch notes, etc.).
- PRs are squash-merged; the maintainer rewrites the final commit message.

## Testing Environment

- Router: `10.47.41.1` (SSH password auth, set by wizard)
- Test mint: `https://mint.minibits.cash/Bitcoin`
- Test with real Cashu tokens from Minibits wallet (these include DLEQ
  proofs by default)
- The mint has rotated keysets, so tokens from older keysets will trigger
  the bug

## File Inventory

### Key Source Files

| File | Purpose |
|------|---------|
| `src/tollwallet/tollwallet.go:112-141` | `Receive()` — calls `w.wallet.Receive()` |
| `src/merchant/merchant.go:437-498` | `PurchaseSession()` — calls `tollwallet.Receive()` |
| `src/go.mod:31` | Replace directive for gonuts-tollgate |
| `CHANGELOG.md` | Where to add the fix entry |

### Upstream Files (in gonuts-tollgate)

| File | Purpose |
|------|---------|
| `wallet/wallet.go:586-670` | `Receive()` — calls `VerifyProofsDLEQ` with only active keyset |
| `wallet/wallet.go:675-697` | `ReceiveHTLC()` — same bug |
| `cashu/nuts/nut12/nut12.go:13-28` | `VerifyProofsDLEQ()` — single keyset, ignores `proof.Id` |
| `wallet/keyset.go:14-41` | `GetMintActiveKeyset()` — fetches only active keyset with keys |
| `wallet/keyset.go:43-64` | `GetMintInactiveKeysets()` — fetches inactive keysets WITHOUT keys |
| `cashu/cashu.go:138-155` | `Proof` and `DLEQProof` types |
| `cashu/cashu.go:422-446` | `TokenV4.Proofs()` — returns copies with DLEQ |
| `cashu/cashu.go:334-380` | `NewTokenV4()` — `includeDLEQ` parameter controls DLEQ in output |

## Detailed Investigation Notes

The full investigation, including code snippets for all four approaches,
alternative designs, test plans, and migration paths, is in
`DLEQ-FIX-PLAN.md` (not committed — it's a working document).

## Glossary

- **DLEQ Proof** (NUT-12): A zero-knowledge proof that the mint signed a
  blinded message without seeing the unblinded message. Optional in Cashu
  tokens.
- **Keyset**: A set of public keys (one per denomination) used by a mint.
  Mints can rotate keysets — old ones become inactive but remain valid for
  verifying existing proofs.
- **Keyset ID**: A hash of the keyset's public keys, embedded in each proof
  (`proof.Id`). Used to identify which keyset a proof was minted under.
- **Proof**: A Cashu ecash unit — contains amount, secret, signature (C),
  and optionally a DLEQ proof.
- **Swap**: The operation of exchanging proofs for new proofs. The mint
  validates old proofs during swap.