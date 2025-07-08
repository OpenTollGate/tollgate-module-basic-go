# Manual Test Cases: Configuration Management Refactor

## 1. Migration Testing

### 1.1. Test Case: Successful Migration
- **Objective:** Verify that the system correctly migrates a legacy `config.json` to the new three-file structure.
- **Preconditions:**
  - A single `config.json` file exists with `config_version` "v0.0.3".
  - `install.json` and `identities.json` do not exist.
- **Steps:**
  1. Start the Tollgate service.
  2. Check if `config.json` is updated to the new simplified format.
  3. Verify that `identities.json` is created and populated with the correct data from the old `config.json`.
  4. Confirm that a backup of the old configuration (`config.json.bak`) is created.
- **Expected Result:** The system should be running with the new configuration, and all data should be correctly migrated.

### 1.2. Test Case: Migration with Missing Fields
- **Objective:** Ensure the migration handles a legacy `config.json` that is missing optional fields.
- **Preconditions:** A `config.json` (v0.0.3) with `trusted_maintainers` and `bragging` sections removed.
- **Steps:**
  1. Start the Tollgate service.
  2. Observe the logs for any errors.
  3. Check the contents of the new `config.json` and `identities.json`.
- **Expected Result:** Migration should complete successfully, with default or empty values for the missing fields.

## 2. File Handling

### 2.1. Test Case: First-Time Setup
- **Objective:** Verify that default configuration files are created correctly on a fresh installation.
- **Preconditions:** No `config.json`, `install.json`, or `identities.json` files exist.
- **Steps:**
  1. Start the Tollgate service.
  2. Check for the existence of all three configuration files in `/etc/tollgate/`.
  3. Validate that each file contains the correct default values.
- **Expected Result:** The service starts successfully with a default configuration.

### 2.2. Test Case: Corrupted `identities.json`
- **Objective:** Test how the system handles a malformed `identities.json`.
- **Preconditions:** `identities.json` contains invalid JSON.
- **Steps:**
  1. Start the Tollgate service.
  2. Check the system logs for errors.
- **Expected Result:** The service should fail to start and log a clear error message about the corrupted file.

## 3. Identity Management

### 3.1. Test Case: Retrieve Public Identity
- **Objective:** Ensure that public identities can be correctly retrieved.
- **Steps:**
  1.  Use a tool or test script to call `config_manager.GetIdentity("developer")`.
- **Expected Result:** The function should return the correct `PublicIdentity` struct for the "developer" identity.

### 3.2. Test Case: Retrieve Owned Identity
- **Objective:** Ensure that owned identities (with private keys) can be correctly retrieved.
- **Steps:**
  1.  Use a tool or test script to call `config_manager.GetOwnedIdentity("merchant")`.
- **Expected Result:** The function should return the correct `OwnedIdentity` struct for the "merchant" identity.

## 4. Bragging Module Removal

### 4.1. Test Case: Confirm Absence of Bragging
- **Objective:** Verify that the bragging module has been completely removed.
- **Steps:**
  1. Check the running processes to ensure no bragging-related process is active.
  2. Review the logs to confirm there are no messages related to bragging.
  3. Inspect the `config.json` to ensure the `bragging` section is no longer present.
- **Expected Result:** No trace of the bragging module should be found in the system.