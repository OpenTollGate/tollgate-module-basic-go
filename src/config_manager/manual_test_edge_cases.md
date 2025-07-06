### Config File Migration Test Cases Checklist

The following scenarios should be thoroughly tested to ensure robust configuration management and successful migrations without data loss or service crashes:

#### **I. `config.json` and `install.json` Robustness (General File Handling)**

*   **File Non-Existence:**
    *   [`config.json`](src/config_manager/config_manager.go) does not exist:
        *   [ ] Verify `tollgate` service starts successfully.
        *   [ ] Verify a default `config.json` is generated with correct default values.
        *   [ ] Verify the generated `config.json` contains `config_version: "v0.0.3"`.
    *   [`install.json`](src/config_manager/config_manager.go) does not exist:
        *   [ ] Verify `tollgate` service starts successfully.
        *   [ ] Verify a default `install.json` is generated with correct default values.
        *   [ ] Verify the generated `install.json` contains `config_version: "v0.0.2"`.
            *   `identities.json` does not exist:
                *   [ ] Verify `tollgate` service starts successfully.
                *   [ ] Verify a default `identities.json` is generated with correct default values.
        *   [ ] Verify `InstalledVersion` in `install.json` reflects the current package version (e.g., from `opkg`).

*   **Empty File:**
    *   [`config.json`](src/config_manager/config_manager.go) exists but is empty:
        *   [ ] Verify `tollgate` service starts successfully.
        *   [ ] Verify a default `config.json` is generated.
        *   [ ] Verify `config_version: "v0.0.3"` is present.
    *   [`install.json`](src/config_manager/config_manager.go) exists but is empty:
        *   [ ] Verify `tollgate` service starts successfully.
        *   [ ] Verify a default `install.json` is generated.

*   **Malformed JSON:**
    *   [`config.json`](src/config_manager/config_manager.go) contains invalid/corrupted JSON:
        *   [ ] Verify `tollgate` service starts successfully (should recover by generating defaults).
        *   [ ] Verify a new default `config.json` is generated.
        *   [ ] Verify `config_version: "v0.0.3"` is present.
    *   [`install.json`](src/config_manager/config_manager.go) contains invalid/corrupted JSON:
        *   [ ] Verify `tollgate` service starts successfully (should recover by generating defaults).
        *   [ ] Verify a new default `install.json` is generated.
    *   [`identities.json`](src/config_manager/config_manager.go) contains invalid/corrupted JSON:
        *   [ ] Verify `tollgate` service starts successfully (should recover by generating defaults).
        *   [ ] Verify a new default `identities.json` is generated.

#### **II. Migration Script Specific Scenarios**

*   **`98-tollgate-config-migration-v0.0.1-to-v0.0.2-migration`:** (Existing)
    *   **Scenario: `config.json` is `v0.0.1` and valid:**
        *   [ ] Create a `config.json` with `config_version: "v0.0.1"` and existing `accepted_mints` (as an array of strings).
        *   [ ] Run the migration script.
        *   [ ] Verify `config_version` is updated to `"v0.0.2"`.
        *   [ ] Verify `accepted_mints` content is preserved and remains an array.
        *   [ ] Verify `price_per_minute` is added with its default value.
    *   **Scenario: `config.json` is `v0.0.1` but `accepted_mints` is not an array (or missing):**
        *   [ ] Create a `config.json` with `config_version: "v0.0.1"` but `accepted_mints` is missing or malformed.
        *   [ ] Run the migration script.
        *   [ ] Verify the migration script exits gracefully without crashing.
        *   [ ] Verify the `tollgate` service starts using default config.
    *   **Scenario: `config.json` exists but is *not* `v0.0.1` (e.g., `v0.0.0` or higher than `v0.0.1`):**
        *   [ ] Create a `config.json` with a version other than `v0.0.1`.
        *   [ ] Run the migration script.
        *   [ ] Verify the script exits, indicating migration is not needed.
        *   [ ] Verify the `config.json` content remains unchanged.

