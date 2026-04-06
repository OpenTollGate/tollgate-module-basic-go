# HLDD: Wireless Improvements

## 1. Overview

This document outlines the high-level design for a series of improvements to the wireless management system in TollGate. The goal is to make the system more robust and user-friendly by improving its ability to handle various Wi-Fi configurations and network conditions.

## 2. Key Features

### 2.1. Automatic STA Interface Creation

*   **Goal:** Ensure that the device always has a station (STA) interface available for connecting to upstream networks.
*   **Mechanism:** If no STA interface is detected, the system will automatically create one on each available radio (2.4GHz and 5GHz).

### 2.2. Sync Known Networks from OpenWRT Config

*   **Goal:** Allow TollGate to use Wi-Fi networks that have been configured through the standard OpenWRT LuCI web interface.
*   **Mechanism:** The system will read the `/etc/config/wireless` file, parse the STA configurations, and add them to TollGate's `known_networks.json` file.

### 2.3. Open Network Support

*   **Goal:** Allow TollGate to connect to upstream networks that do not use WPA2 encryption.
*   **Mechanism:** The `Connector` will be updated to handle open networks by setting the encryption to `none` and removing the `key` option from the UCI configuration.

## 3. System Architecture

These changes primarily affect the `wireless_gateway_manager` component.

*   **Connector:** Will be updated to handle the creation of STA interfaces and connecting to open networks.
*   **Gateway Manager:** Will be updated to trigger the syncing of known networks from the OpenWRT config.

## 4. Testing Strategy

*   **Unit Tests:** Unit tests will be added for the new functionality.
*   **Manual Tests:** A manual testing plan will be developed to test the new features on a real device.