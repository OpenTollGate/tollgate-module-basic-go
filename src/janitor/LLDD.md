# Janitor Module Low-Level Design Document

## Overview

The Janitor module is designed to listen for NIP-94 events announcing new OpenWRT packages, download and install the new package if it is newer than the currently installed one, and ensure the integrity and security of the installation process.

## Requirements

* The module should listen for NIP-94 events signed by trusted maintainers.
* The module should verify the checksum of the downloaded package before installation.
* The module should handle errors and exceptions during the package installation process.
* The module should compare version numbers to determine if a new package is newer than the currently installed one, considering the release channel.

## Configuration

The Janitor module's configuration is stored in `/etc/tollgate/janitor.json`.

```json
{
  "config_version": "v0.0.2",
  "package_path": "",
  "enabled": false,
  "ip_address_randomized": true,
  "release_channel": "stable"
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

* Install a package using `opkg`.

## Version Comparison Logic

The `isNewerVersion` function compares the version string from the NIP-94 event with the output of `opkg list-installed tollgate-basic`. It handles both semantic versioning for stable releases and the `[branch].[height].[hash]` format for dev releases.

## Post-Installation

After downloading a new package, the Janitor module updates the `package_path` in `janitor.json`. The actual installation is handled by a cron job that executes the `check_package_path` script.

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
