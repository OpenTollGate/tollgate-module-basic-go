# Cashu Compatibility Matrix — Token Formats × Keyset Versions

This document explains every combination of Cashu token format and keyset
version, what it means, whether gonuts-tollgate supports it, and how to test it.

For the wallet-*backend* contract (the seam a replacement wallet must
implement, and its acceptance criteria), see
[docs/architecture/walletport-contract.md](architecture/walletport-contract.md).

## Dimensions

### Token Formats (how the token is serialized for transport)

| Format | Prefix | Encoding | Spec Status | Example |
|--------|--------|----------|-------------|---------|
| **V1** | *(none)* | Bare JSON array | Deprecated | `[{\"proofs\":[...],\"mint\":\"...\"}]` |
| **V3** | `cashuA` | Base64url(JSON) | Current standard | `cashuAeyJ0b2tlbiI6...` |
| **V4** | `cashuB` | Base64url(CBOR) | Modern (compact) | `cashuBo2F0gaJhaUIA...` |

**Note**: There is no "V2 token format." The version numbers for tokens and
keysets are independent. V2 refers exclusively to keyset ID format.

### Keyset Versions (how the mint identifies its keyset)

| Version | Prefix | Length | Example | Used By |
|---------|--------|--------|---------|---------|
| **V1** | `00` | 8 bytes (16 hex chars) | `00107937db0cc865` | All production mints today |
| **V2** | `01` | 33 bytes (66 hex chars) | `01a1b2c3d4e5f6...` | CDK 0.16+ mints (future) |

V2 keyset IDs are longer because they include a sha256 hash of all public
keys, making them self-verifying (NUT-02).

## The 6 Combinations


> **Re-verified against `gonuts-tollgate` v0.13.0 (2026-10-06), code-level
> plus the executable suite** (`src/tollwallet/compatibility_matrix_test.go`
> and the round-trip/cross-vector tests, run under `-tags testenv` in
> `make go-battery` — all green on the pinned baseline). What changed
> since the v0.7.6 snapshot this matrix was written against:
>
> - **V1 (bare-JSON) tokens are no longer decoded by the master
>   `DecodeToken` path** (it tries V4, then V3, and stops) — cells 1/2
>   below are corrected accordingly. The executable suite skips the V1
>   cells with that exact rationale.
> - **NUT-13 keyset derivation was generalized** beyond the v0.7.6
>   length-guard: `keysetIdToBigInt` now parses the FULL id (hex or
>   base64) as a big integer mod 2^31−1 — no truncation at any length.
> - **V4 tokens with short keyset IDs** (coinos/minibits-style mints) are
>   resolved to full IDs in `Receive` before any swap
>   (`resolveShortKeysetIds`) — a post-v0.7.6 fix noted in cells 5/6.
>
> Wallet-behavior deltas outside this matrix's token×keyset scope (the
> fund-safety era: monotonic counters, canonical mint identity, the
> ambiguity no-retry policy, NUT-20 quote signatures) live in the fork's
> changelog, v0.8.0 → v0.13.0.

| # | Token | Keyset | Status in gonuts v0.7.6 | Status in cdk-go | Production relevance |
|---|-------|--------|------------------------|-------------------|---------------------|
| 1 | V1 | V1 | ❌ Not decoded by the master path (v0.13.0) | ✅ | Rare — legacy wallets |
| 2 | V1 | V2 | ❌ Not decoded by the master path (v0.13.0) | ✅ | Very rare |
| 3 | V3 | V1 | ✅ Full support | ✅ | **Most common today** |
| 4 | V3 | V2 | ✅ Full support (fixed in v0.7.6) | ✅ | Growing — CDK 0.16+ mints |
| 5 | V4 | V1 | ✅ Full support | ✅ | Modern wallets |
| 6 | V4 | V2 | ✅ Full support (fixed in v0.7.6) | ✅ | **Future standard** |

### Detailed explanation per cell

#### Cell 1: V1 token + V1 keyset
- **What**: Legacy bare JSON token with 8-byte keyset ID
- **gonuts (v0.13.0)**: `DecodeToken` tries V4 (fails, no cashuB prefix), then
  V3 (fails, no cashuA prefix) — and stops. The bare-JSON fallback that
  existed in the v0.7.6 era is gone; a V1 token is refused as an invalid
  token. The executable suite documents this exactly (the V1 cells skip
  with the rationale).
- **Status**: Not decoded. Encoding V1 tokens was never implemented. V1 is a
  deprecated format; if a legacy wallet ever sends one today, the payment
  fails visibly with `invalid token` rather than silently.
- **Risk**: accepted — deprecated format, no known production sender.

