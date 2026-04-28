# Plan: Real-Wallet Integration Test for KICKSTART_DEADLOCK Validation

## Objective

Write a Go integration test that validates the critical assumption behind the KICKSTART_DEADLOCK fix:

> **`tollwallet.TollWallet` can load a BoltDB wallet from disk and perform wallet operations when the Cashu mint is unreachable.**

This is the ONE assumption that all 80 existing mock-based tests skip. If this assumption is wrong, the KICKSTART_DEADLOCK fix does not actually solve the problem.

## Background

### The Bug

When a TollGate router boots without internet and has existing e-cash in its BoltDB wallet, the degraded merchant must be able to:

1. Load the wallet from disk (even though mints are unreachable)
2. Report balance from cached proofs
3. Create payment tokens to pay an upstream gateway for internet access

Without this, the router is stuck in a chicken-and-egg deadlock: no wallet -> no balance -> no payment -> no internet -> mints never reachable -> stuck forever.

### The Fix (commit `86b02b8`)

`MerchantDegraded` now attempts to load the BoltDB wallet via `DefaultWalletFactory` -> `tollwallet.New()` -> `wallet.LoadWallet()`. If loading succeeds, wallet operations (GetBalance, SendWithOverpayment) are delegated to the loaded wallet.

### The Critical Unknown

`wallet.LoadWallet()` is from `github.com/Origami74/gonuts-tollgate`. The fork claims to support offline loading with cached keysets, but this has never been tested with a real BoltDB. The TODO at `src/tollwallet/tollwallet.go:34-36` explicitly notes this risk:

```go
// TODO: Fix issue where wallet db is not unlocked if it doesn't get a network connection when the tollgate application boots.
```

## Prerequisites

### Before starting implementation

1. **Run all existing tests and verify they pass:**
   ```bash
   cd src/merchant && go test -v -count=1 -race ./...
   cd src/upstream_session_manager && go test -v -count=1 -race ./...
   cd src/cli && go test -v -count=1 -race ./...
   ```
   Expected: 80 tests pass, 0 failures, no race conditions.

2. **Ensure `cdk-cli` is installed.** It's needed to mint test tokens from the Cashu test mint. Install via:
   ```bash
   ansible-playbook tests/setup_cdk_testing.yml
   ```
   Or verify it's available: `which cdk-cli`

3. **Ensure network access** to `https://nofees.testnut.cashu.space` (the free Cashu test mint).

## Architecture

### Code Flow Being Tested

```
MerchantDegraded.NewMerchantDegradedWithWallet()
  -> DefaultWalletFactory(walletPath, mintURLs)
       -> newTollWallet(walletPath, mintURLs)
            -> tollwallet.New(walletPath, mintURLs, false)
                 -> wallet.LoadWallet(config)   <-- THIS IS WHAT WE'RE TESTING
                      |
                      +-- If DB exists + mint unreachable: ???
                      |     +-- Returns wallet loaded from cache -> FIX WORKS
                      |     +-- Returns error -> FIX DOESN'T WORK (gap in gonuts fork)
                      |
                      +-- If DB doesn't exist: returns error (expected, first boot)
```

### Test Strategy

Use a **local HTTP reverse proxy** (`httputil.ReverseProxy`) to a real Cashu test mint:

1. Start local proxy -> `https://nofees.testnut.cashu.space`
2. Use `cdk-cli` to mint tokens via the proxy
3. Use `tollwallet.TollWallet` to create a wallet and receive tokens
4. **Stop the proxy** -> mint is now "unreachable"
5. Try to reload the wallet from disk
6. Verify balance and payment operations work offline

The proxy URL (e.g., `http://127.0.0.1:PORT`) is used as the "mint URL" throughout. When the proxy stops, the mint becomes unreachable -- without iptables, root, or OS-specific tricks.

### Why Not Use testnut.cashu.space Directly?

We can't control whether testnut is up or down. The proxy gives us an on/off switch for the "mint reachability" simulation.

