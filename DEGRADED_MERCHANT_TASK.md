# Task: Prevent Boot Loop When All Configured Mints Are Unreachable

**Related PR:** https://github.com/OpenTollGate/tollgate-module-basic-go/pull/99

You are implementing a fix for a critical bug where the tollgate-wrt service enters an infinite crash loop when all configured mints are unreachable. Your job is to follow these instructions precisely and implement the fix across the specified files. When done, report back what you changed and any issues encountered.

---

## Problem Description

When all mints in `/etc/tollgate/config.json` are unreachable, the service crashes on every startup and procd restart-loop it indefinitely. The crash happens because:

1. `merchant.New()` calls `tollwallet.New()` which calls `wallet.LoadWallet()` → `AddMint()` which makes an HTTP request to the mint
2. The mint returns an error (e.g., HTTP 404, connection refused, DNS failure)
3. The gonuts wallet library has a function `isNetworkError()` that only recognizes certain error patterns (DNS failures, connection refused, etc.). HTTP errors like 404 are NOT recognized as network errors, so they propagate as fatal errors
4. The error bubbles up: `AddMint()` → `LoadWallet()` → `tollwallet.New()` → `merchant.New()` → `main.init()` → `log.Fatal()`
5. OpenWrt procd restarts the service (`respawn 3 5 0` = unlimited restarts), creating an infinite crash loop

**Expected behavior:** The service should start, remain running (wireless gateway manager, upstream detector, AP management work independently of mints), and automatically initialize the wallet once a mint becomes reachable via the proactive health check.

---

## Architecture Overview

The relevant initialization chain:

```
main.go init()                          [main.go:79-116]
  └── merchant.New(configManager)       [merchant.go:65-114]
        ├── NewMintHealthTracker()      [mint_health_tracker.go:31-41]
        ├── RunInitialProbe()           [mint_health_tracker.go:86-104]
        ├── tollwallet.New()            [tollwallet.go:20-48] ← CRASHES HERE
        │     └── wallet.LoadWallet()
        │           └── AddMint()       ← HTTP call to mint, fails if unreachable
        ├── CreateAdvertisement()
        └── returns *Merchant
  ├── merchantInstance.StartPayoutRoutine()    [merchant.go:219-239]
  ├── merchantInstance.StartDataUsageMonitoring()
  ├── initCLIServer()                  [main.go:141-151]
  └── initUpstreamDetector()           [main.go:118-139]
```

Key interfaces and types:
- `MerchantInterface` (merchant.go:33-52) — the interface all merchants must implement
- `Merchant` (merchant.go:55-63) — the normal merchant with a working wallet
- `MintHealthTracker` (mint_health_tracker.go:22-29) — tracks which mints are reachable
- `merchantInstance` (main.go:40) — global variable of type `merchant.MerchantInterface`

---

## Solution Design

### Overview

When `RunInitialProbe()` finds no reachable mints, instead of crashing on `tollwallet.New()`, return a **degraded merchant** (a separate struct implementing `MerchantInterface`) that:
- Returns stub/error responses for wallet-dependent operations
- Keeps the proactive health checks running in the background
- Fires a callback when a mint becomes reachable, which upgrades to a full `Merchant`

### Recovery Flow

```
Service starts
  → RunInitialProbe()
  → No mints reachable?
    → YES: Return MerchantDegraded
           - HTTP server starts (returns "service unavailable" on purchase)
           - Proactive checks run every 5 minutes
           - After 3 consecutive successful probes, onFirstReachable fires
             → Initialize wallet, create full Merchant, swap into global
             → Start payout routine and data monitoring
    → NO:  Normal flow (unchanged)
```

---

## Files to Modify

### 1. `src/merchant/mint_health_tracker.go`

**Add** two fields to `MintHealthTracker`:

```go
type MintHealthTracker struct {
	mu                   sync.RWMutex
	reachableMints       map[string]bool
	consecutiveSuccesses map[string]uint8
	httpClient           *http.Client
	configProvider       mintConfigProvider
	recoveryThreshold    uint8
	onFirstReachable     func()   // NEW: callback fired once when any mint becomes reachable
	hadReachableMint     bool     // NEW: guards against firing callback multiple times
}
```

**Add** a setter method:

```go
func (t *MintHealthTracker) SetOnFirstReachable(callback func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onFirstReachable = callback
	t.hadReachableMint = false
}
```

**Modify** `runProactiveCheck()` to fire the callback. After the existing loop that iterates over mints (after line 130), add:

