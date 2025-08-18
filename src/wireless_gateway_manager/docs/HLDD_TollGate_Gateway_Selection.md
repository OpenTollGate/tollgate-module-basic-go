# High-Level Design Document (HLDD): TollGate Wireless Gateway Selection and Connection

## 1. Introduction

This document outlines the high-level design for identifying and connecting to wireless gateways with "TollGate" SSIDs within an OpenWRT environment. The primary goal is to provide a mechanism for OpenWRT-based devices to autonomously discover, prioritize, and connect to these specialized gateways, integrating aspects of Bitcoin and Nostr protocols via vendor elements. To prevent routing loops, a hop count mechanism is implemented, where the hop count is advertised in the SSID.

## 2. System Overview

The system comprises a series of interconnected shell scripts executed on an OpenWRT device. These scripts work in concert to:
1. Scan for available Wi-Fi networks.
2. Identify and extract specific data from "TollGate" networks via vendor elements.
3. Parse a hop count from "TollGate" SSIDs, which follow the format `TollGate-[ID]-[Frequency]-[HopCount]`.
4. Score networks based on signal strength and custom vendor-provided metrics.
5. Filter available gateways, allowing connections only to networks with a hop count strictly lower than the device's own.
6. Allow selection (manual or potentially automatic) of a preferred gateway.
7. Configure the OpenWRT device to connect to the chosen gateway and update its own hop count and advertised SSID.

## 3. Component Interaction and Data Flow

The core components involved are:

*   **[`scan_wifi_networks.sh`](files/root/scan_wifi_networks.sh):** Performs Wi-Fi scanning and initial parsing, enriching "TollGate" network data.
*   **[`get_vendor_elements.sh`](files/root/get_vendor_elements.sh):** Extracts specific vendor-defined information from network beacon frames.
*   **[`decibel.sh`](files/root/decibel.sh):** Utility for converting numerical values to a decibel scale, contributing to network scoring.
*   **[`sort_wifi_networks.sh`](files/root/sort_wifi_networks.sh):** Filters, sorts, and presents network options, facilitating selection.
*   **[`select_gateway.sh`](files/root/select_gateway.sh):** Orchestrates the connection process, applying UCI configurations based on the selected gateway, with specific handling for "TollGate" networks.
*   **OpenWRT UCI:** The Unified Configuration Interface for managing system settings (`wireless`, `network`, `firewall`).

### 3.1. Data Flow Diagram

```mermaid
graph TD
    A[Start Selection Process] --> B(Call select_gateway.sh);
    B --> C{User Selects Network};
    C --> D[select_gateway.sh reads /tmp/selected_ssid.md];
    D --> E[Configure OpenWRT via UCI];
    E --> F{Connect to Gateway};
    F --> G[Check Internet Connectivity];
    G -- If TollGate_ --> H[Update /etc/hosts with Gateway IP];
    G --> I[Enable Local AP (if internet OK)];
    I --> J(select_gateway.sh: Update own Hop Count & AP SSID);
    J --> K[Connection Established];

    subgraph Network Scan & Scoring
        C --- L(Call sort_wifi_networks.sh --select-ssid);
        L --> M(Call scan_wifi_networks.sh);
        M --> N[iw scan];
        N --> O{Parse SSID, Hop Count, & Score Networks};
        O -- TollGate_ SSID --> P[Call get_vendor_elements.sh];
        P --> Q[Call decibel.sh];
        O --> R[Output JSON to sort_wifi_networks.sh];
    end

    subgraph Network Filtering & Sorting
        R --> S(sort_wifi_networks.sh processes JSON);
        S --> T{Get Own Hop Count};
        T --> U{Filter by Hop Count < Own Hop Count};
        U --> V{Sort by Score};
        V --> W[Present Networks to User];
        W -- Selected Network JSON --> X[Save to /tmp/selected_ssid.md];
    end
```

### 3.2. API Definitions / Interface Contracts

*   **[`scan_wifi_networks.sh`](files/root/scan_wifi_networks.sh):**
    *   **Input:** None (retrieves Wi-Fi interface internally).
    *   **Output:** JSON array of Wi-Fi networks, each with `mac`, `ssid`, `encryption`, `signal`, `hop_count`, and `score`. For "TollGate" SSIDs, includes `kb_allocation_dB` and `contribution_dB`.
    *   **External Calls:** `iw`, `awk`, `jq`, [`get_vendor_elements.sh`](files/root/get_vendor_elements.sh), [`decibel.sh`](files/root/decibel.sh).

*   **[`get_vendor_elements.sh`](files/root/get_vendor_elements.sh):**
    *   **Input:** SSID (string), Number of bytes to extract for vendor elements (integer).
    *   **Output:** JSON object containing extracted vendor elements (e.g., `kb_allocation_decimal`, `contribution_decimal`).
    *   **External Calls:** `iw` (implicitly via `parse_beacon.sh` or similar for vendor element parsing).

*   **[`decibel.sh`](files/root/decibel.sh):**
    *   **Input:** Decimal value (integer/float).
    *   **Output:** Decibel value (integer/float).

*   **[`sort_wifi_networks.sh`](files/root/sort_wifi_networks.sh):**
    *   **Input:** JSON array of networks (from `scan_wifi_networks.sh`), optionally command-line arguments (--full-json, --tollgate-json, --ssid-list, --select-ssid). It will also need to determine its own hop count, likely by inspecting its own current connection status via `uci` or `iw`.
    *   **Output:**
        *   `--full-json`, `--tollgate-json`, `--ssid-list`: JSON or plain text list of networks.
        *   `--select-ssid`: Interactive prompt for user selection.
    *   **External Calls:** `jq`, `./scan_wifi_networks.sh`.
    *   **Side Effects:** Writes selected network JSON to `/tmp/selected_ssid.md`, full network JSON to `/tmp/networks.json`.

*   **[`select_gateway.sh`](files/root/select_gateway.sh):**
    *   **Input:** None (orchestrates selection via `sort_wifi_networks.sh`).
    *   **Output:** Configuration changes applied to OpenWRT via UCI. Status messages printed to console.
    *   **External Calls:** `./sort_wifi_networks.sh`, `cat`, `jq`, `uci`, `/etc/init.d/network`, `ping`, `ip route`, `sed`, `wifi`.
    *   **Side Effects:** Modifies UCI configuration (`firewall`, `network`, `wireless`), updates `/etc/hosts` for "TollGate" networks, potentially enables local AP, and updates the local AP's SSID to advertise the new hop count.

## 4. Future Extensibility Considerations

*   **Automated Gateway Selection:** Implement a mode within `sort_wifi_networks.sh` or `select_gateway.sh` to automatically choose the highest-scoring TollGate network without user intervention. This would be crucial for headless devices or automated deployments.
*   **Dynamic Vendor Element Parsing:** Enhance `get_vendor_elements.sh` to dynamically adapt to different vendor element structures or versions, allowing for greater flexibility.
*   **Alternative Scoring Metrics:** Introduce new scoring parameters (e.g., latency, throughput) for more sophisticated network prioritization.
*   **Security for TollGate Networks:** While current design forces encryption to `none`, future enhancements could explore secure, zero-config authentication methods (e.g., based on Nostr keys) that don't rely on traditional Wi-Fi passwords.
*   **Centralized Configuration Management:** For large deployments, integrate with a centralized configuration management system instead of local UCI commands.
*   **Error Handling and Logging:** Implement more robust error handling and detailed logging for debugging and monitoring, especially for network connection failures.
*   **UI Integration:** Develop a LuCI (OpenWRT web interface) module or a separate web application for a more user-friendly interface to manage gateway selection.