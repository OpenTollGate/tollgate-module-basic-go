# Janitor Module Low-Level Design Document

## Overview

The Janitor module is designed to listen for NIP-94 events announcing new OpenWRT packages, download and install the new package if it is newer than the currently installed one, and ensure the integrity and security of the installation process.

## Requirements

* The module should listen for NIP-94 events signed by trusted maintainers.
* The module should verify the checksum of the downloaded package before installation.
* The module should handle errors and exceptions during the package installation process.
* The module should compare version numbers to determine if a new package is newer than the currently installed one.

## Configuration

The configuration data for the Janitor module will be stored in a JSON file named `config.json`. The following is an example of its structure:

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

This configuration includes various settings for the TollGate system, including the private key, accepted mint, pricing information, bragging settings, relays, trusted maintainers, and package information.

## NIP-94 Event Format

The NIP-94 event that announces a new OpenWRT package has the following format:

```json
{
 "id": "ba736977a4ffe67ed774776032b8f202302f9fa01361c42a7ed907c45edf4576",
 "pubkey": "5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a",
 "created_at": synt1735094804,
 "kind": 1063,
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

* Install a package using opkg.

## Error Handling

The Janitor module will handle errors and exceptions during the package installation process by:

* Logging the error using a simple logging mechanism.
* Retrying the installation process if it fails.

## Logging

The Janitor module will use a simple logging mechanism to log events and errors.

## Testing

Unit tests will be written to ensure that the Janitor module functions correctly and handles errors properly.

## Post-Installation

After installing a new package, if the `config.json` file already exists, a post-install script will be run to update its version and timestamp to match the newly installed package.

## Instructions for Engineers Implementing the Feature

1. Update `OpenTollGate/nostr-publish-file-metadata-action/python@main` to include tags for the version and the branch.
2. Use the `version` and `branch` fields in the NIP-94 metadata to track the package version and branch.

## Checklist

- [ ] Implement the Janitor module as a separate Go module.
- [ ] Write unit tests for the Janitor module.
- [ ] Ensure that the module logs events and errors correctly.
- [ ] Implement error handling for package installation failures.
- [ ] Verify the checksum of the downloaded package before installation.
- [ ] Compare version numbers to determine if a new package is newer.
- [ ] Run post-install script to update `config.json` if it exists.