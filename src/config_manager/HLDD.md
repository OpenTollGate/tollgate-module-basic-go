# config_manager HLDD

## 1. Overview

The `config_manager` package is responsible for the lifecycle and management of all configuration files for the Tollgate module. This includes the main `config.json`, the installation-specific `install.json`, and the newly centralized `identities.json`. The manager ensures that configurations are loaded correctly, defaults are applied, and data integrity is maintained across versions through a robust migration system.

A key design principle is the separation of identity-related data (including sensitive keys) from operational configuration, improving security and modularity.

## 2. Key Design Changes: Identity Management Refactor (v0.0.4)

The core of this refactor is to centralize all identity-related information into a single `identities.json` file. This includes user-facing names, Nostr keys (both public and private), and Lightning Addresses.

### 2.1. Centralized Identity Store (`identities.json`)

- **Single Source of Truth:** `identities.json` will be the definitive source for all identity information.
- **Private Key Storage:** The operator's private key, previously `tollgate_private_key` in `config.json`, will be stored *exclusively* within the "operator" identity object in `identities.json`.
- **Flexible Key Formats:** The system will support multiple key formats (`nsec`, `npub`, and `hex_private`) to accommodate different use cases and migration constraints.

### 2.2. Configuration File Decoupling (`config.json`)

- **Removal of `tollgate_private_key`:** The `tollgate_private_key` field will be completely removed from `config.json`.
- **Identity References:** Components that previously used direct Nostr keys or Lightning Addresses (e.g., `ProfitShare`, `Merchant`) will now use a string `identity` field to reference an entry in `identities.json`.

## 3. System Architecture & Data Flow

### 3.1. Data Structures

**`identities.json` Data Structure:**

```json
[
  {
    "name": "operator",
    "key": "nsec1...",
    "key_format": "nsec",
    "lightning_address": "tollgate@minibits.cash"
  },
  {
    "name": "developer",
    "key": "npub1...",
    "key_format": "npub",
    "lightning_address": "tollgate@minibits.cash"
  }
]
```

**`config.json` Data Structure (Relevant Sections):**

```json
{
  "config_version": "v0.0.4",
  "profit_share": [
    { "factor": 0.7, "identity": "operator" },
    { "factor": 0.3, "identity": "developer" }
  ],
  "merchant": {
    "identity": "operator"
  }
}
```

### 3.2. Data Flow Diagram

```mermaid
graph TD
    subgraph Application Startup
        A[main.go] --> B{config_manager.EnsureInitializedConfig};
    end

    subgraph Configuration Initialization
        B --> C{LoadIdentities};
        B --> D{LoadConfig};
        B --> E{LoadInstallConfig};
    end

    subgraph Identity & Key Management
        F[Other Modules e.g., merchant] -- requests key for 'operator' --> G[config_manager];
        G -- GetIdentity("operator") --> C;
        C -- returns operator Identity object --> G;
        G -- GetPrivateKey() on Identity --> H{Key Conversion Logic};
        H -- returns nsec --> G;
        G -- returns nsec to --> F;
    end

    subgraph Migration Flow
        I[Migration Script] -- reads --> J[Old config.json (v0.0.3)];
        J -- contains tollgate_private_key (hex) --> I;
        I -- creates --> K[New identities.json];
        I -- writes hex key & key_format="hex_private" --> K;
        I -- removes tollgate_private_key --> L[New config.json (v0.0.4)];
    end
```

## 4. API & Interfaces

The `ConfigManager` will expose a clear API for managing configurations and identities.

### 4.1. Core Interfaces

- `NewConfigManager(filePath string) (*ConfigManager, error)`: Initializes the manager.
- `EnsureInitializedConfig() error`: The main entry point to ensure all configuration files are present and valid.
- `LoadConfig() (*Config, error)`: Loads `config.json`.
- `SaveConfig(*Config) error`: Saves `config.json`.
- `LoadIdentities() (*IdentityConfig, error)`: Loads `identities.json`.
- `SaveIdentities(*IdentityConfig) error`: Saves `identities.json`.

### 4.2. Identity-Specific Interfaces

- `GetIdentity(name string) (*Identity, error)`: Retrieves a specific identity object by its name.
- `GetPrivateKey(identityName string) (string, error)`: A convenience method that retrieves the private key for a given identity *as an nsec string*. It will handle the conversion from hex if necessary. Returns an error if the identity does not have a private key.
- `GetPublicKey(identityName string) (string, error)`: A convenience method that retrieves the public key for a given identity. It will derive the public key from a private key if present, otherwise it returns the stored public key.

## 5. Migration Strategy

A new migration script will handle the transition from `v0.0.3` to `v0.0.4`.

- **Trigger:** The script runs if `config.json` is at `v0.0.3`.
- **Backup:** Creates a timestamped backup of `config.json`.
- **Actions:**
    1. Reads the `tollgate_private_key` (hex format) from `config.json`.
    2. Creates a new `identities.json` file if one does not exist.
    3. Populates `identities.json` with "operator" and "developer" identities.
        - For "operator", it writes the hex key to the `key` field and sets `key_format` to `"hex_private"`.
    4. Modifies `config.json`:
        - **Removes** the `tollgate_private_key` field entirely.
        - Updates `config_version` to `v0.0.4`.
- **Error Handling:** The script will be designed to be idempotent and includes logging. Backups allow for manual recovery.

## 6. Security Considerations

- **Private Key Isolation:** Storing the private key in `identities.json` separates sensitive credentials from general application settings.
- **File Permissions:** The `config_manager` will ensure that `identities.json` is saved with restrictive file permissions (e.g., `0600`) to protect the private key.

## 7. Pending Tasks

- [ ] Implement the updated `Identity` struct in Go.
- [ ] Implement the `GetIdentity`, `GetPrivateKey`, and `GetPublicKey` methods.
- [ ] Update all modules that require keys (`merchant`, `bragging`, etc.) to use the new `ConfigManager` methods.
- [ ] Create the new `v0.0.3-to-v0.0.4` migration script.
- [ ] Write comprehensive unit tests for the new identity logic and migration.
- [ ] Create manual test cases for the identity refactor.