```go
	if !t.hadReachableMint && t.onFirstReachable != nil {
		for _, mint := range config.AcceptedMints {
			if t.reachableMints[mint.URL] {
				t.hadReachableMint = true
				cb := t.onFirstReachable
				t.mu.Unlock()
				go cb()
				t.mu.Lock()
				break
			}
		}
	}
```

**Important:** The callback must be fired OUTSIDE the lock to avoid deadlocks. Copy the callback reference, unlock, then call it in a goroutine (`go cb()`). Then re-acquire the lock (since the defer will try to unlock).

**Also modify** `RunInitialProbe()`: after the loop (after line 103), check if any mint became reachable and set `hadReachableMint` accordingly:

```go
	for _, mint := range config.AcceptedMints {
		if t.reachableMints[mint.URL] {
			t.hadReachableMint = true
			break
		}
	}
```

This ensures that if the initial probe already found reachable mints, the callback won't fire redundantly during proactive checks.

### 2. `src/merchant/merchant_degraded.go` (NEW FILE)

Create a new file with a `MerchantDegraded` struct implementing `MerchantInterface`. This is a stub merchant returned when no mints are reachable.

```go
package merchant

import (
	"fmt"
	"log"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/Origami74/gonuts-tollgate/cashu"
	"github.com/nbd-wtf/go-nostr"
)

type MerchantDegraded struct {
	configManager     *config_manager.ConfigManager
	mintHealthTracker *MintHealthTracker
}

func NewMerchantDegraded(configManager *config_manager.ConfigManager, mintHealthTracker *MintHealthTracker) *MerchantDegraded {
	return &MerchantDegraded{
		configManager:     configManager,
		mintHealthTracker: mintHealthTracker,
	}
}

func (m *MerchantDegraded) CreatePaymentToken(mintURL string, amount uint64) (string, error) {
	return "", fmt.Errorf("wallet not initialized: no reachable mints")
}

func (m *MerchantDegraded) CreatePaymentTokenWithOverpayment(mintURL string, amount uint64, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error) {
	return "", fmt.Errorf("wallet not initialized: no reachable mints")
}

func (m *MerchantDegraded) DrainMint(mintURL string) (string, uint64, error) {
	return "", 0, fmt.Errorf("wallet not initialized: no reachable mints")
}

func (m *MerchantDegraded) GetAcceptedMints() []config_manager.MintConfig {
	return m.mintHealthTracker.GetReachableMintConfigs()
}

func (m *MerchantDegraded) GetBalance() uint64 {
	return 0
}

func (m *MerchantDegraded) GetBalanceByMint(mintURL string) uint64 {
	return 0
}

func (m *MerchantDegraded) GetAllMintBalances() map[string]uint64 {
	return make(map[string]uint64)
}

func (m *MerchantDegraded) PurchaseSession(cashuToken string, macAddress string) (*nostr.Event, error) {
	noticeEvent, err := m.CreateNoticeEvent("error", "service-unavailable",
		"TollGate is initializing. No reachable mints. Please try again in a few minutes.", macAddress)
	if err != nil {
		return nil, fmt.Errorf("wallet not initialized and failed to create notice: %w", err)
	}
	return noticeEvent, nil
}

func (m *MerchantDegraded) GetAdvertisement() string {
	noticeEvent, err := m.CreateNoticeEvent("warning", "no-reachable-mints",
		"TollGate is initializing. No reachable mints detected. Service will auto-recover.", "")
	if err != nil {
		return fmt.Sprintf(`{"error": "no reachable mints: %v"}`, err)
	}
	bytes, err := json.Marshal(noticeEvent)
	if err != nil {
		return `{"error": "failed to marshal notice"}`
	}
	return string(bytes)
}

func (m *MerchantDegraded) StartPayoutRoutine() {
	log.Printf("WARNING: Payout routine not started — no reachable mints (degraded mode)")
}

func (m *MerchantDegraded) StartDataUsageMonitoring() {
	log.Printf("WARNING: Data usage monitoring not started — no reachable mints (degraded mode)")
}

func (m *MerchantDegraded) CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error) {
	identities := m.configManager.GetIdentities()
	if identities == nil {
		return nil, fmt.Errorf("identities config is nil")
	}
	merchantIdentity, err := identities.GetOwnedIdentity("merchant")
	if err != nil {
		return nil, fmt.Errorf("merchant identity not found: %w", err)
	}
	tollgatePubkey, err := nostr.GetPublicKey(merchantIdentity.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get public key: %w", err)
	}
	noticeEvent := &nostr.Event{
		Kind:      21023,
		PubKey:    tollgatePubkey,
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"level", level},
			{"code", code},
		},
		Content: message,
	}
	if customerPubkey != "" {
		noticeEvent.Tags = append(noticeEvent.Tags, nostr.Tag{"p", customerPubkey})
	}
	err = noticeEvent.Sign(merchantIdentity.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign notice event: %w", err)
	}
	return noticeEvent, nil
}

func (m *MerchantDegraded) GetSession(macAddress string) (*CustomerSession, error) {
	return nil, fmt.Errorf("wallet not initialized: no reachable mints")
}

func (m *MerchantDegraded) AddAllotment(macAddress, metric string, amount uint64) (*CustomerSession, error) {
	return nil, fmt.Errorf("wallet not initialized: no reachable mints")
}

func (m *MerchantDegraded) GetUsage(macAddress string) (string, error) {
	return "-1/-1", nil
}

func (m *MerchantDegraded) Fund(cashuToken string) (uint64, error) {
	return 0, fmt.Errorf("wallet not initialized: no reachable mints")
}
```

