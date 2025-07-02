# Merchant Low-Level Design Document

## Overview

This document provides a detailed low-level design for the `merchant` package, which implements the `MerchantService` interface. It covers the internal structure of the `Merchant` type, its key functions, the mocking strategy for testing, and pending tasks related to test re-enablement.

## `Merchant` Struct

The `Merchant` struct holds the dependencies and state required for the merchant's operations:

```go
type Merchant struct {
	config        *config_manager.Config
	configManager *config_manager.ConfigManager
	tollwallet    tollwallet.TollWallet
	advertisement string
}
```

-   `config`: A pointer to the main application configuration, loaded from `config_manager`. This provides access to accepted mints, profit share settings, metric, step size, and the tollgate's private key.
-   `configManager`: A pointer to the `config_manager.ConfigManager` instance, used for loading configs, accessing Nostr pools, and fetching NIP94 events.
-   `tollwallet`: An instance of `tollwallet.TollWallet`, responsible for handling Cashu token operations (receiving, melting) and interacting with lightning.
-   `advertisement`: A string storing the JSON representation of the signed advertisement Nostr event.

## Core Functions and Implementation Details

### `New(configManager *config_manager.ConfigManager) (*Merchant, error)`

-   **Purpose:** Constructor for the `Merchant` struct.
-   **Details:**
    -   Loads the application configuration using `configManager.LoadConfig()`.
    -   Initializes `tollwallet.New()` with the configured mint URLs.
    -   Calls `CreateAdvertisement()` to generate the initial advertisement string.
    -   Logs initialization status, accepted mints, wallet balance, and advertisement.

### `StartPayoutRoutine()`

-   **Purpose:** Initiates a background routine to periodically process payouts from accepted mints.
-   **Details:**
    -   Iterates through each `MintConfig` in `m.config.AcceptedMints`.
    -   Launches a goroutine for each mint, running `processPayout()` on a `time.NewTicker` (currently set to 1 minute).
    -   `processPayout()` is responsible for checking the balance and initiating melts.

### `processPayout(mintConfig config_manager.MintConfig)`

-   **Purpose:** Checks the balance for a specific mint and processes payouts to profit share recipients.
-   **Details:**
    -   Retrieves the balance for the given `mintConfig.URL` using `m.tollwallet.GetBalanceByMint()`.
    -   Skips payout if the balance is below `mintConfig.MinPayoutAmount`.
    -   Calculates the `aimedPaymentAmount` (balance minus `mintConfig.MinBalance`).
    -   Iterates through `m.config.ProfitShare` recipients.
    -   For each recipient, calculates the proportional `aimedAmount` and calls `PayoutShare()`.
    -   Logs payout completion.

### `PayoutShare(mintConfig config_manager.MintConfig, aimedPaymentAmount uint64, lightningAddress string)`

-   **Purpose:** Executes a single payout share to a lightning address.
-   **Details:**
    -   Calculates `tolerancePaymentAmount` based on `aimedPaymentAmount` and `mintConfig.BalanceTolerancePercent`.
    -   Determines `maxCost` for the melt operation.
    -   Calls `m.tollwallet.MeltToLightning()` to send the funds.
    -   Logs errors if melting fails and skips the payout for that share.

### `PurchaseSession(paymentEvent nostr.Event) (*nostr.Event, error)`

-   **Purpose:** Main entry point for processing customer payment events and issuing session events.
-   **Details:**
    -   **Event Parsing:** Extracts `payment` token and `device-identifier` (MAC address) from `paymentEvent.Tags`.
    -   **Validation:** Validates the extracted MAC address using `utils.ValidateMACAddress()`.
    -   **Payment Processing:**
        -   Decodes the Cashu token using `cashu.DecodeToken()`.
        -   Calls `m.tollwallet.Receive()` to process the payment.
        -   Handles `Token already spent` and other payment processing errors, returning appropriate notice events.
    -   **Allotment Calculation:** Calls `calculateAllotment()` to determine the session duration (or bytes) based on the received amount and mint-specific pricing.
    -   **Session Management:**
        -   Calls `getLatestSession()` to check for an existing active session for the customer.
        -   If an active session exists, calls `extendSessionEvent()` to extend it.
        -   Otherwise, calls `createSessionEvent()` to create a new session.
    -   **Network Access Control:** Calls `valve.OpenGateForSession()` to apply network rules for the new/extended session.
    -   **Local Event Publishing:** Publishes the resulting session event to the local Nostr relay using `m.publishLocal()` for internal state management and privacy.
    -   **Error Handling:** Returns a Nostr Kind 21023 notice event with specific error codes and messages for various failures (e.g., invalid token, payment processing errors, session creation/extension failures, gate opening failures).

### `CreateAdvertisement(config *config_manager.Config) (string, error)`

-   **Purpose:** Constructs and signs a Nostr Kind 10021 advertisement event.
-   **Details:**
    -   Creates a `nostr.Event` with Kind 10021.
    -   Adds `metric` and `step_size` tags from the global config.
    -   Adds `price_per_step` tags for each accepted mint, including `cashu`, `PricePerStep`, `PriceUnit`, `URL`, and `MinPurchaseSteps`.
    -   Signs the event using `config.TollgatePrivateKey`.
    -   Marshals the signed event to a JSON string.

### `extractPaymentToken(paymentEvent nostr.Event) (string, error)`

-   **Purpose:** Helper function to extract the payment token from a Nostr event's `payment` tag.

### `extractDeviceIdentifier(paymentEvent nostr.Event) (string, error)`

-   **Purpose:** Helper function to extract the device identifier (MAC address) from a Nostr event's `device-identifier` tag.

