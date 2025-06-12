# Low-Level Design Document: crowsnest

## Overview

The `crowsnest` module identifies available network interfaces that are already connected and determines which ones are free networks and which are tollgates. It parses pricing information from tollgate merchants and provides this data to the merchant module.

## Data Structures

### NetworkInterface

The `NetworkInterface` struct represents a network interface with the following fields:

```go
type NetworkInterface struct {
    Name        string     // Name of the interface (e.g., "wlan0")
    IsTollgate  bool       // Whether the interface is a tollgate
    IsAvailable bool       // Whether the interface is available for connection
    Metric      string     // Metric used for pricing (e.g., "milliseconds")
    StepSize    uint64     // Size of each pricing step
    PricePerStep uint64    // Price per step in satoshis
    URL         string     // URL for the tollgate (only for tollgates)
    AcceptedMints []string // List of accepted mints
}
```

### Crowsnest

The `Crowsnest` struct manages network interfaces with the following fields:

```go
type Crowsnest struct {
    config        *config_manager.Config
    interfaces    []NetworkInterface
}
```

## Functions

### New()

```go
func New(configManager *config_manager.ConfigManager) (*Crowsnest, error)
```

Creates a new Crowsnest instance:
1. Loads configuration using the provided ConfigManager
2. Initializes the interfaces array
3. Returns the Crowsnest instance or an error

### GetConnected()

```go
func (c *Crowsnest) GetConnected() ([]NetworkInterface, error)
```

Returns a list of available network interfaces:
1. Uses `ip route` or similar commands to identify connected interfaces
2. Determines which interfaces are connected to tollgates by checking for merchant API availability
3. For tollgate interfaces, parses the merchant advertisement to extract pricing details
4. Returns the list of interfaces with their details

### ParseMerchantAdvertisement()

```go
func (c *Crowsnest) ParseMerchantAdvertisement(advertisementJSON string) (NetworkInterface, error)
```

Parses a merchant advertisement to extract pricing and mint information:
1. Decodes the JSON into a nostr.Event
2. Extracts the metric, step_size, and price_per_step from the tags
3. Extracts the list of accepted mints
4. Creates a NetworkInterface struct with the parsed information

## Error Handling

- All functions return appropriate errors when operations fail
- GetConnected() handles failures for individual interfaces gracefully

## Testing

Unit tests cover:
- Parsing merchant advertisements with different pricing structures
- Interface detection with mock system outputs
- Error handling for various scenarios