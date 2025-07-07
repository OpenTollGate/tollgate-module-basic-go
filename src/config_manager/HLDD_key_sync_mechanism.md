# HLDD: Automatic Public Key Synchronization Mechanism

## 1. Overview

This document outlines the high-level design for a mechanism to automatically update the operator's public key in `identities.json` whenever the `TollgatePrivateKey` in `config.json` is modified. The current system only synchronizes these keys at application startup, which can lead to data inconsistency if `config.json` is edited while the application is running.

The proposed solution introduces a file watcher that monitors `config.json` for changes and triggers a synchronization process to regenerate the public key and update `identities.json` accordingly. This ensures that the public key always reflects the current private key, maintaining system integrity.

## 2. System Architecture

The new mechanism will be integrated into the existing `ConfigManager` and will consist of a file watcher, an event handler, and a dedicated synchronization function.

```mermaid
graph TD
    subgraph ConfigManager
        A[File Watcher (fsnotify)] -->|Write Event on config.json| B{Event Handler};
        B -->|Triggers| C[SyncOperatorIdentity()];
    end

    subgraph File System
        D(User/Process edits config.json) -->|Modifies| E[config.json];
        C -->|1. Reads| E;
        C -->|2. Reads| F[identities.json];
        C -->|3. Writes| F;
    end

    style A fill:#f9f,stroke:#333,stroke-width:2px
    style C fill:#ccf,stroke:#333,stroke-width:2px
```

### Components:

*   **File Watcher (`fsnotify`)**: A lightweight, non-blocking file system watcher that monitors `config.json` for write operations.
*   **Event Handler**: A simple handler that, upon receiving a notification from the watcher, will invoke the key synchronization logic.
*   **`SyncOperatorIdentity()` function**: A dedicated function that encapsulates the logic to read the private key, derive the public key, and update the `identities.json` file if a change is detected.

## 3. Detailed Mechanism

### 3.1. Change Detection

The `ConfigManager` will initialize an `fsnotify` watcher during its creation. This watcher will be configured to monitor the `config.json` file for `fsnotify.Write` events. This is an efficient and standard way to detect file modifications in Go.

### 3.2. Triggering Mechanism

Upon detecting a write event, the file watcher will trigger the `SyncOperatorIdentity()` function. This process will run in a separate goroutine to avoid blocking the main application thread. A mutex will be used to ensure that only one synchronization operation can run at a time, preventing race conditions from rapid file saves.

### 3.3. Public Key Regeneration and Update

The `SyncOperatorIdentity()` function will perform the following steps:
1.  Acquire a lock to prevent concurrent execution.
2.  Load the `config.json` file to get the current `TollgatePrivateKey`.
3.  Load the `identities.json` file.
4.  Derive the public key (`npub`) from the private key using `nostr.GetPublicKey()`.
5.  Find the "operator" identity in the loaded identities.
6.  Compare the newly derived public key with the existing public key in `identities.json`.
7.  If they differ, update the "operator" identity with the new public key and save the `identities.json` file.
8.  Release the lock.

## 4. Interface Changes

### 4.1. `ConfigManager` Struct
*   A new `syncMutex` of type `sync.Mutex` will be added to handle concurrent access.

### 4.2. New Functions
*   `watchConfigFiles()`: A private method on `ConfigManager` that sets up and runs the file watcher loop in a goroutine.
*   `SyncOperatorIdentity()`: A public method on `ConfigManager` containing the core synchronization logic. This makes the logic accessible for both the file watcher and the initial startup check.

### 4.3. Modified Functions
*   `NewConfigManager()`: Will be modified to call `watchConfigFiles()` to start the file watcher as a background process.
*   `EnsureDefaultIdentities()`: Will be refactored to call `SyncOperatorIdentity()` to simplify its logic and avoid code duplication.

## 5. Error Handling and Resilience

*   **Invalid Private Key**: If a user saves an invalid private key to `config.json`, `nostr.GetPublicKey()` will fail. The error will be logged, and the synchronization process will be aborted for that attempt, leaving `identities.json` unchanged.
*   **File I/O Errors**: Any errors during reading or writing of the configuration files will be logged, and the operation will be aborted.
*   **Race Conditions**: A `sync.Mutex` will be used to prevent race conditions caused by multiple, rapid changes to `config.json`.

## 6. Performance Considerations

The `fsnotify` library is highly optimized and has a very low resource footprint, making it suitable for deployment on resource-constrained OpenWRT devices. The synchronization process itself is lightweight and only performs file I/O and a cryptographic operation when a change is detected.

## 7. Future Extensibility

The file watcher mechanism can be extended in the future to monitor other configuration files (e.g., `install.json`) and trigger other reactive logic as needed, providing a foundation for a more dynamic configuration management system.