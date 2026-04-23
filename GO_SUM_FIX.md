# Fix: Missing go.sum Entries in Sub-Modules

## Problem

Running `go build .` from `src/merchant/` or `src/tollwallet/` fails with:

```
missing go.sum entry for module providing package github.com/Origami74/gonuts-tollgate/cashu
missing go.sum entry for module providing package github.com/Origami74/gonuts-tollgate/wallet
```

Running `go mod tidy` to fix it fails with:

```
github.com/ltcsuite/ltcd/chaincfg/chainhash: ambiguous import: found package in multiple modules
```

## Root Cause

### Issue 1: Incomplete go.sum files

The project is a Go multi-module monorepo. The CI builds from the parent `src/` module:

```yaml
# .github/workflows/build-package.yml
go build -C src -o bin/tollgate-wrt main.go
```

The parent `src/go.sum` contains entries for `gonuts-tollgate v0.6.1`. However, the sub-modules (`src/merchant/go.sum`, `src/tollwallet/go.sum`) were **never** populated with these entries. The `go.sum` files in the sub-modules are incomplete because `go mod tidy` was only ever run from the parent `src/` module.

This was masked because nobody built from the sub-modules directly — the CI and all build scripts use the parent module.

### Issue 2: Ambiguous import in gonuts-tollgate's transitive deps

`gonuts-tollgate v0.6.1` depends on `lightningnetwork/lnd`, which depends on `lightninglabs/neutrino`, which pulls in **two modules** that both provide the same Go package path:

```
gonuts-tollgate v0.6.1
  └── lightningnetwork/lnd v0.18.2-beta
        └── lightninglabs/neutrino
              ├── ltcsuite/ltcd v0.0.0-20190101042124     ← parent module
              │   └── chaincfg/chainhash/  (sub-package)    ← provides package
              │
              └── ltcsuite/ltcd/chaincfg/chainhash v1.0.2  ← standalone module
                  └── chainhash.go                         ← provides same package
```

Both modules provide the package `github.com/ltcsuite/ltcd/chaincfg/chainhash`. Go cannot determine which one to use and refuses to proceed.

This is a bug in `gonuts-tollgate`'s dependency tree (it was resolved upstream in `ltcsuite/ltcd` by splitting into sub-modules, but `neutrino` still depends on the old monolithic version alongside the new split module).

### The Catch-22

- You can't `go build` because go.sum is missing entries
- You can't `go mod tidy` to add the entries because of the ambiguous import

## Fix

### Step 1: Exclude the conflicting module version

Add an `exclude` directive to `src/tollwallet/go.mod` and `src/merchant/go.mod`:

```
exclude github.com/ltcsuite/ltcd v0.0.0-20190101042124-f37f8bf35796
```

This tells Go to never use the old monolithic `ltcd` pseudoversion, leaving only the standalone `ltcsuite/ltcd/chaincfg/chainhash v1.0.2` module. This resolves the ambiguity.

### Step 2: Run `go mod tidy` in sub-modules

```bash
cd src/tollwallet && go mod tidy
cd src/merchant && go mod tidy
```

This populates the missing `gonuts-tollgate` entries in each sub-module's `go.sum`.

### Step 3: Run `go mod tidy` in parent module

```bash
cd src && go mod tidy
```

Keeps the aggregate parent `go.sum` consistent with the sub-modules.

### Step 4: Verify

```bash
cd src && go build ./...
cd src/merchant && go build .
cd src/tollwallet && go build .
```

## Why this doesn't affect the CI

The CI builds from `src/` (the parent module), which has a complete `go.sum`. The parent module's `go.sum` already contains `gonuts-tollgate` entries and has already resolved the `ltcd` ambiguity (likely because it was set up before the conflicting version was introduced, or because the parent module's dependency graph happens to resolve it differently).

## Why the exclude is safe

`ltcsuite/ltcd v0.0.0-20190101042124-f37f8bf35796` is a 2019 Litecoin node implementation. This project (TollGate) deals exclusively with Bitcoin Cashu mints and has no need for Litecoin chain logic. The `neutrino` dependency that pulls it in only uses it for chain scanning infrastructure that isn't exercised by `gonuts-tollgate`. Excluding it has no runtime impact.
