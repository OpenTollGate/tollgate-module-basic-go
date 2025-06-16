# config_manager HLDD

## Overview

The `config_manager` package provides a `ConfigManager` struct that manages configuration stored in multiple files, including a main configuration file and an installation configuration file (`install.json`). It includes migration support, pretty-printed JSON output, and flexible metric-based pricing structure.

## Responsibilities (Updated v0.0.4)

- Initialize with a specific file path for the main configuration
- Load and save configuration with pretty-printed JSON formatting
- Manage migration between configuration versions (v0.0.2 → v0.0.3)
- Handle flexible metric-based pricing structure
- Store and manage `release_channel` information for packages
- Provide mint fee retrieval functionality

## Configuration Structure Changes

### Main Config:
- **Removed**: `PricePerMinute` (global pricing)
- **Added**: `Metric` and `StepSize` for flexible pricing units
- **Enhanced**: Mint-specific configuration with detailed settings

### MintConfig Structure:
- `PricePerStep`: Individual pricing per mint
- `PriceUnit`: Unit of pricing (e.g., "sat")
- `MinPurchaseSteps`: Minimum purchase requirement per mint
- Existing fields: URL, MinBalance, BalanceTolerancePercent, etc.

## Interfaces

- `NewConfigManager(filePath string) (*ConfigManager, error)`: Creates a new `ConfigManager` instance with the specified file path
- `LoadConfig() (*Config, error)`: Reads the main configuration from the managed file
- `SaveConfig(config *Config) error`: Writes configuration with pretty-printed JSON formatting
- `LoadInstallConfig() (*InstallConfig, error)`: Reads the installation configuration from `install.json`
- `SaveInstallConfig(installConfig *InstallConfig) error`: Writes the installation configuration to `install.json`
- `EnsureDefaultConfig() (*Config, error)`: Ensures a default main configuration exists with v0.0.3 structure
- `GetMintFee(mintURL string) (uint64, error)`: Retrieves mint fees (delegates to tollwallet)

## Migration Support

### Configuration Migration:
- Automatic detection of configuration version
- Migration scripts for v0.0.2 → v0.0.3 transformation
- Backup creation and error recovery
- Version-specific migration guards

### Default Configuration (v0.0.3):
- `Metric`: "milliseconds"
- `StepSize`: 60000 (1 minute in milliseconds)
- Mint-specific `PricePerStep` instead of global `PricePerMinute`

## Pretty-Printed JSON

All configuration files are saved with `json.MarshalIndent()` using 2-space indentation for human readability and easier manual editing.

## Centralized Rate Limiting for relayPool

To address the 'too many concurrent REQs' error, we implement centralized rate limiting for `relayPool` within `config_manager`. This involves initializing `relayPool` in `config_manager` and providing a controlled access mechanism through a member function. This approach ensures that all services using `relayPool` are rate-limited, preventing excessive concurrent requests to relays.
