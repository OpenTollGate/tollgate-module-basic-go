# LLDD: Gateway Reliability

## 1. Introduction

This document provides the low-level implementation details for the gateway reliability tracking feature.

## 2. Component Implementation

### 2.1. Gateway Manager

*   **Blacklist Data Structure:** A new field `gatewaysWithNoInternet map[string]time.Time` will be added to the `GatewayManager` struct. This map will store the BSSID of unreliable gateways and the time they were blacklisted.
*   **Gateway Filtering:** The `selectBestGateway` function will be modified to:
    1.  Iterate through the list of available gateways.
    2.  For each gateway, check if it exists in the `gatewaysWithNoInternet` map.
    3.  If it does, check if the blacklist period has expired.
    4.  If the gateway is not blacklisted, it will be considered for selection.
*   **Blacklisting Logic:** After a failed connectivity check, the gateway's BSSID will be added to the `gatewaysWithNoInternet` map with the current timestamp.

## 3. Commits to Cherry-Pick

The following commits will be cherry-picked from `feature/price-in-ssid` to `feature/gateway-reliability`:

```
0ef6c258d1ccf978046574b3f665554a3122ada0
0ad29ed93a0aa03fbf6836045e4513c76b76e1f1
```

## 4. Testing Plan

### 4.1. Manual Tests

1.  **Setup:**
    *   Set up two TollGate devices, A and B.
    *   Configure device A to have no internet connection.
2.  **Test Case 1: Gateway Blacklisting**
    *   Power on both devices.
    *   Verify that device B connects to device A.
    *   Verify that device B's connectivity check fails.
    *   Verify that device B disconnects from device A and adds it to the blacklist.
    *   Verify that device B does not attempt to reconnect to device A until the blacklist period has expired.