# HLDD: Identity Management Refactor

## 1. Overview

This document outlines a significant architectural refactoring of the identity management system within the `config_manager`. The current design stores the `TollgatePrivateKey` in `config.json` and its corresponding public key (`npub`) in `identities.json`. This separation creates data consistency issues, as the public key can become stale if the private key is modified while the application is running.

The proposed solution is to treat the private key as the single source of truth. This will be achieved by:
1.  Relocating the operator's private key from `config.json` into `identities.json`.
2.  Removing the public key from being stored in `identities.json`.
3.  Deriving the public key on-demand from the private key whenever it is needed.

This change eliminates the need for complex file-watching and synchronization mechanisms, guarantees key consistency by design, and simplifies the overall architecture.

## 2. System Architecture

The new architecture removes the need for any synchronization between `config.json` and `identities.json`. The `Identity` struct will be modified to store the private key, and a getter function will provide the public key on-demand.

```mermaid
graph TD
    subgraph ConfigManager
        A[GetOperatorPublicKey()] -->|1. Reads| B[identities.json];
        B --_private key_--> A;
        A -->|2. Derives| C[nostr.GetPublicKey(privateKey)];
        C --_public key_--> A;
        A -->|Returns public key| D((Calling Function));
    end

    style A fill:#ccf,stroke:#333,stroke-width:2px
```

### Components:

*   **`identities.json`**: The single source of truth for all identity information, including the operator's private key.
*   **`GetOperatorPublicKey()`**: A new getter function that reads the private key from `identities.json`, derives the public key, and returns it. This function will be the sole entry point for accessing the operator's public key.

## 3. Data Structure Changes

### 3.1. `config.json`

*   The `TollgatePrivateKey` field will be **removed** from the `Config` struct.

### 3.2. `identities.json`

*   The `Identity` struct will be modified to use a single `Key` field. This field will store the key in Bech32 format (`nsec` for private keys, `npub` for public keys). This allows the system to gracefully handle both controlled identities (like the operator) and external identities (like developers).

**Old `Identity` Struct:**
```go
type Identity struct {
    Name             string `json:"name"`
    Npub             string `json:"npub"`
    LightningAddress string `json:"lightning_address"`
}
```

**New `Identity` Struct:**
```go
type Identity struct {
    Name             string `json:"name"`
    Key              string `json:"key,omitempty"`
    LightningAddress string `json:"lightning_address"`
}
```

## 4. Functional Changes

### 4.1. New Functions

*   `GetIdentity(name string) (*Identity, error)`: A function to retrieve a specific identity object by name from `identities.json`.
*   `GetPublicKey(name string) (string, error)`: A versatile function that returns the public key (`npub`) for any given identity name.
    *   If the identity's key is an `nsec`, it derives and returns the `npub`.
    *   If the identity's key is an `npub`, it returns it directly.
*   `GetPrivateKey(name string) (string, error)`: A function that returns the private key (`nsec`) for a given identity name. It will return an error if the identity only has a public key.

### 4.2. Modified Functions

*   `EnsureDefaultIdentities()`: This function will be modified to generate a new private key for the `operator` identity and store it in `nsec` format in the `Key` field. For the `developer` identity, it will store a known `npub` key.
*   All functions that currently use the operator's public key will be refactored to call `GetPublicKey("operator")`.

## 5. Migration Strategy

A migration path must be provided for existing installations. When the `ConfigManager` loads, it will check for the presence of `TollgatePrivateKey` in `config.json`. If found, it will:
1.  Move the key to the `operator` identity in `identities.json`.
2.  Remove the `TollgatePrivateKey` from `config.json`.
3.  Save both modified files.

This ensures a seamless, one-time upgrade for users.

## 6. Evaluation Summary

*   **Security:** Acceptable. While the private key is loaded into memory for derivation, this is managed within a tight scope. Overall security posture is improved by simplifying the system.
*   **Performance:** The performance impact of on-demand key derivation is considered negligible for the expected usage patterns on OpenWRT devices.
*   **Complexity:** Drastically reduced. Eliminates the need for file watchers, concurrency management (mutexes), and complex synchronization logic.
*   **Consistency:** Guaranteed by design. This is the primary benefit of this architectural change.

This refactoring represents a significant improvement in the robustness and maintainability of the identity management system.