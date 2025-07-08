# HLDD: Configuration Management Refactor

## 1. Overview

This document outlines the high-level design for refactoring the `config_manager` module. The primary goals of this refactoring are:

- **Simplify Configuration:** Reduce the complexity of `config.json` by separating identity and installation-specific data into their own files.
- **Improve Maintainability:** Break down the monolithic `config_manager.go` file into smaller, more focused files, each responsible for a single configuration file.
- **Decouple Modules:** Remove the `bragging` module, which is no longer required.
- **Introduce Dedicated Identity Management:** Create a new `identities.json` file to manage all user and system identities in a structured way.

## 2. New Configuration Structure

The configuration will be split across three files located in `/etc/tollgate/`:

### 2.1. `config.json` (Simplified)

This file will contain the core operational parameters for the Tollgate service.

```json
{
  "config_version": "v0.0.4",
  "accepted_mints": [],
  "profit_share": [],
  "step_size": 600000,
  "metric": "milliseconds",
  "relays": [],
  "show_setup": true
}
```

### 2.2. `install.json` (Unchanged)

This file will continue to store installation-specific information. Its structure remains the same.

```json
{
  "config_version": "v0.0.2",
  "package_path": "",
  "ip_address_randomized": true,
  "install_time": 1751970133,
  "download_time": 0,
  "release_channel": "stable",
  "ensure_default_timestamp": 1751970139,
  "installed_version": "0.0.0"
}
```

### 2.3. `identities.json` (New)

This new file will manage all cryptographic and public identities used by the system.

```json
{
  "config_version": "v0.0.1",
  "owned_identities": [
    {
      "name": "merchant",
      "privatekey": "..."
    }
  ],
  "public_identities": [
    {
      "name": "developer",
      "lightning_address": "..."
    },
    {
      "name": "trusted_maintainer_1",
      "pubkey": "..."
    },
    {
      "name": "owner",
      "pubkey": "[placeholder]",
      "lightning_address": "..."
    }
  ]
}
```

## 3. Component Architecture

The `config_manager` Go package will be refactored from a single `config_manager.go` file into a more modular structure.

```mermaid
graph TD
    A[config_manager] --> B[config_manager_config.go];
    A --> C[config_manager_install.go];
    A --> D[config_manager_identities.go];
    A --> E[config_manager.go (main)];

    subgraph "Responsibilities"
        B -- manages --> F[config.json];
        C -- manages --> G[install.json];
        D -- manages --> H[identities.json];
        E -- coordinates & provides unified API --> B;
        E --> C;
        E --> D;
    end
```

- **`config_manager.go`:** Will contain the main `ConfigManager` struct and act as the primary entry point for the package, coordinating the other files.
- **`config_manager_config.go`:** Will handle all logic related to `config.json` (loading, saving, defaults).
- **`config_manager_install.go`:** Will handle all logic related to `install.json`.
- **`config_manager_identities.go`:** Will handle all logic related to the new `identities.json`.

## 4. API Changes

The public API of the `config_manager` will be updated to reflect the new structure. Key functions will include:

- `NewConfigManager(filePath string) (*ConfigManager, error)`
- `LoadAllConfigs() error`
- `GetConfig() *Config`
- `GetInstallConfig() *InstallConfig`
- `GetIdentities() *IdentitiesConfig`
- `GetIdentity(name string) (*PublicIdentity, error)`
- `GetOwnedIdentity(name string) (*OwnedIdentity, error)`

## 5. Bragging Module Removal

The `bragging` module and its associated code will be completely removed from the codebase. This includes:
- Deleting the `src/bragging` directory.
- Removing the `BraggingConfig` struct from `config_manager`.
- Removing any calls to the bragging module from other parts of the application.

## 6. Migration Plan

A migration script will be created to transition from the old configuration format to the new one. The migration will be triggered automatically if an old `config.json` is detected.

The process will be:
1.  Read the existing `config.json`.
2.  Create a new `identities.json` file and populate it with data from the old `config.json` (`tollgate_private_key`, `trusted_maintainers`).
3.  Create a new, simplified `config.json` file, removing the fields that were moved to `identities.json`.
4.  Backup the old `config.json` to `config.json.bak`.
