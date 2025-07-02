# Config Manager Low-Level Design Document

## Overview (Updated v0.0.4)

The `config_manager` package provides robust configuration management, including graceful handling of missing/corrupted files, version tracking, resilient installed version retrieval, migration support, pretty-printed JSON output, and a flexible metric-based pricing structure.

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

### Migration Scripts (`files/etc/uci-defaults/`):
- **`98-tollgate-config-migration-v0.0.1-to-v0.0.2-migration`**:
    - **Purpose:** Migrates `config.json` from `v0.0.1` (no `config_version` field) to `v0.0.2`.
    - **Validation:** Includes robust checks for file existence, non-emptiness, and valid JSON. It also verifies the presence of `accepted_mints` (as an array) to confirm it's a `v0.0.1` structure.
    - **Changes:** Adds the `config_version` field.
- **`99-tollgate-config-migration-v0.0.2-to-v0.0.3-migration`**:
    - **Purpose:** Migrates `config.json` from `v0.0.2` to `v0.0.3`.
    - **Validation:** Similar robust checks for file existence, non-emptiness, and valid JSON. Crucially, it specifically checks that `config_version` is *exactly* `v0.0.2` and that `price_per_minute` exists (and is not null) to ensure correct migration.
    - **Changes:** Converts `price_per_minute` to mint-specific `price_per_step`, adds `metric` and `step_size` fields, and updates the `config_version` to `v0.0.3`.

### General Migration Process:
1.  **Existence and Integrity Checks:** Scripts first verify the presence, non-emptiness, and JSON validity of the `config.json` file.
2.  **Version Check:** Determine the current `config_version`. If it's already the target version or newer, the migration exits.
3.  **Backup Creation:** A timestamped backup of the original `config.json` is created before any modifications.
4.  **Transformation:** `jq` is used to perform the necessary JSON transformations (e.g., adding/removing fields, modifying values).
5.  **Error Recovery:** The presence of backups allows for manual recovery in case of unexpected issues during migration.
6.  **Post-Migration Validation:** Implicitly, the `config_manager`'s `LoadConfig` and `EnsureDefaultConfig` functions will validate the migrated config's structure upon application startup.

## Core Functions

### NewConfigManager Function
- Creates a new `ConfigManager` instance with the specified file path.
- Initializes public and local Nostr relay pools.

### EnsureInitializedConfig Function
- Orchestrates the initialization of both main and install configurations.
- Calls `EnsureDefaultConfig()` and `EnsureDefaultInstall()`.
- Updates the `CurrentInstallationID` based on the installed version.

### LoadConfig Function
- Reads the main configuration from the managed file (`config.json`).
- **Robustness:** If the file does not exist, is empty, or contains malformed JSON, it returns `nil` and a `nil` error (if `os.IsNotExist` or unmarshalling error), allowing `EnsureDefaultConfig` to create a new default.

### SaveConfig Function (Enhanced)
- Writes the `Config` struct to the managed file using `json.MarshalIndent()` with 2-space indentation for human readability.

### LoadInstallConfig Function
- Reads the installation configuration from `install.json`.
- **Robustness:** Similar to `LoadConfig`, handles missing, empty, or malformed `install.json` by returning `nil` config, triggering `EnsureDefaultInstall`.

### SaveInstallConfig Function
- Writes the `InstallConfig` struct to `install.json`.

### EnsureDefaultConfig Function (Updated)
- Ensures a default `Config` exists. If `LoadConfig` returns `nil` (due to missing/invalid file), it generates a new private key and populates a default `Config` struct with `v0.0.3` version, default mints, profit share, relays, and merchant info.
- Calls `setUsername` after saving the initial config to publish profile metadata.

### EnsureDefaultInstall Function (Updated)
- Ensures a default `InstallConfig` exists. If `LoadInstallConfig` returns `nil`, it creates a new `InstallConfig` with default values (e.g., `InstalledVersion: "0.0.0"`, `ReleaseChannel: "stable"`).
- If an existing `install.json` is loaded but is missing fields from older versions, it populates those fields with default values, ensuring backward compatibility.

