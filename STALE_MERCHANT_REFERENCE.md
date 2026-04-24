# Bug: Stale Merchant Reference in Upstream Session Manager

## Problem

When the merchant upgrades from `MerchantDegraded` to a full `Merchant` via `swapMerchant()`, the `UpstreamSessionManager` (USM) continues using the stale degraded reference. The USM captures the merchant interface at construction time and never receives the updated reference.

## Root Cause

`swapMerchant()` in `main.go` only updates the global `merchantInstance` variable. The USM stores its own copy of the `merchant.MerchantInterface` at construction time:

```go
// src/upstream_session_manager/upstream_session_manager.go:28-34
type UpstreamSessionManager struct {
    configManager  *config_manager.ConfigManager
    merchant       merchant.MerchantInterface  // captured at construction, never updated
}
```

```go
// src/main.go:143-144
usmInstance, err := upstream_session_manager.NewUpstreamSessionManager(configManager, merchantInstance)
```

When `swapMerchant()` is called during the degraded→full upgrade:

```go
// src/main.go:49-53
func swapMerchant(newMerchant merchant.MerchantInterface) {
    merchantInstanceMu.Lock()
    defer merchantInstanceMu.Unlock()
    merchantInstance = newMerchant  // only updates global var
}
```

The USM's `merchant` field still points to the `MerchantDegraded` instance.

## Impact

Even if the kickstart deadlock (KICKSTART_DEADLOCK.md) were fixed and the merchant upgraded successfully, the USM would still fail to create payment tokens because it would call methods on the stale degraded reference.

Additionally, the CLI server has the same problem:

```go
// src/cli/server.go
type CLIServer struct {
    configManager *config_manager.ConfigManager
    merchant      merchant.MerchantInterface  // captured at construction
}
```

## Code Locations

| File | Line | What |
|------|------|------|
| `src/main.go` | 49-53 | `swapMerchant()` — only updates global |
| `src/main.go` | 143-144 | USM constructed with current `merchantInstance` |
| `src/main.go` | 157 | CLI server constructed with current `merchantInstance` |
| `src/upstream_session_manager/upstream_session_manager.go` | 28-34 | USM struct with captured `merchant` field |
| `src/cli/server.go` | 26 | CLI server struct with captured `merchant` field |

## Solution Direction

Either:

1. **Add a `SetMerchant()` method** to `UpstreamSessionManager` and `CLIServer`, called from the `OnUpgrade` callback in `main.go`
2. **Use an indirection pointer** — have the USM and CLI server read from a shared `atomic.Value` or `*MerchantInterface` pointer that `swapMerchant()` updates
3. **Move merchant access through a getter** — instead of storing the interface directly, store a reference to the global mutex+variable and read through it

Option 2 is cleanest — a `merchantProvider` interface with a `GetMerchant() MerchantInterface` method that returns the current value from the mutex-protected global.
