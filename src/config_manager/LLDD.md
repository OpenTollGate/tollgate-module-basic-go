# Config Manager Low-Level Design Document

## 1. Overview

This document details the low-level implementation plan for the `config_manager` package, focusing on the `v0.0.4` identity management refactor. The core change is the centralization of identities into `identities.json`, the removal of the private key from `config.json`, and the introduction of a flexible key management system.

## 2. Data Structure Implementation

### 2.1. `IdentityConfig` and `Identity` Structs

The Go structs in `config_manager.go` will be updated to reflect the new `identities.json` structure.

```go
// IdentityConfig holds the identities configuration parameters, including its version
type IdentityConfig struct {
	ConfigVersion string     `json:"config_version"`
	Identities    []Identity `json:"identities"`
}

// Identity represents a single identity with a flexible key field.
type Identity struct {
	Name             string `json:"name"`
	Key              string `json:"key"`
	KeyFormat        string `json:"key_format"` // "nsec", "npub", or "hex_private"
	LightningAddress string `json:"lightning_address"`
}
```

### 2.2. `Config` Struct Changes

The `Config` struct will be updated to remove the `TollgatePrivateKey`.

```go
// Config holds the configuration parameters
type Config struct {
	ConfigVersion         string              `json:"config_version"`
	// TollgatePrivateKey is now REMOVED.
	AcceptedMints         []MintConfig        `json:"accepted_mints"`
	ProfitShare           []ProfitShareConfig `json:"profit_share"`
	StepSize              uint64              `json:"step_size"`
	Metric                string              `json:"metric"`
	Bragging              BraggingConfig      `json:"bragging"`
	Merchant              MerchantConfig      `json:"merchant"`
	Relays                []string            `json:"relays"`
	TrustedMaintainers    []string            `json:"trusted_maintainers"`
	ShowSetup             bool                `json:"show_setup"`
	CurrentInstallationID string              `json:"current_installation_id"`
}
```

## 3. Core Function Implementation

### 3.1. `GetIdentity(name string)`

This function will be added to `ConfigManager`.

- **Logic:**
    1. Load the `identities.json` file using `LoadIdentities()`.
    2. Iterate through the `Identities` slice.
    3. If an identity with the matching `name` is found, return a pointer to that `Identity` struct.
    4. If not found, return `nil` and an error (e.g., `fmt.Errorf("identity not found: %s", name)`).

### 3.2. `GetPrivateKey(identityName string)`

This function will be added to `ConfigManager`.

- **Logic:**
    1. Call `GetIdentity(identityName)` to retrieve the identity object.
    2. Check the `KeyFormat` field.
    3. If `KeyFormat` is `"hex_private"`:
        - Use `nostr.EncodePrivateKey()` to convert the hex string from the `Key` field into an `nsec` string.
        - Return the resulting `nsec` string.
    4. If `KeyFormat` is `"nsec"`:
        - Return the `Key` field directly.
    5. If `KeyFormat` is `"npub"` or any other value, return an error indicating a private key is not available.

### 3.3. `GetPublicKey(identityName string)`

This function will be added to `ConfigManager`.

- **Logic:**
    1. Call `GetIdentity(identityName)` to retrieve the identity object.
    2. Check the `KeyFormat` field.
    3. If `KeyFormat` is `"hex_private"` or `"nsec"`:
        - First, get the private key as hex (if `nsec`, use `nostr.DecodePrivateKey` to get the hex).
        - Use `nostr.GetPublicKey()` to derive the public key.
        - Return the public key (hex format).
    4. If `KeyFormat` is `"npub"`:
        - Use `nostr.DecodePublicKey()` to convert the `npub` string to a hex public key.
        - Return the hex public key.
    5. If the key format is invalid, return an error.

### 3.4. `EnsureDefaultIdentities()`

This function will be updated significantly.

- **Logic:**
    1. Attempt to load `identities.json` using `LoadIdentities()`.
    2. If the file doesn't exist (`identityConfig == nil`):
        - Create a new `IdentityConfig` with `ConfigVersion: "v0.0.1"`.
        - Generate a new private key using `nostr.GeneratePrivateKey()`.
        - Encode it to `nsec` using `nostr.EncodePrivateKey()`.
        - Create a default "operator" `Identity` with `Name: "operator"`, `Key: <nsec_key>`, `KeyFormat: "nsec"`, and a default LN address.
        - Create a default "developer" `Identity`.
        - Save the new `IdentityConfig`.
    3. If the file exists, check for the "operator" and "developer" identities and add them if they are missing.
    4. Ensure `ConfigVersion` is set to `v0.0.1`.

### 3.5. `EnsureDefaultConfig()`