## Implementation Steps

### Step 0: Run all existing tests

```bash
cd src/merchant && go test -v -count=1 -race ./...
cd src/upstream_session_manager && go test -v -count=1 -race ./...
cd src/cli && go test -v -count=1 -race ./...
```

**Do not proceed until all 80 tests pass.** If any test fails, stop and report the failure.

### Step 1: Create the test file

Create `src/merchant/offline_wallet_integration_test.go` with:

```go
//go:build integration

package merchant
```

The `//go:build integration` tag ensures this test is NOT run by default with `go test ./...`. It must be explicitly enabled with `go test -tags=integration ./...`. This prevents CI failures when cdk-cli or network is unavailable.

### Step 2: Implement helper functions

#### `setupReverseProxy(t, targetURL) *httptest.Server`

Creates a local HTTP reverse proxy to the target URL. Uses `httputil.NewSingleHostReverseProxy`.

```go
func setupReverseProxy(t *testing.T, targetURL string) *httptest.Server {
    t.Helper()
    target, err := url.Parse(targetURL)
    if err != nil {
        t.Fatalf("failed to parse target URL: %v", err)
    }
    proxy := httputil.NewSingleHostReverseProxy(target)
    // The proxy's Director rewrites the Host header; we need to also update
    // the scheme so outbound requests go to HTTPS
    originalDirector := proxy.Director
    proxy.Director = func(req *http.Request) {
        originalDirector(req)
        req.URL.Scheme = target.Scheme
        req.URL.Host = target.Host
        req.Host = target.Host
    }
    server := httptest.NewServer(proxy)
    return server
}
```

#### `mintTestTokens(t, proxyURL, walletDir) string`

Uses `cdk-cli` via `exec.Command` to mint 1000 sats and send 100 sats, returning the cashu token string.

```go
func mintTestTokens(t *testing.T, proxyURL string, walletDir string) string {
    t.Helper()

    // Mint 1000 sats to cdk-cli wallet
    cmd := exec.Command("cdk-cli", "-w", walletDir, "mint", proxyURL, "1000")
    cmd.Env = append(os.Environ(), "HOME="+walletDir)
    output, err := cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("cdk-cli mint failed: %v\noutput: %s", err, output)
    }

    // Send 100 sats from cdk-cli wallet, producing a cashu token
    cmd = exec.Command("cdk-cli", "-w", walletDir, "send", "--mint-url", proxyURL)
    cmd.Stdin = strings.NewReader("100\n")
    cmd.Env = append(os.Environ(), "HOME="+walletDir)
    output, err = cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("cdk-cli send failed: %v\noutput: %s", err, output)
    }

    // Parse the cashu token from output (last line starting with "cashu")
    lines := strings.Split(strings.TrimSpace(string(output)), "\n")
    for i := len(lines) - 1; i >= 0; i-- {
        if strings.HasPrefix(lines[i], "cashu") {
            return lines[i]
        }
    }
    t.Fatalf("no cashu token found in cdk-cli output: %s", output)
    return ""
}
```

#### `requireCDKCLI(t)`

Checks that cdk-cli is available, skips the test if not.

```go
func requireCDKCLI(t *testing.T) {
    t.Helper()
    _, err := exec.LookPath("cdk-cli")
    if err != nil {
        t.Skip("cdk-cli not found, skipping integration test. Install with: ansible-playbook tests/setup_cdk_testing.yml")
    }
}
```

#### `requireNetworkAccess(t, url)`

Checks that the target URL is reachable, skips if not.

```go
func requireNetworkAccess(t *testing.T, targetURL string) {
    t.Helper()
    client := &http.Client{Timeout: 5 * time.Second}
    resp, err := client.Get(targetURL + "/v1/info")
    if err != nil {
        t.Skipf("cannot reach %s: %v (network unavailable?)", targetURL, err)
    }
    resp.Body.Close()
}
```

### Step 3: Implement the main test

