# Janitor Module Design Document

## Overview

The Janitor module is a critical component of the TollGate system, responsible for listening to NIP-94 events and updating the OpenWRT package on the device based on the release channel.

## Requirements

* The module should listen for NIP-94 events on specified relays.
* The module should verify events are signed by trusted maintainers.
* The module should download and install new packages if they are newer than the currently installed version based on the version number and release channel.

## Configuration

The Janitor module uses the ConfigManager to access the main configuration and its own configuration stored in `/etc/tollgate/janitor.json`.

The `janitor.json` file has the following structure:

```json
{
  "config_version": "v0.0.2",
  "package_path": "",
  "enabled": false,
  "ip_address_randomized": true,
  "release_channel": "stable"
}
```

### Fields

- **config_version**: The version of the configuration file format.
- **package_path**: The path to the downloaded package to be installed by the cron job.
- **enabled**: A boolean flag to enable or disable the janitor's automatic update functionality.
- **ip_address_randomized**: A boolean flag to indicate if the LAN IP address has been randomized.
- **release_channel**: The release channel to track for updates (e.g., "stable", "dev").

## NIP-94 Event Format

Events have the following structure:

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
    ["filename", "basic-gl-mt3000-aarch64_cortex-a53.ipk"],
    ["architecture", "aarch64_cortex-a53"],
    ["version", "multiple_mints_rebase_taglist-b97e743"],
    ["release_channel", "dev"]
  ]
}
```

For the dev channel, the version string is of the format `[branch_name].[commit_count].[commit_hash]`. For the stable channel, the version number is just the release tag (e.g., `0.0.1`).

## Workflow

1. Check if the `enabled` flag in `janitor.json` is `true`.
2. If enabled, listen for NIP-94 events on specified relays with a 6-month time window.
3. Verify the event signature against trusted maintainers.
4. Compare the version from the event with the currently installed version using `opkg`.
5. If a newer version is found, download the package.
6. Verify the SHA256 checksum of the downloaded package.
7. Update `package_path` in `janitor.json` to trigger the installation by the cron job.

## Security Considerations

* Checksum verification before installation.
* Atomic installation process.

## Logging

Logs will be written using `log.Printf` with a standard format.

## Error Handling

* Errors during installation will be logged and retried.

## Testing

Unit tests will be written to ensure correct functionality and error handling.

## Instructions for Engineers Implementing the Feature

1. Update the Janitor module to distinguish between dev and stable channels based on the `release_channel` tag in NIP-94 events.
2. Modify the version comparison logic to handle the new versioning scheme for dev and stable channels.

## Conclusion

The Janitor module will be updated to handle the new release channel concept and versioning scheme, ensuring the security and integrity of the OpenWRT package update process on the TollGate device.

## Centralized Rate Limiting for relayPool

To address the 'too many concurrent REQs' error, we will implement centralized rate limiting for `relayPool` within `config_manager`. This involves initializing `relayPool` in `config_manager` and providing a controlled access mechanism through a member function. This approach ensures that all services using `relayPool`, including the Janitor module, are rate-limited, preventing excessive concurrent requests to relays.
