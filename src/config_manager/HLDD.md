# config_manager HLDD

## Overview

The `config_manager` package provides a `ConfigManager` struct that manages configuration stored in multiple files, including a main configuration file (`config.json`), an installation configuration file (`install.json`), and an identities file (`identities.json`). It includes migration support, pretty-printed JSON output, and flexible metric-based pricing structure.

## Responsibilities (Updated v0.0.4)

- Initialize with a specific file path for the main configuration
- Load and save configuration with pretty-printed JSON formatting, handling missing/corrupted files gracefully
- Manage migration between configuration versions (v0.0.1 → v0.0.2 → v0.0.3) for `config.json` and `install.json` with robust validation
- Ensure default configuration and installation configuration exist and are properly initialized
- Track and manage the current installed version and installation ID, ensuring consistency with NIP94 events
- Provide resilient retrieval of installed package version, including retry mechanisms for OpenWRT `opkg`
- Handle flexible metric-based pricing structure
- Store and manage `release_channel` information for packages
- Provide mint fee retrieval functionality
- Manage identities for profit sharing and merchant information through `identities.json`.

## Configuration Structure Changes

### Identities Config (`identities.json`):
- **Added**: A new file to store a list of identities.
- Each identity has a `name`, `npub`, and `lightning_address`.
- The `name` field serves as a unique identifier.

### Main Config (`config.json`):
- **Version**: v0.0.4
- **Modified**: `MerchantConfig` now references an identity from `identities.json`.
- **Modified**: `ProfitShareConfig` now references an identity from `identities.json`.
- **Removed**: `lightning_address` from `ProfitShareConfig`.
- **Removed**: `name`, `lightning_address`, `website` from `MerchantConfig`.
- **Added**: `identity` to `MerchantConfig`.
- **Added**: `identity` to `ProfitShareConfig`.

### MintConfig Structure:
- `PricePerStep`: Individual pricing per mint
- `PriceUnit`: Unit of pricing (e.g., "sat")
- `MinPurchaseSteps`: Minimum purchase requirement per mint
- Existing fields: URL, MinBalance, BalanceTolerancePercent, etc.

### Install Config (`install.json`):
- **Added**: `ConfigVersion` for version tracking of the `install.json` schema.

## Interfaces

- `NewConfigManager(filePath string) (*ConfigManager, error)`: Creates a new `ConfigManager` instance with the specified file path
- `EnsureInitializedConfig() error`: Ensures default main configuration, install configuration, and identities file exist, creating them if necessary. This is the primary entry point for ensuring a valid system state.
- `LoadConfig() (*Config, error)`: Reads the main configuration from the managed file. Handles cases where the file is missing, empty, or malformed.
- `SaveConfig(config *Config) error`: Writes configuration with pretty-printed JSON formatting.
- `LoadInstallConfig() (*InstallConfig, error)`: Reads the installation configuration from `install.json`. Handles cases where the file is missing, empty, or malformed.
- `SaveInstallConfig(installConfig *InstallConfig) error`: Writes the installation configuration to `install.json`.
- `LoadIdentities() ([]Identity, error)`: Reads the identities from `identities.json`.
- `SaveIdentities(identities []Identity) error`: Writes the identities to `identities.json`.
- `EnsureDefaultConfig() (*Config, error)`: Ensures a default main configuration exists with v0.0.3 structure, generating a private key if needed.
- `EnsureDefaultInstall() (*InstallConfig, error)`: Ensures a default install configuration exists, populating missing fields from older versions.
- `EnsureDefaultIdentities() ([]Identity, error)`: Ensures a default identities file exists, creating it with default "operator" and "developer" identities if necessary.
- `UpdateCurrentInstallationID() error`: Compares the installed version with the NIP94 event version and resets `CurrentInstallationID` if they don't match.
- `GetNIP94Event(eventID string) (*nostr.Event, error)`: Fetches a NIP-94 event from configured relays.
- `ExtractPackageInfo(event *nostr.Event) (*PackageInfo, error)`: Extracts package information from a NIP-94 event.
- `GetInstalledVersion() (string, error)`: Retrieves the installed package version, with retry logic for `opkg`.
- `GetArchitecture() (string, error)`: Retrieves the device architecture from OpenWRT.
- `GetTimestamp() (int64, error)`: Retrieves the installation timestamp from NIP94 event or install config.
- `GetReleaseChannel() (string, error)`: Determines the release channel from NIP94 event or install config.
- `GetPublicPool() *nostr.SimplePool`: Returns the public Nostr relay pool.
- `GetLocalPool() *nostr.SimplePool`: Returns the local Nostr relay pool.
- `PublishToLocalPool(event nostr.Event) error`: Publishes an event to the local relay.
- `QueryLocalPool(filters []nostr.Filter) (chan *nostr.Event, error)`: Queries events from the local relay.
- `GetLocalPoolEvents(filters []nostr.Filter) ([]*nostr.Event, error)`: Retrieves all events from the local pool matching filters.
- `GetMintFee(mintURL string) (uint64, error)`: Retrieves mint fees (delegates to tollwallet).

## Migration Support

### Configuration Migration (`config.json`):
- Automatic detection of configuration version
- Migration scripts for v0.0.3 → v0.0.4 transformation
- Backup creation and error recovery
- Version-specific migration guards

### Install Configuration Migration (`install.json`):
- Migration script for unversioned (`v0.0.1`) to `v0.0.2` transformation.
- Automatic detection of `install.json` version.
- Backup creation and error recovery.
- Version-specific migration guards.

### Default Configuration (v0.0.3):
- `Metric`: "milliseconds"
- `StepSize`: 60000 (1 minute in milliseconds)
- Mint-specific `PricePerStep` instead of global `PricePerMinute`

## Pretty-Printed JSON

All configuration files are saved with `json.MarshalIndent()` using 2-space indentation for human readability and easier manual editing.

## Centralized Rate Limiting for relayPool

To address the 'too many concurrent REQs' error, centralized rate limiting for `relayPool` is implemented within `config_manager`. This involves:

- Initializing `relayPool` in `config_manager` with a semaphore to limit concurrent requests.
- Providing a controlled access mechanism through `rateLimitedRelayRequest` and other member functions.
- Ensuring all services using `relayPool` are rate-limited, preventing excessive concurrent requests to relays.

## Robustness Improvements

- **Config File Handling:** The `LoadConfig` and `LoadInstallConfig` functions now gracefully handle scenarios where configuration files are missing, empty, or contain malformed JSON. Instead of returning an error, they return `nil` config, which triggers the `EnsureDefaultConfig` or `EnsureDefaultInstall` functions to create a default valid configuration. This prevents application startup failures due to invalid config states.
- **Installed Version Tracking:** The `InstallConfig` struct includes an `InstalledVersion` field. The `UpdateCurrentInstallationID` function ensures that if the locally installed version (obtained via `GetInstalledVersion()`) does not match the version advertised in the `CurrentInstallationID`'s NIP94 event, the `CurrentInstallationID` is reset. This prompts a re-evaluation or re-initialization of the installation state, preventing inconsistencies.
- **Resilient Version Retrieval:** The `GetInstalledVersion()` function now incorporates a retry mechanism with exponential backoff when `opkg` encounters temporary lock issues. This improves the reliability of version detection, especially in resource-constrained or busy OpenWRT environments.