```go
func TestIntegration_KickstartDeadlock_RealWalletOffline(t *testing.T) {
    // --- Prerequisites ---
    requireCDKCLI(t)
    testMintURL := "https://nofees.testnut.cashu.space"
    requireNetworkAccess(t, testMintURL)

    // --- Phase 1: Set up proxy and fund a wallet (online) ---

    // Start reverse proxy to the test mint
    proxy := setupReverseProxy(t, testMintURL)
    proxyMintURL := proxy.URL
    t.Logf("Proxy mint URL: %s", proxyMintURL)

    // Create temp dirs
    cdkWalletDir := t.TempDir()    // for cdk-cli
    gonutsWalletDir := t.TempDir() // for gonuts TollWallet

    // Mint tokens via cdk-cli through the proxy
    tokenString := mintTestTokens(t, proxyMintURL, cdkWalletDir)
    t.Logf("Got cashu token (length=%d): %s...", len(tokenString), tokenString[:min(50, len(tokenString))])

    // Create gonuts TollWallet using the proxy URL as the mint
    tw, err := tollwallet.New(gonutsWalletDir, []string{proxyMintURL}, false)
    if err != nil {
        t.Fatalf("Failed to create TollWallet: %v", err)
    }

    // Receive the token into the gonuts wallet
    parsedToken, err := cashu.DecodeToken(tokenString)
    if err != nil {
        t.Fatalf("Failed to decode cashu token: %v", err)
    }

    received, err := tw.Receive(parsedToken)
    if err != nil {
        t.Fatalf("Failed to receive token into TollWallet: %v", err)
    }
    t.Logf("Received %d sats into TollWallet", received)

    // Verify balance
    balance := tw.GetBalance()
    if balance == 0 {
        t.Fatal("Expected non-zero balance after receiving token")
    }
    t.Logf("Balance after receive: %d sats", balance)

    balanceByMint := tw.GetBalanceByMint(proxyMintURL)
    if balanceByMint == 0 {
        t.Fatal("Expected non-zero balance for proxy mint URL")
    }

    // --- Phase 2: Take the mint offline ---
    proxy.CloseClientConnections()
    proxy.Close()
    t.Log("Proxy stopped -- mint is now 'offline'")

    // Verify the proxy is actually dead
    _, err = http.Get(proxyMintURL + "/v1/info")
    if err == nil {
        t.Log("Warning: proxy still responding, test may not be valid")
    }

    // --- Phase 3: Reload wallet from disk with mint unreachable ---

    // This is THE critical test. If wallet.LoadWallet() fails here,
    // the KICKSTART_DEADLOCK fix has a gap.
    tw2, err := tollwallet.New(gonutsWalletDir, []string{proxyMintURL}, false)
    if err != nil {
        t.Fatalf("CRITICAL: tollwallet.New() failed when mint is offline: %v\n"+
            "This means the KICKSTART_DEADLOCK fix does not work as intended.\n"+
            "The gonuts-tollgate fork's wallet.LoadWallet() cannot load from an existing BoltDB when the mint is unreachable.",
            err)
    }
    t.Log("Wallet reloaded successfully from disk with mint offline")

    // Verify balance after offline reload
    offlineBalance := tw2.GetBalance()
    if offlineBalance != balance {
        t.Errorf("Balance mismatch after offline reload: got %d, expected %d", offlineBalance, balance)
    }
    t.Logf("Balance after offline reload: %d sats (correct)", offlineBalance)

    offlineBalanceByMint := tw2.GetBalanceByMint(proxyMintURL)
    if offlineBalanceByMint != balanceByMint {
        t.Errorf("Balance by mint mismatch: got %d, expected %d", offlineBalanceByMint, balanceByMint)
    }

    // --- Phase 4: Test offline payment creation ---

    // Try to create a payment token with overpayment while offline.
    // This is what the degraded merchant needs to pay the upstream gateway.
    token, err := tw2.SendWithOverpayment(1, proxyMintURL, 10000, 100)
    if err != nil {
        t.Fatalf("SendWithOverpayment failed with mint offline: %v\n"+
            "This means the degraded merchant cannot pay upstream gateways when offline.\n"+
            "The KICKSTART_DEADLOCK fix only partially works (balance reporting works, payment doesn't).",
            err)
    }
    if token == "" {
        t.Fatal("SendWithOverpayment returned empty token")
    }
    t.Logf("Successfully created payment token offline (length=%d): %s...",
        len(token), token[:min(50, len(token))])

    t.Log("=== KICKSTART_DEADLOCK validation PASSED ===")
    t.Log("The gonuts-tollgate fork supports offline wallet loading and payment creation.")
    t.Log("The KICKSTART_DEADLOCK fix is valid.")
}
```

