# LLDD: Configuration Management Refactor

## 1. Introduction

This document provides a low-level design for the refactoring of the `config_manager` module. It details the data structures, file organization, and function signatures required to implement the new architecture outlined in the HLDD.

## 2. Data Structures

The following Go structs will be defined to represent the new configuration files.

### 2.1. `config.go`

```go
package config_manager

// Config represents the main configuration for the Tollgate service.
type Config struct {
	ConfigVersion string       `json:"config_version"`
	AcceptedMints []MintConfig `json:"accepted_mints"`
	ProfitShare   []ProfitShareConfig `json:"profit_share"`
	StepSize      uint64       `json:"step_size"`
	Metric        string       `json:"metric"`
	Relays        []string     `json:"relays"`
	ShowSetup     bool         `json:"show_setup"`
}

// MintConfig holds configuration for a specific mint.
type MintConfig struct {
	URL                     string `json:"url"`
	MinBalance              uint64 `json:"min_balance"`
	BalanceTolerancePercent uint64 `json:"balance_tolerance_percent"`
	PayoutIntervalSeconds   uint64 `json:"payout_interval_seconds"`
	MinPayoutAmount         uint64 `json:"min_payout_amount"`
	PricePerStep            uint64 `json:"price_per_step"`
	PriceUnit               string `json:"price_unit"`
	MinPurchaseSteps        uint64 `json:"purchase_min_steps"`
}

// ProfitShareConfig defines how profits are shared.
type ProfitShareConfig struct {
	Factor   float64 `json:"factor"`
	Identity string  `json:"identity"`
}
```

### 2.2. `install.go`

```go
package config_manager

// InstallConfig holds installation-specific parameters.
type InstallConfig struct {
	ConfigVersion          string `json:"config_version"`
	PackagePath            string `json:"package_path"`
	IPAddressRandomized    bool   `json:"ip_address_randomized"`
	InstallTimestamp       int64  `json:"install_time"`
	DownloadTimestamp      int64  `json:"download_time"`
	ReleaseChannel         string `json:"release_channel"`
	EnsureDefaultTimestamp int64  `json:"ensure_default_timestamp"`
	InstalledVersion       string `json:"installed_version"`
}
```

### 2.3. `identities.go`

```go
package config_manager

// IdentitiesConfig holds all user and system identities.
type IdentitiesConfig struct {
	ConfigVersion    string            `json:"config_version"`
	OwnedIdentities  []OwnedIdentity   `json:"owned_identities"`
	PublicIdentities []PublicIdentity  `json:"public_identities"`
}

// OwnedIdentity represents an identity with a private key.
type OwnedIdentity struct {
	Name       string `json:"name"`
	PrivateKey string `json:"privatekey"`
}

// PublicIdentity represents a public-facing identity.
type PublicIdentity struct {
	Name             string `json:"name"`
	PubKey           string `json:"pubkey,omitempty"`
	LightningAddress string `json:"lightning_address,omitempty"`
}
```

## 3. File and Function Organization

The `config_manager` package will be split into the following files:

### 3.1. `config_manager.go`

- **`ConfigManager` struct:**
  ```go
  type ConfigManager struct {
      ConfigFilePath     string
      InstallFilePath    string
      IdentitiesFilePath string
      config             *Config
      installConfig      *InstallConfig
      identitiesConfig   *IdentitiesConfig
      // ... nostr pools
  }
  ```
- **`NewConfigManager(configPath, installPath, identitiesPath string) (*ConfigManager, error)`:** Initializes the manager and loads all configurations.
- **Getters:** `GetConfig()`, `GetInstallConfig()`, `GetIdentities()`, `GetIdentity()`, `GetOwnedIdentity()`.

### 3.2. `config_manager_config.go`

- **`LoadConfig(filePath string) (*Config, error)`:** Loads and parses `config.json`.
- **`SaveConfig(filePath string, config *Config) error`:** Saves `config.json`.
- **`EnsureDefaultConfig(filePath string) (*Config, error)`:** Creates a default `config.json` if one doesn't exist.

