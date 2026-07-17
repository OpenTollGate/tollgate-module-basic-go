# Task: Fix DLEQ Proof Verification Bug in tollgate-module-basic-go

## Overview

You are fixing a bug where Cashu tokens from wallets that include DLEQ
proofs (NUT-12) are rejected with `"invalid DLEQ proof"` when the mint has
rotated its keysets. This blocks all Cashu token payments from wallets like
Minibits that embed DLEQ proofs by default.

## Repositories

1. **tollgate-module-basic-go** (the project you're working in):
   - GitHub: `https://github.com/OpenTollGate/tollgate-module-basic-go.git`
   - Clone: `git clone https://github.com/OpenTollGate/tollgate-module-basic-go.git`
   - Go code is under `src/` — run all Go tooling from `src/`, not the repo
     root.

2. **gonuts-tollgate** (the upstream Cashu library with the bug):
   - Original: `github.com/Origami74/gonuts-tollgate` (v0.6.1)
   - Already forked at: `github.com/OpenTollGate/gonuts-tollgate` (v0.7.1)
   - The fork is where you publish the fix.
   - Current replace directive in `src/go.mod`:
     ```
     github.com/Origami74/gonuts-tollgate => github.com/OpenTollGate/gonuts-tollgate v0.7.1
     ```

## The Bug

In `gonuts-tollgate`, `wallet.Receive()` calls
`nut12.VerifyProofsDLEQ(proofsToSwap, *keyset)` where `keyset` is obtained
from `w.getActiveKeyset(tokenMint)` — **only the mint's current active
keyset**.

`VerifyProofsDLEQ` (in `nut12.go`) does this per proof:
1. If `proof.DLEQ == nil` → **skip** (returns true, verification passes).
2. If `proof.DLEQ != nil` → look up `keyset.PublicKeys[proof.Amount]` and
   verify the DLEQ proof against that public key.

The critical bug: **`VerifyProofsDLEQ` only receives the active keyset.** It
does not check `proof.Id` (the keyset ID the proof was actually minted
under). If the mint has rotated its keyset since the wallet minted the
token, the proof's keyset ID differs from the active keyset ID. The public
keys won't match, and verification fails — even though the proof is
perfectly valid.

The same bug exists in `ReceiveHTLC` (another function in `wallet.go`).

### Affected Code Path

```
merchant.go:437  cashu.DecodeToken(token)
merchant.go:455  m.tollwallet.Receive(paymentCashuToken)
    └─ tollwallet.go:127  w.wallet.Receive(token, swapToTrusted)
        └─ wallet.go:590   w.getActiveKeyset(tokenMint)  ← only active keyset
        └─ wallet.go:596   nut12.VerifyProofsDLEQ(proofsToSwap, *keyset)  ← FAILS
```

### Additional Observation

`GetMintInactiveKeysets` (keyset.go) fetches inactive keysets but **does
not fetch their public keys** — it creates `WalletKeyset` structs with empty
`PublicKeys` maps. So even if you pass inactive keysets to
`VerifyProofsDLEQ`, they won't have the keys needed for verification. You
need to fetch keys on demand via `GetKeysetKeys(mintURL, keysetID)`.

**Important:** Line numbers above reference v0.6.1. The actual go.mod
replaces with v0.7.1, so line numbers may have shifted. Always verify
against the actual source code of the version you're patching.

## What To Do

### Preferred: Approach A — Patch gonuts-tollgate (Proper Fix)

1. **Clone the gonuts-tollgate fork:**
   ```bash
   git clone https://github.com/OpenTollGate/gonuts-tollgate.git
   cd gonuts-tollgate
   git checkout v0.7.1  # or the latest tag
   git checkout -b fix/dleq-keyset-rotation
   ```

2. **Add `VerifyProofsDLEQWithKeysets()` to `cashu/nuts/nut12/nut12.go`:**

   A new function that accepts a map of keysets keyed by ID, and for each
   proof, selects the correct keyset using `proof.Id`:

   ```go
   func VerifyProofsDLEQWithKeysets(
       proofs cashu.Proofs,
       activeKeyset crypto.WalletKeyset,
       keysetsByID map[string]crypto.WalletKeyset,
   ) bool {
       for _, proof := range proofs {
           if proof.DLEQ == nil {
               continue
           }
           var keyset crypto.WalletKeyset
           if proof.Id == activeKeyset.Id {
               keyset = activeKeyset
           } else if ks, ok := keysetsByID[proof.Id]; ok {
               keyset = ks
           } else {
               return false // unknown keyset — can't verify
           }
           pubkey, ok := keyset.PublicKeys[proof.Amount]
           if !ok {
               return false
           }
           if !VerifyProofDLEQ(proof, pubkey) {
               return false
           }
       }
       return true
   }
   ```

3. **Update `wallet.Receive()` in `wallet/wallet.go`:**

   Before calling DLEQ verification, build a map of keysets keyed by ID.
   For any proof with `DLEQ != nil` whose `proof.Id` doesn't match the
   active keyset, fetch keys via `GetKeysetKeys(tokenMint, proof.Id)`:

   ```go
   // Build keyset map for DLEQ verification
   keysetsByID := map[string]crypto.WalletKeyset{
       keyset.Id: *keyset,
   }
   for _, proof := range proofsToSwap {
       if proof.DLEQ == nil {
           continue
       }
       if _, ok := keysetsByID[proof.Id]; ok {
           continue
       }
       // Fetch keys for this keyset ID
       keys, err := GetKeysetKeys(tokenMint, proof.Id)
       if err == nil {
           keysetsByID[proof.Id] = crypto.WalletKeyset{
               Id:         proof.Id,
               MintURL:    tokenMint,
               PublicKeys: keys,
           }
       }
   }
   if !nut12.VerifyProofsDLEQWithKeysets(proofsToSwap, *keyset, keysetsByID) {
       return 0, errors.New("invalid DLEQ proof")
   }
   ```

4. **Apply the same fix to `ReceiveHTLC()`** in the same file.

5. **Optionally update `GetMintInactiveKeysets`** in `wallet/keyset.go` to
   fetch public keys for inactive keysets, so they're available without
   on-demand HTTP calls. This is an optimization, not a requirement.

6. **Tag and publish the patched version:**
   ```bash
   git push origin fix/dleq-keyset-rotation
   # Create PR, merge, then tag:
   git tag v0.7.2
   git push origin v0.7.2
   ```

7. **Update tollgate-module-basic-go:**
   ```bash
   cd tollgate-module-basic-go/src
   # Update the replace directive in go.mod to v0.7.2
   # Change: github.com/Origami74/gonuts-tollgate => github.com/OpenTollGate/gonuts-tollgate v0.7.2
   go mod tidy
   ```

### Fallback: Approach B — Strip DLEQ Proofs (Quick Workaround)

If patching the upstream library is too complex, you can strip DLEQ proofs
before calling `wallet.Receive()`. This is simpler but loses the DLEQ
cryptographic guarantee.

In `src/tollwallet/tollwallet.go`, in the `Receive()` function, before
calling `w.wallet.Receive(token, swapToTrusted)`:

```go
// Strip DLEQ proofs to avoid "invalid DLEQ proof" errors when the
// mint has rotated keysets. The mint verifies proof validity during
// the swap operation, so DLEQ is an optional client-side check.
proofs := token.Proofs()
for i := range proofs {
    proofs[i].DLEQ = nil
}
strippedToken, err := cashu.NewTokenV4(proofs, token.Mint(), cashu.Sat, false)
if err != nil {
    return 0, fmt.Errorf("failed to reconstruct token without DLEQ: %w", err)
}
// Use strippedToken instead of token for the rest of Receive()
```

**Note:** The `cashu.Token` interface's `Proofs()` method returns copies, so
you must reconstruct a new token via `cashu.NewTokenV4()`. You need to
verify that the `cashu.NewTokenV4` function is available and its signature
matches — check the actual source in the version you're using.

**Trade-off:** The mint still validates proofs during the swap operation
server-side. DLEQ is a client-side integrity check. For a tollgate that
already trusts its configured mints, this is acceptable as an interim
measure.

If using Approach B, you may also want to add a config option
`skip_dleq_verification` (default: `true`) so operators can re-enable DLEQ
verification once the proper fix is in place. See `DLEQ-FIX-PLAN.md` for
details on the config approach.

## Build & Test

From the `src/` directory:

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

Add an entry under `[Unreleased]` → `### Fixed` in `CHANGELOG.md`. Match the
existing entries' style (bold lead-in, wrapped, PR link):

```markdown
- **DLEQ proof verification across keyset rotations.** When a mint
  rotates its keysets, tokens minted under a now-inactive keyset
  correctly verify their DLEQ proofs against the original keyset's
  public keys rather than the active keyset
  ([#N](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/N)).
```

If using Approach B instead:

```markdown
- **DLEQ proof verification workaround for keyset rotations.** Cashu
  tokens from wallets that include DLEQ proofs (NUT-12) were rejected
  with "invalid DLEQ proof" when the mint had rotated its keysets.
  DLEQ proofs are now stripped before verification to unblock payments
  ([#N](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/N)).
```

## PR Rules (from AGENTS.md)

- **One logical change per PR**, targeting `main`. No drive-by
  reformatting, no unrelated cleanups, no scope creep.
- **No coding-assistant attribution** in commits or PR bodies — no
  `Co-Authored-By: Claude`, no `Generated with ...` footers.
- **Do NOT commit planning documents** (`*-plan.md`, `PLAN-*.md`,
  `TODO-*.md`, scratch notes). Only production documentation is committed.
- PRs are squash-merged; the maintainer rewrites the final commit message.

### One PR per concern

If you do both Approach A (upstream patch) and the tollgate update, these
should be separate PRs:
1. PR to `OpenTollGate/gonuts-tollgate` — the library fix itself.
2. PR to `OpenTollGate/tollgate-module-basic-go` — update go.mod to use the
   patched version + CHANGELOG entry.

If you do Approach B (strip DLEQ in tollwallet), that's a single PR to
`tollgate-module-basic-go`.

## Testing Environment

- Router: `10.47.41.1` (SSH password auth, set by wizard)
- Test mint: `https://mint.minibits.cash/Bitcoin`
- Test with real Cashu tokens from Minibits wallet (these include DLEQ
  proofs by default)
- The mint has rotated keysets, so tokens from older keysets will trigger
  the bug
- Unit tests passing does not mean a router-visible change works — say so
  honestly in PR descriptions

## Reference Documents

- `DLEQ-FIX-PLAN.md` — Full investigation with code snippets for all
  approaches, test plans, and migration paths. NOT committed to git.
- `AGENTS.md` — Repository conventions and rules for AI coding agents.
- `CONTRIBUTING.md` — Contributing process details.
- `PR-REVIEW.md` — 13-criteria PR review checklist used by maintainers.