### Step 4: Implement a second test for the full MerchantDegraded path

```go
func TestIntegration_DegradedMerchant_RealWalletOffline(t *testing.T) {
    // Same setup as above, but tests through the MerchantDegraded layer
    // instead of TollWallet directly

    requireCDKCLI(t)
    testMintURL := "https://nofees.testnut.cashu.space"
    requireNetworkAccess(t, testMintURL)

    proxy := setupReverseProxy(t, testMintURL)
    defer proxy.Close()

    cdkWalletDir := t.TempDir()
    gonutsWalletDir := t.TempDir()

    // Fund the gonuts wallet (same steps as TestIntegration_KickstartDeadlock_RealWalletOffline)
    tokenString := mintTestTokens(t, proxy.URL, cdkWalletDir)

    tw, err := tollwallet.New(gonutsWalletDir, []string{proxy.URL}, false)
    if err != nil {
        t.Fatalf("Failed to create TollWallet: %v", err)
    }

    parsedToken, err := cashu.DecodeToken(tokenString)
    if err != nil {
        t.Fatalf("Failed to decode token: %v", err)
    }

    _, err = tw.Receive(parsedToken)
    if err != nil {
        t.Fatalf("Failed to receive token: %v", err)
    }

    walletBalance := tw.GetBalance()
    if walletBalance == 0 {
        t.Fatal("Expected non-zero balance after receive")
    }

    // Stop the proxy -- mint is now offline
    proxy.CloseClientConnections()
    proxy.Close()

    // Set up config manager with the (now dead) proxy as a configured mint
    testDir := t.TempDir()
    t.Setenv("TOLLGATE_TEST_CONFIG_DIR", testDir)
    configPath := filepath.Join(testDir, "config.json")
    installPath := filepath.Join(testDir, "install.json")
    identitiesPath := filepath.Join(testDir, "identities.json")

    cm, err := config_manager.NewConfigManager(configPath, installPath, identitiesPath)
    if err != nil {
        t.Fatalf("Failed to create config manager: %v", err)
    }

    cfg := cm.GetConfig()
    cfg.AcceptedMints = []config_manager.MintConfig{
        {URL: proxy.URL, PricePerStep: 1, PriceUnit: "sats"},
    }

    // Create MintHealthTracker (all mints unreachable since proxy is stopped)
    tracker := NewMintHealthTracker(cm)
    tracker.RunInitialProbe()

    if len(tracker.GetReachableMintConfigs()) != 0 {
        t.Fatal("Expected no reachable mints after proxy stopped")
    }

    // Create MerchantDegraded with real wallet factory pointing to the gonuts wallet dir
    factory := DefaultWalletFactory
    deg := NewMerchantDegradedWithWallet(cm, tracker, factory, gonutsWalletDir)

    if !deg.WalletLoaded() {
        t.Fatal("MerchantDegraded failed to load wallet -- KICKSTART_DEADLOCK fix gap")
    }

    // Verify degraded merchant returns all configured mints (not just reachable)
    mints := deg.GetAcceptedMints()
    if len(mints) != 1 {
        t.Fatalf("Expected 1 configured mint, got %d", len(mints))
    }

    // Verify balance through degraded merchant
    balance := deg.GetBalance()
    if balance != walletBalance {
        t.Errorf("Balance mismatch: got %d, expected %d", balance, walletBalance)
    }

    // Verify payment creation through degraded merchant
    token, err := deg.CreatePaymentTokenWithOverpayment(proxy.URL, 1, 10000, 100)
    if err != nil {
        t.Fatalf("CreatePaymentTokenWithOverpayment failed: %v", err)
    }
    if token == "" {
        t.Fatal("Expected non-empty token")
    }

    t.Log("=== Degraded merchant with real wallet PASSED ===")
}
```

