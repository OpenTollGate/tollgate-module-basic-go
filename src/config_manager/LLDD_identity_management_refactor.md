# LLDD: Identity Management Refactor

## 1. Introduction

This document provides the low-level implementation details for the identity management refactoring, as specified in `HLDD_identity_management_refactor.md`. The goal is to move to a single-source-of-truth model for cryptographic keys by storing them in `identities.json` in `nsec` or `npub` format.

## 2. Data Structures

### 2.1. Identity Struct

The `Identity` struct in `config_manager.go` will be updated as follows. The `Npub` field will be replaced by `Key`.

```go
// src/config_manager/config_manager.go

// Identity holds the configuration for a single identity.
type Identity struct {
    Name             string `json:"name"`
    Key              string `json:"key,omitempty"` // Stores nsec or npub
    LightningAddress string `json:"lightning_address"`
}
```

### 2.2. Config Struct

The `TollgatePrivateKey` field will be removed from the `Config` struct.

```go
// src/config_manager/config_manager.go

// Config holds the configuration parameters
type Config struct {
	ConfigVersion         string              `json:"config_version"`
	// TollgatePrivateKey    string              `json:"tollgate_private_key"` // REMOVED
	AcceptedMints         []MintConfig        `json:"accepted_mints"`
	// ... rest of the struct
}
```

## 3. Function Implementation

### 3.1. `GetIdentity(name string)`

This function will retrieve an identity by its name from the `identities.json` file.

**File:** `src/config_manager/config_manager.go`

```go
// GetIdentity retrieves a specific identity by name.
func (cm *ConfigManager) GetIdentity(name string) (*Identity, error) {
    identityConfig, err := cm.LoadIdentities()
    if err != nil {
        return nil, fmt.Errorf("failed to load identities: %w", err)
    }
    if identityConfig == nil {
        return nil, fmt.Errorf("identities configuration is not loaded")
    }

    for _, identity := range identityConfig.Identities {
        if identity.Name == name {
            return &identity, nil
        }
    }

    return nil, fmt.Errorf("identity '%s' not found", name)
}
```

### 3.2. `GetPublicKey(name string)`

This function will return the `npub` for a given identity, deriving it if necessary.

**File:** `src/config_manager/config_manager.go`

```go
// GetPublicKey returns the public key (npub) for a given identity.
// It derives the public key if an nsec is present.
func (cm *ConfigManager) GetPublicKey(name string) (string, error) {
    identity, err := cm.GetIdentity(name)
    if err != nil {
        return "", err
    }

    if identity.Key == "" {
        return "", fmt.Errorf("key for identity '%s' is empty", name)
    }

    // Check if the key is a private key (nsec)
    if strings.HasPrefix(identity.Key, "nsec") {
        _, hexKey, err := nostr.Decode(identity.Key)
        if err != nil {
            return "", fmt.Errorf("failed to decode nsec for identity '%s': %w", name, err)
        }
		
        hexPubKey, err := nostr.GetPublicKey(hexKey.(string))
        if err != nil {
            return "", fmt.Errorf("failed to derive public key for identity '%s': %w", name, err)
        }
        
        npub, err := nostr.EncodePublicKey(hexPubKey)
        if err != nil {
            return "", fmt.Errorf("failed to encode public key for identity '%s': %w", name, err)
        }
        return npub, nil
    }

    // Check if the key is a public key (npub)
    if strings.HasPrefix(identity.Key, "npub") {
        return identity.Key, nil
    }

    return "", fmt.Errorf("invalid key format for identity '%s': must be nsec or npub", name)
}
```

### 3.3. `GetPrivateKey(name string)`

This function will return the hex-encoded private key for a given identity.

**File:** `src/config_manager/config_manager.go`

