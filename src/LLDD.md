# Low-Level Design Document: main.go

## Overview

The `main.go` file is the entry point of the TollGate application. It handles HTTP requests, interacts with the Nostr protocol, and manages configuration.

## tollgateDetailsEvent Structure

The `tollgateDetailsEvent` is a Nostr event of kind 21021. Its structure includes:

- `Kind`: 21021
- `Tags`: A list of tags containing information such as metric, step_size, price_per_step, and accepted_mints
- `Content`: An empty string

The `Tags` field includes the following information:
- `metric`: The metric used for pricing (e.g., "milliseconds")
- `step_size`: The step size for pricing (e.g., "60000")
- `price_per_step`: The price per step in satoshis
- For each accepted mint, a separate tag is created in the format `["mint", "mint_url", "min_payment"]`

## Code Structure

The code is structured into several functions:
- `init()`: Initializes the configuration manager, loads configuration, and sets up the Nostr event
- `initJanitor()`: Initializes the janitor module
- `handleRoot()`: Handles HTTP requests to the root endpoint
- `handleRootPost()`: Handles POST requests to the root endpoint
- `handleStatus()`: Returns the remaining time for a client
- `announceSuccessfulPayment()`: Announces successful payments via Nostr
- `main()`: The entry point of the application

## Functions

### init()

- Initializes the configuration manager using `config_manager.NewConfigManager()`
- Loads configuration using `configManager.LoadConfig()`
- Sets up the Nostr event with the accepted mints and their minimum payments

### handleRootPost()

- Handles POST requests to the root endpoint
- Verifies the event signature and extracts the MAC address and payment token
- Decodes the Cashu token and verifies its value
- Processes and swaps the token for fresh proofs
- Opens the gate for the specified duration using the valve module
- Returns a 200 OK response with a JSON Nostr event containing:
  - Granted time in seconds
  - Expiry timestamp
  - Access status
  - Payment details (amount, fees)

### handleStatus()

- Handles GET requests to the `/status` endpoint
- Automatically identifies the client's MAC address
- Queries the valve module to retrieve the remaining time for the MAC address
- Returns a 404 Not Found status if the MAC address has no active timer
- Returns a 200 OK status with a JSON Nostr event containing:
  - Expiry timestamp (Unix timestamp)
  - Access status (via the `status` tag)

### announceSuccessfulPayment()

- Announces successful payments via Nostr if enabled in the configuration
- Creates a Nostr event with the payment details and publishes it to the configured relays

## Error Handling

- Errors are handled using log statements and HTTP status codes
- For the status endpoint, returns 404 when the MAC address has no active timer
- For the payment endpoint, returns appropriate error codes for invalid payments

## Testing

- Unit tests should be written to ensure the correct functionality of the `main.go` file
- Tests should cover both the payment processing and status checking functionality

## Centralized Rate Limiting for relayPool

To address the 'too many concurrent REQs' error, we will implement centralized rate limiting for `relayPool`. This involves initializing `relayPool` in `config_manager` and providing a controlled access mechanism through a member function. This approach ensures that all services using `relayPool` are rate-limited, preventing excessive concurrent requests to relays.

## Implementation Tasks

1. Modify the payment success response in `handleRootPost()` to return a structured JSON Nostr event
2. Create a new HTTP handler function `handleStatus()` for the `/status` endpoint
3. Implement MAC address detection in the status handler
4. Add a function in the valve module to get remaining time for a MAC address
5. Update the HTTP server routing to include the new status endpoint