### `calculateAllotment(amountSats uint64, mintURL string) (uint64, error)`

-   **Purpose:** Calculates the session allotment (e.g., milliseconds) based on the paid amount and the specific mint's pricing.
-   **Details:**
    -   Finds the matching `MintConfig` for the `mintURL`.
    -   Calculates `steps` (amount divided by `PricePerStep`).
    -   Checks if `steps` meet `MinPurchaseSteps`.
    -   Delegates to metric-specific calculation functions (e.g., `calculateAllotmentMs`).

### `calculateAllotmentMs(steps uint64, mintConfig *config_manager.MintConfig) (uint64, error)`

-   **Purpose:** Calculates allotment in milliseconds from `steps` using `config.StepSize`.

### `getLatestSession(customerPubkey string) (*nostr.Event, error)`

-   **Purpose:** Queries the local Nostr relay for the most recent active session event for a given customer.
-   **Details:**
    -   Constructs a Nostr filter for Kind 1022 (session events), authored by the tollgate's public key, and tagged with the customer's public key.
    -   Uses `config_manager.GetLocalPoolEvents()` to retrieve events.
    -   Iterates through events to find the latest one based on `CreatedAt`.
    -   Calls `isSessionActive()` to determine if the found session is still valid.

### `isSessionActive(sessionEvent *nostr.Event) bool`

-   **Purpose:** Determines if a session event is still active (not expired).
-   **Details:**
    -   Extracts `allotment` from the session event.
    -   Calculates `sessionExpiresAt` by adding the allotment duration to `sessionEvent.CreatedAt`.
    -   Compares `time.Now()` with `sessionExpiresAt`.

### `createSessionEvent(paymentEvent nostr.Event, allotment uint64) (*nostr.Event, error)`

-   **Purpose:** Creates a new Nostr Kind 1022 session event.
-   **Details:**
    -   Populates tags for `p` (customer pubkey), `d` (device identifier), `allotment`, `metric`, `timestamp`, and `payment_event_id`.
    -   Signs the event with `config.TollgatePrivateKey`.

### `extendSessionEvent(existingSession *nostr.Event, additionalAllotment uint64) (*nostr.Event, error)`

-   **Purpose:** Extends an existing Nostr Kind 1022 session event.
-   **Details:**
    -   Extracts existing `allotment` from `existingSession`.
    -   Calculates new total allotment.
    -   Creates a new session event with updated `allotment` and `timestamp`.
    -   Copies relevant tags from the existing session.
    -   Signs the new event.

### `extractAllotment(sessionEvent *nostr.Event) (uint64, error)`

-   **Purpose:** Helper function to extract the `allotment` value from a session event's tag.

### `publishLocal(event *nostr.Event) error`

-   **Purpose:** Publishes a Nostr event to the local relay pool.
-   **Details:** Uses `config_manager.PublishToLocalPool()`.

### `publishPublic(event *nostr.Event) error`

-   **Purpose:** Publishes a Nostr event to the public relay pool.
-   **Details:** Uses `config_manager.GetPublicPool()` and `rateLimitedRelayRequest`.

### `CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error)`

-   **Purpose:** Creates a Nostr Kind 21023 notice event for communication with the customer.
-   **Details:**
    -   Populates tags for `p` (customer pubkey), `level`, `code`, and `message`.
    -   Sets the event `Content` to the message.
    -   Signs the event with `config.TollgatePrivateKey`.

## Mocking Strategy for Testing

To facilitate isolated unit testing of components that interact with `MerchantService`, a mock implementation `MockMerchant` is provided in `src/merchant/merchant_test.go` and utilized in `src/main_test_init.go` and `src/handlers_test.go`.

### `MockMerchant` Struct

```go
type MockMerchant struct {
	mock.Mock
}
```

-   `MockMerchant` embeds `mock.Mock` from `github.com/stretchr/testify/mock`.
-   It implements the `MerchantService` interface, allowing test functions to define expected calls (`On`) and return values (`Return`) for each method.

### `src/main_test_init.go` Role

-   This file, tagged with `//go:build test`, is compiled only during test runs.
-   It overrides the global `configManager` variable with a `MockConfigManager` instance.
-   This prevents the main application's `initializeApplication` function from being called during tests, avoiding real file system access or network calls.
-   It ensures that tests start with a controlled environment and mock dependencies.

## Pending Tasks (Test Re-enablement)

The following merchant-related tests in `src/handlers_test.go` and `src/main_test.go` were commented out to facilitate `config_manager` fixes. They need to be re-enabled and properly implemented using the new `MerchantService` interface and `MockMerchant` for isolated testing.

-   **`src/handlers_test.go`:**
    -   `TestHandleRoot` (GET request for advertisement)
    -   `TestHandleRootPost` (POST request for payment event processing)
    -   `TestHandleRootPostInvalidKind` (Invalid Nostr event kind)
    -   `TestHandleRootPostInvalidSignature` (Invalid Nostr event signature)
-   **`src/main_test.go`:**
    -   `TestHandleRoot`
    -   `TestHandleRootPost`
    -   `TestHandleRootPostInvalidKind`
    -   `TestHandleRootPostInvalidSignature`

**Steps to Re-enable and Fix Tests:**
1.  Uncomment the respective test functions.
2.  In each test, create and configure a `MockMerchant` instance.
3.  Set up expectations on the `MockMerchant` for the methods that `handleRootPost` (or other handlers) will call (e.g., `PurchaseSession`, `CreateNoticeEvent`).
4.  Ensure that `handleRootPost` (and other handlers) receive the `MerchantService` interface as a parameter, allowing the mock to be injected.
5.  Verify test assertions against the expected behavior of the mock and the handler's output.