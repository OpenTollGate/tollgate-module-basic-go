# DLEQ Fix Plan — "invalid DLEQ proof" Error

## Problem Summary

When users submit Cashu tokens from wallets that include DLEQ proofs (NUT-12),
the tollgate backend rejects them with `"invalid DLEQ proof"`. This blocks all
Cashu token payments from wallets like Minibits that embed DLEQ proofs by
default.

### Root Cause

In `gonuts-tollgate@v0.6.1`, `wallet.Receive()` (wallet.go:586–598) calls
`nut12.VerifyProofsDLEQ(proofsToSwap, *keyset)` where `keyset` is obtained from
`w.getActiveKeyset(tokenMint)`. This fetches **only the mint's current active
keyset** — the one that is active *now*, not the one that was active when the
wallet minted the token.

`VerifyProofsDLEQ` (nut12.go:13–28) does the following per proof:

1. If `proof.DLEQ == nil` → **skip** (returns true).
2. If `proof.DLEQ != nil` → look up `keyset.PublicKeys[proof.Amount]` and verify
   the DLEQ proof against that public key.

The critical bug: **`VerifyProofsDLEQ` only receives the active keyset.** It does
not check `proof.Id` (the keyset ID the proof was minted under). If the mint has
rotated its keyset since the wallet minted the token, the proof's keyset ID
differs from the active keyset ID. The public keys won't match, and verification
fails — even though the proof is perfectly valid.

The same bug exists in `ReceiveHTLC` (wallet.go:679–685).

### Key Observation

`GetMintInactiveKeysets` (keyset.go:43–64) fetches inactive keysets but **does
not fetch their public keys** — it creates `WalletKeyset` structs with empty
`PublicKeys` maps (line 53–59). So even if we passed inactive keysets to
`VerifyProofsDLEQ`, they wouldn't have the keys needed for verification.

The `AddMint` function (wallet.go:173–210) does fetch keys for inactive keysets
and saves them to the DB, but it **clears the in-memory PublicKeys** for inactive
keysets (line 204: `keyset.PublicKeys = make(map[uint64]*secp256k1.PublicKey)`).
They're available in the DB but not in the in-memory `walletMint` struct.

### Affected Code Path

```
merchant.go:437  cashu.DecodeToken(token)
merchant.go:455  m.tollwallet.Receive(paymentCashuToken)
    └─ tollwallet.go:127  w.wallet.Receive(token, swapToTrusted)
        └─ wallet.go:590   w.getActiveKeyset(tokenMint)  ← only active keyset
        └─ wallet.go:596   nut12.VerifyProofsDLEQ(proofsToSwap, *keyset)  ← FAILS
```

### Reproduction

- Mint: `https://mint.minibits.cash/Bitcoin`
- User has a valid 5-sat token minted under a keyset that is no longer active.
- The wallet includes DLEQ proofs (standard for Minibits wallet).
- `getActiveKeyset` returns the *current* active keyset.
- `VerifyProofsDLEQ` tries to verify the DLEQ proof against the wrong public keys.
- Returns false → `"invalid DLEQ proof"`.

---

## Fix Approaches

### Approach 1: Strip DLEQ Proofs Before `wallet.Receive()` (Quick Workaround)

**Where:** `src/tollwallet/tollwallet.go`, in `Receive()` before calling
`w.wallet.Receive()`.

**How:** Iterate over `token.Proofs()`, set `proof.DLEQ = nil` for each proof,
then reconstruct the token. Since `VerifyProofsDLEQ` skips proofs with
`DLEQ == nil` (nut12.go:15–16), verification passes.

**Challenge:** The `cashu.Token` interface doesn't expose a way to mutate proofs
in-place. `TokenV4.Proofs()` returns a copy (cashu.go:422–446). We need to
either:

- **Option A:** Decode the token string, strip DLEQ from the decoded proofs,
  re-encode as a new V4 token, then pass to `wallet.Receive()`. This requires
  access to the original token string, not the parsed `cashu.Token` interface.
  The merchant calls `cashu.DecodeToken(cashuToken)` first (merchant.go:437),
  so we'd need to strip before decoding or change `Receive` to accept the raw
  string.
- **Option B:** Change `Receive` signature to accept the raw token string, do
  the stripping inside `tollwallet.Receive()`, then decode and pass to
  `wallet.Receive()`. This is cleaner but changes the API.
