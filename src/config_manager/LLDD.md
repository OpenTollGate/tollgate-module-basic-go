# Config Manager Low-Level Design Document

## Overview (Updated v0.0.4)

The `config_manager` package provides configuration management with migration support, pretty-printed JSON output, and flexible metric-based pricing structure.

## Config Struct (v0.0.3)

The `Config` struct has been updated for flexible metric-based pricing:

```json
{
  "config_version": "v0.0.3",
  "tollgate_private_key": "8a45d0add1c7ddf668f9818df550edfa907ae8ea59d6581a4ca07473d468d663",
  "accepted_mints": [
    {
      "url": "https://mint.minibits.cash/Bitcoin",
      "min_balance": 100,
      "balance_tolerance_percent": 10,
      "payout_interval_seconds": 60,
      "min_payout_amount": 200,
      "price_per_step": 1,
      "price_unit": "sat",
      "purchase_min_steps": 0
    }
  ],
  "profit_share": [
    {
      "factor": 0.70,
      "lightning_address": "tollgate@minibits.cash"
    },
    {
      "factor": 0.30,
      "lightning_address": "tollgate@minibits.cash"
    }
  ],
  "step_size": 60000,
  "metric": "milliseconds",
  "bragging": {
    "enabled": true,
    "fields": ["amount", "mint", "duration"]
  },
  "relays": [
    "wss://relay.damus.io",
    "wss://nos.lol",
    "wss://nostr.mom"
  ],
  "trusted_maintainers": [
    "5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a"
  ],
  "show_setup": true,
  "current_installation_id": ""
}
```

## Configuration Structure Changes

### Removed Fields:
- `price_per_minute`: Global pricing removed

### Added Fields:
- `step_size`: Configurable step size (e.g., 60000 for 1 minute in milliseconds)
- `metric`: Pricing metric ("milliseconds", "bytes", etc.)

### Enhanced MintConfig:
- `price_per_step`: Individual pricing per mint
- `price_unit`: Unit of pricing (e.g., "sat")
- `purchase_min_steps`: Minimum purchase requirement per mint

## Migration Support

### Migration Functions:
- Automatic version detection in `EnsureDefaultConfig()`
- Migration scripts handle v0.0.2 → v0.0.3 transformation
- Backup creation with timestamped files
- Error recovery with backup restoration

### Migration Process:
1. Check configuration version
2. Create timestamped backup
3. Transform configuration structure
4. Convert `price_per_minute` to mint-specific `price_per_step`
5. Add `metric` and `step_size` fields
6. Verify migration success

## Core Functions

### NewConfigManager Function
- Creates a new `ConfigManager` instance with the specified file path
- Calls `EnsureDefaultConfig` to ensure valid configuration exists
- Initializes relay pools for centralized rate limiting

### LoadConfig Function
- Reads the main configuration from the managed file
- Handles version detection and migration triggers

### SaveConfig Function (Enhanced)
- Writes configuration using `json.MarshalIndent()` with 2-space indentation
- Creates human-readable, easily editable configuration files

### EnsureDefaultConfig Function (Updated)
- Ensures default v0.0.3 configuration exists
- Creates configuration with:
  - `metric`: "milliseconds"
  - `step_size`: 60000
  - Mint-specific `price_per_step`: 1
  - Default accepted mints with complete configuration

## PackageInfo Struct

The `PackageInfo` struct holds information extracted from NIP-94 events:

```go
type PackageInfo struct {
	Version        string
	Timestamp      int64
	ReleaseChannel string
}
```

## InstallConfig Struct

The `InstallConfig` struct holds the installation configuration parameters:

```json
{
  "package_path": "/path/to/package",
  "current_installation_id": "e74289953053874ae0beb31bea8767be6212d7a1d2119003d0853e115da23597",
  "download_timestamp": 1674567890
}
```

## Helper Functions

### ExtractPackageInfo Function
- Extracts `PackageInfo` from a given NIP-94 event
- Handles version, timestamp, and release channel extraction

### GetNIP94Event Function
- Fetches a NIP-94 event from a relay using the provided event ID
- Iterates through configured relays to find the event
- Implements rate limiting for relay requests

## Centralized Rate Limiting for relayPool

To address the 'too many concurrent REQs' error, we implement centralized rate limiting for `relayPool` within `config_manager`. This involves:

- Initializing `relayPool` in `config_manager`
- Providing controlled access through member functions
- Ensuring all services using `relayPool` are rate-limited
- Preventing excessive concurrent requests to relays

## Testing

- Updated test files for new configuration structure
- Migration testing with v0.0.2 configuration samples
- Pretty-printed JSON output validation
- Backward compatibility verification
