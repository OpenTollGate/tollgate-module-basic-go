# Upstream Session Manager - High-Level Design Document

## Overview

The `upstream_session_manager` module is responsible for managing upstream session state and executing purchases to upstream routers. It acts as the operational layer that handles the actual payment transactions while the merchant module makes the strategic decisions.

## Responsibilities

- **Session State Management**: Track current upstream session details and expiration
- **Payment Execution**: Send payment events to upstream routers via TIP-03 HTTP protocol
- **Metric-Specific Monitoring**: Handle both time-based (milliseconds) and data-based (bytes) sessions
- **Data Usage Tracking**: Monitor data consumption for byte-based upstream sessions
- **Session Lifecycle**: Create, update, and clear upstream sessions

## Architecture Position

```
[Merchant] --> [Upstream Session Manager] <-- HTTP/TIP-03 --> [Upstream Router]
     |                    |
     |                    v
[Payment Token]    [Session State]
```

## Key Components

### UpstreamSession Structure
```go
type UpstreamSession struct {
    Event         *nostr.Event // Original session event from upstream
    Allotment     uint64       // Total allotted time/data
    Metric        string       // "milliseconds" or "bytes"
    CreatedAt     time.Time    // Session creation time
    ExpiresAt     time.Time    // Session expiration (time-based only)
    DeviceID      string       // Device identifier
    IsActive      bool         // Current session status
    ConsumedBytes uint64       // Data consumed (bytes metric only)
}
```

### UpstreamSessionManager Structure
```go
type UpstreamSessionManager struct {
    currentSession   *UpstreamSession  // Current active session
    upstreamURL      string           // Upstream router URL
    deviceID         string           // This tollgate's device ID
    sessionMutex     sync.RWMutex     // Thread-safe session access
    httpClient       *http.Client     // HTTP client for upstream communication
    dataUsageTracker *DataUsageTracker // Data consumption tracking
}
```

### DataUsageTracker Structure
```go
type DataUsageTracker struct {
    totalConsumed uint64      // Total bytes consumed
    lastCheck     time.Time   // Last monitoring check
    isActive      bool        // Tracking status
    mutex         sync.RWMutex // Thread-safe access
}
```

## Core Functions

### Session Management
- `New(upstreamURL, deviceID string) *UpstreamSessionManager` - Create new manager instance
- `GetUpstreamSessionInfo() (*UpstreamSession, error)` - Get current session details
- `IsUpstreamActive() bool` - Check if upstream session is active
- `ClearSession()` - Clear current session state
- `SetUpstreamURL(url string)` - Update upstream router URL

### Purchase Operations
- `PurchaseUpstreamTime(amount uint64, paymentToken string) (*nostr.Event, error)` - Execute upstream purchase
- `sendPaymentToUpstream(paymentEvent nostr.Event) (*nostr.Event, error)` - Send payment via HTTP
- `updateSessionFromEvent(sessionEvent *nostr.Event) error` - Update session from response

### Metric-Specific Functions
- `GetTimeUntilExpiry() (uint64, error)` - Get remaining time (milliseconds metric)
- `GetBytesRemaining() (uint64, error)` - Get remaining data (bytes metric)
- `GetCurrentMetric() (string, error)` - Get session metric type
- `MonitorDataUsage() error` - Track data consumption

## Protocol Implementation

