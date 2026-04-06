# High-Level Design Document (HLDD): `scan_wifi_networks.sh` Script

## 1. Introduction

This document provides a high-level design for the `scan_wifi_networks.sh` shell script, which is responsible for scanning available Wi-Fi networks on an OpenWRT device. A key aspect of this script is its ability to identify "TollGate" SSIDs and integrate external data (via vendor elements) into a scoring mechanism for network prioritization.

## 2. System Overview

The `scan_wifi_networks.sh` script functions as a foundational component for network discovery within the OpenWRT environment. It leverages standard `iw` utilities to perform Wi-Fi scans and processes the raw output into a structured JSON format. Its unique feature is the conditional enrichment of data for "TollGate" networks, where external helper scripts are used to extract and process specific vendor-defined information.

## 3. Component Interaction and Data Flow

The `scan_wifi_networks.sh` script interacts primarily with the following:

*   **`iw` command:** The standard Linux utility for configuring wireless devices, used for performing the Wi-Fi scan.
*   **`awk` utility:** Used for powerful text processing and parsing of the `iw scan` output into a structured format.
*   **`jq` utility:** A lightweight and flexible command-line JSON processor, used for formatting the final output and for error checking.
*   **[`get_vendor_elements.sh`](files/root/get_vendor_elements.sh):** An external script invoked to extract specific vendor-defined information from the beacon frames of "TollGate" networks.
*   **[`decibel.sh`](files/root/decibel.sh):** An external script used to convert numerical values (likely from vendor elements) into a decibel scale for scoring.

### 3.1. Data Flow Diagram

```mermaid
graph TD
    A[Start scan_wifi_networks.sh] --> B(Identify Wi-Fi Interface);
    B --> C[Execute 'iw dev <interface> scan'];
    C --> D{Parse Scan Results with awk};
    D -- For each BSS (Network) --> E{Is SSID 'TollGate_'?};

    E -- Yes --> F[Call get_vendor_elements.sh];
    F --> G[Extract kb_allocation_decimal & contribution_decimal];
    G --> H[Call decibel.sh for each value];
    H --> I[Calculate Combined Score (signal + dB values)];
    I --> J[Add Vendor Element dB Values & Score to JSON];

    E -- No --> K[Calculate Simple Score (signal only)];
    K --> L[Add Score to JSON];

    J --> M(Accumulate JSON Objects);
    L --> M;
    M --> N[Pipe to jq for Final Formatting];
    N --> O[Output JSON Array];
    O --> P[Error Handling & Retry Logic];
    P --> Q(External Script / User);
```

### 3.2. API Definitions / Interface Contracts (within `scan_wifi_networks.sh`)

*   **`get_wifi_interface()`:**
    *   **Input:** None.
    *   **Output:** Returns the name of the first available managed Wi-Fi interface (e.g., `wlan0`).
*   **`scan_wifi_networks_to_json()`:**
    *   **Input:** None (takes `interface` from `get_wifi_interface`).
    *   **Output:** JSON array of Wi-Fi network objects. Each object contains:
        *   `mac` (string): BSSID of the network.
        *   `ssid` (string): SSID of the network.
        *   `encryption` (string): Detected encryption type (e.g., "Open", "WPA2").
        *   `signal` (integer): Signal strength in dBm.
        *   `score` (integer): Calculated network score (signal only for non-TollGate, richer for TollGate).
        *   (Optional for TollGate) `kb_allocation_dB` (string): Decibel value derived from vendor element.
        *   (Optional for TollGate) `contribution_dB` (string): Decibel value derived from vendor element.
*   **`scan_until_success()`:**
    *   **Input:** None.
    *   **Output:** Returns the JSON array from `scan_wifi_networks_to_json` if successful. Retries on failure.
    *   **Error Codes:** `1` for general failure, `2` for "Resource busy".

## 4. Future Extensibility Considerations

*   **Customizable Scoring:** Allow administrators to configure the weighting of signal vs. vendor elements in the scoring algorithm.
*   **Support for More Vendor Elements:** Extend `get_vendor_elements.sh` to parse and integrate other vendor-specific information beyond `kb_allocation` and `contribution`.
*   **Performance Optimization:** For devices with very limited resources, consider alternative parsing methods or more efficient `awk` scripts to reduce CPU overhead.
*   **Asynchronous Scanning:** Explore non-blocking scan approaches if the script is to be integrated into a larger, more responsive system.
*   **Direct Go Integration:** For advanced scenarios, a custom Go program could replace this script, offering better performance, type safety, and more robust error handling for parsing `iw` output and vendor elements.