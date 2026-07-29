# nut04.State Wire Format

## Source

- Library: github.com/Origami74/gonuts-tollgate v0.7.4 (replace-directed to github.com/OpenTollGate/gonuts-tollgate v0.7.4)
- Package: cashu/nuts/nut04
- File: cashu/nuts/nut04/nut04.go
- Version: v0.7.4

## Type Definition

```go
type State int

const (
    Unpaid  State = iota  // 0
    Paid                  // 1
    Issued                // 2
    Pending               // 3
    Unknown               // 4
)
```

## Constants

| Name | Value | String() Output |
|------|-------|-----------------|
| nut04.Unpaid | 0 | "UNPAID" |
| nut04.Paid | 1 | "PAID" |
| nut04.Issued | 2 | "ISSUED" |
| nut04.Pending | 3 | "PENDING" |
| nut04.Unknown | 4 | "unknown" (LOWERCASE - gotcha) |
| out-of-range (5, -1, etc.) | — | "unknown" (default case) |

## JSON Marshaling

**State has NO custom MarshalJSON/UnmarshalJSON** — it serializes as a raw integer (`0`, `1`, `2`, `3`, `4`).

**PostMintQuoteBolt11Response** has custom MarshalJSON/UnmarshalJSON on POINTER receiver only:
- Pointer marshaling: converts State to String() → uppercase string ("PAID", "ISSUED", etc.)
- Value marshaling: Go bypasses pointer method → raw integer (1, 2, etc.)
- Unmarshal: ONLY accepts string format; integers fail

## Critical Production Concern

`lightningQuoteRecord.CachedState nut04.State` is NOT persisted to disk. The on-disk struct is `persistedQuote` (quote_store.go:15-26) which explicitly excludes transient fields:

```go
// persistedQuote is the serializable subset of lightningQuoteRecord written to disk
// so quote state survives process restarts. Transient fields (Processing, CachedState,
// etc.) are intentionally excluded.
```

Therefore: refactoring CachedState's type CANNOT corrupt existing quotes.json files.

## Cashu NUT Specification

Per NUT-04/NUT-20 (https://cashubtc.github.io/nuts/04/):
- Canonical wire format: `"state": <str_enum[STATE]>` (uppercase strings)
- Example: `"state": "UNPAID"`
- Integer format from gonuts value-marshaling is non-spec

## Migration Target (MintQuoteState)

To preserve wire-format compatibility and align with spec:

1. Underlying type: `type MintQuoteState int` (sentinel value parity: 0=Unpaid, 1=Paid, 2=Issued, 3=Pending, 4=Unknown)
2. MarshalJSON: emit uppercase strings per Cashu spec (value receiver)
3. UnmarshalJSON: accept BOTH integers (0-4, backward compat) AND strings (case-insensitive, canonical)
4. String() method: return uppercase including "UNKNOWN" for StateUnknown (spec-compliant cleanup; gonuts's lowercase "unknown" was a bug)
5. Pattern based on protoc-gen-go-json + transitland-lib migration precedent

## Cross-Reference

- Task 1.3 characterization test: src/merchant/quotes_wireformat_test.go
- Task 2.1 implementation: src/tollwallet/port.go
- Task 4.2 refactor: src/merchant/lightning.go