#### Cell 2: V1 token + V2 keyset
- **What**: Legacy bare JSON token with 33-byte keyset ID
- **gonuts (v0.13.0)**: not decoded (same master-path refusal as cell 1 —
  the v0.7.6-era bare-JSON fallback is gone). The V2-keyset derivation bug
  this cell originally tracked stays fixed for the formats that do decode.
- **Status**: ❌ Not decoded. Vanishingly rare (old wallet + new mint).

#### Cell 3: V3 token + V1 keyset ← MOST COMMON TODAY
- **What**: `cashuA` base64(JSON) token with 8-byte keyset ID
- **gonuts**: Full support. Decode, encode, swap, receive, send — all work.
  `NewTokenV3()` creates these. `DecodeTokenV3()` parses them.
  `BigEndian.Uint64(8_bytes)` derives correct NUT-13 path.
- **Status**: ✅ Production-proven. Every currently known production mint
  (coinos.io, minibits.cash, lnserver.com, macadamia.cash, westernbtc.com,
  kashu.me, cubabitcoin.org) uses V1 keysets.
  Every wallet that sends cashuA tokens uses V3 format with V1 keysets.

#### Cell 4: V3 token + V2 keyset ← GROWING
- **What**: `cashuA` base64(JSON) token with 33-byte keyset ID
- **gonuts**: Decode works (cashuA path). Swap was BROKEN before v0.7.6
  (`BigEndian.Uint64` truncated 33-byte ID to 8 bytes → wrong derivation path).
  **Fixed in v0.7.6** (hashes >8-byte IDs via sha256).
- **Status**: ✅ Fixed. This combination will become common as CDK 0.16+ mints
  proliferate and existing wallets (still sending V3 tokens) interact with them.

#### Cell 5: V4 token + V1 keyset
- **What**: `cashuB` base64(CBOR) token with 8-byte keyset ID
- **gonuts**: Full support. `DecodeTokenV4()` uses `cbor.Unmarshal` which
  correctly reads `json` struct tags. `NewTokenV4()` + `Serialize()` creates
  valid CBOR tokens. V1 keyset path works (same as Cell 3).
- **Status**: ✅ Working. Modern wallets (cashu-ts v4+, CDK wallets) send V4
  tokens by default. TollGate receives them correctly.
- **v0.13.0 nuance**: V4 tokens store keyset IDs as 8-byte short IDs;
  mints of the coinos/minibits shape embed short IDs that must be
  resolved to full IDs before any swap — `Receive` does this via
  `resolveShortKeysetIds` (the fix behind the portal #517/#545-era
  payment failures). No caller-side handling needed.

#### Cell 6: V4 token + V2 keyset ← FUTURE STANDARD
- **What**: `cashuB` base64(CBOR) token with 33-byte keyset ID
- **gonuts**: V4 decode works (CBOR). V2 keyset swap was BROKEN before v0.7.6.
  **Fixed in v0.7.6** (same fix as Cell 4 — the keyset ID length fix applies
  regardless of token format).
- **Status**: ✅ Fixed. This is the combination CDK 0.16+ mints will produce
  by default. When the ecosystem fully migrates to V2 keysets, all tokens
  will be V4+V2 (or V3+V2 from older wallets).

## What was broken before v0.7.6 and why

Only cells **4 and 6** (any token format + V2 keyset) were broken. The
root cause was in `DeriveKeysetPath` (NUT-13), not in token decode:

```go
// BROKEN (v0.7.4 and earlier):
bigEndianBytes := binary.BigEndian.Uint64(keysetBytes)
// For V2 IDs (33 bytes): reads first 8 bytes, ignores 25 bytes

// v0.7.6: length guard — ≤8 bytes reads direct, >8 bytes hashed first.

// CURRENT (v0.13.0), keysetIdToBigInt: the whole ID, any length, hex or
// base64, as one big integer — no truncation is possible by construction:
result.SetString(id, 16)            // or base64-decode first
result.Mod(result, big.NewInt(2147483647)) // 2^31 - 1
```

The token format (V1/V3/V4) is irrelevant to the swap operation — the keyset
ID is extracted from the decoded proofs, and the derivation path is computed
from the keyset ID regardless of how the token was serialized.

## How to test each cell

**Executable (no network, runs in every `make go-battery` under
`-tags testenv`):**
`TestCompatibilityMatrix` (all 6 cells through decode + field extraction —
the V1 cells skip with the documented rationale),
`TestV4RoundTripAllKeysets` / `TestV3RoundTripAllKeysets`
(encode → decode → extract for both keyset versions), and
`TestHashToCurveCrossVectors` (cross-implementation vectors).

**Live-mint (network, still pending for this re-verification):** swap
round-trips against a V2-keyset mint (CDK 0.16+) and a short-ID mint
(coinos/minibits class) — tracked as the open checklist item; the
code-level verification above covers the derivation and decode paths
those swaps exercise.
