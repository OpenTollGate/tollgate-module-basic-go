# Manual Test Edge Cases for Janitor Module

This document outlines the manual test edge cases for the refactored Janitor module.

## Test Cases

| Test Case ID | Description | Steps to Reproduce | Expected Result |
| --- | --- | --- | --- |
| TC-01 | **Janitor Disabled** | 1. Set `enabled` to `false` in `/etc/tollgate/janitor.json`. <br> 2. Publish a new, valid NIP-94 event for a newer package version. | The janitor module should not download the new package or update `package_path`. |
| TC-02 | **Janitor Enabled** | 1. Set `enabled` to `true` in `/etc/tollgate/janitor.json`. <br> 2. Publish a new, valid NIP-94 event for a newer package version. | The janitor module should download the new package and update `package_path` in `janitor.json`. The cron job should then install the package. |
| TC-03 | **Same Version** | 1. Set `enabled` to `true`. <br> 2. Publish a NIP-94 event with the same version as the currently installed package. | The janitor module should not download the package or update `package_path`. |
| TC-04 | **Older Version** | 1. Set `enabled` to `true`. <br> 2. Publish a NIP-94 event with an older version than the currently installed package. | The janitor module should not download the package or update `package_path`. |
| TC-05 | **Invalid Checksum** | 1. Set `enabled` to `true`. <br> 2. Publish a NIP-94 event with a newer version but an invalid checksum. | The janitor module should download the package but fail the checksum verification. `package_path` should not be updated. |
| TC-06 | **Different Release Channel** | 1. Set `release_channel` to "stable" in `janitor.json`. <br> 2. Publish a NIP-94 event for a "dev" release channel. | The janitor module should ignore the event. |
| TC-07 | **`janitor.json` Missing** | 1. Delete `/etc/tollgate/janitor.json`. <br> 2. Restart the tollgate-basic service. | The service should create a default `janitor.json` with `enabled` set to `false`. |
| TC-08 | **IP Address Randomized Flag** | 1. Manually change the LAN IP address. <br> 2. Set `ip_address_randomized` to `false`. <br> 3. Restart the service. | The `95-random-lan-ip` script should run and set `ip_address_randomized` to `true`. |