- **Option C:** Fork the upstream `wallet.Receive()` to add a `skipDLEQ`
  parameter. Already a fork (Origami74/gonuts-tollgate), so this is viable.

**Pros:**
- Minimal code change, no upstream dependency.
- Immediate fix — can be deployed without waiting for upstream changes.
- Safe: the mint will verify the proofs during the swap operation anyway
  (the mint checks that C is a valid signature on the secret).

**Cons:**
- Loses the DLEQ cryptographic guarantee (proof that the mint signed the
  blinded message without seeing the unblinded message).
- Doesn't fix the root cause — keyset rotation still breaks DLEQ verification
  for any code path that goes through upstream `wallet.Receive()`.

**Implementation (Option B — recommended for quick fix):**

```go
// In tollwallet.go

func (w *TollWallet) Receive(token cashu.Token) (uint64, error) {
    // Strip DLEQ proofs to avoid "invalid DLEQ proof" errors when the
    // mint has rotated keysets. The mint verifies proof validity during
    // the swap operation, so DLEQ is an optional client-side check.
    proofs := token.Proofs()
    for i := range proofs {
        proofs[i].DLEQ = nil
    }

    // Reconstruct token without DLEQ
    strippedToken, err := cashu.NewTokenV4(proofs, token.Mint(), cashu.Sat, false)
    if err != nil {
        return 0, fmt.Errorf("failed to reconstruct token without DLEQ: %w", err)
    }

    // ... rest of Receive() using strippedToken instead of token
}
```

**Risk:** Low. The mint's swap endpoint validates proofs server-side. DLEQ is a
client-side integrity check — skipping it means we trust the mint rather than
verifying locally. For a tollgate that already trusts the mint (it's in
`acceptedMints`), this is acceptable.

---

### Approach 2: Try All Keysets (Proper Fix)

**Where:** Upstream `gonuts-tollgate@v0.6.1`, `wallet.go` `Receive()` and/or
`nut12.go` `VerifyProofsDLEQ()`.

**How:** Instead of only checking the active keyset, look up the keyset by
`proof.Id` and verify against that specific keyset's public keys. This is the
correct NUT-12 behavior — each proof carries its keyset ID for exactly this
reason.

**Root cause in detail:** `VerifyProofsDLEQ` receives a single `keyset
crypto.WalletKeyset` and uses `keyset.PublicKeys[proof.Amount]` for all proofs.
But proofs from different keysets (e.g., after rotation) have different
`proof.Id` values. The function should use `proof.Id` to look up the correct
keyset.

**Implementation (upstream patch):**