**Important:** This file needs these imports:
```go
import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/Origami74/gonuts-tollgate/cashu"  // may not be needed, check if PurchaseSession compiles without it
	"github.com/nbd-wtf/go-nostr"
)
```

Remove `cashu` import if `PurchaseSession` doesn't reference `cashu` types directly. The existing `Merchant.PurchaseSession` uses `cashu.DecodeToken` internally, but the degraded version doesn't decode the token, so it likely doesn't need it. Check after writing.

### 3. `src/merchant/merchant.go`

**Modify** `New()` function (lines 65-114). After `RunInitialProbe()` (line 75) and before `tollwallet.New()` (line 88), add a check:

Replace lines 73-114 with:

```go
	// Initialize mint health tracker and run initial probe
	mintHealthTracker := NewMintHealthTracker(configManager)
	mintHealthTracker.RunInitialProbe()

	reachableMints := mintHealthTracker.GetReachableMintConfigs()
	if len(reachableMints) == 0 {
		log.Printf("WARNING: No reachable mints detected. Starting in degraded mode. Service will auto-recover when a mint becomes available.")
		return NewMerchantDegraded(configManager, mintHealthTracker), nil
	}

	// Extract mint URLs from reachable mints only
	mintURLs := make([]string, len(reachableMints))
	for i, mint := range reachableMints {
		mintURLs[i] = mint.URL
	}

	log.Printf("Setting up wallet...")
	walletDirPath := filepath.Dir(configManager.ConfigFilePath)
	if err := os.MkdirAll(walletDirPath, 0700); err != nil {
		return nil, fmt.Errorf("failed to create wallet directory %s: %w", walletDirPath, err)
	}
	tw, walletErr := tollwallet.New(walletDirPath, mintURLs, false)

	if walletErr != nil {
		return nil, fmt.Errorf("failed to create wallet: %w", walletErr)
	}
	balance := tw.GetBalance()

	// Set advertisement
	advertisementStr, err := CreateAdvertisement(configManager, mintHealthTracker)
	if err != nil {
		return nil, fmt.Errorf("failed to create advertisement: %w", err)
	}

	log.Printf("Accepted Mints: %v", config.AcceptedMints)
	log.Printf("Wallet Balance: %d", balance)
	log.Printf("Advertisement: %s", advertisementStr)
	log.Printf("=== Merchant ready ===")

	return &Merchant{
		config:            config,
		configManager:     configManager,
		tollwallet:        *tw,
		mintHealthTracker: mintHealthTracker,
		advertisement:     advertisementStr,
		customerSessions:  make(map[string]*CustomerSession),
	}, nil
```

**Key changes:**
1. After `RunInitialProbe()`, check `mintHealthTracker.GetReachableMintConfigs()`
2. If empty, return `NewMerchantDegraded()` immediately — no wallet init, no crash
3. If non-empty, extract mint URLs from **reachable** mints only (not all configured mints)
4. Rename local `tollwallet` variable to `tw` to avoid shadowing the package name (existing code has this issue but it works; keeping it consistent)

### 4. `src/main.go`

**Modify** the `init()` function (lines 79-116). The changes are:

a) Change `merchantInstance` from a simple `var` to support atomic swap. Replace line 40:
```go
var merchantInstance merchant.MerchantInterface
```
with:
```go
var (
	merchantInstance     merchant.MerchantInterface
	merchantInstanceMu   sync.Mutex
)
```

