# Reseller Mode Automatic Reconnection: Debugging Summary

## 1. Goal

The primary objective is to ensure a "customer" router running `tollgate-wrt` in reseller mode can automatically recover its internet connection and resume micropayments after its upstream "gateway" router is power-cycled. Currently, the customer router reconnects to the gateway's Wi-Fi but fails to re-establish a payment session, requiring a manual restart of the `tollgate-wrt` service to restore connectivity.

## 2. Debugging Journey & Current Status

Our investigation has gone through several phases, leading to our current understanding of the problem.

### Phase 1: Initial Analysis (Incorrect)

*   **Observation:** The client router would enter a loop of failed connectivity checks and Wi-Fi scans.
*   **Initial Hypothesis:** The `crowsnest` module's `discoveryTracker` was caching a successful discovery result and refusing to re-probe the gateway after it came back online. This was seemingly confirmed by logs showing the state-clearing function (`handleInterfaceDown`) was not being called.
*   **Action Taken:** A patch was applied to `crowsnest/network_monitor.go` to ensure `EventInterfaceDown` events were never throttled, with the expectation that this would guarantee the state was cleared.
*   **Result:** **The fix was ineffective.** The logs from the subsequent test run showed the failure occurred *before* `crowsnest` was even involved in the reconnection logic.

### Phase 2: Deeper Log Analysis (Race Condition Identified)

*   **Observation:** The new logs revealed the true point of failure. The `wireless_gateway_manager` would successfully reconnect to the gateway, but the payment session would never be established.
*   **Hypothesis:** A race condition existed between the `wireless_gateway_manager`'s reconnection logic and the `NetworkMonitor`'s periodic connectivity check. The monitor would run its check before the payment session was established, causing the check to fail and triggering a new, disruptive scan.

### Phase 3: The Solution - A Grace Period

*   **Observation:** The race condition was confirmed. The `NetworkMonitor` was too aggressive, not allowing enough time for the full reconnection and payment pipeline to complete.
*   **Solution:** A "grace period" was implemented.
    1.  A `lastConnectionAttempt` timestamp was added to the `GatewayManager`.
    2.  This timestamp is updated whenever a new connection attempt is initiated.
    3.  The `NetworkMonitor` now checks this timestamp. If a connection attempt was made within the last 60 seconds, it skips its connectivity check, giving the system time to complete the payment process without interruption.

## 3. Current Challenges

The core challenge was the race condition between the `NetworkMonitor` and the `wireless_gateway_manager`. This has now been addressed.

## 4. Proposed Solution

The implemented solution is the "grace period" described above. This should prevent the `NetworkMonitor` from interfering with the reconnection and payment process.

## 5. Unresolved Questions

*   **Is the 60-second grace period sufficient?** This is the main question. We believe it is, but it will need to be validated through testing.
*   **Are there any other, more subtle race conditions?** The current fix is targeted at the most obvious race condition. Further testing will reveal if any other timing issues exist.