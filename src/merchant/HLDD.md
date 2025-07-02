# Merchant HLDD

## Overview

The `merchant` package acts as the financial decision-maker within the Tollgate system. Its primary responsibilities include processing payments (specifically Cashu tokens), managing user sessions, handling advertisement generation, and orchestrating payout routines to profit share recipients. It interacts with the `config_manager` for configuration, `tollwallet` for payment processing, and `valve` for network access control.

## Responsibilities

-   **Process Payments:** Receives and validates payment events (Nostr Kind 21000) containing Cashu tokens. Decodes and processes these tokens via the `tollwallet`.
-   **Manage User Sessions:** Creates new sessions or extends existing ones based on successful payments. Sessions grant network access for a calculated allotment (e.g., time or data).
-   **Generate Advertisements:** Creates and signs Nostr Kind 10021 advertisement events, detailing pricing, metrics, and accepted mints.
-   **Orchestrate Payouts:** Periodically checks mint balances and initiates payouts to configured profit share recipients via the `tollwallet`.
-   **Create Notice Events:** Generates Nostr Kind 21023 notice events to inform customers about payment errors or session-related issues.
-   **Interact with Network Access Control:** Collaborates with the `valve` module to open and close network gates based on session validity.

## Interfaces

### `MerchantService` Interface

The `merchant` package exposes its core functionality through the `MerchantService` interface. This promotes loose coupling and allows for easy mocking in tests or alternative implementations in the future.

```go
type MerchantService interface {
	PurchaseSession(event nostr.Event) (*nostr.Event, error)
	CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error)
	GetAdvertisement() string
	StartPayoutRoutine()
}
```

-   `PurchaseSession(event nostr.Event) (*nostr.Event, error)`: Processes a payment event. Returns a Nostr session event (Kind 1022) on success or a notice event (Kind 21023) on failure.
-   `CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error)`: Generates a standardized Nostr notice event for error reporting or informational messages.
-   `GetAdvertisement() string`: Returns the JSON string representation of the signed advertisement event.
-   `StartPayoutRoutine()`: Initiates background routines for periodic payouts from accepted mints.

### Dependencies

The `Merchant` struct implements the `MerchantService` interface and depends on the following:

-   `config_manager.ConfigManager`: For loading application configuration, accessing Nostr pools (local and public), and retrieving NIP94 events.
-   `tollwallet.TollWallet`: For Cashu token reception (`Receive`) and lightning payments (`MeltToLightning`).
-   `utils` package: For utility functions like MAC address validation.
-   `valve` package: For controlling network access (`OpenGateForSession`).

## Component Interactions

```mermaid
graph TD
    A[Main Application] -->|Initializes| B[Merchant]
    B -->|Uses Config| C[ConfigManager]
    B -->|Processes Payments| D[TollWallet]
    B -->|Controls Network Access| E[Valve]
    B -->|Generates Events| F[Nostr Relays (Local/Public)]
    C -->|Provides Config| B
    D -->|Cashu Operations| B
    E -->|Gate Control| B
    F -->|Publishes/Queries Events| B
    G[HTTP Handlers] -->|Calls| B
    H[Customer Device] -->|Sends Payment Event| G
    B -->|Sends Session/Notice Event| H
```

**Flow for `PurchaseSession`:**

1.  HTTP Handler receives a Nostr Kind 21000 payment event from a customer.
2.  Handler calls `Merchant.PurchaseSession()`.
3.  `PurchaseSession` extracts payment token and device identifier from the event.
4.  Validates the device identifier (e.g., MAC address).
5.  Calls `tollwallet.Receive()` to process the Cashu token.
6.  Calculates session allotment (duration/bytes) based on the received amount and configured pricing (`config.Metric`, `mintConfig.PricePerStep`).
7.  Queries `config_manager.GetLocalPoolEvents()` to check for existing sessions for the customer.
8.  If an existing session is found and active, `extendSessionEvent` is called; otherwise, `createSessionEvent` is called.
9.  The resulting session event (Nostr Kind 1022) is passed to `valve.OpenGateForSession()` to grant network access.
10. The session event is published to the local Nostr relay via `config_manager.PublishToLocalPool()` (for privacy and local state management).
11. Returns the session event or a notice event if any step fails.

## Future Extensibility

The `MerchantService` interface allows for:
-   **Alternative Payment Methods:** New implementations of `MerchantService` could integrate with different payment systems (e.g., Lightning Network directly, other e-cash protocols) without affecting the core application logic.
-   **Complex Business Logic:** The `Merchant` could incorporate more sophisticated pricing models, loyalty programs, or fraud detection by extending its internal logic while adhering to the `MerchantService` interface.
-   **Multi-Merchant Support:** In a more advanced setup, multiple `MerchantService` implementations could be run concurrently, each handling different configurations or payment types.