b) Add a helper function to swap the merchant:
```go
func swapMerchant(newMerchant merchant.MerchantInterface) {
	merchantInstanceMu.Lock()
	defer merchantInstanceMu.Unlock()
	merchantInstance = newMerchant
}
```

c) Modify the init function around lines 103-109. Replace:
```go
	var err2 error
	merchantInstance, err2 = merchant.New(configManager)
	if err2 != nil {
		mainLogger.WithError(err2).Fatal("Failed to create merchant")
	}
	merchantInstance.StartPayoutRoutine()
	merchantInstance.StartDataUsageMonitoring()
```
with:
```go
	var err2 error
	merchantInstance, err2 = merchant.New(configManager)
	if err2 != nil {
		mainLogger.WithError(err2).Fatal("Failed to create merchant")
	}

	// If merchant started in degraded mode, register callback to upgrade
	// when a mint becomes reachable. The degraded merchant's StartPayoutRoutine
	// and StartDataUsageMonitoring are no-ops, so we don't call them here.
	if degraded, ok := merchantInstance.(*merchant.MerchantDegraded); ok {
		mainLogger.Warn("Merchant started in degraded mode — wallet will initialize when a mint becomes reachable")
		// The proactive checks are started inside merchant.New for the degraded path.
		// We need to set up the upgrade callback.
		// Access the health tracker via the degraded merchant and register the callback.
		// This is done by passing the callback through the degraded merchant.
		// For now, we need to expose the health tracker or add a method to MerchantDegraded.
	} else {
		merchantInstance.StartPayoutRoutine()
		merchantInstance.StartDataUsageMonitoring()
	}
```

**Wait — this approach has a problem.** The `MerchantDegraded` is returned from `merchant.New()` but we can't easily register the upgrade callback from `main.go` because the health tracker is a private field of `MerchantDegraded`.

**Better approach:** Move the upgrade logic into the `merchant` package itself. Add an `OnUpgrade(callback func(MerchantInterface))` method to `MerchantDegraded` that registers the callback, and call it from `merchant.New()` before returning.

**Revised approach for `merchant.go`:**

Add an `onUpgrade` field to `MerchantDegraded`:

```go
type MerchantDegraded struct {
	configManager     *config_manager.ConfigManager
	mintHealthTracker *MintHealthTracker
	onUpgrade         func(MerchantInterface)
}
```

Add a method:
```go
func (m *MerchantDegraded) OnUpgrade(callback func(MerchantInterface)) {
	m.onUpgrade = callback
}
```

In `merchant.New()`, after creating the degraded merchant and before returning it, register the callback:

```go
	if len(reachableMints) == 0 {
		log.Printf("WARNING: No reachable mints detected. Starting in degraded mode.")
		deg := NewMerchantDegraded(configManager, mintHealthTracker)
		mintHealthTracker.StartProactiveChecks()
		mintHealthTracker.SetOnFirstReachable(func() {
			log.Printf("Mint became reachable — attempting to upgrade from degraded mode")
			fullMerchant, err := newFullMerchant(configManager, mintHealthTracker)
			if err != nil {
				log.Printf("ERROR: Failed to upgrade from degraded mode: %v", err)
				return
			}
			if deg.onUpgrade != nil {
				deg.onUpgrade(fullMerchant)
			}
		})
		return deg, nil
	}
```

Extract the wallet init code into a helper function `newFullMerchant()`:

```go
func newFullMerchant(configManager *config_manager.ConfigManager, mintHealthTracker *MintHealthTracker) (MerchantInterface, error) {
	config := configManager.GetConfig()
	if config == nil {
		return nil, fmt.Errorf("main config is nil")
	}

	reachableMints := mintHealthTracker.GetReachableMintConfigs()
	if len(reachableMints) == 0 {
		return nil, fmt.Errorf("no reachable mints")
	}

	mintURLs := make([]string, len(reachableMints))
	for i, mint := range reachableMints {
		mintURLs[i] = mint.URL
	}

	log.Printf("Setting up wallet...")
	walletDirPath := filepath.Dir(configManager.ConfigFilePath)
	if err := os.MkdirAll(walletDirPath, 0700); err != nil {
		return nil, fmt.Errorf("failed to create wallet directory %s: %w", walletDirPath, err)
	}
	tw, walletErr := tollwallet.New(walletDirPath, mintURLs, false)
	if walletErr != nil {
		return nil, fmt.Errorf("failed to create wallet: %w", walletErr)
	}
	balance := tw.GetBalance()

	advertisementStr, err := CreateAdvertisement(configManager, mintHealthTracker)
	if err != nil {
		return nil, fmt.Errorf("failed to create advertisement: %w", err)
	}

	log.Printf("Accepted Mints: %v", config.AcceptedMints)
	log.Printf("Wallet Balance: %d", balance)
	log.Printf("Advertisement: %s", advertisementStr)
	log.Printf("=== Merchant ready ===")

	m := &Merchant{
		config:            config,
		configManager:     configManager,
		tollwallet:        *tw,
		mintHealthTracker: mintHealthTracker,
		advertisement:     advertisementStr,
		customerSessions:  make(map[string]*CustomerSession),
	}

	m.StartPayoutRoutine()
	m.StartDataUsageMonitoring()

	return m, nil
}
```

