# BoltDB In-Process Locking Issue in gonuts-tollgate

## Problem Statement

When gonuts-tollgate's `wallet.LoadWallet()` is called twice in the same process on the same wallet directory, the second call **blocks forever**. This prevents the degraded-to-full merchant upgrade path from completing when the application boots offline and internet recovers later.

## Root Cause

### The Bug: `nil` Options in `InitBolt`

**File:** `wallet/storage/bolt.go`, line 38

```go
func InitBolt(path string) (*BoltDB, error) {
    db, err := bolt.Open(filepath.Join(path, "wallet.db"), 0600, nil)
    //                                                       ^^^
    //                                            nil = DefaultOptions
    //                                            Timeout: 0 = INFINITE WAIT
```

When `nil` is passed as the options argument, bbolt uses `DefaultOptions`:

```go
// From bbolt db.go:
var DefaultOptions = &Options{
    Timeout:      0,  // <-- wait indefinitely for the file lock
    ...
}
```

### How bbolt File Locking Works

BoltDB uses `flock(LOCK_EX | LOCK_NB)` (exclusive, non-blocking) to prevent concurrent writers from corrupting the database file:

```go
// From bbolt bolt_unix.go:
func flock(db *DB, exclusive bool, timeout time.Duration) error {
    flag := syscall.LOCK_NB
    if exclusive {
        flag |= syscall.LOCK_EX
    }
    for {
        err := syscall.Flock(int(fd), flag)
        if err == nil {
            return nil                              // lock acquired
        } else if err != syscall.EWOULDBLOCK {
            return err                              // unexpected error
        }
        if timeout != 0 && time.Since(t) > timeout-flockRetryTimeout {
            return errors.ErrTimeout                // <-- never reached when timeout=0
        }
        time.Sleep(flockRetryTimeout)              // 50ms pause, then retry
    }
}
```

The lock is acquired at `bolt.Open()` time and held until `db.Close()` is called. On Linux, `flock()` exclusive locks from different processes conflict — only one process can hold `LOCK_EX` on a file at a time. Within the **same process**, `flock()` locks on different file descriptors to the same file should theoretically not conflict, but in practice bbolt's locking logic treats them as conflicting (the lock acquisition returns `EWOULDBLOCK`), causing the infinite retry loop.

### The Call Chain

```
bolt.Open(path, 0600, nil)        // nil options → Timeout: 0
  → flock(db, exclusive=true, timeout=0)
    → syscall.Flock(fd, LOCK_EX | LOCK_NB)
    → returns EWOULDBLOCK (another handle holds lock)
    → timeout == 0 → skip timeout check
    → sleep 50ms
    → retry FOREVER
```

## Reproduction

### Via Integration Test

The file `src/merchant/offline_wallet_integration_test.go` in the `tollgate-module-basic-go` repo contains `TestIntegration_RecoveryAndUpgrade` which reproduces this issue:

```
Phase 1: Start proxy, fund wallet at testDir, stop proxy
Phase 2: Create degraded merchant (opens BoltDB at testDir/wallet.db)
Phase 3: Restart proxy, trigger proactive checks → onFirstReachable callback fires
         → newFullMerchant() → tollwallet.New() → wallet.LoadWallet() → bolt.Open()
         → BLOCKS FOREVER because degraded merchant still has BoltDB open
```

### Minimal Reproduction in Go

```go
package main

import (
    "fmt"
    "time"
    bolt "go.etcd.io/bbolt"
)

func main() {
    path := "/tmp/test.db"

    // First open — succeeds, holds lock
    db1, err := bolt.Open(path, 0600, nil)
    if err != nil {
        panic(err)
    }
    fmt.Println("db1 opened")

    // Second open — BLOCKS FOREVER in same process
    fmt.Println("attempting to open db2...")
    db2, err := bolt.Open(path, 0600, nil)  // <-- hangs here
    if err != nil {
        fmt.Println("db2 error:", err)
    } else {
        fmt.Println("db2 opened")
        db2.Close()
    }

    db1.Close()
}
```

## Impact on the Downstream Application

### The Production Code Path

In `tollgate-module-basic-go`, when a router boots offline:

```
1. merchant.New() detects no reachable mints
2. NewMerchantDegradedWithWallet()
   → DefaultWalletFactory()
     → tollwallet.New()
       → wallet.LoadWallet()
         → bolt.Open("wallet.db", nil)    ← EXCLUSIVE LOCK ACQUIRED
         → wallet returned, stored in deg.wallet
         → Lock held indefinitely (no Close/Shutdown exposed)

3. deg.wallet keeps the bolt.DB open and locked

4. [time passes, internet comes back]

5. MintHealthTracker.runProactiveCheck() detects mint recovery
6. onFirstReachable callback fires in a goroutine:
   → newFullMerchant()
     → tollwallet.New()
       → wallet.LoadWallet()
         → bolt.Open("wallet.db", nil)    ← BLOCKS FOREVER
         → flock() returns EWOULDBLOCK
         → timeout=0, retries infinitely
         → GOROUTINE IS STUCK

7. Degraded merchant continues working (it has the DB)
8. The upgrade to full merchant NEVER completes
9. The router stays in degraded mode until rebooted
```

