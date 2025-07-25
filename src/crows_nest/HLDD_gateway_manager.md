# High-Level Design Document (HLDD): `crows_nest` Go Module - Gateway Manager

## 1. Introduction

This document outlines the high-level design for the new `crows_nest` Go module. This module will replace the existing shell scripts (`scan_wifi_networks.sh`, `sort_wifi_networks.sh`, `select_gateway.sh`) responsible for scanning available Wi-Fi gateways, determining the most suitable connection, and managing the connection process within an OpenWRT environment. The module will operate continuously in the background as part of the `tollgate-module-basic-go` application, exposing functions for external business logic to query available networks and initiate connections.

## 2. System Overview

The `crows_nest` Go module is a core component integrated directly into the `main` application of `tollgate-module-basic-go`. It will run as a long-lived background process, periodically performing Wi-Fi scans and updating its internal state with a list of available gateways. It will not handle user interaction directly, but rather provide a programmatic interface (APIs) for other parts of the system to interact with it. Network configuration will be managed by executing standard `uci` commands. Bitcoin and Nostr specific vendor elements will be processed for gateway scoring and can be set for the local access point.

## 3. Component Interaction and Data Flow

The `gateway_manager` module will primarily consist of a `GatewayManager` orchestrator and several sub-components.

*   **`GatewayManager`:** This struct will serve as the main entry point and orchestrator for the module. It will manage the periodic scanning routine, maintain the list of available gateways, and coordinate connection requests.
*   **`Scanner`:** Responsible for executing `iw scan` commands and parsing their raw output into structured Go data types (e.g., a list of `NetworkInfo` objects). It will handle filtering and basic error detection related to `iw` commands.
*   **`Connector`:** Handles all interactions with the OpenWRT Unified Configuration Interface (UCI). It will execute `uci` commands via `os/exec` to configure wireless interfaces, network bridges, and firewall rules for connecting to a selected gateway. It will also manage network restarts and internet connectivity checks.
*   **`VendorElementProcessor`:** This component will be responsible for:
    *   Extracting and parsing Bitcoin/Nostr specific vendor elements from scanned Wi-Fi network information. This involves understanding 802.11u standards and specific OUI/IE (Organizationally Unique Identifier / Information Element) for Bitcoin/Nostr.
    *   Converting data from vendor elements into a score (e.g., decibel conversion).
    *   Providing functionality to set specific vendor elements on the local OpenWRT device's access point, likely via direct `uci` configurations.
*   **Data Models:** Go structs will be defined to represent entities such as `Gateway` (containing network details, signal strength, score, and vendor-specific data), `NetworkInfo`, and configuration parameters.

### 3.1. Data Flow Diagram

```mermaid
graph TD
    A[main.go - TollGate Application] --> B(Initialize GatewayManager);
    B --> C{GatewayManager Goroutine: Periodic Scan};

    C --> D(Scanner: Execute iw scan);
    D --> E{Scanner: Parse iw scan output};
    E --> F{VendorElementProcessor: Extract & Score Vendor Elements};
    F --> G(GatewayManager: Update Available Gateways List);

    H[External Business Logic] --> I(GatewayManager: GetAvailableGateways());
    I --> J{Formatted List of Gateways};

    H --> K(GatewayManager: ConnectToGateway(mac, password));
    K --> L(Connector: Execute UCI commands);
    L --> M(Connector: Network Restart & Connectivity Check);
    M --> N(Connection Status Update);

    O[External Business Logic] --> P(GatewayManager: SetLocalAPVendorElements(elements));
    P --> Q(VendorElementProcessor: Configure Local AP Vendor Elements);

    R[External Business Logic] --> S(GatewayManager: GetLocalAPVendorElements());
    S --> T(VendorElementProcessor: Read Local AP Vendor Elements);
    T --> U{Local AP Vendor Elements};
```

## 4. API Definitions / Interface Contracts (Go Module)

The `crows_nest` module will expose the following public API for interaction:

*   `func Init(ctx context.Context) (*GatewayManager, error)`: Initializes the `GatewayManager` and starts its internal background scanning routine. The context allows for graceful shutdown.
*   `func (gm *GatewayManager) GetAvailableGateways() ([]Gateway, error)`: Returns a snapshot of the currently available and scored Wi-Fi gateways. The `Gateway` struct will contain details like SSID, BSSID, signal strength, encryption type, and calculated score (including vendor element contributions).
*   `func (gm *GatewayManager) ConnectToGateway(bssid string, password string) error`: Instructs the `GatewayManager` to connect to the specified gateway. The connection process will be handled asynchronously internally, with status updates potentially exposed via dedicated channels or status getters.
*   `func (gm *GatewayManager) SetLocalAPVendorElements(elements map[string]string) error`: Sets specific Bitcoin/Nostr related vendor elements in the beacon frames of the local OpenWRT device's Access Point. The exact mechanism for setting these elements (e.g., direct UCI config, custom binaries) will be detailed in the LLDD.
*   `func (gm *GatewayManager) GetLocalAPVendorElements() (map[string]string, error)`: Retrieves the currently configured vendor elements for the local OpenWRT device's Access Point.

## 5. Future Extensibility Considerations

*   **Advanced Scoring Algorithms:** Implement more complex scoring beyond simple signal strength and vendor elements, possibly incorporating latency, throughput, or historical performance.
*   **Persistent Configuration:** Store preferred gateway settings or connection history using the `config_manager` package.
*   **Dynamic Vendor Element Discovery:** Extend `VendorElementProcessor` to dynamically identify and parse new or evolving vendor element structures without requiring hardcoded parsing rules.
*   **LuCI/Web UI Integration:** Potentially develop a LuCI application or a separate web server to provide a graphical user interface for gateway management, building upon the exposed Go APIs.
*   **Connection State Machine:** Implement a robust state machine for managing network connections (connecting, connected, disconnected, failed) and recovery strategies.