# Plan: Mint Health Tracker with Two-Tier Reachability

## Problem

When configured mints become unreachable, the TollGate service goes down. Currently there is no mint reachability tracking — mints are assumed always available once configured. Failures during melt/payout are logged but not tracked, and all configured mints are always advertised to customers.

## Solution

Create a `MintHealthTracker` that maintains a dynamic "reachable mints" subset using two strategies:

1. **Proactive** (every 5 min): probes all configured mints via `GET /v1/info`. Can both remove unreachable mints and add recovered mints back (after 3 consecutive successes — see recovery threshold below)
2. **Reactive** (on failure): immediately removes a mint from the reachable set when operations fail

### Recovery Threshold (Hysteresis)

To avoid flapping (a mint briefly coming back then going down again), a mint must respond to **3 consecutive** successful probes before being re-added to the reachable set. This means a minimum ~15 minute recovery window at 5-minute probe intervals. Removal is always immediate (either reactive on failure or proactive on a failed probe).

## Reachability Definition

A mint is considered reachable if it responds to `GET {mintURL}/v1/info` with a successful HTTP status code (2xx). This is a lightweight probe that confirms the mint is alive without requiring token operations.

## Behavior

| Scenario | What happens |
|---|---|
| Mint goes down | Reactive: next operation failure triggers `MarkUnreachable()` → removed immediately. Proactive: next 5-min probe fails → removed and success counter reset to 0 |
| Mint comes back up | Mint must pass 3 consecutive proactive probes (~15 min). After the 3rd success, it is re-added to the reachable set |
| Mint flaps (up briefly, then down) | One failed probe resets the consecutive success counter to 0, so the mint is not prematurely re-added |
| Service starts | Initial probe runs synchronously in `New()` → reachable set populated before first advertisement |
| All mints unreachable | `GetAcceptedMints()` returns empty slice, advertisement has no `price_per_step` tags, portal shows no pricing |
| Payout for down mint | Skipped entirely (no goroutine churn / log spam) |

## What Stays Unchanged

- `config.json` is never modified
- `tollwallet.acceptedMints` stays as-is (wallet still trusts those mints for token receiving/swapping)
- `GetAllMintBalances()` / `DrainMint()` work on all wallet mints regardless of reachability
- The captive portal requires no changes — it reads the advertisement which will only contain reachable mints

## Files to Create

### `src/merchant/mint_health_tracker.go`

New file containing the `MintHealthTracker`:

```go
type MintHealthTracker struct {
    mu                    sync.RWMutex
    reachableMints        map[string]bool          // mint URL -> currently reachable
    consecutiveSuccesses  map[string]uint8         // mint URL -> consecutive successful probe count
    httpClient            *http.Client             // with ~5s timeout
    configManager         *config_manager.ConfigManager
    recoveryThreshold     uint8                    // consecutive successes required to re-add (default: 3)
}

func NewMintHealthTracker(configManager *config_manager.ConfigManager) *MintHealthTracker

// StartProactiveChecks runs a background goroutine that probes all
// configured mints every 5 minutes via GET {mintURL}/v1/info.
//
// Proactive probe behavior:
//   - Mint responds successfully → increment consecutiveSuccesses[url].
//     If count >= recoveryThreshold and mint not in reachable set → add it.
//   - Mint fails to respond → reset consecutiveSuccesses[url] to 0.
//     If mint is in reachable set → remove it.
func (t *MintHealthTracker) StartProactiveChecks()

// IsReachable returns true if the mint is currently in the reachable set
func (t *MintHealthTracker) IsReachable(mintURL string) bool

// GetReachableMintConfigs returns only the MintConfigs for reachable mints
func (t *MintHealthTracker) GetReachableMintConfigs() []config_manager.MintConfig

// MarkUnreachable reactively removes a mint from the reachable set.
// Called immediately when a mint operation fails.
// Also resets consecutiveSuccesses[url] to 0 so the mint must pass
// recoveryThreshold consecutive proactive probes before being re-added.
func (t *MintHealthTracker) MarkUnreachable(mintURL string)

// probeMint checks if a single mint responds to GET /v1/info
func (t *MintHealthTracker) probeMint(mintURL string) bool
```

## Files to Modify

### `src/merchant/merchant.go`

1. **`Merchant` struct** — add `mintHealthTracker *MintHealthTracker` field
2. **`New()`** — initialize tracker, call `StartProactiveChecks()`, do initial probe so reachable set is populated before first ad
3. **`GetAcceptedMints()`** — return `mintHealthTracker.GetReachableMintConfigs()` instead of raw `config.AcceptedMints`
4. **`CreateAdvertisement()`** — filter `config.AcceptedMints` through `mintHealthTracker` so only reachable mints get `price_per_step` tags
5. **`StartPayoutRoutine()`** — skip payout goroutines for unreachable mints (check `IsReachable` before entering the ticker loop, and check again inside the loop)
6. **`PayoutShare()`** — on `MeltToLightning` failure, call `mintHealthTracker.MarkUnreachable(mintConfig.URL)`
7. **`PurchaseSession()`** — on `tollwallet.Receive` failure with a network/connection error, call `MarkUnreachable` for that mint

### `src/main.go`

No changes needed — `GetAdvertisement()` and `GetAcceptedMints()` are already called through `merchantInstance`, so filtering happens automatically.

## Data Flow

```
config.json (all mints, never modified)
        │
        ▼
┌─────────────────────────┐
│   MintHealthTracker     │
│                         │
│  Proactive (5 min)  ────┼──► GET /v1/info ──► add to reachable set
│  Reactive (on error) ───┼──► mark unreachable
│                         │
│  reachableMints map     │
└────────┬────────────────┘
         │
         ▼
┌─────────────────────────┐
│   Merchant              │
│                         │
│  GetAcceptedMints()     │◄── returns only reachable mints
│  CreateAdvertisement()  │◄── only tags reachable mints
│  StartPayoutRoutine()   │◄── skips unreachable mints
│  PayoutShare()          │◄── marks unreachable on failure
│  PurchaseSession()      │◄── marks unreachable on failure
└────────┬────────────────┘
         │
         ▼
┌─────────────────────────┐
│   Captive Portal        │
│   (reads advertisement) │◄── only sees reachable mints
└─────────────────────────┘
```