*   **`99-tollgate-config-migration-v0.0.2-to-v0.0.3-migration`:** (Existing)
    *   **Scenario: `config.json` is `v0.0.2` and valid:**
        *   [ ] Create a `config.json` with `config_version: "v0.0.2"` and existing `price_per_minute`.
        *   [ ] Run the migration script.
        *   [ ] Verify `config_version` is updated to `"v0.0.3"`.
        *   [ ] Verify `price_per_minute` content is preserved.
        *   [ ] Verify `payout_share` and `payout_destination` are added with their default values.
    *   **Scenario: `config.json` is `v0.0.2` but `price_per_minute` is missing or malformed:**
        *   [ ] Create a `config.json` with `config_version: "v0.0.2"` but `price_per_minute` is missing or malformed.
        *   [ ] Run the migration script.
        *   [ ] Verify the migration script exits gracefully without crashing.
        *   [ ] Verify the `tollgate` service starts using default config.
    *   **Scenario: `config.json` exists but is *not* `v0.0.2` (e.g., `v0.0.1` or higher than `v0.0.2`):**
        *   [ ] Create a `config.json` with a version other than `v0.0.2`.
        *   [ ] Run the migration script.
        *   [ ] Verify the script exits, indicating migration is not needed.
        *   [ ] Verify the `config.json` content remains unchanged.

*   **`tollgate-config-migration-v0.0.3-to-v0.0.4-migration`:** (New)
    *   **Scenario: `config.json` is `v0.0.3` and valid:**
        *   [ ] Create a `config.json` with `config_version: "v0.0.3"`.
        *   [ ] Run the migration script.
        *   [ ] Verify `config_version` is updated to `"v0.0.4"`.
        *   [ ] Verify `identities.json` is created.
        *   [ ] Verify `profit_share` and `merchant` now use identity references.
    *   **Scenario: `config.json` is not `v0.0.3`:**
        *   [ ] Create a `config.json` with a version other than `v0.0.3`.
        *   [ ] Run the migration script.
        *   [ ] Verify the script exits and `config.json` is unchanged.
 
*   **`97-tollgate-install-config-migration-v0.0.1-to-v0.0.2`:** (New)
    *   **Purpose:** Migrates `install.json` from `v0.0.1` (no `config_version` field) to `v0.0.2`.
    *   **Scenario: `install.json` is `v0.0.1` and valid (no `config_version` field):**
        *   [ ] Create an `install.json` without a `config_version` field.
        *   [ ] Run the migration script.
        *   [ ] Verify `config_version` is added and set to `"v0.0.1"`.
        *   [ ] Verify existing fields (e.g., `PackagePath`, `IPAddressRandomized`) are preserved.
    *   **Scenario: `install.json` exists but is *not* `v0.0.1` (e.g., already `v0.0.2` or malformed):**
        *   [ ] Create an `install.json` with `config_version: "v0.0.2"` or a malformed JSON.
        *   [ ] Run the migration script.
        *   [ ] Verify the script exits, indicating migration is not needed or cannot be performed safely.
        *   [ ] Verify the `install.json` content remains unchanged.


 #### **III. Data Preservation During Default Generation**
 
 *   **Existing `config.json` content merged with defaults:**
     *   [ ] Create a `config.json` with only a few valid fields (e.g., `username`, `private_key`).
     *   [ ] Delete `config_version` or make it malformed to trigger default generation.
     *   [ ] Start the `tollgate` service.
     *   [ ] Verify the existing fields (`username`, `private_key`) are preserved.
     *   [ ] Verify missing fields (e.g., `relay_url`, `price_per_minute`) are populated with defaults.
     *   [ ] Verify `config_version: "v0.0.3"` is added.
 
 ---
 
#### **IV. Config Field Defaulting Scenarios (New)**

**Objective:** Verify that `EnsureDefaultConfig`, `EnsureDefaultInstall`, and `EnsureDefaultIdentities` correctly populate missing fields with their default values when an existing (but incomplete) configuration file is loaded.

**Setup:** For each test case below, ensure the `tollgate` service is stopped before modifying the files, and then started after modification.

