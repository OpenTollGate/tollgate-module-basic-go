# TIP-01 Addition: Notice Events
`draft` `optional` `kind=21023`

---

## Notice Events

Notice events are used by TollGates to communicate issues, warnings, and informational messages to customers. These events provide structured error reporting and debugging information.

```json
{
    "kind": 21023,
    "pubkey": "tollgate_pubkey",
    "tags": [
        ["p", "customer_pubkey"], // Optional - only when addressing specific customer
        ["level", "<error|warning|info|debug>"],
        ["code", "<text-code>"]
    ],
    "content": "Human-readable message",
    "created_at": timestamp,
    "sig": "signature"
}
```

## Tags

### Required Tags
- `level`: Severity level of the notice
  - `error`: Critical errors preventing operation
  - `warning`: Non-critical issues that may affect experience
  - `info`: Informational messages
  - `debug`: Debug information for troubleshooting, should not be used in production settings.

- `code`: Machine-readable error code for programmatic handling
  - `payment-error`: Payment processing failed
  - `invalid-event`: Malformed or invalid event received
  - `insufficient-funds`: Payment amount below minimum requirements
  - `session-error`: Session creation or management error
  - `mint-not-accepted`: Payment from unsupported mint

### Optional Tags
- `p`: Customer pubkey when addressing a specific customer. Omitted for general notices.

## Content
Human-readable message describing the issue or information. Should be user-friendly and provide actionable guidance when possible.

## Examples

### Payment Error
```json
{
    "kind": 21023,
    "pubkey": "24d6...3662",
    "tags": [
        ["p", "63gy...9xvq"],
        ["level", "error"],
        ["code", "payment-err"]
    ],
    "content": "Payment processing failed: insufficient token value for minimum session duration",
    "created_at": 1640995200,
    "sig": "signature..."
}
```

### Invalid Event Warning
```json
{
    "kind": 21023,
    "pubkey": "24d6...3662", 
    "tags": [
        ["p", "63gy...9xvq"],
        ["level", "error"],
        ["code", "invalid-event"]
    ],
    "content": "Payment event signature verification failed",
    "created_at": 1640995200,
    "sig": "signature..."
}
```

### General Information
```json
{
    "kind": 21023,
    "pubkey": "24d6...3662",
    "tags": [
        ["level", "info"],
        ["code", "maintenance"]
    ],
    "content": "Scheduled maintenance window: 02:00-04:00 UTC",
    "created_at": 1640995200,
    "sig": "signature..."
}
```

## Usage Guidelines

### When to Send Notice Events
- Payment processing failures
- Invalid or malformed events received (http server only)
- Session management errors
- Network access issues
- Maintenance notifications

### Error Response Pattern
When a TollGate encounters an error that prevents normal operation:

1. **Log the error** for operator debugging
2. **Create a notice event** with appropriate level and code
3. **Return the notice event** as HTTP response body
4. **Set appropriate HTTP status code** (400, 402, 500, etc.)

### Client Handling
Clients should:
- Check response event `kind` to distinguish between sessions (21022) and notices (21023)
- Parse `level` and `code` tags for programmatic error handling
- Display `content` to users when appropriate
- Implement retry logic based on error codes

## Integration with TIP-03

Notice events are returned as HTTP response bodies following the same pattern as session events:

### Error Response Example
```
HTTP/1.1 402 Payment Required
Content-Type: application/json

{
    "kind": 21023,
    "pubkey": "24d6...3662",
    "tags": [
        ["p", "63gy...9xvq"],
        ["level", "error"], 
        ["code", "insufficient-funds"]
    ],
    "content": "Payment amount too low for minimum session duration",
    "created_at": 1640995200,
    "sig": "signature..."
}
```

This ensures consistent response format while providing structured error information to clients.