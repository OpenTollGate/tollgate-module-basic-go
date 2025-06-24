# Crowsnest Module - High-Level Design Document

## Overview

The `crowsnest` module is responsible for upstream router detection and pricing information gathering. It acts as the "lookout" for the tollgate, discovering upstream routers and providing pricing information to the merchant module for purchase decision-making.

## Responsibilities

- **Dynamic Upstream Discovery**: Discover upstream router IP addresses (placeholder for out-of-scope connection logic)
- **Pricing Information Gathering**: Fetch and parse upstream router advertisements (TIP-01, TIP-02)
- **Connection Monitoring**: Monitor upstream router availability and health
- **Protocol Implementation**: Implement TIP-03 HTTP client for upstream communication

## Architecture Position

```
[Upstream Router] <-- HTTP/TIP-03 --> [Crowsnest] --> [Merchant] --> [Purchase Decision]
```

## Key Components

### Crowsnest Structure
```go
type Crowsnest struct {
    currentUpstreamURL string        // Currently discovered upstream
    lastDiscoveryTime  time.Time     // Last discovery timestamp
    discoveryInterval  time.Duration // How often to check upstream health
}
```

### UpstreamPricing Structure
```go
type UpstreamPricing struct {
    Metric           string              // "milliseconds" or "bytes"
    StepSize         uint64              // Size of each pricing step
    PricePerStep     map[string]uint64   // mint_url -> price
    PriceUnit        map[string]string   // mint_url -> unit ("sat", "eur", etc.)
    MinPurchaseSteps map[string]uint64   // mint_url -> minimum steps
    AcceptedMints    []string            // List of accepted mint URLs
}
```

## Core Functions

### Discovery and Configuration
- `New() *Crowsnest` - Create new crowsnest instance
- `DiscoverUpstreamRouter() (string, error)` - Discover upstream IP (placeholder)
- `SetUpstreamURL(url string)` - Set upstream URL (called by connection logic)
- `GetCurrentUpstreamURL() string` - Get currently configured upstream
- `IsUpstreamAvailable() bool` - Check if upstream is available

### Pricing Information
- `GetUpstreamPricing(upstreamURL string) (*UpstreamPricing, error)` - Main pricing fetch function
- `GetUpstreamAdvertisement(upstreamURL string) (*nostr.Event, error)` - Fetch TIP-01 advertisement
- `parseAdvertisementToPricing(event *nostr.Event) (*UpstreamPricing, error)` - Parse advertisement to pricing

### Monitoring
- `MonitorUpstreamConnection() error` - Check upstream health
- `StartMonitoring()` - Start periodic health monitoring

## Protocol Implementation

### TIP-03 HTTP Client
- **GET /**: Fetch upstream router advertisement
- **Timeout**: 10-second timeout for HTTP requests
- **Error Handling**: Connection failures, HTTP errors, invalid responses

### TIP-01 Advertisement Parsing
- **Event Kind**: 10021 (Discovery)
- **Required Tags**: `metric`, `step_size`
- **Pricing Tags**: `price_per_step` with format `["price_per_step", "cashu", price, unit, mint_url, min_steps]`
- **Signature Validation**: Verify event signature before processing

### TIP-02 Cashu Integration
- **Payment Method**: Only "cashu" payment method supported
- **Multi-Mint Support**: Parse multiple mint configurations
- **Pricing Structure**: Per-mint pricing with individual minimum purchase requirements

## Error Handling

### Connection Errors
- **Network Failures**: HTTP connection timeouts and errors
- **Invalid Responses**: Non-200 status codes, malformed JSON
- **Protocol Violations**: Wrong event kinds, missing required fields

### Validation Errors
- **Signature Verification**: Invalid or missing event signatures
- **Required Fields**: Missing metric, step_size, or pricing information
- **Data Consistency**: Invalid numeric values, malformed mint URLs

### Recovery Behavior
- **Connection Loss**: Clear upstream URL and notify merchant
- **Temporary Failures**: Log errors but maintain current configuration
- **Health Monitoring**: Periodic checks to detect and recover from failures

## Integration Points

### Merchant Module Interface
```go
// Merchant calls crowsnest to get pricing information
pricing, err := crowsnest.GetUpstreamPricing(upstreamURL)
if err != nil {
    // Handle pricing fetch failure
}

// Use pricing for purchase calculations
amount := calculatePurchaseAmount(pricing)
```

### Configuration Integration
- **Dynamic Discovery**: IP addresses are discovered, not configured
- **Health Monitoring**: Automatic monitoring and connection management
- **Fallback Handling**: Clear upstream state on persistent failures

## Monitoring and Logging

### Health Monitoring
- **Interval**: 30-second monitoring intervals
- **Health Checks**: Periodic advertisement fetches to verify connectivity
- **State Management**: Automatic clearing of failed upstream connections

### Logging Strategy
- **Discovery Events**: Log when upstream URLs are set or cleared
- **Pricing Fetches**: Log successful pricing information retrieval
- **Health Status**: Log connection health checks and failures
- **Error Details**: Detailed error logging for troubleshooting

## Testing Strategy

### Unit Tests
- **Discovery Functions**: Test URL management and availability checks
- **HTTP Client**: Test advertisement fetching with mock servers
- **Parsing Logic**: Test advertisement parsing with various event structures
- **Error Handling**: Test all error scenarios and edge cases

### Integration Tests
- **Protocol Compliance**: Test against real TIP-compliant upstream routers
- **Network Resilience**: Test behavior with network failures and timeouts
- **Multi-Mint Scenarios**: Test parsing of complex multi-mint advertisements

## Future Extensibility

### Out-of-Scope Features
- **Automatic Discovery**: Currently uses placeholder for actual network discovery
- **Multiple Upstreams**: Framework ready for multiple upstream management
- **Advanced Monitoring**: Health scoring and upstream selection logic

### Metric Support
- **Time-Based**: Full support for millisecond-based upstream pricing
- **Data-Based**: Architecture ready for byte-based pricing (future)
- **Flexible Parsing**: Generic parsing supports additional metrics

This module provides the foundation for upstream router interaction while maintaining clean separation of concerns and protocol compliance.