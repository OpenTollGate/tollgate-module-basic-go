# Low-Level Design Document: main.go

## Overview

The `main.go` file is the entry point of the TollGate application. It handles HTTP requests, processes Nostr payment events, and coordinates between merchant and other modules for payment processing.

## Advertisement Event Structure (Kind 10021)

The advertisement event structure has been updated to support flexible metrics:

- `Kind`: 10021
- `Tags`: Dynamic tags based on configuration
- `Content`: Empty string

### Tag Structure:
- `["metric", config.Metric]`: e.g., "milliseconds" or "bytes"
- `["step_size", fmt.Sprintf("%d", config.StepSize)]`: e.g., "60000"
- `["tips", "1", "2", "3"]`: Static tips
- For each mint: `["price_per_step", "cashu", price, unit, mint_url, min_steps]`

## Code Structure (v0.0.4)

### Main Functions:
- `init()`: Initializes configuration manager and merchant service
- `initJanitor()`: Initializes janitor module for auto-updates
- `handleRoot()`: Serves advertisement event as JSON
- `handleRootPost()`: Processes Nostr payment events (kind 21000)
- `sendNoticeResponse()`: Creates notice events for error responses
- `main()`: Entry point and HTTP server setup

## Payment Processing Flow

### handleRootPost() - Updated Logic:

1. **Event Validation**:
   - Read and parse Nostr event from request body
   - Validate event signature using `event.CheckSignature()`
   - Verify event kind is 21000 (payment event)

2. **Merchant Processing**:
   - Call `merchantInstance.PurchaseSession(event)`
   - Merchant handles all payment logic and validation
   - Returns either session event (success) or notice event (error)

3. **Response Handling**:
   - Check event kind: 1022 (session) vs 21023 (notice)
   - Set appropriate HTTP status code
   - Return event as JSON response

## Error Handling (Enhanced)

### Notice Event Generation:
- **Granular Error Codes**: `payment-error-token-spent`, `invalid-mac-address`, etc.
- **Merchant-Generated**: Error handling moved from main to merchant
- **Notice Events**: Kind 21023 events with structured error information

### Error Flow:
1. Merchant validates payment and returns notice event on error
2. Main checks event kind and sets HTTP status accordingly
3. Notice events returned with 400 status, sessions with 200

## Session Event Structure (Kind 1022)

Updated to support dynamic metrics:

```json
{
  "kind": 1022,
  "tags": [
    ["p", "customer_pubkey"],
    ["device-identifier", "mac", "device_mac"],
    ["allotment", "allotment_amount"],
    ["metric", "milliseconds"]
  ]
}
```

## Configuration Integration

### Migration Support:
- `99-tollgate-config-migration-v0.0.2-to-v0.0.3-migration` script
- Converts `price_per_minute` to mint-specific `price_per_step`
- Adds `metric` and `step_size` to main config

### Pretty-Printed Config:
- `json.MarshalIndent()` for human-readable configuration files
- 2-space indentation for easy editing

## Testing

- Updated test files for new configuration structure
- Merchant module handles most payment logic testing
- Main focuses on HTTP handling and event routing

## Dependencies Integration

- **Merchant**: All financial decisions and payment processing
- **TollWallet**: Cashu token operations
- **Config Manager**: Migration support and pretty-printed configs
- **Valve**: Network access control with session events
- **Janitor**: Auto-update functionality preserved

## Centralized Rate Limiting for relayPool

To address the 'too many concurrent REQs' error, we will implement centralized rate limiting for `relayPool`. This involves initializing `relayPool` in `config_manager` and providing a controlled access mechanism through a member function. This approach ensures that all services using `relayPool` are rate-limited, preventing excessive concurrent requests to relays.