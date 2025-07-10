# Unit Test Edge Cases for Janitor Module

This document outlines the unit test edge cases for the refactored Janitor module.

## `isNewerVersion` Function

| Test Case ID | Input: `newVersion` | Input: `currentVersion` | Input: `releaseChannel` | Expected Result |
| --- | --- | --- | --- | --- |
| UT-01 | "v0.0.2" | "v0.0.1" | "stable" | `true` |
| UT-02 | "v0.0.1" | "v0.0.2" | "stable" | `false` |
| UT-03 | "v0.0.1" | "v0.0.1" | "stable" | `false` |
| UT-04 | "main.10.abcdef" | "main.5.fedcba" | "dev" | `true` |
| UT-05 | "main.5.fedcba" | "main.10.abcdef" | "dev" | `false` |
| UT-06 | "main.5.abcdef" | "main.5.fedcba" | "dev" | `false` |
| UT-07 | "v0.0.2" | "main.10.abcdef" | "stable" | `false` (or error) |
| UT-08 | "main.10.abcdef" | "v0.0.1" | "dev" | `false` (or error) |
| UT-09 | "invalid-version" | "v0.0.1" | "stable" | `false` |
| UT-10 | "v0.0.2" | "invalid-version" | "stable" | `false` |

## `listenForNIP94Events` Function

- **Test with `enabled: false`**: Verify that no relays are contacted.
- **Test with `enabled: true`**: Verify that the function connects to relays and processes events.
- **Test event filtering**:
    - Mock events with different release channels to ensure only the correct one is processed.
    - Mock events with different architectures.
- **Test signature verification**:
    - Mock an event with an invalid signature.
    - Mock an event from an untrusted public key.
- **Test package download and checksum**:
    - Mock a successful download and valid checksum.
    - Mock a download failure.
    - Mock a checksum mismatch.
- **Test `janitor.json` update**:
    - Verify that `package_path` is correctly updated after a successful download and checksum verification.

## `config_manager` interaction

- **Test `LoadJanitorConfig`**:
    - Test loading a valid `janitor.json`.
    - Test with a missing `janitor.json` (should create a default).
    - Test with a corrupted `janitor.json`.
- **Test `SaveJanitorConfig`**:
    - Test saving changes to `janitor.json`.