```go
// nut12.go — new function that takes all keysets

func VerifyProofsDLEQWithKeysets(proofs cashu.Proofs, activeKeyset crypto.WalletKeyset, inactiveKeysets map[string]crypto.WalletKeyset) bool {
    for _, proof := range proofs {
        if proof.DLEQ == nil {
            continue
        }

        // Determine which keyset this proof belongs to
        var keyset crypto.WalletKeyset
        if proof.Id == activeKeyset.Id {
            keyset = activeKeyset
        } else if ks, ok := inactiveKeysets[proof.Id]; ok {
            keyset = ks
        } else {
            // Unknown keyset — can't verify
            return false
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

**Additional requirement:** `GetMintInactiveKeysets` must fetch public keys for
inactive keysets. Currently it doesn't (keyset.go:53–59 — no `GetKeysetKeys`
call). This means either:

1. Patch `GetMintInactiveKeysets` to also call `GetKeysetKeys` for each inactive
   keyset.
2. Or, in `wallet.Receive()`, fetch keys for the specific keyset IDs found in
   the proofs using `GetKeysetKeys(mintURL, proof.Id)`.

Option 2 is more efficient (only fetches keys for keysets actually referenced by
the proofs, not all inactive keysets).

**Implementation in wallet.go Receive():**

```go
func (w *Wallet) Receive(token cashu.Token, swapToTrusted bool) (uint64, error) {
    proofsToSwap := token.Proofs()
    tokenMint := token.Mint()

    keyset, err := w.getActiveKeyset(tokenMint)
    if err != nil {
        return 0, fmt.Errorf("could not get active keyset: %v", err)
    }

    // Build a map of keysets keyed by ID for DLEQ verification.
    // Start with the active keyset, then add inactive keysets with
    // their public keys fetched on demand.
    keysetsByID := map[string]crypto.WalletKeyset{
        keyset.Id: *keyset,
    }

    // Fetch keys for any proof keyset IDs not in the map
    for _, proof := range proofsToSwap {
        if proof.DLEQ == nil {
            continue
        }
        if _, ok := keysetsByID[proof.Id]; ok {
            continue
        }
        // Check if mint is known and has this keyset cached
        if mint, ok := w.mints[tokenMint]; ok {
            if ks, ok := mint.inactiveKeysets[proof.Id]; ok {
                // Fetch keys for this inactive keyset
                keys, err := GetKeysetKeys(tokenMint, proof.Id)
                if err == nil {
                    ks.PublicKeys = keys
                    keysetsByID[proof.Id] = ks
                }
                continue
            }
        }
        // Unknown mint or keyset — fetch from mint directly
        keys, err := GetKeysetKeys(tokenMint, proof.Id)
        if err == nil {
            keysetsByID[proof.Id] = crypto.WalletKeyset{
                Id:         proof.Id,
                MintURL:    tokenMint,
                PublicKeys: keys,
            }
        }
    }

    // verify DLEQ in proofs if present
    if !nut12.VerifyProofsDLEQWithKeysets(proofsToSwap, *keyset, keysetsByID) {
        return 0, errors.New("invalid DLEQ proof")
    }
    // ... rest unchanged
}
```

**Pros:**
- Correct behavior — verifies DLEQ against the keyset the proof was actually
  minted under.
- Preserves the cryptographic guarantee.
- Fixes the root cause for all code paths.

**Cons:**
- Requires patching the upstream library (already a fork, so feasible).
- Additional HTTP calls to fetch keys for inactive keysets (one per unique
  keyset ID in the proofs — usually just 1).
- More complex implementation.
- Requires updating `go.mod` to point to a new fork version.

---

### Approach 3: Config Option to Skip DLEQ Verification

**Where:** `src/config_manager/config_manager_config.go` (add config field),
`src/tollwallet/tollwallet.go` (use config), and potentially upstream
`wallet.go` (add `SkipDLEQ` to `Config`).

**How:** Add a boolean config option `skip_dleq_verification` (default: `false`).
When `true`, strip DLEQ proofs before calling `wallet.Receive()`.

**Config schema change:**

```go
// config_manager_config.go

type Config struct {
    // ... existing fields ...
    SkipDLEQVerification bool `json:"skip_dleq_verification,omitempty"`
}
```

**TollWallet change:**

```go
type TollWallet struct {
    wallet                     *wallet.Wallet
    acceptedMints              []string
    allowAndSwapUntrustedMints bool
    skipDLEQVerification       bool
    registeredMints            map[string]bool
    mu                         sync.Mutex
}

func New(walletPath string, acceptedMints []string, allowAndSwapUntrustedMints bool, skipDLEQVerification bool) (*TollWallet, error) {
    // ...
    tw := &TollWallet{
        wallet:                     cashuWallet,
        acceptedMints:              acceptedMints,
        allowAndSwapUntrustedMints: allowAndSwapUntrustedMints,
        skipDLEQVerification:       skipDLEQVerification,
        registeredMints:            map[string]bool{normalizeMintURL(acceptedMints[0]): true},
    }
    // ...
}

