# Janitor Module Design Document
## Overview
The Janitor module is a critical component of the TollGate system, responsible for listening to NIP-94 events and updating the OpenWRT package on the device.

## Requirements
The module should:
- Listen for NIP-94 events on specified relays
- Verify events are signed by trusted maintainers
- Download and install new packages if they are newer than the currently installed version based on the version number

## Configuration
Configuration data will be stored in `files/etc/tollgate/config.json`

```json
{
  "tollgate_private_key": "8a45d0add1c7ddf668f9818df550edfa907ae8ea59d6581a4ca07473d468d663",
  "accepted_mint": "https://mint.minibits.cash/Bitcoin",
  "price_per_minute": 1,
  "min_payment": 1,
  "mint_fee": 0,
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
  "package_info": {
      "version": "1.2.3",
      "timestamp": 1745751288
  }
}
```

## NIP-94 Event Format
Events have the following structure:

```json
{
 "id": "ba736977a4ffe67ed774776032b8f202302f9fa01361c42a7ed907c45edf4576",
 "pubkey": "5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a",
 "created_at":1745094804,
 "kind":1063,
 "content": "TollGate Module Package: basic for gl-mt3000",
 "tags": [
 ["url", "https://blossom.swissdash.site/55d4d74b4b9184f6c51af4fc38ae59b9f0318593d0a727b7265d9c3d81a405d5.ipk"],
 ["m", "application/octet-stream"],
 ["x", "55d4d74b4b9184f6c51af4fc38ae59b9f0318593d0a727b7265d9c3d81a405d5"],
 ["filename", "basic-gl-mt3000-aarch64_cortex-a53.ipk"],
 ["arch", "aarch64_cortex-a53"],
 ["version", "1.2.3"],
 ["branch", "main"]
 ]
}
```

## Workflow

1. Listen for NIP-94 events on specified relays
2. Verify event signature and trustworthiness
3. Compare version numbers to determine if the new package is newer
4. Download new package if event is valid and newer
5. Verify the SHA256 sum of the downloaded package matches the expected hash from the NIP-94 event
6. Install new package using opkg
7. Run post-install script to update `config.json` with the new package version and timestamp if it already exists

## Security Considerations

- Checksum verification before installation
- Atomic installation process

## Logging

Logs will be written using `log.Printf` with a standard format.

## Error Handling

- Errors during installation will be logged and retried.

## Testing

Unit tests will be written to ensure correct functionality and error handling.

## Instructions for Engineers Implementing the Feature

1. Update `OpenTollGate/nostr-publish-file-metadata-action/python@main` to include tags for the version and the branch.
2. Use the `version` and `branch` fields in the NIP-94 metadata to track the package version and branch.

## Conclusion

The Janitor module will be implemented as a separate Go module, ensuring the security and integrity of the OpenWRT package update process on the TollGate device.