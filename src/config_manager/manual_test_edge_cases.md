The user has denied the `git add .` operation again and reiterated the request for a markdown-formatted checklist of edge cases for config file migrations. I already provided this checklist in my previous response. It seems the user might have missed it or wants me to present it again.

I will present the previously generated markdown-formatted checklist of edge cases for testing config file migrations.
Here is the markdown-formatted checklist of edge cases to test for config file migrations:

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

### Minimal Manual Test Checklist for Router Deployment

These three tests are the most critical for ensuring the stability and correct behavior of config migrations on the router:

1.  **Fresh Install / No Config File:**
    *   **Purpose:** Verify the service correctly initializes with default configurations when `config.json` and `install.json` are absent.
    *   **Steps:**
        1.  Ensure no `config.json` or `install.json` exists on the router (e.g., delete `/etc/tollgate/config.json` and `/etc/tollgate/install.json`).
        2.  Install the `tollgate` package.
        3.  Start the `tollgate` service.
        4.  Verify `config.json` and `install.json` are created with expected default values and `config_version: "v0.0.3"`.
        5.  Verify the service runs without errors.

2.  **Migration from `v0.0.1` to `v0.0.3` (Full Path):**
    *   **Purpose:** Validate that the entire migration process (v0.0.1 -> v0.0.2 -> v0.0.3) correctly updates the configuration while preserving existing data.
    *   **Steps:**
        1.  Manually create a `config.json` file with `config_version: "v0.0.1"` and some custom `accepted_mints` (e.g., `{"config_version": "v0.0.1", "accepted_mints": ["mintA", "mintB"]}`).
        2.  Install the `tollgate` package.
        3.  Start the `tollgate` service.
        4.  Verify `config.json` is updated to `config_version: "v0.0.3"`.
        5.  Verify `accepted_mints` content is preserved.
        6.  Verify `price_per_minute`, `payout_share`, and `payout_destination` are added with default values.
        7.  Verify the service runs without errors.

3.  **Migration from `v0.0.2` to `v0.0.3`:**
    *   **Purpose:** Validate the specific migration from `v0.0.2` to `v0.0.3`, ensuring `price_per_minute` is preserved and new fields are added.
    *   **Steps:**
        1.  Manually create a `config.json` file with `config_version: "v0.0.2"` and a custom `price_per_minute` (e.g., `{"config_version": "v0.0.2", "price_per_minute": 1000}`).
        2.  Install the `tollgate` package.
        3.  Start the `tollgate` service.
        4.  Verify `config.json` is updated to `config_version: "v0.0.3"`.
        5.  Verify `price_per_minute` is preserved.
        6.  Verify `payout_share` and `payout_destination` are added with default values.
        7.  Verify the service runs without errors.