func (w *TollWallet) Receive(token cashu.Token) (uint64, error) {
    if w.skipDLEQVerification {
        proofs := token.Proofs()
        for i := range proofs {
            proofs[i].DLEQ = nil
        }
        // Reconstruct token without DLEQ
        // ... (same as Approach 1)
    }
    // ... rest unchanged
}
```

**Callers to update:**
- `src/merchant/merchant.go:127` — `tollwallet.New(walletDirPath, mintURLs, false, cfg.SkipDLEQVerification)`
- `src/merchant/merchant_degraded.go:203` — `tollwallet.New(walletPath, mintURLs, false, cfg.SkipDLEQVerification)`

**Pros:**
- Operator can choose: strict DLEQ verification (secure) or lenient (compatible).
- Default `false` preserves current behavior (backwards compatible).
- Can be enabled per-deployment without code changes.
- Useful for troubleshooting.

**Cons:**
- Doesn't fix the root cause — just makes it configurable.
- Requires config schema migration and documentation.
- Operators need to know about the option to use it.

---

### Approach 4: Wizard-Side Fix (Strip DLEQ in Portal JS)

**Where:** `net4sats-wizard-go/portal/assets/splash-C0y8YH4o.js` (built JS)
or the source TS/TSX files if they exist.

**Analysis of wizard portal code:**

The portal's Cashu payment flow (from the built JS):
1. User pastes token in input field (variable `C`).
2. `pc(C)` validates the token — decodes it, extracts proofs, checks amounts.
3. On submit, `Ge(C.trim())` sends the raw token string to the backend via
   `POST /` with `Content-Type: text/plain`.
4. The backend (`merchant.go`) calls `cashu.DecodeToken(cashuToken)` then
   `tollwallet.Receive()`.

The portal sends the **raw token string** — it does not modify proofs or DLEQ.
The `pc()` function uses `@cashu/cashu-ts` (`fs` = `decodeToken`) to validate
and extract amounts, but the actual submission sends the original string.

**How to strip DLEQ client-side:**

```javascript
// Before submitting, decode token, strip DLEQ, re-encode
async function stripDLEQ(tokenString) {
    const decoded = decodeToken(tokenString);
    if (decoded.token) {
        // V3 token
        for (let proof of decoded.token.flatMap(t => t.proofs || [])) {
            delete proof.dleq;
        }
    } else if (decoded.proofs) {
        // V4 token
        for (let proof of decoded.proofs) {
            delete proof.dleq;
        }
    }
    return getEncodedToken(decoded);
}

