# Rebase Plan for PR #140 (`pr-e-degraded-mode`) onto main

## Context

PR #140 was a single squash commit bundling the entire PR chain (merchant_types + health tracker + degraded mode). It was branched from `f17c547` (the Revert of #124), which is 9 commits behind current main. Both #138 (merchant_types) and #139 (health tracker) are now merged to main.

## Decisions

- **USM/CLI field naming:** `merchantProvider` (main's convention), not `merchant` (PR #140's)
- **USM nil guard:** Preserve PR #139's `if merchantProvider == nil` check
- **main.go approach:** Start from main's version, layer PR #140's degraded-mode architecture on top
- **Import alias:** `merchant_types "github.com/..."` (aliased, PR #140's style — needed for the provider adapter)

## Phase 1: Initiate rebase

```bash
git fetch github main pr-e-degraded-mode
git checkout -b pr-e-degraded-mode-rebase github/pr-e-degraded-mode
git rebase github/main
```

Produces 11 conflicted files.

## Phase 2: Resolve conflicts (file-by-file)

### 2.1 `src/main.go` — CRITICAL (manual merge)

**Strategy:** Start from main (HEAD), apply PR #140's changes.

Changes to layer from PR #140 onto main's base:

1. **Import:** Add `merchant_types "github.com/..."` alias. Keep `"net/url"` (needed by `isLocalOrigin`). Remove `"os/exec"` (old getMacAddress used it, main's doesn't).
2. **Import comment:** Remove `"context" // Added for context.Background()` → plain `"context"`
3. **Global vars:** Replace `merchantInstance` + `merchantProvider` + `healthTracker` globals with PR #140's `merchantProvider *merchantTypesProvider` single global
4. **Provider types:** Add `merchantTypesProvider` struct, `GetMerchant()`, `swapMerchant()`, `registerReachableSetChangedCallback()` from PR #140
5. **Remove `getMerchant()` helper:** Replace all `getMerchant()` calls with `merchantProvider.inner.GetMerchant()` (PR #140's pattern)
6. **`init()` TLS:** Keep PR #140's formatting (whitespace-only diff), apply PR #140's degraded boot sequence
7. **`init()` merchant creation:** Replace main's `merchant.New()` + `healthTracker` + `SetOnFirstReachable` with PR #140's simpler `merchant.New()` + `merchantProvider` + `OnUpgrade`/`registerReachableSetChangedCallback` pattern
8. **`initUpstreamDetector()`:** Pass `merchantProvider` (pointer adapter, not `merchantTypesProvider{merchantProvider}` value)
9. **`initCLIServer()`:** Pass `merchantProvider.inner` (to get `*merchant.MutexMerchantProvider`)
10. **`WriteTimeout`:** 15s → 120s (PR #140's change)
11. **Keep from main:** `getMacAddress` (Go-native + ARP), `getIP` (localhost-only headers), `isLocalRequest`, `isLocalOrigin`, `privateCIDRs`, `http.MaxBytesReader` body limit, all PR #104 security fixes

### 2.2 `src/merchant/merchant.go` — Semantic (spent-token check)

Take PR #140's `errors.Is(err, tollwallet.ErrTokenAlreadySpent)` at line 415. The guard above it (`!errors.Is` → `MarkUnreachable`) is already correct from auto-merge.

### 2.3 `src/upstream_session_manager/upstream_session_manager.go` — 3 conflicts

1. **Import:** Take PR #140's aliased `merchant_types "github.com/..."`
2. **Struct field:** Keep `merchantProvider` (main/HEAD), reject `merchant` (PR #140)
3. **Nil check:** Keep HEAD's `if merchantProvider == nil` guard (PR #139 safety), keep constructor body but use `merchantProvider:` assignment

### 2.4 `src/upstream_session_manager/session.go` — 2 conflicts

1. **Import:** Take PR #140's aliased import
2. **Struct field:** Keep `merchantProvider` (HEAD). All `s.merchant.` → `s.merchantProvider.`

### 2.5 `src/upstream_session_manager/merchant_provider_test.go` — 7 conflicts

All are `merchant` → `merchantProvider` field name. Keep HEAD's naming:

- `usm.merchantProvider` not `usm.merchant`
- `session.merchantProvider` not `session.merchant`
- `{merchantProvider: p}` not `{merchant: p}`

### 2.6 `src/merchant_types/types_test.go` — Trivial (add/add)

Both branches added identical content. Remove trailing conflict markers — keep test content up to line 98.

### 2.7-2.11 `go.mod` / `go.sum` — Mechanical

Take HEAD (main's newer deps) for all conflict blocks:

- `src/merchant/go.mod` — take HEAD's ltcd, ginkgo lines
- `src/tollwallet/go.mod` — take HEAD's btclog/btcwallet/lnd versions, remove ginkgo per HEAD
- `src/go.sum`, `src/merchant/go.sum`, `src/tollwallet/go.sum` — accept HEAD for all, then `go mod tidy`

## Phase 3: Post-conflict rename pass (non-conflicted but needs naming fix)

### 3.1 `src/cli/server.go`

- Struct field: `merchant` → `merchantProvider`
- Constructor: `merchant: merchantProvider` → `merchantProvider: merchantProvider`
- All `s.merchant.GetMerchant()` → `s.merchantProvider.GetMerchant()`
- `handleStatusCommand`: `s.merchant != nil` → `s.merchantProvider != nil`
- Keep PR #140's removal of `config`/`health` commands and the TODO removal
- Keep PR #140's `manualPauseDuration` nil-check addition

### 3.2 `src/cli/merchant_provider_test.go` (new file)

- `s.merchant` → `s.merchantProvider` throughout
- Helper `getCLIMerchantName`: `s.merchant.GetMerchant()` → `s.merchantProvider.GetMerchant()`

## Phase 4: Dependency tidy

```bash
cd src && go mod tidy
cd src/merchant && go mod tidy
cd src/tollwallet && go mod tidy
cd src/cli && go mod tidy
cd src/upstream_session_manager && go mod tidy
cd src/merchant_types && go mod tidy
```

## Phase 5: Verification

```bash
cd src && go build ./...
cd src && go test ./...
cd src && grep -rn '<<<<<<<\|=======\|>>>>>>>' .
```

## Phase 6: Push

```bash
git add -A && git rebase --continue
git push github pr-e-degraded-mode --force-with-lease
```

---

## Checklist

- [ ] Phase 1: Create rebase branch, start rebase
- [ ] Phase 2.1: Resolve `src/main.go` — layer PR #140 degraded mode onto main's security base
- [ ] Phase 2.2: Resolve `src/merchant/merchant.go` — take `errors.Is()` sentinel check
- [ ] Phase 2.3: Resolve `src/upstream_session_manager/upstream_session_manager.go` — keep `merchantProvider` + nil guard + aliased import
- [ ] Phase 2.4: Resolve `src/upstream_session_manager/session.go` — keep `merchantProvider` + aliased import
- [ ] Phase 2.5: Resolve `src/upstream_session_manager/merchant_provider_test.go` — keep `merchantProvider` naming
- [ ] Phase 2.6: Resolve `src/merchant_types/types_test.go` — remove trailing conflict markers
- [ ] Phase 2.7: Resolve `src/merchant/go.mod` — take HEAD's deps
- [ ] Phase 2.8: Resolve `src/tollwallet/go.mod` — take HEAD's deps
- [ ] Phase 2.9: Resolve `src/go.sum` — take HEAD, tidy after
- [ ] Phase 2.10: Resolve `src/merchant/go.sum` — take HEAD, tidy after
- [ ] Phase 2.11: Resolve `src/tollwallet/go.sum` — take HEAD, tidy after
- [ ] Phase 3.1: Rename `merchant` → `merchantProvider` in `src/cli/server.go`
- [ ] Phase 3.2: Rename `merchant` → `merchantProvider` in `src/cli/merchant_provider_test.go`
- [ ] Phase 4: Run `go mod tidy` in all modules
- [ ] Phase 5.1: `go build ./...` passes
- [ ] Phase 5.2: `go test ./...` passes
- [ ] Phase 5.3: No leftover conflict markers
- [ ] Phase 6: Force-push to update PR #140
