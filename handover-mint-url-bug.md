# Handover: Mint URL Exact-Match Bug in calculateAllotment

## Issue
https://github.com/OpenTollGate/tollgate-module-basic-go/issues/251

## Summary

`calculateAllotment()` in `src/merchant/merchant.go:628` uses `==` (exact string match) to find the mint configuration for a payment. A proper URL comparison function `mintURLMatches()` already exists in `src/tollwallet/tollwallet.go:278` but is not used here.

## Root Cause

When a user pays with a Cashu token, the backend:
1. `PurchaseSession()` calls `tollwallet.Receive()` — this WORKS because tollwallet uses `mintURLMatches()` for URL comparison
2. `PurchaseSession()` calls `calculateAllotment()` — this FAILS because it uses `==`

The token's mint URL (from `paymentCashuToken.Mint()`) may differ from the config URL by:
- Trailing slash: `https://mint.minibits.cash/Bitcoin/` vs `https://mint.minibits.cash/Bitcoin`
- Host case: `https://Mint.Minibits.Cash/Bitcoin` vs `https://mint.minibits.cash/Bitcoin`
- Path encoding

Result: sats are received by the wallet, but allotment calculation fails → user gets error, no session granted, sats consumed.

## Affected Code

**Bug location:** `src/merchant/merchant.go:625-648`
```go
func (m *Merchant) calculateAllotment(amountSats uint64, mintURL string) (uint64, error) {
    var mintConfig *config_manager.MintConfig
    for _, mint := range m.config.AcceptedMints {
        if mint.URL == mintURL {  // BUG: exact string match
            mintConfig = &mint
            break
        }
    }
    if mintConfig == nil {
        return 0, fmt.Errorf("mint configuration not found for URL: %s", mintURL)
    }
    // ...
}
```

**Existing fix:** `src/tollwallet/tollwallet.go:278-289`
```go
func mintURLMatches(a, b string) bool {
    ua, err := url.Parse(a)
    if err != nil { return a == b }
    ub, err := url.Parse(b)
    if err != nil { return a == b }
    return strings.EqualFold(ua.Host, ub.Host) &&
        ua.Scheme == ub.Scheme &&
        ua.Path == ub.Path
}
```

## Proposed Fix

Three options (pick one):

### Option A: Export from tollwallet (simplest)
1. Export `mintURLMatches` as `MintURLMatches` in tollwallet
2. Import tollwallet in merchant.go and call `tollwallet.MintURLMatches(mint.URL, mintURL)`

### Option B: Move to utils package (cleanest)
1. Move `mintURLMatches` to `src/utils/mint.go`
2. Import utils in both tollwallet.go and merchant.go
3. Replace all usages

### Option C: Inline in merchant.go (least disruptive)
1. Add a local `mintURLMatches` function in merchant.go (or a shared helper)
2. Replace `mint.URL == mintURL` with `mintURLMatches(mint.URL, mintURL)`

## Testing

1. Unit test: call `calculateAllotment()` with a mintURL that has a trailing slash vs config without
2. Unit test: call with different host casing
3. Integration test on router: pay with token from mint whose URL has trailing slash
4. Run: `go test -race -count=1 -tags testenv ./...` from `src/`

## Build & Deploy

- Go workspace: run all tooling from `src/`, NOT repo root
- Build: `go build ./...` from `src/`
- Test: `gofmt -l . && go vet ./... && go test -race -count=1 -tags testenv ./...`
- PR target: `main` branch
- Follow CONTRIBUTING.md and PR-REVIEW.md
- Add CHANGELOG.md entry under `[Unreleased]` → `Fixed`