*   **Scenario 1: `config.json` with missing fields**
    *   [ ] Create a `config.json` file missing some fields (e.g., `accepted_mints`, `profit_share`, `bragging`).
    *   [ ] Start `tollgate` service.
    *   [ ] Verify that the missing fields are now present in `config.json` and populated with their default values.
    *   [ ] Verify `config_version` is `v0.0.4`.

*   **Scenario 2: `install.json` with missing fields**
    *   [ ] Create an `install.json` file missing some fields (e.g., `release_channel`, `installed_version`).
    *   [ ] Start `tollgate` service.
    *   [ ] Verify that the missing fields are now present in `install.json` and populated with their default values.
    *   [ ] Verify `config_version` is `v0.0.2`.

*   **Scenario 3: `identities.json` with missing `npub` for operator**
    *   [ ] Create an `identities.json` file where the "operator" identity has an empty `npub`.
    *   [ ] Ensure `config.json` has a valid `tollgate_private_key`.
    *   [ ] Start `tollgate` service.
    *   [ ] Verify that the "operator" `npub` in `identities.json` is now populated from the `tollgate_private_key`.

*   **Scenario 4: `identities.json` with missing `lightning_address`**
    *   [ ] Create an `identities.json` file where one or more identities have an empty `lightning_address`.
    *   [ ] Start `tollgate` service.
    *   [ ] Verify that the missing `lightning_address` fields are populated with `"tollgate@minibits.cash"`.

