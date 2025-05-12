# Janitor Module Low-Level Design Document

## Overview

The Janitor module is designed to listen for NIP-94 events announcing new OpenWRT packages, download and install the new package if it is newer than the currently installed one, and ensure the integrity and security of the installation process.

## Requirements

* The module should listen for NIP-94 events signed by trusted maintainers.
* The module should verify the checksum of the downloaded package before installation.
* The module should handle errors and exceptions during the package installation process.
* The module should compare version numbers to determine if a new package is newer than the currently installed one, considering the release channel.

## Configuration

The configuration data for the Janitor module will be stored in a JSON file named `config.json`. The following is an example of its structure:

```json
{
  "tollgate_private_key": "8a45d0add1c7ddf668f9818df550edfa907ae8ea59d6581a4ca07473d468d663",
  "accepted_mint": "https://mint.minibits.cash/Bitcoin",
  "mint_fee": 0,
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
  ]
}
```

## NIP-94 Event Format

The NIP-94 event that announces a new OpenWRT package has the following format:

```json
{
  "id": "b5fbf776e2b0bcaca4cc0343a49101787db853cbf32582d15926b536548e83dc",
  "pubkey": "5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a",
  "created_at": 1746436890,
  "kind": 1063,
  "content": "TollGate Module Package: basic for gl-mt3000",
  "tags": [
    ["url", "https://blossom.swissdash.site/28d3dd37c76ab69a3de4eb921db63f509b212a2954cb9abb58c531aac28696e5.ipk"],
    ["m", "application/octet-stream"],
    ["x", "28d3dd37c76ab69a3de4eb921db63f509b212a2954cb9abb58c531aac28696e5"],
    ["ox", "28d3dd37c76ab69a3de4eb921db63f509b212a2954cb9abb58c531aac28696e5"],
    ["filename", "basic-gl-mt3000-aarch64_cortex-a53.ipk"],
    ["architecture", "aarch64_cortex-a53"],
    ["version", "multiple_mints_rebase_taglist-b97e743"],
    ["release_channel", "dev"]
  ]
}
```

For the dev channel, the version string is of the format `[branch_name].[commit_count].[commit_hash]`. For the stable channel, the version number is just the release tag (e.g., `0.0.1`).

## Code Structure

The code will be structured as follows:

* `janitor.go`: the main file for the Janitor module.
* `nip94.go`: a file containing functions for handling NIP-94 events.

## Functions

### ListenForNIP94Events

* Listen for NIP-94 events on the specified relays.

### DownloadPackage

* Download a package from a given URL.

### InstallPackage

* Install a package using opkg, considering the release channel.

## Version Comparison Logic

The `isNewerVersion` function compares the new version with the current version. For the stable release channel, it uses the `version` package to compare version numbers. If the release channel is dev, it returns an error as version comparison is not applicable for dev builds.

## Error Handling

The Janitor module will handle errors and exceptions during the package installation process by:

* Logging the error using a simple logging mechanism.
* Retrying the installation process if it fails.

## Logging

The Janitor module will use a simple logging mechanism to log events and errors.

## Testing

Unit tests will be written to ensure that the Janitor module functions correctly and handles errors properly.

## Post-Installation

After installing a new package, the Janitor module updates the `install.json` file with the new package path and NIP94 event ID using the ConfigManager.

## Instructions for Engineers Implementing the Feature

1. Update the Janitor module to distinguish between dev and stable channels based on the `release_channel` tag in NIP-94 events.
2. Modify the version comparison logic to handle the new versioning scheme for dev and stable channels.

## Checklist

- [ ] Update Janitor module to handle `release_channel`.
- [ ] Modify version comparison logic.
- [ ] Update documentation to reflect changes.

## Handling Multiple Mints

The Janitor module has been updated to handle multiple mints. The `ConfigManager` now supports multiple accepted mints through the `accepted_mints` field in the `Config` struct. This enhancement allows the TollGate to process NIP-94 events for multiple mints, improving its functionality and user experience.
## Centralized Rate Limiting for relayPool

To address the 'too many concurrent REQs' error, we will implement centralized rate limiting for `relayPool` within `config_manager`. This involves initializing `relayPool` in `config_manager` and providing a controlled access mechanism through a member function. This approach ensures that all services using `relayPool`, including the Janitor module, are rate-limited, preventing excessive concurrent requests to relays.
