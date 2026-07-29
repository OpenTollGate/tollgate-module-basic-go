# Cashu Compatibility Matrix — Token Formats × Keyset Versions

This document explains every combination of Cashu token format and keyset
version, what it means, whether gonuts-tollgate supports it, and how to test it.

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

| # | Token | Keyset | Status in gonuts v0.7.6 | Status in cdk-go | Production relevance |
|---|-------|--------|------------------------|-------------------|---------------------|
| 1 | V1 | V1 | ⚠️ Decode only (no V1 encoder) | ✅ | Rare — legacy wallets |
| 2 | V1 | V2 | ⚠️ Decode only | ✅ | Very rare |
| 3 | V3 | V1 | ✅ Full support | ✅ | **Most common today** |
| 4 | V3 | V2 | ✅ Full support (fixed in v0.7.6) | ✅ | Growing — CDK 0.16+ mints |
| 5 | V4 | V1 | ✅ Full support | ✅ | Modern wallets |
| 6 | V4 | V2 | ✅ Full support (fixed in v0.7.6) | ✅ | **Future standard** |

### Detailed explanation per cell

#### Cell 1: V1 token + V1 keyset
- **What**: Legacy bare JSON token with 8-byte keyset ID
- **gonuts**: `DecodeToken` tries V4 (fails, no cashuB prefix), then V3 (fails,
  no cashuA prefix), then the bare JSON fallback parses it. Keyset operations
  use `BigEndian.Uint64(8_bytes)` which works correctly.
- **Status**: Decode works. Encoding V1 tokens is not implemented (no `NewTokenV1`).
  TollGate doesn't need to SEND V1 tokens — only receive them.
- **Risk**: A V1 token from an old wallet would be received correctly.

#### Cell 2: V1 token + V2 keyset
- **What**: Legacy bare JSON token with 33-byte keyset ID
- **gonuts**: Decode works (same V1 path). Swap would have used the V2 keyset
  ID in `DeriveKeysetPath` — **fixed in v0.7.6** (was silently truncated).
- **Status**: ✅ Fixed. Very rare in practice (V1 tokens + V2 keysets = old
  wallet + new mint).

#### Cell 3: V3 token + V1 keyset ← MOST COMMON TODAY
- **What**: `cashuA` base64(JSON) token with 8-byte keyset ID
- **gonuts**: Full support. Decode, encode, swap, receive, send — all work.
  `NewTokenV3()` creates these. `DecodeTokenV3()` parses them.
  `BigEndian.Uint64(8_bytes)` derives correct NUT-13 path.
- **Status**: ✅ Production-proven. Every currently known production mint
  (coinos.io, minibits.cash, testnut.cashu.exchange) uses V1 keysets.
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

// FIXED (v0.7.6):
if len(keysetBytes) <= 8 {
    keysetIdInt = binary.BigEndian.Uint64(keysetBytes) % (1<<31 - 1)
} else {
    h := sha256.Sum256(keysetBytes)
    keysetIdInt = binary.BigEndian.Uint64(h[:8]) % (1<<31 - 1)
}
```

The token format (V1/V3/V4) is irrelevant to the swap operation — the keyset
ID is extracted from the decoded proofs, and the derivation path is computed
from the keyset ID regardless of how the token was serialized.

## How to test each cell

All tests use the gonuts-tollgate library directly (no network needed for
decode tests; network needed for swap tests against live mints).
