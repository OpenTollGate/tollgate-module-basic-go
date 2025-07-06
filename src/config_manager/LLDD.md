# Config Manager Low-Level Design Document

## Overview (Updated v0.0.4)

The `config_manager` package provides robust configuration management, including graceful handling of missing/corrupted files, version tracking, resilient installed version retrieval, migration support, pretty-printed JSON output, and a flexible metric-based pricing structure.

## Config Struct (v0.0.4)

The `Config` struct has been updated to reference the new `identities.json` file.

### `identities.json`

```json
[
  {
    "name": "operator",
    "npub": "npub1g53qcy6e58ycm3xrtacd993z265jfa2u848rtda7x2v5z4gha0dskp4l6t",
    "lightning_address": "tollgate@minibits.cash"
  },
  {
    "name": "developer",
    "npub": "npub1...",
    "lightning_address": "tollgate@minibits.cash"
  }
]
```
> **Note:** When `EnsureDefaultIdentities` creates a new `identities.json` file, the `npub` for the `operator` identity is derived from the `tollgate_private_key` in `config.json`. However, if an `identities.json` file already exists, this process will not overwrite any existing `npub` values.

### `config.json` (v0.0.4)

```json
{
  "config_version": "v0.0.4",
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
      "identity": "operator"
    },
    {
      "factor": 0.30,
      "identity": "developer"
    }
  ],
  "step_size": 60000,
  "metric": "milliseconds",
  "bragging": {
    "enabled": true,
    "fields": ["amount", "mint", "duration"]
  },
   "merchant": {
    "identity": "operator"
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

### Identity Struct
```go
type Identity struct {
	Name             string `json:"name"`
	Npub             string `json:"npub"`
	LightningAddress string `json:"lightning_address"`
}
```

### Removed Fields from `config.json`:
- `lightning_address` from `ProfitShareConfig`
- `name`, `lightning_address`, `website` from `MerchantConfig`

### Added Fields to `config.json`:
- `identity` to `ProfitShareConfig`
- `identity` to `MerchantConfig`

## Migration Support

### Migration Scripts (`files/etc/uci-defaults/`):
- **`tollgate-config-migration-v0.0.3-to-v0.0.4-migration`**:
    - **Purpose:** Migrates `config.json` from `v0.0.3` to `v0.0.4` and creates `identities.json`.
    - **Validation:** Checks if `config_version` is `v0.0.3`.
    - **Changes:**
        - Creates `identities.json` with "operator" and "developer" identities based on the existing `merchant` and `profit_share` fields. The operator's `npub` is derived from the `tollgate_private_key` if not already set.
        - Updates `config.json` to reference these identities.
        - Bumps `config_version` to `v0.0.4`.

### General Migration Process:
1.  **Existence and Integrity Checks:** Scripts first verify the presence, non-emptiness, and JSON validity of the configuration file.
2.  **Version Check:** Determine the current `config_version`. If it's already the target version or newer, the migration exits.
3.  **Backup Creation:** A timestamped backup of the original configuration file is created before any modifications.
4.  **Transformation:** `jq` is used to perform the necessary JSON transformations (e.g., adding/removing fields, modifying values).
5.  **Error Recovery:** The presence of backups allows for manual recovery in case of unexpected issues during migration.
6.  **Post-Migration Validation:** Implicitly, the `config_manager`'s `LoadConfig` and `EnsureDefaultConfig` (or `LoadInstallConfig` and `EnsureDefaultInstall`) functions will validate the migrated config's structure upon application startup.

## Core Functions

### NewConfigManager Function
- Creates a new `ConfigManager` instance with the specified file path.
- Initializes public and local Nostr relay pools.

### EnsureInitializedConfig Function
- Orchestrates the initialization of both main and install configurations.
- Calls `EnsureDefaultConfig()`, `EnsureDefaultInstall()`, and `EnsureDefaultIdentities()`.
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

### Handling Missing Fields in Existing Configurations

When an existing `config.json` is loaded (i.e., `config != nil`), the `EnsureDefaultConfig` function will perform checks for specific fields. If a field is found to be at its zero value (indicating it might be missing from an older configuration file), it will be populated with its default value. This ensures backward compatibility and prevents unexpected behavior when new fields are introduced.

The following fields will be checked and defaulted if missing:

- **`AcceptedMints`**: If `len(config.AcceptedMints) == 0`, populate with the default `MintConfig` list.
- **`ProfitShare`**: If `len(config.ProfitShare) == 0`, populate with the default `ProfitShareConfig` list.
- **`StepSize`**: If `config.StepSize == 0`, set to `600000`.
- **`Metric`**: If `config.Metric == ""`, set to `"milliseconds"`.
- **`Bragging`**: If `config.Bragging.Fields == nil` (or `config.Bragging.Enabled` is false and fields are empty), populate with the default `BraggingConfig` (`Enabled: true`, `Fields: ["amount", "mint", "duration"]`).
- **`Relays`**: If `len(config.Relays) == 0`, populate with the default list of relays (`"wss://relay.damus.io"`, `"wss://nos.lol"`, `"wss://nostr.mom"`).
- **`TrustedMaintainers`**: If `len(config.TrustedMaintainers) == 0`, populate with the default list of trusted maintainers (`"5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a"`).
- **`Merchant`**: If `config.Merchant.Identity == ""`, set `Identity` to `"operator"`.

### EnsureDefaultInstall Function (Updated)
- Ensures a default `InstallConfig` exists.
- If `LoadInstallConfig` returns `nil` (due to missing/invalid file), it creates a new `InstallConfig` with default values, including `ConfigVersion: "v0.0.2"`.
- If an existing `install.json` is loaded but is missing the `ConfigVersion` field, it will be populated with `"v0.0.1"` to signify its original unversioned state. This enables future migration scripts to identify and upgrade it.
- Populates missing fields from older versions with default values, ensuring backward compatibility.

### Handling Missing Fields in Existing Installations

When an existing `install.json` is loaded (i.e., `installConfig != nil`), the `EnsureDefaultInstall` function will perform checks for specific fields. If a field is found to be at its zero value (indicating it might be missing from an older installation file), it will be populated with its default value. This ensures backward compatibility and prevents unexpected behavior when new fields are introduced.

The following fields will be checked and defaulted if missing:

- **`PackagePath`**: If `installConfig.PackagePath == ""`, set to `""`. (Note: The previous default was "false", which is now handled by setting to empty string).
- **`IPAddressRandomized`**: If `installConfig.IPAddressRandomized` is false (and it should be true), set to `true`.
- **`InstallTimestamp`**: If `installConfig.InstallTimestamp == 0`, set to `0` (unknown).
- **`DownloadTimestamp`**: If `installConfig.DownloadTimestamp == 0`, set to `0` (unknown).
- **`ReleaseChannel`**: If `installConfig.ReleaseChannel == ""`, set to `"stable"`.
- **`EnsureDefaultTimestamp`**: If `installConfig.EnsureDefaultTimestamp == 0`, set to the current timestamp.
- **`InstalledVersion`**: If `installConfig.InstalledVersion == ""`, set to `"0.0.0"`.

### EnsureDefaultIdentities Function
- Ensures a default `identities.json` file exists.
- If the file is missing, it creates one with default "operator" and "developer" identities.

### Handling Missing Fields in Existing Identities Configurations

When an existing `identities.json` is loaded, the `EnsureDefaultIdentities` function will perform checks for specific fields within the `Identity` structs. If a field is found to be at its zero value (indicating it might be missing from an older identities file), it will be populated with its default value. This ensures backward compatibility and prevents unexpected behavior when new fields are introduced.

The following fields will be checked and defaulted if missing:

- **`Npub`**: If an `Identity` has `Npub == ""`, and its `Name` is "operator", the `Npub` will be derived from the `tollgate_private_key` in `config.json`.
- **`LightningAddress`**: If an `Identity` has `LightningAddress == ""`, it will be set to `"tollgate@minibits.cash"`.

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

## InstallConfig Struct (v0.0.2)

The `InstallConfig` struct holds the installation configuration parameters, including details about the installed package, timestamps, and config version:

```go
type InstallConfig struct {
	ConfigVersion          string `json:"config_version"`
	PackagePath            string `json:"package_path"`
	IPAddressRandomized    bool   `json:"ip_address_randomized"`
	InstallTimestamp       int64  `json:"install_time"`
	DownloadTimestamp      int64  `json:"download_time"`
	ReleaseChannel         string `json:"release_channel"`
	EnsureDefaultTimestamp int64  `json:"ensure_default_timestamp"`
	InstalledVersion       string `json:"installed_version"`
}
```

**Fields:**
- `ConfigVersion`: The version of the `install.json` schema. New installations will default to `"v0.0.2"`. Unversioned existing files will be treated as `"v0.0.1"`.
- `PackagePath`: Path to the installed package.
- `IPAddressRandomized`: Indicates if the IP address has been randomized (boolean).
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
    - **Default Field Population:** New unit tests will verify that `EnsureDefaultConfig`, `EnsureDefaultInstall`, and `EnsureDefaultIdentities` correctly populate missing fields with their default values when an existing (but incomplete) configuration file is loaded. This includes testing various combinations of missing fields.
- Mocking of external dependencies (e.g., `nostr.SimplePool` for relay interactions) in tests.
- Migration testing implicitly covered by the robustness tests of `LoadConfig` and `EnsureDefaultConfig` when encountering older or invalid formats.
- Pretty-printed JSON output validation is performed.
- Backward compatibility verification is ensured through the default value population in `EnsureDefaultInstall` for older `install.json` formats.
