# High-Level Design Document (HLDD) - Router Identity Management

## 1. Problem Statement
The TollGate router currently has a static and predictable network identity (MAC addresses and SSID). This presents two challenges:
1.  **Privacy:** A static identity can be tracked over time.
2.  **Inflexibility:** There is no easy, built-in mechanism for an operator to rotate the router's network identifiers for security or operational reasons.

The goal is to create a system that allows an operator to manage and rotate the router's network identity in a deterministic way.

## 2. Goal
1.  Create a mechanism to programmatically get a list of all network interface MAC addresses on the device.
2.  Create a mechanism to programmatically set/rotate all of these MAC addresses to new, random, valid values that persist across reboots.
3.  Use a cryptographic hash of the complete list of MAC addresses as a stable, deterministic seed for generating the TollGate Wi-Fi SSID.
4.  Ensure this entire system is integrated into the main installation script and can be manually triggered by the operator.

## 3. Proposed Solution Architecture
The solution will be composed of three new internal helper scripts and modifications to the main setup script.

1.  **`get-identity.sh`:**
    *   An internal helper script that scans all network interfaces (`/sys/class/net/*`).
    *   It will deterministically (sorted alphabetically by interface name) collect the MAC address of every interface.
    *   It will output a simple, sorted list of all MAC addresses, which represents the router's complete network identity.

2.  **`set-identity.sh`:**
    *   A manually-triggered helper script that performs the identity rotation.
    *   It will iterate through all network interfaces that have a MAC address.
    *   For each interface, it will generate a new, valid, random MAC address.
    *   It will use `uci` to set the new MAC address in the persistent configuration (`/etc/config/network` and `/etc/config/wireless`).
    *   After setting all new MACs, it will trigger a network restart to apply the changes.

3.  **`generate-tollgate-ssid.sh`:**
    *   An internal helper script that generates the TollGate SSID.
    *   It will call `get-identity.sh` to get the full list of current MAC addresses.
    *   It will compute a SHA256 hash of this list.
    *   It will take the first 4 hexadecimal characters of the hash to create the SSID suffix (e.g., `TollGate-ABCD`).

4.  **`99-tollgate-setup` (Modified):**
    *   The main setup script will now call `generate-tollgate-ssid.sh` once to get the deterministic SSID.
    *   It will then apply this single SSID to all existing AP interfaces on all radios, ensuring a consistent network name.

## 4. Data Flow Diagram (Mermaid)

```mermaid
graph TD
    subgraph Identity Rotation (Manual Trigger)
        A[Operator runs set-identity.sh] --> B{For each network interface...};
        B --> C[Generate new random MAC];
        C --> D[uci set new MAC (persistent)];
        D --> B;
        B -- After loop --> E[Reboot or Network Restart];
    end

    subgraph SSID Generation (Automatic on Setup/Boot)
        F[99-tollgate-setup] --> G[calls get-identity.sh];
        G --> H[Get sorted list of all MACs];
        H --> I[SHA256 Hash of list];
        I --> J[Take first 4 chars of hash];
        J --> K[SSID: TollGate-XXXX];
        F --> L{For each radio...};
        L --> M[Find existing AP];
        M --> N[Set SSID to TollGate-XXXX];
        N --> L;
    end
```

## 5. Future Extensibility Considerations
*   **Selective Rotation:** A future version could allow the operator to rotate the MAC address of only a specific interface (e.g., `set-identity.sh radio0`).
*   **Hardware vs. Virtual MACs:** The scripts could be made more intelligent to distinguish between physical hardware MACs and virtual interface MACs if more granular control is ever needed.