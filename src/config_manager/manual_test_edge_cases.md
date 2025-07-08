# Manual Testing Plan: Config Manager

This document outlines manual test cases for the `config_manager` module, covering file handling, migrations, and the identity management system.

---

### **I. General Robustness (File Handling)**

*   **File Non-Existence:**
    *   `config.json` does not exist:
        *   [ ] Verify `tollgate` service starts successfully.
        *   [ ] Verify a default `config.json` is generated with `config_version: "v0.0.4"` and no `tollgate_private_key`.
    *   `install.json` does not exist:
        *   [ ] Verify `tollgate` service starts successfully.
        *   [ ] Verify a default `install.json` is generated with `config_version: "v0.0.2"`.
    *   `identities.json` does not exist:
        *   [ ] Verify `tollgate` service starts successfully.
        *   [ ] Verify a default `identities.json` is generated with `config_version: "v0.0.1"`.
        *   [ ] Verify the "operator" identity has a valid `nsec` key.

*   **Empty Files:**
    *   `config.json`, `install.json`, or `identities.json` exist but are empty:
        *   [ ] Verify the service starts successfully for each case.
        *   [ ] Verify a default file is generated with the correct content and version for each case.

*   **Malformed JSON:**
    *   `config.json`, `install.json`, or `identities.json` contain invalid JSON:
        *   [ ] Verify the service starts successfully for each case (recovering by generating defaults).
        *   [ ] Verify a new, valid default file is generated for each case.

---

### **II. Identity Management & Migration (v0.0.3 to v0.0.4)**

#### **Scenario 1: Full Migration**

*   **Objective**: Ensure the migration script correctly transforms a `v0.0.3` configuration to the new `v0.0.4` structure.
*   **Setup**:
    1.  Prepare a `config.json` file with `config_version: "v0.0.3"`.
    2.  Ensure it has a valid `tollgate_private_key`.
    3.  Delete any existing `identities.json` file.
*   **Steps**:
    1.  Place the `v0.0.3` `config.json` in `/etc/tollgate/`.
    2.  Run the migration script (or reboot the device to trigger `uci-defaults`).
    3.  **Verification**:
        *   [ ] Check that a backup of the original `config.json` was created.
        *   [ ] Inspect the new `config.json`:
            *   `config_version` must be `"v0.0.4"`.
            *   The `tollgate_private_key` field must be **deleted**.
        *   [ ] Inspect the new `identities.json`:
            *   `config_version` must be `"v0.0.1"`.
            *   An "operator" identity must exist.
            *   The operator's `key` must match the hex private key from the old `config.json`.
            *   The operator's `key_format` must be `"hex_private"`.
    4.  Start the Tollgate application.
    5.  [ ] Monitor the logs for any errors. The application should start and run normally.
    6.  [ ] Trigger an action that requires signing (e.g., a brag event) and verify it's signed with the correct key.

#### **Scenario 2: Fresh Installation**

*   **Objective**: Verify that a fresh installation correctly generates the new configuration structure from scratch.
*   **Steps**:
    1.  [ ] Ensure `/etc/tollgate/` is empty (delete `config.json` and `identities.json`).
    2.  [ ] Start the Tollgate application.
    3.  **Verification**:
        *   [ ] Inspect `config.json`: `config_version` must be `"v0.0.4"` and it must **not** contain `tollgate_private_key`.
        *   [ ] Inspect `identities.json`: `config_version` must be `"v0.0.1"`, an "operator" must exist with a valid `nsec` key and `key_format: "nsec"`.
    4.  [ ] The application should be running without errors.

#### **Scenario 3: Key Functionality Post-Refactor**

*   **Objective**: Ensure core application functions that rely on the operator's key work correctly.
*   **Setup**: Use the state from either Scenario 1 or 2.
*   **Steps**:
    1.  [ ] **Merchant Advertisement**: Verify the advertisement event is created and signed successfully.
    2.  [ ] **Bragging**: Trigger a brag note and verify its signature corresponds to the operator's public key.
    3.  [ ] **Session Events**: Create a session and verify the session event (kind 7001) is signed correctly.

#### **Scenario 4: Identity Edge Cases**

*   **Objective**: Test the system's resilience to invalid identity configurations.
*   **Steps**:
    1.  [ ] **Invalid `identities.json`**: Manually corrupt the `identities.json` file. Verify the application logs a clear error and fails to start.
    2.  [ ] **Missing Operator Identity**: Remove the "operator" object from `identities.json`. Verify the application logs "identity not found: operator" and fails to start.

---

### **III. Legacy Migration Tests**

*   **`98-tollgate-config-migration-v0.0.1-to-v0.0.2-migration`:**
    *   [ ] **Scenario**: Given a valid `v0.0.1` `config.json`, verify it migrates to `v0.0.2` correctly.
*   **`99-tollgate-config-migration-v0.0.2-to-v0.0.3-migration`:**
    *   [ ] **Scenario**: Given a valid `v0.0.2` `config.json`, verify it migrates to `v0.0.3` correctly.
*   **`97-tollgate-install-config-migration-v0.0.1-to-v0.0.2`:**
    *   [ ] **Scenario**: Given an `install.json` without a version, verify it migrates correctly by adding `config_version: "v0.0.1"`.

---

### **IV. IP Randomization Scenarios**

*   **Test Case 1: Fresh Install**
    *   [ ] Verify that a fresh installation correctly randomizes the LAN IP and creates a versioned `install.json` with `ip_address_randomized: true`.
*   **Test Case 2: Upgrade from Unversioned `install.json`**
    *   [ ] Verify that an upgrade from a version with an unversioned `install.json` correctly migrates the file and randomizes the IP.
*   **Test Case 3: Upgrade with Randomized IP**
    *   [ ] Verify that an upgrade on a system where the IP has already been randomized does *not* re-randomize the IP.