---

 #### **V. Identity Management Specific Scenarios (`identities.json`)**
 
 *   **Empty `identities.json` (but file exists):**
     *   [ ] Verify `tollgate` service starts successfully.
     *   [ ] Verify default "operator" and "developer" identities are generated and saved to `identities.json`.
     *   [ ] Verify `operator`'s `npub` is correctly derived from `tollgate_private_key` if present in `config.json`.
 *   **`identities.json` with invalid `npub` or `lightning_address` format:**
     *   [ ] Verify `tollgate` service starts successfully.
     *   [ ] Verify that invalid entries do not crash the service.
 *   **`identities.json` with duplicate identity names:**
     *   [ ] Verify `tollgate` service starts successfully.
     *   [ ] Verify `config_manager` handles duplicate names gracefully (e.g., uses the first occurrence, logs a warning).
 *   **`identities.json` with missing required fields (e.g., `name`, `lightning_address`):**
     *   [ ] Verify `tollgate` service starts successfully.
     *   [ ] Verify `config_manager` handles missing fields gracefully (e.g., treats as empty string, logs a warning).
 
 #### **VI. Interaction between `config.json` and `identities.json`**
 
 *   **`config.json` references a non-existent identity in `profit_share` or `merchant`:**
     *   [ ] Verify `tollgate` service starts successfully.
     *   [ ] Verify `merchant.go`'s `processPayout` logs a warning and skips payouts for the non-existent identity.
     *   [ ] Consider if `config_manager` should warn about such references on startup.
 *   **`config.json` references an identity with an empty/invalid `lightning_address` in `identities.json`:**
     *   [ ] Verify `tollgate` service starts successfully.
     *   [ ] Verify `merchant.go`'s `PayoutShare` function handles an empty/invalid lightning address gracefully (e.g., logs an error, does not attempt an invalid payment).
 
 ---
 
 #### **VII. Lightning-related specific edge cases (beyond just config files)**
 
 *   **`TollgatePrivateKey` in `config.json` is invalid/empty:**
     *   [ ] Verify `EnsureDefaultIdentities` handles this gracefully (e.g., `operator`'s `npub` remains empty).
     *   [ ] Verify `tollgate` service starts successfully.
 *   **Payout to a lightning address that is offline/unreachable:**
     *   [ ] Verify `PayoutShare` handles network errors gracefully (e.g., retries, logs error, doesn't block).
 *   **Payout to a lightning address that returns an error (e.g., insufficient funds on receiver side, invalid invoice):**
     *   [ ] Verify `PayoutShare` handles application-level errors from the lightning service gracefully.
 
 ---
 
 #### **VIII. IP Randomization Scenarios**
 
 ### Test Case 1: Fresh Install
 
 **Objective:** To verify that a fresh installation correctly randomizes the LAN IP and creates a versioned `install.json`.
 
 **Steps:**
 
 1.  [ ] **Reset the router to a clean state:**
      *   If a previous version of `tollgate-module-basic-go` is installed, remove it: `opkg remove tollgate-module-basic-go`.
      *   Remove the `/etc/tollgate` directory: `rm -rf /etc/tollgate`.
      *   Reset the network configuration to a known default (e.g., `192.168.1.1`):
          ```sh
          uci set network.lan.ipaddr='192.168.1.1'
          uci commit network
          /etc/init.d/network restart
          ```
 
 2.  [ ] **Install the new package:**
      *   Copy the `.ipk` file to the router's `/tmp` directory.
      *   Install the package: `opkg install /tmp/<package_name>.ipk`.
 
 3.  [ ] **Verify IP Randomization:**
      *   Check the LAN IP address: `uci get network.lan.ipaddr`.
      *   **Expected Result:** The IP address should *not* be `192.168.1.1`. It should be a randomized IP address.
 
 4.  [ ] **Verify `install.json`:**
      *   Check the content of `/etc/tollgate/install.json`: `cat /etc/tollgate/install.json | jq`.
      *   **Expected Result:**
          *   The `config_version` field should exist and be set to `"v0.0.2"`.
          *   The `ip_address_randomized` field should be `true`.
 
 ### Test Case 2: Upgrade from Unversioned `install.json`
 
 **Objective:** To verify that an upgrade from a version with an unversioned `install.json` correctly migrates the file and randomizes the IP.
 
 **Steps:**
 
 1.  [ ] **Set up the unversioned state:**
      *   Reset the router as in Test Case 1.
      *   Create an unversioned `/etc/tollgate/install.json` file:
          ```sh
          mkdir -p /etc/tollgate
          echo '{"ip_address_randomized":false}' > /etc/tollgate/install.json
          ```
 
 2.  [ ] **Install the new package:**
      *   Install the package as in Test Case 1.
 
 3.  [ ] **Verify IP Randomization:**
      *   Check the LAN IP address: `uci get network.lan.ipaddr`.
      *   **Expected Result:** The IP address should be randomized.
 
 4.  [ ] **Verify `install.json` Migration:**
      *   Check the content of `/etc/tollgate/install.json`: `cat /etc/tollgate/install.json | jq`.
      *   **Expected Result:**
          *   The `config_version` field should exist and be set to `"v0.0.1"`.
          *   The `ip_address_randomized` field should be `true`.
 
 ### Test Case 3: Upgrade with Randomized IP
 
 **Objective:** To verify that an upgrade on a system where the IP has already been randomized does *not* re-randomize the IP.
 
 **Steps:**
 
 1.  [ ] **Set up the randomized state:**
      *   Reset the router as in Test Case 1.
      *   Create a versioned `/etc/tollgate/install.json` with `ip_address_randomized: true`:
          ```sh
          mkdir -p /etc/tollgate
          echo '{"config_version":"v0.0.2", "ip_address_randomized":true}' > /etc/tollgate/install.json
          ```
      *   Set a custom, non-default IP address:
          ```sh
          uci set network.lan.ipaddr='10.20.30.1'
          uci commit network
          /etc/init.d/network restart
          ```
 
 2.  [ ] **Install the new package:**
      *   Install the package as in Test Case 1.
 
 3.  [ ] **Verify IP is not re-randomized:**
      *   Check the LAN IP address: `uci get network.lan.ipaddr`.
      *   **Expected Result:** The IP address should remain `10.20.30.1`.
 
 4.  [ ] **Verify `install.json`:**
      *   Check the content of `/etc/tollgate/install.json`: `cat /etc/tollgate/install.json | jq`.
      *   **Expected Result:** The file should be unchanged, with `ip_address_randomized: true`.