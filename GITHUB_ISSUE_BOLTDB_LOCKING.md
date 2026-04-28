# `wallet.LoadWallet()` blocks forever when called twice in the same process

## Summary

`storage.InitBolt()` passes `nil` options to `bolt.Open()`, resulting in an infinite file lock timeout. When `wallet.LoadWallet()` is called a second time on the same wallet directory within the same process (e.g., during degraded-to-full merchant upgrade), the second call blocks forever waiting for the `flock()` that the first call still holds.

## Steps to Reproduce

1. Call `wallet.LoadWallet()` with a config pointing to some wallet directory — this succeeds and holds an exclusive `flock` on `wallet.db`.
2. Without closing the first wallet, call `wallet.LoadWallet()` again with the same wallet directory.
3. The second call blocks forever at `bolt.Open()` → `flock(LOCK_EX | LOCK_NB)` retries infinitely because `Options.Timeout` is `0`.

Minimal reproduction:

```go
dir := "/tmp/testwallet"

// First open — succeeds
cfg1 := wallet.Config{WalletPath: dir, CurrentMintURL: "https://some-mint.example.com"}
w1, _ := wallet.LoadWallet(cfg1)

// Second open — blocks forever
cfg2 := wallet.Config{WalletPath: dir, CurrentMintURL: "https://some-mint.example.com"}
w2, err := wallet.LoadWallet(cfg2)  // <-- never returns
```

## Expected Behavior

The second `bolt.Open()` should fail after a reasonable timeout (e.g., 5 seconds) with a `timeout` error, not block forever.

## Actual Behavior

The second `bolt.Open()` blocks indefinitely. The `flock()` retry loop in bbolt's `bolt_unix.go` never exits because `Options.Timeout` is `0` (infinite wait).

## Root Cause

`wallet/storage/bolt.go:38`:

```go
func InitBolt(path string) (*BoltDB, error) {
    db, err := bolt.Open(filepath.Join(path, "wallet.db"), 0600, nil)
    //                                                       ^^^
    //                                    nil → DefaultOptions → Timeout: 0
    //                                    0 = wait indefinitely for file lock
```

bbolt's `DefaultOptions`:

```go
var DefaultOptions = &Options{
    Timeout: 0,  // "When set to zero it will wait indefinitely for a lock."
}
```

## Impact

This affects downstream applications that use the wallet in a degraded-to-full recovery pattern:

1. Application boots offline → creates degraded wallet (opens BoltDB, acquires lock)
2. Internet comes back → recovery callback fires
3. Recovery callback tries to create a new full wallet via `wallet.LoadWallet()` on the same directory
4. `bolt.Open()` blocks forever → the recovery goroutine is permanently stuck
5. The application never upgrades from degraded to full mode

## Suggested Fix

Pass a timeout option to `bolt.Open()`:

```go
func InitBolt(path string) (*BoltDB, error) {
    db, err := bolt.Open(filepath.Join(path, "wallet.db"), 0600, &bolt.Options{Timeout: 5 * time.Second})
```

With a 5-second timeout, the second `bolt.Open()` will retry for 5 seconds and then return `errors.ErrTimeout` instead of blocking forever. The caller can handle the error gracefully (log it, retry later, etc.).

## Environment

- gonuts-tollgate version: v0.6.1
- bbolt version: v1.4.0 (resolved via go.mod)
- OS: Linux (tested on amd64, Go 1.24)
- The issue affects any platform using bbolt's `flock()`-based locking (Linux, macOS, BSD)