### UpdateCurrentInstallationID Function
- Loads the current `Config`.
- If `CurrentInstallationID` is set, it fetches the corresponding NIP94 event and extracts `PackageInfo`.
- **Consistency Check:** If the `InstalledVersion` (obtained via `GetInstalledVersion()`) does not match the version from the NIP94 event, it clears `CurrentInstallationID` in the config and saves it. This prompts the system to re-evaluate its installation state, potentially leading to a new installation ID being set.

### GetInstalledVersion Function
- Retrieves the installed `tollgate` package version using `opkg list-installed`.
- **Resilience:** Implements a retry mechanism with exponential backoff (up to 5 attempts) to handle `opkg.lock` issues (`Resource temporarily unavailable`).
- Returns a default version (`0.0.1+1cac608`) if `opkg` is not found, useful for development environments.

### GetNIP94Event Function
- Fetches a NIP-94 event given an `eventID` from configured public relays.
- Utilizes a rate-limited relay request mechanism (`rateLimitedRelayRequest`) to prevent overwhelming relays.

### ExtractPackageInfo Function
- Extracts `Version`, `Timestamp`, and `ReleaseChannel` from a given Nostr event's tags.

### GetTimestamp Function
- Determines the installation timestamp, preferring the NIP94 event timestamp if `CurrentInstallationID` is set, otherwise deriving it from `InstallConfig` (prioritizing `DownloadTimestamp`, then `InstallTimestamp`, then `EnsureDefaultTimestamp`).

### GetReleaseChannel Function
- Determines the release channel, preferring the NIP94 event's release channel if `CurrentInstallationID` is set, otherwise using the `InstallConfig`'s `ReleaseChannel`.

### GeneratePrivateKey Function
- Generates a new Nostr private key.

### SetUsername Function
- Publishes a NIP-01 kind 0 (profile metadata) event to configured relays, setting the profile `name` to the provided username.
- Uses `rateLimitedRelayRequest` for publishing.

### GetPublicPool and GetLocalPool Functions
- Provide access to the initialized Nostr simple pools for public and local relays, respectively.

### PublishToLocalPool and QueryLocalPool Functions
- Facilitate interaction with the local Nostr relay (e.g., `ws://localhost:4242`) for publishing and querying events, primarily for internal application communication.

### GetLocalPoolEvents Function
- Retrieves all events from the local pool matching specified filters, handling EOSE (End of Stored Events) and implementing a fallback timeout.

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

The `InstallConfig` struct holds the installation configuration parameters, including details about the installed package and timestamps:

```go
type InstallConfig struct {
	PackagePath            string `json:"package_path"`
	IPAddressRandomized    string `json:"ip_address_randomized"`
	InstallTimestamp       int64  `json:"install_time"`
	DownloadTimestamp      int64  `json:"download_time"`
	ReleaseChannel         string `json:"release_channel"`
	EnsureDefaultTimestamp int64  `json:"ensure_default_timestamp"`
	InstalledVersion       string `json:"installed_version"`
}
```

**Fields:**
- `PackagePath`: Path to the installed package.
- `IPAddressRandomized`: Indicates if the IP address has been randomized.
- `InstallTimestamp`: Timestamp of the installation.
- `DownloadTimestamp`: Timestamp of the package download.
- `ReleaseChannel`: The release channel (e.g., "stable", "dev").
- `EnsureDefaultTimestamp`: Timestamp when default install config was ensured.
- `InstalledVersion`: The version of the installed package.

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

- Extensive unit tests (`config_manager_test.go`) cover `ConfigManager` functionality, including:
    - `EnsureDefaultConfig` and `EnsureDefaultInstall` creation and population.
    - Loading and saving of both main and install configurations, including scenarios with missing, empty, or malformed files.
    - Verification of private key generation and username setting.
    - `UpdateCurrentInstallationID` behavior, especially when versions mismatch.
- Mocking of external dependencies (e.g., `nostr.SimplePool` for relay interactions) in tests.
- Migration testing implicitly covered by the robustness tests of `LoadConfig` and `EnsureDefaultConfig` when encountering older or invalid formats.
- Pretty-printed JSON output validation is performed.
- Backward compatibility verification is ensured through the default value population in `EnsureDefaultInstall` for older `install.json` formats.