### Step 5: Run the tests

```bash
# Run existing tests (must all pass before integration tests)
cd src/merchant && go test -v -count=1 -race ./...

# Run the new integration test
cd src/merchant && go test -v -count=1 -tags=integration -run TestIntegration ./...
```

## Expected Results and How to Interpret Them

### Scenario A: All tests pass

The gonuts-tollgate fork supports offline wallet loading and payment creation. The KICKSTART_DEADLOCK fix is validated. Proceed to merge (after hardware smoke test per Option 2).

### Scenario B: `tollwallet.New()` fails when mint is offline

```
CRITICAL: tollwallet.New() failed when mint is offline: failed to create wallet: <error>
```

**Meaning**: The gonuts-tollgate fork's `wallet.LoadWallet()` cannot load from an existing BoltDB without contacting the mint. The KICKSTART_DEADLOCK fix is incomplete.

**Next step**: Fix `wallet.LoadWallet()` in the gonuts-tollgate fork to support offline loading. The fix must:
1. Open the BoltDB file from disk
2. Load cached keysets and proofs
3. NOT make HTTP calls to the mint during loading
4. Return a functional wallet that can report balance and create tokens from cached proofs

### Scenario C: Wallet loads but `SendWithOverpayment()` fails

**Meaning**: Balance reporting works offline, but token creation requires mint interaction. The fix is partially valid -- the degraded merchant can report balance but can't pay upstream gateways.

**Next step**: Investigate whether gonuts-tollgate's `SendWithOptions` can work with cached keysets offline, or if a different approach is needed (e.g., storing pre-created payment tokens).

### Scenario D: Test skipped (cdk-cli not found or network unavailable)

**Meaning**: The test environment doesn't meet prerequisites. Install cdk-cli or ensure network access and re-run.

## Important Notes

1. **Do NOT modify any existing files.** Only create the new test file `src/merchant/offline_wallet_integration_test.go`.

2. **Run all existing tests first** and verify all 80 pass before implementing. If any existing test fails, stop and report.

3. **The test uses `//go:build integration`** so it won't break `go test ./...` for developers who don't have cdk-cli.

4. **The proxy approach simulates offline reliably** because:
   - The gonuts wallet uses `http://127.0.0.1:PORT` as the mint URL
   - When the proxy stops, `connection refused` is the same error the router would see when mints are unreachable
   - No iptables, root, or OS-specific tricks needed

5. **Import paths** -- The test file is in `package merchant` and can use:
   - `tollwallet` via import `"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"`
   - `cashu` via import `"github.com/Origami74/gonuts-tollgate/cashu"`
   - All internal types from the merchant package

6. **The `tollwallet` import** needs the Go module path. Check `src/merchant/go.mod` for the correct module name and replace directives.

7. **The `cdk-cli send` command** is interactive -- it prompts for the amount on stdin. The test feeds `"100\n"` via `cmd.Stdin`. If `cdk-cli send` uses different flags or prompt format on your version, check `cdk-cli send --help` and adjust accordingly. The existing `tests/conftest.py` uses the same approach and is known to work.

8. **Test isolation** -- Each test creates its own temp directories (`t.TempDir()`) for both the cdk-cli wallet and the gonuts wallet. Tests don't share state. Temp dirs are automatically cleaned up.
