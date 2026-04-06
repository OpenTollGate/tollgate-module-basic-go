# LLDD: Wireless Improvements

## 1. Introduction

This document provides the low-level implementation details for the wireless improvements feature.

## 2. Component Implementation

### 2.1. Automatic STA Interface Creation

*   A new private function `ensureSTAInterfaceExists()` will be added to `connector.go`.
*   This function will:
    1.  Get a list of all wireless interfaces.
    2.  Check if a `tollgate_sta` interface already exists.
    3.  If not, it will determine the available radios (e.g., `radio0`, `radio1`).
    4.  For each radio, it will use `uci` commands to create a new `wifi-iface` section with `device` set to the radio, `mode` set to `sta`, and `network` set to `wan`.

### 2.2. Sync Known Networks from OpenWRT Config

*   A new private function `syncKnownNetworksFromWirelessConfig()` will be added to `wireless_gateway_manager.go`.
*   This function will:
    1.  Execute `uci show wireless` to get the wireless configuration.
    2.  Parse the output to find all `wifi-iface` sections with `mode=sta`.
    3.  For each section, it will extract the `ssid`, `key`, and `encryption`.
    4.  It will then add this network to the `knownNetworks` map in the `GatewayManager`.
    5.  Finally, it will save the updated `knownNetworks` map to `known_networks.json`.

### 2.3. Open Network Support

*   The `Connect` function in `connector.go` will be modified to handle open networks.
*   If the `Encryption` field of the `Gateway` is `Open`, it will:
    1.  Set `wireless.tollgate_sta.encryption` to `none`.
    2.  Delete the `wireless.tollgate_sta.key` option, but only if it exists.

## 3. Commits to Cherry-Pick

The following commits will be cherry-picked from `feature/price-in-ssid` to `feature/wireless-improvements`:

```
3dc7760c6812fa5efe73f3945dc5cbf1d7d50ade
754b627fbf3a5650f4720c1a90de5d1d49ecc6f7
5816f143c04ce9fb8ce5554042c242b6145a4126
ba9eb5f9c123be5d81eaf1e0b35c97c568e9b5dd
b2c936ef8b8b0bd94c8a472be4c54b718a89ec5f
085c3c7d2275f8c4dec8b7397929efedbde1dedd
```

## 4. Testing Plan

### 4.1. Manual Tests

1.  **Test Case 1: Automatic STA Interface Creation**
    *   Delete the `tollgate_sta` interface.
    *   Restart the TollGate service.
    *   Verify that a new `tollgate_sta` interface is created.
2.  **Test Case 2: Sync Known Networks**
    *   Add a new Wi-Fi network using the LuCI web interface.
    *   Restart the TollGate service.
    *   Verify that the new network appears in `known_networks.json`.
3.  **Test Case 3: Open Network Support**
    *   Set up an open Wi-Fi network.
    *   Attempt to connect to it using TollGate.
    *   Verify that the connection is successful.