This function will be updated to remove any logic related to `TollgatePrivateKey`.

- **Change:** The section that generates and sets `config.TollgatePrivateKey` will be removed.

## 4. Module Updates

All modules that previously accessed `config.TollgatePrivateKey` must be updated to use the new `ConfigManager` methods.

- **Example (`merchant.go`):**
    - **Old Code:** `err := advertisementEvent.Sign(config.TollgatePrivateKey)`
    - **New Code:**
        ```go
        operatorKey, err := m.configManager.GetPrivateKey("operator")
        if err != nil {
            return "", fmt.Errorf("failed to get operator private key: %w", err)
        }
        // The key is now in nsec format, need to decode to hex for signing
        hexKey, err := nostr.DecodePrivateKey(operatorKey)
        if err != nil {
            return "", fmt.Errorf("failed to decode operator private key: %w", err)
        }
        err = advertisementEvent.Sign(hexKey)
        ```

## 5. Migration Script (`99-tollgate-config-migration-v0.0.3-to-v0.0.4-migration`)

A new migration script will be created.

- **File Path:** `files/etc/uci-defaults/99-tollgate-config-migration-v0.0.3-to-v0.0.4-migration`
- **Logic (`sh` and `jq`):**
    ```sh
    #!/bin/sh
    # Migration from v0.0.3 to v0.0.4

    CONFIG_FILE="/etc/tollgate/config.json"
    IDENTITIES_FILE="/etc/tollgate/identities.json"
    # ... (backup and validation logic) ...

    # Extract private key
    OPERATOR_PRIVKEY_HEX=$(jq -r '.tollgate_private_key // ""' "$CONFIG_FILE")

    # Create identities.json
    jq -n \
      --arg version "v0.0.1" \
      --arg op_key "$OPERATOR_PRIVKEY_HEX" \
      '{
        "config_version": $version,
        "identities": [
          {
            "name": "operator",
            "key": $op_key,
            "key_format": "hex_private",
            "lightning_address": "tollgate@minibits.cash"
          },
          {
            "name": "developer",
            "key": "npub1...",
            "key_format": "npub",
            "lightning_address": "tollgate@minibits.cash"
          }
        ]
      }' > "$IDENTITIES_FILE"

    # Update config.json: remove private key and update version
    jq 'del(.tollgate_private_key) | .config_version = "v0.0.4"' "$CONFIG_FILE" > tmp_config && mv tmp_config "$CONFIG_FILE"

    log "Migration to v0.0.4 complete."
    exit 0
    ```

## 6. Testing Plan

### 6.1. Unit Tests (`config_manager_test.go`)

- **`TestGetIdentity`:**
    - Test retrieving an existing identity.
    - Test retrieving a non-existent identity (expect error).
- **`TestGetPrivateKey`:**
    - Test with `key_format: "nsec"`.
    - Test with `key_format: "hex_private"`.
    - Test with `key_format: "npub"` (expect error).
- **`TestGetPublicKey`:**
    - Test deriving from `nsec`.
    - Test deriving from `hex_private`.
    - Test retrieving from `npub`.
- **`TestEnsureDefaultIdentities`:**
    - Test creation of a new `identities.json` file.
    - Verify the generated operator key is a valid `nsec`.
- **`TestFullMigrationFlow`:**
    - A new integration-style test that:
        1. Creates a `v0.0.3` `config.json` with a `tollgate_private_key`.
        2. Runs the core logic of the migration.
        3. Asserts that `identities.json` is created correctly.
        4. Asserts that `config.json` is updated to `v0.0.4` and the private key is removed.
        5. Loads the new configs with `ConfigManager` and verifies that `GetPrivateKey("operator")` returns the correct key.

### 6.2. Manual Tests

- Add a new section to `manual_test_edge_cases.md` for the identity refactor.
- **Test Case:** Manual migration.
    - Start with a `v0.0.3` config.
    - Run the migration script.
    - Verify the contents of `config.json` and `identities.json`.
    - Start the application and ensure it runs correctly.
- **Test Case:** Fresh install.
    - Delete all config files.
    - Start the application.
    - Verify the contents of the newly generated `config.json` and `identities.json`.

## 7. Pending Tasks

- [ ] Implement the updated `Identity` and `IdentityConfig` structs.
- [ ] Implement `GetIdentity`, `GetPrivateKey`, and `GetPublicKey`.
- [ ] Update `EnsureDefaultIdentities` and `EnsureDefaultConfig`.
- [ ] Update `merchant`, `bragging`, and other modules to use the new key retrieval methods.
- [ ] Create the `99-tollgate-config-migration-v0.0.3-to-v0.0.4-migration` script.
- [ ] Implement all new unit tests.
- [ ] Update the manual testing document.
