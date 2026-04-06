# Design Document: Access Point Shutdown Behavior

## 1. Overview

This document explains the "eventually consistent" shutdown behavior of the 2.4GHz and 5GHz local access points (APs) when the upstream internet connection is lost. It details the technical reasons for the discrepancy and proposes solutions to achieve more synchronous behavior.

## 2. Observed Behavior

When the upstream gateway (e.g., `homespot-DB84-2.4GHz`) is disconnected, the following occurs:

1.  The **2.4GHz `TollGate-` AP disappears almost instantly**.
2.  The **5GHz `TollGate-` AP remains active** for a short period (approximately 25-30 seconds).
3.  After this delay, the **5GHz AP is disabled** by the `tollgate-basic` service.

This creates a temporary state where only one of the two local APs is broadcasting, which could be confusing for users.

## 3. Technical Explanation

The inconsistent shutdown timing is caused by two different mechanisms operating at different layers of the network stack.

### 3.1. Immediate 2.4GHz AP Shutdown (Layer 2 - Driver Behavior)

The 2.4GHz radio (`radio0`) is operating in a "sta+ap" mode. It performs two functions simultaneously:
*   **Client (STA) Mode:** Connects to the upstream `homespot` network.
*   **Access Point (AP) Mode:** Broadcasts the local `TollGate-...-2.4GHz` SSID.

These two functions are intrinsically linked at the driver and hardware level. When the upstream `homespot` gateway is unplugged, the client (STA) interface loses its wireless link. The Wi-Fi driver immediately determines that it can no longer maintain the associated AP on the same radio and tears it down. This is a low-level, automatic action and is not controlled by our application.

### 3.2. Delayed 5GHz AP Shutdown (Layer 3 - Application Behavior)

The 5GHz radio (`radio1`) is operating only in AP mode, broadcasting the `TollGate-...-5GHz` SSID. It has no dependency on the client connection.

Its shutdown is triggered by the `NetworkMonitor` component in the `wireless_gateway_manager` module, which operates at the application layer. This monitor periodically pings an external server (e.g., `8.8.8.8`) to check for internet connectivity. When the upstream gateway goes down, these pings begin to fail.

The monitor is configured to wait for **5 consecutive failures** before declaring the connection lost. With a ping interval, this process takes time. Once the threshold is met, the `NetworkMonitor` calls the `DisableLocalAP()` function, which then shuts down both the 2.4GHz and 5GHz APs via UCI commands. By this time, the 2.4GHz AP has already been disabled by the driver.

## 4. Options to Synchronize AP Shutdown Behavior

To avoid this inconsistent state, we can implement solutions to make the shutdown of both APs nearly simultaneous.

### Option 1: Link-Layer Trigger (Recommended Software Solution)

We can enhance the `NetworkMonitor` to react to link-layer events in addition to application-layer ping tests.

*   **Implementation:** The `NetworkMonitor` can subscribe to netlink events to monitor the state of the client (STA) interface (`phy0-wifinet0`). When it detects that the STA interface has lost its association with the upstream AP, it can immediately trigger the `DisableLocalAP()` function.
*   **Pros:**
    *   This is the most robust software solution.
    *   It synchronizes the shutdown of both APs, making them disappear at almost the same time.
    *   It's much faster than waiting for multiple ping timeouts.
*   **Cons:**
    *   Requires more complex logic to handle netlink events correctly.

### Option 2: Faster Ping Checks (Configuration Tweak)

We can make the existing ping-based detection more aggressive.

*   **Implementation:** Reduce the ping interval and/or the number of consecutive failures required in the `NetworkMonitor` configuration.
*   **Pros:**
    *   Simple to implement; only requires changing configuration values.
*   **Cons:**
    *   May lead to "flapping" (the APs turning on and off frequently) on an unstable or high-latency internet connection.
    *   Will still have a delay, even if it's shorter.

### Option 3: Dedicated Radios (Hardware Solution)

Using hardware with three separate radios would completely decouple the client and AP functions.

*   **Implementation:**
    *   Radio 0: Client (STA) connection to upstream AP.
    *   Radio 1: Local 2.4GHz AP.
    *   Radio 2: Local 5GHz AP.
*   **Pros:**
    *   Eliminates the shared radio issue entirely.
    *   Potentially better performance as radios are dedicated to a single task.
*   **Cons:**
    *   Dependent on specific, less common hardware.
    *   Not a feasible software-only solution for existing devices.