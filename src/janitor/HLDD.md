# Janitor Module Design Document

## Overview

The Janitor module is a critical component of the TollGate system, responsible for listening to NIP-94 events and updating the OpenWRT package on the device based on the release channel.

## Requirements

* The module should listen for NIP-94 events on specified relays.
* The module should verify events are signed by trusted maintainers.
* The module should download and install new packages if they are newer than the currently installed version based on the version number and release channel.

## Configuration

The Janitor module uses the ConfigManager to access both the main configuration and the installation configuration stored in `install.json`.

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

For the dev channel, the version string is of the format `[branch_name]-[commit-hash]-[timestamp]`. For the stable channel, the version number is just the release tag (e.g., `0.0.1`).

## Workflow

1. Listen for NIP-94 events on specified relays.
2. Verify event signature and trustworthiness.
3. Compare version numbers considering the release channel to determine if the new package is newer. For the dev channel, version comparison is not applicable and will result in an error. We must compare the timestamps to determine if events are newer in the dev channel.
4. Download new package if event is valid and newer.
5. Verify the SHA256 sum of the downloaded package matches the expected hash from the NIP-94 event.
6. Install new package using opkg.

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
