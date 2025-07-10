# Manual Test Cases: Configuration Management

**Prerequisites:**
- A running OpenWRT device with the `tollgate-basic` package installed.
- SSH access to the device.

## 1. Configuration Resilience Testing

### 1.1. Test Case: Corrupted `config.json`
- **Objective:** Verify the system recovers from a corrupted `config.json`.
- **Steps:**
  - [ ] 1. `echo "this is not valid json" > /etc/tollgate/config.json`
  - [ ] 2. `service tollgate-basic restart`
- **Expected Result:**
  - A warning is logged: `logread | grep "Invalid 'config' config file found"`
  - A backup file is created in `/etc/tollgate/config_backups/`.
  - A new default `config.json` is created.
  - The service starts successfully.

### 1.2. Test Case: `janitor.json` Version Mismatch
- **Objective:** Verify the system recovers from an outdated `janitor.json`.
- **Steps:**
  - [ ] 1. Edit `/etc/tollgate/janitor.json` and set `"config_version": "v0.0.1"`.
  - [ ] 2. `service tollgate-basic restart`
- **Expected Result:**
  - A warning is logged for `janitor.json`.
  - A backup of `janitor.json` is created.
  - A new default `janitor.json` is created with the correct version.

### 1.3. Test Case: Missing `identities.json`
- **Objective:** Verify the system creates a default `identities.json` if it's missing.
- **Steps:**
  - [ ] 1. `rm /etc/tollgate/identities.json`
  - [ ] 2. `service tollgate-basic restart`
- **Expected Result:**
  - A new default `identities.json` is created.
  - No backup is created (as the file was missing, not invalid).
  - The service starts successfully.

### 1.4. Test Case: Read-Only Filesystem (Negative Test)
- **Objective:** Ensure the system fails safely if it cannot perform the backup.
- **Steps:**
  - [ ] 1. Conceptually, make `/etc/tollgate/` read-only.
  - [ ] 2. Corrupt `config.json` as in Test Case 1.1.
  - [ ] 3. `service tollgate-basic restart`
- **Expected Result:**
  - The service fails to start.
  - A critical error is logged about the failure to back up the file.

## 2. First-Time Setup

### 2.1. Test Case: Fresh Installation
- **Objective:** Verify that default configuration files are created correctly.
- **Preconditions:** No `config.json`, `janitor.json`, or `identities.json` files exist.
- **Steps:**
  - [ ] 1. `service tollgate-basic start`
- **Expected Result:**
  - All three configuration files are created with default values.
  - The service starts successfully.

## 3. Identity Management

### 3.1. Test Case: Retrieve Identities
- **Objective:** Ensure that identities can be correctly retrieved after a default setup.
- **Steps:**
  - [ ] 1. Use a tool or test script to call `config_manager.GetIdentity("developer")` and `config_manager.GetOwnedIdentity("merchant")`.
- **Expected Result:** The functions should return the correct default identity structs.