Then `New()` becomes:

```go
func New(configManager *config_manager.ConfigManager) (MerchantInterface, error) {
	log.Printf("=== Merchant Initializing ===")

	config := configManager.GetConfig()
	if config == nil {
		return nil, fmt.Errorf("main config is nil")
	}

	mintHealthTracker := NewMintHealthTracker(configManager)
	mintHealthTracker.RunInitialProbe()

	reachableMints := mintHealthTracker.GetReachableMintConfigs()
	if len(reachableMints) == 0 {
		log.Printf("WARNING: No reachable mints detected. Starting in degraded mode.")
		deg := NewMerchantDegraded(configManager, mintHealthTracker)
		mintHealthTracker.StartProactiveChecks()
		mintHealthTracker.SetOnFirstReachable(func() {
			log.Printf("Mint became reachable — attempting to upgrade from degraded mode")
			fullMerchant, err := newFullMerchant(configManager, mintHealthTracker)
			if err != nil {
				log.Printf("ERROR: Failed to upgrade from degraded mode: %v", err)
				return
			}
			if deg.onUpgrade != nil {
				deg.onUpgrade(fullMerchant)
			}
		})
		return deg, nil
	}

	return newFullMerchant(configManager, mintHealthTracker)
}
```

**Note:** `StartPayoutRoutine()` and `StartDataUsageMonitoring()` are now called inside `newFullMerchant()`, so remove them from `main.go:init()`.

**Revised `main.go` init():**

```go
	var err2 error
	merchantInstance, err2 = merchant.New(configManager)
	if err2 != nil {
		mainLogger.WithError(err2).Fatal("Failed to create merchant")
	}

	// If merchant started in degraded mode, register upgrade callback
	// to swap the global merchantInstance when wallet initializes
	if deg, ok := merchantInstance.(*merchant.MerchantDegraded); ok {
		mainLogger.Warn("Merchant started in degraded mode — wallet will initialize when a mint becomes reachable")
		deg.OnUpgrade(func(full merchant.MerchantInterface) {
			mainLogger.Info("Upgrading from degraded to full merchant")
			swapMerchant(full)
		})
	}
```

And add the swap helper + mutex:

```go
var (
	merchantInstance   merchant.MerchantInterface
	merchantInstanceMu sync.Mutex
)

func swapMerchant(newMerchant merchant.MerchantInterface) {
	merchantInstanceMu.Lock()
	defer merchantInstanceMu.Unlock()
	merchantInstance = newMerchant
}
```

Add `"sync"` to the imports in `main.go`.

**Important:** You also need to add `"sync"` to the imports in `main.go` since we use `sync.Mutex`. Check the existing imports — `sync` is NOT currently imported in main.go.

---

## Summary of All Changes

### `src/merchant/mint_health_tracker.go`
1. Add `onFirstReachable func()` and `hadReachableMint bool` fields to `MintHealthTracker`
2. Add `SetOnFirstReachable(callback func())` method
3. In `RunInitialProbe()`: after the mint loop, set `hadReachableMint = true` if any mint is reachable
4. In `runProactiveCheck()`: after the mint loop, check if any mint just became reachable and fire `onFirstReachable` (outside the lock, in a goroutine)

### `src/merchant/merchant_degraded.go` (NEW FILE)
1. Create `MerchantDegraded` struct implementing `MerchantInterface`
2. All wallet-dependent methods return errors or zero values
3. `CreateNoticeEvent()` works normally (only needs identities config)
4. `GetAdvertisement()` returns a notice event as JSON string
5. `PurchaseSession()` returns a notice event with code `"service-unavailable"`
6. `StartPayoutRoutine()` and `StartDataUsageMonitoring()` are no-ops (log warning)
7. Add `onUpgrade func(MerchantInterface)` field and `OnUpgrade()` method

