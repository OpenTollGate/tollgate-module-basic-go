# Reseller Mode Reconnection Debugging Summary

## 1. Initial Problem

The core issue was the failure of a client router in "reseller mode" to reliably reconnect and resume payments to an upstream gateway router after the gateway was power-cycled. While the client would reconnect to the Wi-Fi, it would fail to re-establish a payment session, leading to a loss of internet connectivity.

## 2. Key Challenges & Architectural Flaws

Our investigation revealed several compounding problems:

*   **Race Conditions:** The most significant issue was a race condition between two different components trying to manage the network state:
    1.  The **`WirelessGatewayManager`** in the main Go daemon had its own internal timer that would proactively scan for new Wi-Fi networks.
    2.  The **OpenWrt hotplug script (`96-tollgate-scan`)** would separately trigger a scan for TollGate advertisements on an already-connected interface.
    These two mechanisms would often interfere with each other, leading to unpredictable behavior.

*   **Redundant Logic:** The `WirelessGatewayManager` contained multiple, overlapping mechanisms for managing its state, including a periodic scan timer, a separate connectivity monitor, and a "grace period" timestamp. This created complex, hard-to-debug interactions.

*   **Silent Captive Portal Failures:** We discovered that even when the client reconnected, the gateway router's captive portal (`nodogsplash`) was not authorizing the client's MAC address, silently blocking internet access. The client code was not equipped to detect or report this critical failure.

## 3. The Solution: A Unified, Single-Trigger Architecture

Through a collaborative, iterative process, we completely refactored the client-side logic to create a clean, robust, and predictable system.

*   **Single Source of Truth:** The `NetworkMonitor` is now the **one and only** component responsible for detecting a persistent loss of connectivity.
*   **Simplified Logic:** We removed all redundant timers and grace periods. The `NetworkMonitor` now uses a simple failure counter. After 4 consecutive failed checks (a 120-second window), it triggers a single, decisive action.
*   **Passive `WirelessGatewayManager`:** The `WirelessGatewayManager` is now a purely passive listener. It no longer runs its own timers. It only acts when it receives a signal from the `NetworkMonitor`, at which point it performs a scan for a new Wi-Fi gateway.
*   **Clear Separation of Concerns:**
    *   **`NetworkMonitor`**: Decides *when* to find a new network.
    *   **`WirelessGatewayManager`**: Decides *how* to find and connect to that new network.
    *   **`96-tollgate-scan` (Hotplug)**: Triggers a Crowsnest scan *after* a successful Wi-Fi connection to find the TollGate service on that network.

This new architecture is simpler, eliminates all race conditions, and correctly separates the responsibilities of each component.

## 4. Current Status & Next Steps

The client-side refactoring is complete. We have successfully fixed the reconnection logic, and the client now reliably re-establishes a connection after the first gateway power cycle.

However, testing revealed a new problem: the connection fails after the *second* power cycle.

**Current Hypothesis:** The gateway router's captive portal (`nodogsplash`) is refusing to grant internet access to our client after the second reconnection.

**Upcoming Steps:**

1.  **Deploy New Build:** Compile and deploy the latest client code, which includes enhanced logging for the captive portal interaction.
2.  **Perform Test:** Power-cycle the gateway router twice to reproduce the failure.
3.  **Gather Data:** After the second power cycle fails, run the following commands on the **client router**:
    *   `logread | grep crowsnest`
    *   `ifconfig`
    *   `route -n`
4.  **Analyze Gateway Response:** Analyze the logs to inspect the full HTTP response (status code, headers, and body) from the gateway's captive portal. This will tell us precisely why it is refusing to authorize the client's MAC address on the second attempt and allow us to formulate a final fix.