// In the submit handler:
const stripped = await stripDLEQ(C.trim());
const result = await Ge(stripped);
```

**Pros:**
- Fixes the issue without backend changes.
- DLEQ proofs never reach the backend.
- No config needed.

**Cons:**
- The wizard portal ships pre-built JS (`splash-C0y8YH4o.js`) — there are no
  source `.ts`/`.tsx` files in the repo. Changes would need to be made in the
  source repo (likely `net4sats-wizard-go` has a separate build step or source
  in another location).
- Modifying minified JS is fragile and error-prone.
- Doesn't help other clients (direct API users, other wizards).
- The portal embeds `@cashu/cashu-ts` which has `decodeToken` and
  `getEncodedToken` functions — these are available in the bundle.
- **This approach is not recommended as the primary fix** — it should be a
  secondary defense-in-depth measure after the backend fix.

---

## Recommended Implementation Plan

### Phase 1: Immediate Fix (Approach 1 + 3 combined)

**Goal:** Unblock Cashu payments within hours, with config control.

1. **Add config option** `skip_dleq_verification` to
   `config_manager_config.go` (default: `true` — we want to fix the issue
   immediately for all deployments).
2. **Update `TollWallet`** to accept `skipDLEQVerification` parameter.
3. **In `tollwallet.Receive()`**, when `skipDLEQVerification` is true:
   - Extract proofs from token.
   - Set `proof.DLEQ = nil` for all proofs.
   - Reconstruct token via `cashu.NewTokenV4(proofs, token.Mint(), cashu.Sat, false)`.
   - Pass reconstructed token to `w.wallet.Receive()`.
4. **Update callers** in `merchant.go` and `merchant_degraded.go` to pass the
   config value.
5. **Add unit test** that creates a token with DLEQ proofs, calls `Receive()`
   with `skipDLEQVerification=true`, and verifies the token is accepted.
6. **Update config schema** documentation and defaults.

**Files to modify:**
- `src/config_manager/config_manager_config.go` — add `SkipDLEQVerification` field
- `src/tollwallet/tollwallet.go` — add field, update `New()`, update `Receive()`
- `src/merchant/merchant.go` — pass config to `tollwallet.New()`
- `src/merchant/merchant_degraded.go` — pass config to `tollwallet.New()`
- `src/tollwallet/tollwallet_test.go` — add test for DLEQ stripping

### Phase 2: Proper Fix (Approach 2)

**Goal:** Correctly verify DLEQ against the proof's actual keyset.

1. **Fork/patch upstream** `gonuts-tollgate` to add
   `VerifyProofsDLEQWithKeysets()` that accepts a map of keysets keyed by ID.
2. **Patch `wallet.Receive()`** to fetch keys for keyset IDs found in proofs
   (using `GetKeysetKeys(mintURL, proof.Id)`) and pass all keysets to the new
   verification function.
3. **Patch `ReceiveHTLC()`** with the same fix.
4. **Bump `go.mod`** to the new fork version.
5. **Set `skip_dleq_verification` default to `false`** once the proper fix is
   in place.
6. **Add integration test** with a real mint that has rotated keysets.

**Files to modify (upstream):**
- `cashu/nuts/nut12/nut12.go` — add `VerifyProofsDLEQWithKeysets()`
- `wallet/wallet.go` — update `Receive()` and `ReceiveHTLC()` to fetch keys per
  proof keyset ID and call the new function
- `wallet/keyset.go` — optionally update `GetMintInactiveKeysets` to fetch keys

**Files to modify (tollgate):**
- `src/go.mod` — bump upstream version
- `src/config_manager/config_manager_config.go` — change default to `false`
- `src/tollwallet/tollwallet_test.go` — update tests

### Phase 3: Wizard-Side Defense (Approach 4)

**Goal:** Strip DLEQ at the portal as defense-in-depth.

1. **Locate portal source** — the built JS is at
   `net4sats-wizard-go/portal/assets/splash-C0y8YH4o.js`. Source files are not
   in the repo (likely built from a separate UI repo or a `src/` directory not
   present here).
2. **In the portal source**, before `Ge(C.trim())`, decode the token, strip
   DLEQ from all proofs, re-encode, and submit the stripped token.
3. **Rebuild** the portal assets.

**Note:** This phase depends on finding the portal source. The wizard repo only
contains built assets. The source may be in a separate repo or build pipeline.

---

## Key Design Decisions

### Why strip DLEQ rather than verify against all keysets in Phase 1?

1. **Speed:** Stripping DLEQ is a one-liner per proof. Fetching keys for
   inactive keysets requires HTTP calls to the mint, which adds latency and
   failure modes.
2. **No upstream changes:** Stripping can be done entirely in
   `tollwallet.go` without modifying the upstream library.
3. **Security is acceptable:** The mint verifies proof validity during the swap
   operation (the swap endpoint checks that C is a valid signature). DLEQ is a
   client-side check that the mint didn't see the unblinded message — important
   for trustless wallet-to-wallet transfers, but less critical for a tollgate
   that trusts its configured mints.
4. **Reversibility:** With the config option, operators can re-enable DLEQ
   verification after Phase 2 deploys the proper fix.

### Why default `skip_dleq_verification` to `true` in Phase 1?

The current behavior is broken — DLEQ verification fails for valid tokens
whenever the mint rotates keysets. This affects all users with wallets that
include DLEQ proofs (Minibits, Enno, and most modern Cashu wallets). Defaulting
to `true` immediately unblocks payments. Once Phase 2 lands, the default can
return to `false`.

### Why not patch `VerifyProofsDLEQ` to use `proof.Id` directly?

The function signature `VerifyProofsDLEQ(proofs, keyset)` only receives one
keyset. To use `proof.Id`, we'd need to either:
- Change the signature (breaks API compatibility).
- Add a new function (cleaner, but requires wallet.go changes too).

Either way, the caller (`wallet.Receive()`) needs to fetch keys for the proof's
keyset ID. That's the bulk of the work, and it belongs in `wallet.go` where we
have access to the mint URL and the `GetKeysetKeys` function.

---

## Test Plan

### Phase 1 Tests

1. **Unit test: DLEQ stripping**
   - Create a `Proof` with `DLEQ` set.
   - Call `Receive()` with `skipDLEQVerification=true`.
   - Verify the proof passed to `wallet.Receive()` has `DLEQ == nil`.
   - Verify the function doesn't return `"invalid DLEQ proof"`.

2. **Unit test: DLEQ preserved when disabled**
   - Same setup, but `skipDLEQVerification=false`.
   - Verify DLEQ is not stripped (current behavior preserved).

3. **Config test**
   - Verify `SkipDLEQVerification` defaults to `true`.
   - Verify config file with `skip_dleq_verification: false` is respected.

### Phase 2 Tests

4. **Integration test: keyset rotation**
   - Create proofs under keyset A.
   - Rotate to keyset B (make A inactive).
   - Call `Receive()` with DLEQ proofs from keyset A.
   - Verify DLEQ is checked against keyset A's public keys, not B's.
   - Verify the token is accepted.

5. **Integration test: unknown keyset**
   - Create proofs under a keyset that doesn't exist on the mint.
   - Verify `Receive()` returns `"invalid DLEQ proof"` (can't verify).

---

## Migration Path

1. **Phase 1 deployed:** `skip_dleq_verification` defaults to `true`. All
   Cashu payments work. DLEQ is not verified.
2. **Phase 2 deployed:** Upstream patched to verify DLEQ against correct
   keyset. Config default changes to `false`. DLEQ is verified correctly.
3. **Phase 3 deployed:** Portal strips DLEQ as defense-in-depth. Even if
   backend DLEQ verification is enabled, the portal never sends DLEQ proofs,
   avoiding any edge cases.

**Rollback:** Set `skip_dleq_verification: true` in config to immediately
disable DLEQ verification at any time.

---

## File Inventory

### Files to Modify

| File | Phase | Change |
|------|-------|--------|
| `src/config_manager/config_manager_config.go` | 1 | Add `SkipDLEQVerification` field |
| `src/tollwallet/tollwallet.go` | 1 | Add field, update `New()`, strip DLEQ in `Receive()` |
| `src/merchant/merchant.go` | 1 | Pass config to `tollwallet.New()` |
| `src/merchant/merchant_degraded.go` | 1 | Pass config to `tollwallet.New()` |
| `src/tollwallet/tollwallet_test.go` | 1 | Add DLEQ stripping tests |
| `src/go.mod` | 2 | Bump upstream version |
| `src/config_manager/config_manager_config.go` | 2 | Change default to `false` |
| Upstream `nut12.go` | 2 | Add `VerifyProofsDLEQWithKeysets()` |
| Upstream `wallet.go` | 2 | Update `Receive()` and `ReceiveHTLC()` |
| Upstream `keyset.go` | 2 | Optionally fetch keys in `GetMintInactiveKeysets` |
| Portal source (TBD) | 3 | Strip DLEQ before submission |

### Key Files Analyzed (Read-Only)

| File | Purpose |
|------|---------|
| `gonuts-tollgate@v0.6.1/wallet/wallet.go:586-670` | `Receive()` — calls `VerifyProofsDLEQ` with only active keyset |
| `gonuts-tollgate@v0.6.1/wallet/wallet.go:675-697` | `ReceiveHTLC()` — same bug |
| `gonuts-tollgate@v0.6.1/cashu/nuts/nut12/nut12.go:13-28` | `VerifyProofsDLEQ()` — single keyset, ignores `proof.Id` |
| `gonuts-tollgate@v0.6.1/wallet/keyset.go:14-41` | `GetMintActiveKeyset()` — fetches only active keyset with keys |
| `gonuts-tollgate@v0.6.1/wallet/keyset.go:43-64` | `GetMintInactiveKeysets()` — fetches inactive keysets WITHOUT keys |
| `gonuts-tollgate@v0.6.1/wallet/keyset.go:83-172` | `getActiveKeyset()` — handles keyset rotation, caches in memory |
| `gonuts-tollgate@v0.6.1/cashu/cashu.go:138-155` | `Proof` and `DLEQProof` types |
| `gonuts-tollgate@v0.6.1/cashu/cashu.go:422-446` | `TokenV4.Proofs()` — returns copies with DLEQ |
| `gonuts-tollgate@v0.6.1/cashu/cashu.go:334-380` | `NewTokenV4()` — `includeDLEQ` parameter controls DLEQ in output |
| `src/tollwallet/tollwallet.go:112-141` | `Receive()` — calls `w.wallet.Receive()` |
| `src/merchant/merchant.go:437-498` | `PurchaseSession()` — calls `tollwallet.Receive()` |
| `src/config_manager/config_manager_config.go:15-30` | `Config` struct — where to add `SkipDLEQVerification` |
| `net4sats-wizard-go/portal/assets/splash-C0y8YH4o.js` | Built portal JS — `Ge()` submits raw token, `pc()` validates |

---

## Glossary

- **DLEQ Proof** (NUT-12): A zero-knowledge proof that the mint signed a blinded
  message without seeing the unblinded message. Proves the mint didn't forge
  signatures. Optional in Cashu tokens.
- **Keyset**: A set of public keys (one per denomination) used by a mint. Mints
  can rotate keysets — old ones become inactive but remain valid for verifying
  existing proofs.
- **Keyset ID**: A hash of the keyset's public keys, embedded in each proof
  (`proof.Id`). Used to identify which keyset a proof was minted under.
- **Proof**: A Cashu ecash unit — contains amount, secret, signature (C), and
  optionally a DLEQ proof.
- **Swap**: The operation of exchanging proofs for new proofs (e.g., to change
  keyset or split amounts). The mint validates old proofs during swap.