### `src/merchant/merchant.go`
1. Extract wallet init code from `New()` into `newFullMerchant(configManager, mintHealthTracker)`
2. `newFullMerchant()` calls `StartPayoutRoutine()` and `StartDataUsageMonitoring()` before returning
3. `New()` calls `RunInitialProbe()`, checks for reachable mints, returns degraded if none
4. If degraded, starts proactive checks and registers `onFirstReachable` callback
5. The callback calls `newFullMerchant()` and fires `deg.onUpgrade()`
6. Remove `StartPayoutRoutine()` and `StartDataUsageMonitoring()` calls from `New()` (they're now in `newFullMerchant()`)

### `src/main.go`
1. Change `merchantInstance` to use `sync.Mutex` for thread-safe swapping
2. Add `swapMerchant()` helper function
3. After `merchant.New()`, type-assert to `*merchant.MerchantDegraded` and register `OnUpgrade` callback
4. Remove `merchantInstance.StartPayoutRoutine()` and `merchantInstance.StartDataUsageMonitoring()` from `init()` (they're now called inside `newFullMerchant()`)
5. Add `"sync"` to imports

---

## Testing

After implementing, run the existing tests:

```bash
cd src && go test ./merchant/ -v
```

All 21 existing tests must still pass.

Write new tests in `src/merchant/merchant_degraded_test.go`:

1. **TestMerchantDegraded_PurchaseSession_ReturnsNotice** — verify `PurchaseSession()` returns a notice event with kind 21023
2. **TestMerchantDegraded_GetAdvertisement_ReturnsNotice** — verify `GetAdvertisement()` returns a JSON string that unmarshals to a nostr.Event with kind 21023
3. **TestMerchantDegraded_GetBalance_ReturnsZero** — verify all balance methods return 0
4. **TestMerchantDegraded_StartPayoutRoutine_NoPanic** — verify no panic
5. **TestMerchantDegraded_StartDataUsageMonitoring_NoPanic** — verify no panic
6. **TestOnFirstReachable_FiredOnce** — verify the callback fires exactly once when a mint transitions from unreachable to reachable via `RunProactiveCheck()`
7. **TestOnFirstReachable_NotFiredIfInitiallyReachable** — verify callback does NOT fire if mints were already reachable after `RunInitialProbe()`
8. **TestNew_ReturnsDegradedWhenNoMintsReachable** — verify `New()` returns `*MerchantDegraded` when all mints fail the probe

**For tests that need identities:** The `CreateNoticeEvent` method requires a merchant identity from `configManager.GetIdentities()`. For tests that call methods which internally call `CreateNoticeEvent` (like `PurchaseSession` and `GetAdvertisement`), you have two options:
- Create a mock `ConfigManager` that returns a valid identity (check if `ConfigManager` is an interface or concrete type)
- If mocking is too complex, skip those specific tests and only test the simpler methods

**IMPORTANT:** Check how `config_manager.ConfigManager` works. If it's a concrete struct (not an interface), you may need to create a test helper that sets up a minimal config. Look at how the existing tests in `mint_health_tracker_test.go` handle this — they use `mockConfigProvider` which only implements `GetConfig()`. The degraded merchant also needs `GetIdentities()`. You may need to extend the mock or create a separate one.

---

## Constraints

- Do NOT add any comments to the code unless they are TODO comments referencing known issues
- Do NOT modify any files outside the four listed above
- Do NOT modify the `MerchantInterface` definition
- Do NOT modify the `Merchant` struct or any of its existing methods (except `New()`)
- Keep all existing log messages unchanged
- The `"log"` package (not logrus) is used in merchant.go — keep using it
- The existing `tollwallet` variable name in `New()` shadows the package name — rename to `tw` in `newFullMerchant()`
- The `MintHealthTracker` uses `sync.RWMutex` — the callback must NOT be fired while holding the write lock
- The callback fires from the proactive check goroutine — use `go cb()` to avoid blocking it
- Do NOT add `"encoding/json"` import to merchant_degraded.go unless `GetAdvertisement()` actually uses `json.Marshal`

---

## Verification

After implementing all changes, verify:

1. `cd src && go build ./...` — must compile without errors
2. `cd src && go test ./merchant/ -v` — all existing + new tests must pass
3. `cd src && go vet ./...` — no vet warnings