```go
// GetPrivateKey returns the private key (hex) for a given identity.
// It returns an error if the identity only has a public key.
func (cm *ConfigManager) GetPrivateKey(name string) (string, error) {
    identity, err := cm.GetIdentity(name)
    if err != nil {
        return "", err
    }

    if !strings.HasPrefix(identity.Key, "nsec") {
        return "", fmt.Errorf("private key not available for identity '%s'", name)
    }

    _, hexKey, err := nostr.Decode(identity.Key)
    if err != nil {
        return "", fmt.Errorf("failed to decode nsec for identity '%s': %w", name, err)
    }

    return hexKey.(string), nil
}
```

### 3.4. `EnsureDefaultIdentities()` Modification

This function requires significant changes to align with the new model.

**File:** `src/config_manager/config_manager.go`

The logic inside `EnsureDefaultIdentities` should be updated to:
1.  Check for the "operator" identity.
2.  If the operator's `Key` field is empty, generate a new private key using `nostr.GeneratePrivateKey()`.
3.  Encode the new private key into `nsec` format using `nostr.EncodePrivateKey()`.
4.  Save the `nsec` key to the operator's `Key` field.
5.  Check for the "developer" identity.
6.  If the developer's `Key` field is empty, populate it with the known default `npub`.

## 4. Migration Logic

A new private function, `migrateLegacyPrivateKey()`, will be created and called from within `EnsureInitializedConfig()`.

**File:** `src/config_manager/config_manager.go`

```go
// migrateLegacyPrivateKey moves the private key from config.json to identities.json
// if it exists in the old location.
func (cm *ConfigManager) migrateLegacyPrivateKey() error {
    config, err := cm.LoadConfig()
    if err != nil {
        return fmt.Errorf("migration: failed to load config: %w", err)
    }

    // If legacy key doesn't exist, there's nothing to do.
    if config == nil || config.TollgatePrivateKey == "" {
        return nil
    }

    log.Println("Legacy TollgatePrivateKey found in config.json. Migrating to identities.json...")

    identities, err := cm.LoadIdentities()
    if err != nil {
        return fmt.Errorf("migration: failed to load identities: %w", err)
    }
    if identities == nil {
        // This case should be handled by EnsureDefaultIdentities creating a new file.
        // If it's still nil, something is wrong.
        return fmt.Errorf("migration: cannot migrate key, identities file is nil")
    }

    foundOperator := false
    for i, identity := range identities.Identities {
        if identity.Name == "operator" {
            // Encode the legacy hex private key to nsec format
            nsec, err := nostr.EncodePrivateKey(config.TollgatePrivateKey)
            if err != nil {
                return fmt.Errorf("migration: failed to encode private key to nsec: %w", err)
            }
            identities.Identities[i].Key = nsec
            foundOperator = true
            break
        }
    }

    if !foundOperator {
        return fmt.Errorf("migration: 'operator' identity not found in identities.json")
    }

    // Clear the legacy key and save the config
    config.TollgatePrivateKey = ""
    if err := cm.SaveConfig(config); err != nil {
        return fmt.Errorf("migration: failed to save updated config: %w", err)
    }

    // Save the updated identities
    if err := cm.SaveIdentities(identities); err != nil {
        return fmt.Errorf("migration: failed to save updated identities: %w", err)
    }
    
    log.Println("Successfully migrated private key and cleared it from config.json.")
    return nil
}

// In EnsureInitializedConfig(), add a call to the migration function.
func (cm *ConfigManager) EnsureInitializedConfig() error {
    // ... existing calls ...
    
    if err := cm.migrateLegacyPrivateKey(); err != nil {
        // Log as a warning because the system might still be able to function
        log.Printf("Warning: Failed to migrate legacy private key: %v", err)
    }

	// ... rest of the function ...
	return nil
}
```

## 5. Refactoring Plan

All existing code that accesses `config.TollgatePrivateKey` or the operator's `npub` from the `Identity` struct must be refactored to use the new getter functions:
-   `GetPrivateKey("operator")` for signing operations.
-   `GetPublicKey("operator")` for retrieving the public key.

This systematic replacement will ensure the new identity model is used consistently throughout the application.