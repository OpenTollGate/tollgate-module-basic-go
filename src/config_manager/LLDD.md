## Config Struct

The `Config` struct holds the main configuration parameters as defined:

```json
{
  "tollgate_private_key": "8a45d0add1c7ddf668f9818df550edfa907ae8ea59d6581a4ca07473d468d663",
  "accepted_mints": ["https://mint.minibits.cash/Bitcoin"],
  "price_per_minute": 1,
  "bragging": {
    "enabled": true,
    "fields": ["amount", "mint", "duration"]
  },
  "relays": [
    "wss://relay.damus.io",
    "wss://nos.lol",
    "wss://nostr.mom",
    "wss://relay.tollgate.me"
  ],
  "trusted_maintainers": [
    "5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a"
  ],
  "fields_to_be_reviewed": [
    "price_per_minute",
    "relays",
    "tollgate_private_key",
    "trusted_maintainers"
  ],
  "NIP94EventID_currently_installed": []
}
```

## PackageInfo Struct

The `PackageInfo` struct holds information extracted from NIP-94 events:

```go
type PackageInfo struct {
	Version        string
	Branch         string
	Timestamp      int64
	ReleaseChannel string
}
```

## InstallConfig Struct

The `InstallConfig` struct holds the installation configuration parameters:

```json
{
  "package_path": "/path/to/package",
  "nip94_event_id": "e74289953053874ae0beb31bea8767be6212d7a1d2119003d0853e115da23597"
}
```

## ConfigManager Struct

The `ConfigManager` struct manages both the main configuration file and the installation configuration file (`install.json`).

## NewConfigManager Function

- Creates a new `ConfigManager` instance with the specified file path for the main configuration.
- Calls `EnsureDefaultConfig` to ensure a valid main configuration exists.

## LoadConfig Function

- Reads the main configuration from the managed file.

## SaveConfig Function

- Writes the main configuration to the managed file.

## LoadInstallConfig Function

- Reads the installation configuration from `install.json`.

## SaveInstallConfig Function

- Writes the installation configuration to `install.json`.

## EnsureDefaultConfig Function

- Ensures a default main configuration exists, creating it if necessary.
- Includes defaults for `bragging`, `relays`, `trusted_maintainers`, and other parameters.



## EnsureDefaultConfig Function

- Attempts to load the configuration from the managed file.
- If no configuration file exists or is invalid, creates a default `Config` struct with the following defaults:
  - `accepted_mint`: "https://mint.minibits.cash/Bitcoin"
  - `bragging`: enabled with fields "amount", "mint", "duration"
  - `nip94_event_id`: the ID of the NIP94 event announcing the package
  - `price_per_minute`: hardcoded value if not set
  - `relays`: hardcoded list if not set
  - `tollgate_private_key`: generated using nostr tools if not set
  - `trusted_maintainers`: hardcoded list with a warning to review
  - `fields_to_be_reviewed`: list of fields that require user attention, including:
    - `price_per_minute` if not already set
    - `relays` if not already set
    - `tollgate_private_key` if not already set
    - `trusted_maintainers` if not already set
- Saves the default configuration to the managed file.
- Returns the loaded or default configuration and any error encountered.

## GetNIP94Event Function

- Fetches a NIP-94 event from a relay using the provided event ID.
- Iterates through the configured relays to find the event.

## ExtractPackageInfo Function

- Extracts `PackageInfo` from a given NIP-94 event.

## Janitor Integration

The `Janitor` updates the `install.json` with the package path and NIP94 event ID when a new package is installed.

## GetReleaseChannel Function

- Retrieves the release channel from the `PackageInfo` struct.
- Returns the current release channel as a string.