### 3.3. `config_manager_install.go`

- **`LoadInstallConfig(filePath string) (*InstallConfig, error)`:** Loads and parses `install.json`.
- **`SaveInstallConfig(filePath string, config *InstallConfig) error`:** Saves `install.json`.
- **`EnsureDefaultInstall(filePath string) (*InstallConfig, error)`:** Creates a default `install.json`.

### 3.4. `config_manager_identities.go`

- **`LoadIdentities(filePath string) (*IdentitiesConfig, error)`:** Loads and parses `identities.json`.
- **`SaveIdentities(filePath string, config *IdentitiesConfig) error`:** Saves `identities.json`.
- **`EnsureDefaultIdentities(filePath string) (*IdentitiesConfig, error)`:** Creates a default `identities.json`.

## 4. Bragging Module Removal

All files, structs, and functions related to the `bragging` module will be deleted.
- Remove `BraggingConfig` from the old `Config` struct.
- Delete the `src/bragging` directory.

## 5. Configuration Resilience Logic

The resilience logic will be implemented within the `EnsureDefault...` functions for each configuration file (`config_manager_config.go`, `config_manager_install.go`, `config_manager_identities.go`). This approach avoids a central migration function and handles invalid configurations at the source.

### 5.1. Generic Backup Function

A new helper function will be added to `config_manager.go` to handle the file backup process.

```go
// backupAndLog backs up a specified file and logs the action.
func backupAndLog(filePath, backupDir, fileType, codeVersion string) error {
    // 1. Ensure backup directory exists
    if err := os.MkdirAll(backupDir, 0755); err != nil {
        return fmt.Errorf("failed to create backup directory: %w", err)
    }

    // 2. Generate backup filename
    timestamp := time.Now().UTC().Format("2006-01-02T15-04-05Z")
    backupFilename := fmt.Sprintf("%s_%s_%s.json", fileType, timestamp, codeVersion)
    backupPath := filepath.Join(backupDir, backupFilename)

    // 3. Move the file
    if err := os.Rename(filePath, backupPath); err != nil {
        return fmt.Errorf("failed to move config to backup: %w", err)
    }

    // 4. Log the action
    log.Printf("WARNING: Invalid '%s' config file found. Backed up to %s", fileType, backupPath)
    return nil
}
```

### 5.2. Modified `EnsureDefault...` Functions

Each `EnsureDefault...` function will be modified to include the resilience logic. Below is the example for `EnsureDefaultConfig`. The same pattern will be applied to `EnsureDefaultInstall` and `EnsureDefaultIdentities`.

**`config_manager_config.go`**

```go
// EnsureDefaultConfig ensures a default config.json exists, loading from file if present.
func EnsureDefaultConfig(filePath string) (*Config, error) {
    defaultConfig := NewDefaultConfig()
    data, err := os.ReadFile(filePath)
    if err != nil {
        if os.IsNotExist(err) {
            // File does not exist, save the default config
            return defaultConfig, SaveConfig(filePath, defaultConfig)
        }
        return nil, err // Other read error
    }

    // File exists, attempt to unmarshal
    var config Config
    if err := json.Unmarshal(data, &config); err != nil || config.ConfigVersion != defaultConfig.ConfigVersion {
        // Unmarshal failed or version mismatch, trigger backup and recreate
        if backupErr := backupAndLog(filePath, "/etc/tollgate/config_backups", "config", defaultConfig.ConfigVersion); backupErr != nil {
            log.Printf("CRITICAL: Failed to backup and remove invalid config: %v", backupErr)
            // Depending on desired behavior, we might return an error or proceed with default
            return nil, backupErr
        }
        // Save new default config
        return defaultConfig, SaveConfig(filePath, defaultConfig)
    }

    return &config, nil
}
```

This implementation ensures that any time a configuration file is found to be invalid or outdated, it is safely backed up, and the system self-heals by creating a new default file, preventing crashes and improving overall robustness.