### What Works

- Offline boot with existing wallet: PASS
- Balance reporting offline: PASS
- Payment creation offline (SendWithOverpayment): PASS
- The degraded merchant is fully functional offline

### What Doesn't Work

- The degraded → full merchant upgrade after internet recovery: **BLOCKS FOREVER**
- The `onFirstReachable` goroutine is permanently stuck and never freed

## The Fix

### Change Required

**File:** `wallet/storage/bolt.go`, line 38

```go
// Before:
func InitBolt(path string) (*BoltDB, error) {
    db, err := bolt.Open(filepath.Join(path, "wallet.db"), 0600, nil)
```

```go
// After:
func InitBolt(path string) (*BoltDB, error) {
    db, err := bolt.Open(filepath.Join(path, "wallet.db"), 0600, &bolt.Options{Timeout: 5 * time.Second})
```

With a 5-second timeout:
1. The second `bolt.Open()` will retry for 5 seconds
2. If the lock can't be acquired, it returns `errors.ErrTimeout`
3. `wallet.LoadWallet()` returns an error
4. The `onFirstReachable` callback can log the error and return cleanly
5. The goroutine is not leaked
6. The upgrade can be retried on the next proactive check cycle

### Why 5 Seconds

- bbolt retries every 50ms (`flockRetryTimeout = 50 * time.Millisecond`)
- 5 seconds = ~100 retry attempts
- Enough time for a brief lock contention scenario
- Short enough to not block the proactive check goroutine excessively
- Matches the timeout that the gonuts codebase already intended (the gonuts `storage.InitBolt` at v0.6.1 happens to pass `nil`, but the concept of a timeout is standard bbolt practice)

### Testing the Fix

#### Unit Test

Add a test in `wallet/storage/` that verifies `InitBolt` fails gracefully when the DB is already locked:

```go
func TestInitBoltLockTimeout(t *testing.T) {
    dir := t.TempDir()

    // First open — holds lock
    db1, err := InitBolt(dir)
    if err != nil {
        t.Fatalf("first InitBolt: %v", err)
    }
    defer db1.Close()

    // Second open — should fail with timeout, not hang
    start := time.Now()
    _, err = InitBolt(dir)
    elapsed := time.Since(start)

    if err == nil {
        t.Fatal("expected error from second InitBolt (DB locked)")
    }
    if elapsed > 10*time.Second {
        t.Fatalf("InitBolt took too long (%v) — timeout not working?", elapsed)
    }
    if elapsed < 3*time.Second {
        t.Logf("WARNING: InitBolt returned quickly (%v) — lock behavior may differ on this platform", elapsed)
    }
    t.Logf("InitBolt correctly failed after %v: %v", elapsed, err)
}
```

#### Integration Test

After applying the fix, re-run the integration test suite in `tollgate-module-basic-go`:

```bash
cd src/merchant && go test -v -count=1 -tags=integration -run TestIntegration_RecoveryAndUpgrade -timeout 60s ./...
```

Before the fix: the test passes but logs "Full merchant creation: DEFERRED (BoltDB locking)" and the `newFullMerchant` call blocks for 10 seconds before the test gives up.

After the fix: the `newFullMerchant` call should either:
- Fail quickly with a timeout error (degraded merchant still holds lock), logged cleanly
- OR succeed if the lock contention is resolved within the timeout

The key validation is that `newFullMerchant` **no longer blocks forever** — it returns within the timeout period either successfully or with an error.

## Related Code References

| File | Lines | Description |
|------|-------|-------------|
| `wallet/storage/bolt.go` | 37-38 | `InitBolt` — the function with the bug (`nil` options) |
| `wallet/storage/bolt.go` | 56-58 | `BoltDB.Close()` — releases the flock |
| `wallet/wallet.go` | 73-76 | `InitStorage` — calls `InitBolt` |
| `wallet/wallet.go` | 78-87 | `LoadWallet` — calls `InitStorage`, acquires lock |
| `wallet/wallet.go` | 168-170 | `Wallet.Shutdown()` — calls `db.Close()`, releases lock |
| bbolt `db.go` | 248 | `flock(db, !readOnly, options.Timeout)` — where the block happens |
| bbolt `db.go` | 1274-1276 | `Options.Timeout` — "When set to zero it will wait indefinitely" |
| bbolt `db.go` | 1347-1348 | `DefaultOptions.Timeout = 0` — infinite wait |
| bbolt `bolt_unix.go` | 17-45 | `flock()` — the retry loop that blocks forever when timeout=0 |
