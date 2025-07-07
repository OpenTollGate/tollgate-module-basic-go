# LLDD: Automatic Public Key Synchronization Mechanism

## 1. Introduction

This document provides the low-level design and implementation details for the automatic public key synchronization mechanism, as outlined in the corresponding HLDD. It is intended to guide a developer in implementing the file watcher and the key synchronization logic within the `config_manager` package.

## 2. File and Struct Modifications

### 2.1. `config_manager.go`

*   **Imports**: The `github.com/fsnotify/fsnotify` package and `sync` package will be added to the imports.

*   **`ConfigManager` Struct**: The struct will be updated to include a mutex for handling concurrent synchronization attempts.

    ```go
    import (
        "sync"
        "github.com/fsnotify/fsnotify"
    )

    type ConfigManager struct {
        FilePath      string
        PublicPool    *nostr.SimplePool
        LocalPool     *nostr.SimplePool
        syncMutex     sync.Mutex
    }
    ```

## 3. Function Implementation

### 3.1. `NewConfigManager`

This function will be modified to initialize the file watcher as a background process.

```go
func NewConfigManager(filePath string) (*ConfigManager, error) {
    publicPool := nostr.NewSimplePool(context.Background())
    localPool := nostr.NewSimplePool(context.Background())
    cm := &ConfigManager{
        FilePath:   filePath,
        PublicPool: publicPool,
        LocalPool:  localPool,
    }

    go cm.watchConfigFiles() // Start the file watcher in a goroutine

    return cm, nil
}
```

### 3.2. `watchConfigFiles`

This new private method will set up and manage the `fsnotify` watcher.

```go
func (cm *ConfigManager) watchConfigFiles() {
    watcher, err := fsnotify.NewWatcher()
    if err != nil {
        log.Printf("Error creating file watcher: %v", err)
        return
    }
    defer watcher.Close()

    done := make(chan bool)
    go func() {
        for {
            select {
            case event, ok := <-watcher.Events:
                if !ok {
                    return
                }
                // We only care about write events.
                if event.Op&fsnotify.Write == fsnotify.Write {
                    log.Printf("Detected change in %s, triggering sync.", event.Name)
                    cm.SyncOperatorIdentity()
                }
            case err, ok := <-watcher.Errors:
                if !ok {
                    return
                }
                log.Printf("File watcher error: %v", err)
            }
        }
    }()

    err = watcher.Add(cm.FilePath)
    if err != nil {
        log.Printf("Error adding file to watcher: %v", err)
    }
    <-done
}
```

### 3.3. `SyncOperatorIdentity`

This new public method will contain the core logic for synchronizing the operator's public key. It will be called by both the file watcher and `EnsureDefaultIdentities`.

```go
// SyncOperatorIdentity ensures the operator's npub in identities.json is derived from the private key in config.json.
func (cm *ConfigManager) SyncOperatorIdentity() error {
    cm.syncMutex.Lock()
    defer cm.syncMutex.Unlock()

    config, err := cm.LoadConfig()
    if err != nil {
        return fmt.Errorf("failed to load config for identity sync: %w", err)
    }
    if config == nil || config.TollgatePrivateKey == "" {
        // This can happen if the config is deleted or malformed.
        // We log it but don't return an error to prevent crashing the watcher loop.
        log.Println("Skipping identity sync: config or private key is missing.")
        return nil
    }

    pubKey, err := nostr.GetPublicKey(config.TollgatePrivateKey)
    if err != nil {
        log.Printf("Invalid private key in config.json, cannot derive public key: %v", err)
        return nil // Don't propagate error, just log and abort.
    }

    identityConfig, err := cm.LoadIdentities()
    if err != nil {
        return fmt.Errorf("failed to load identities for sync: %w", err)
    }
    if identityConfig == nil {
        log.Println("Skipping identity sync: identities.json not found.")
        return nil
    }

    changed := false
    found := false
    for i, identity := range identityConfig.Identities {
        if identity.Name == "operator" {
            found = true
            if identity.Npub != pubKey {
                log.Printf("Operator npub out of sync. Updating from %s to %s", identity.Npub, pubKey)
                identityConfig.Identities[i].Npub = pubKey
                changed = true
            }
            break
        }
    }

    if !found {
        log.Println("Operator identity not found, cannot sync.")
        return nil
    }

    if changed {
        log.Printf("Saving updated identities configuration to %s", cm.identitiesFilePath())
        if err := cm.SaveIdentities(identityConfig); err != nil {
            return err
        }
    }

    return nil
}
```

### 3.4. Refactoring `EnsureDefaultIdentities`

The existing `EnsureDefaultIdentities` function will be simplified. The logic for updating the operator's `npub` will be removed and replaced with a call to the new `SyncOperatorIdentity` function. This avoids code duplication and centralizes the synchronization logic.

**Before:**
```go
// (inside EnsureDefaultIdentities)
for i, identity := range identityConfig.Identities {
    if identity.Name == "operator" && identity.Npub == "" {
        config, err := cm.LoadConfig()
        if err != nil {
            log.Printf("Warning: Failed to load config to update operator npub: %v", err)
        } else if config != nil && config.TollgatePrivateKey != "" {
            if pubKey, getPubKeyErr := nostr.GetPublicKey(config.TollgatePrivateKey); getPubKeyErr == nil {
                identityConfig.Identities[i].Npub = pubKey
                log.Printf("Updated operator npub to %s", pubKey)
                changed = true
            }
        }
    }
    // ...
}
```

**After:**
```go
// (at the end of EnsureDefaultIdentities, before returning)
if err := cm.SyncOperatorIdentity(); err != nil {
    log.Printf("Failed to sync operator identity during initialization: %v", err)
    // Decide if this should be a fatal error during startup
}
// The original loop for updating the npub can be removed.
```

## 4. Testing Strategy

The implementation will be validated through unit and manual tests as outlined in their respective test plan documents. Key scenarios to test include:
*   Correctly updating the `npub` when `config.json` is changed.
*   Handling of invalid private keys.
*   Graceful recovery from file I/O errors.
*   Correct behavior when `config.json` or `identities.json` are missing.
*   No race conditions during rapid file modifications.