### TIP-03 HTTP Client
- **POST /**: Send payment events to upstream router
- **Request Format**: JSON-encoded Nostr event (Kind 21000)
- **Response Handling**: Parse session events (Kind 1022) or notice events (Kind 21023)
- **Timeout**: 15-second timeout for HTTP requests

### TIP-01 Session Event Processing
- **Event Kind**: 1022 (Session)
- **Required Tags**: `allotment`, `metric`, `device-identifier`
- **Session Creation**: Extract session parameters and calculate expiration
- **Signature Validation**: Verify upstream router's event signature

### Payment Event Creation
- **Event Kind**: 21000 (Payment)
- **Required Tags**: `device-identifier`, `payment`
- **Token Integration**: Accept payment tokens from merchant module
- **Device Identity**: Include this tollgate's device identifier

## Metric-Specific Behavior

### Time-Based Sessions (milliseconds)
- **Expiration Calculation**: `expiresAt = createdAt + allotment`
- **Active Check**: `time.Now().Before(expiresAt)`
- **Monitoring**: Simple countdown timer
- **Remaining Time**: Calculate milliseconds until expiration

### Data-Based Sessions (bytes)
- **No Time Expiration**: Sessions don't expire based on time
- **Active Check**: `consumedBytes < allotment`
- **Monitoring**: Track network data consumption
- **Remaining Data**: Calculate `allotment - consumedBytes`

## Thread Safety

### Session Access
- **RWMutex Protection**: All session operations are thread-safe
- **Atomic Updates**: Session state changes are atomic
- **Copy Return**: Return copies of session data to prevent external modification

### Data Usage Tracking
- **Separate Mutex**: Data usage tracking has its own mutex
- **Concurrent Monitoring**: Multiple goroutines can safely monitor usage
- **State Synchronization**: Tracker state synced with session lifecycle

## Error Handling

### HTTP Communication Errors
- **Network Failures**: Connection timeouts and network errors
- **HTTP Status Codes**: Handle 400, 402, and other error responses
- **Response Parsing**: Invalid JSON or malformed events
- **Upstream Errors**: Notice events (Kind 21023) from upstream

### Session Validation Errors
- **Missing Fields**: Required tags not present in session events
- **Invalid Values**: Malformed numeric values or empty strings
- **Signature Verification**: Invalid or missing event signatures
- **Metric Mismatches**: Wrong metric type for operation

### Recovery Strategies
- **Session Clearing**: Clear invalid sessions automatically
- **Error Propagation**: Return detailed error information to merchant
- **State Consistency**: Maintain consistent state even during errors

## Integration Points

### Merchant Module Interface
```go
// Merchant creates payment token and calls session manager
paymentToken, err := merchant.CreateUpstreamPaymentToken(amount, mintURL)
if err != nil {
    return err
}

sessionEvent, err := sessionManager.PurchaseUpstreamTime(amount, paymentToken)
if err != nil {
    return err
}
```

### Crowsnest Module Integration
- **URL Updates**: Receive upstream URL from crowsnest discoveries
- **Connection State**: Coordinate with crowsnest connection monitoring
- **Pricing Information**: Use crowsnest pricing data for purchase amounts

## Monitoring and Logging

### Session Lifecycle Events
- **Purchase Initiation**: Log when purchases are started
- **Session Updates**: Log successful session creation/updates
- **Session Expiration**: Log when sessions expire or are cleared
- **Error Events**: Detailed error logging for troubleshooting

### Data Usage Monitoring
- **Consumption Tracking**: Log data usage for byte-based sessions
- **Threshold Warnings**: Alert when approaching data limits
- **Performance Metrics**: Track monitoring performance and accuracy

## Testing Strategy

### Unit Tests
- **Session Management**: Test all session lifecycle operations
- **HTTP Communication**: Mock server tests for payment execution
- **Metric Handling**: Test both time-based and data-based scenarios
- **Error Scenarios**: Comprehensive error handling tests

### Integration Tests
- **End-to-End Flow**: Test complete purchase and session management
- **Protocol Compliance**: Verify TIP-01, TIP-02, TIP-03 implementation
- **Concurrency**: Test thread safety under concurrent access
- **Failure Recovery**: Test recovery from various failure scenarios

## Future Extensibility

### Additional Metrics
- **Extensible Design**: Architecture supports additional metrics beyond time/data
- **Monitoring Framework**: Generic monitoring interface for new metric types
- **Validation Logic**: Pluggable validation for metric-specific rules

### Advanced Features
- **Session Persistence**: Framework ready for session state persistence
- **Multiple Upstreams**: Architecture supports multiple concurrent upstream sessions
- **Load Balancing**: Foundation for upstream router load balancing

This module provides robust upstream session management with metric flexibility while maintaining clean separation from business logic and strategic decision-making.