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

### Phase 2: Deeper Log Analysis (Current Diagnosis)

*   **Observation:** The new logs revealed the true point of failure. When the `wireless_gateway_manager` attempts to run a scan after losing internet, it consistently fails with the error:
    ```
    ERRO[2025-12-07T19:51:28Z] Failed to wait for physical interface to become available error="timed out waiting for physical interface for network wwan to become available"
    ```
*   **Current Hypothesis:** This timeout is caused by a **race condition** originating from redundant `wifi reload` commands within the `wireless_gateway_manager/connector.go` file. The sequence is as follows:
    1.  `ScanWirelessNetworks` calls `EnableInterface`, which may issue a `wifi reload`.
    2.  It then immediately calls `PrepareForScan`, which *always* issues a second `wifi reload`.
    3.  These two back-to-back, disruptive commands leave the Wi-Fi subsystem in an unstable state.
    4.  The subsequent `waitForInterface` function begins polling `ubus` for the interface to become ready, but it times out because the interface is still flapping from the double reload.

## 3. Current Challenges

The core challenge is the instability caused by the `wifi reload` race condition. The `PrepareForScan` function is too aggressive, and its `wifi reload` is both unnecessary and harmful. A `uci commit` is sufficient to apply the configuration changes needed to prepare an interface for scanning; a full reload is not required.

## 4. Proposed Solution

The plan is to resolve the race condition and improve our diagnostic capabilities:

1.  **Eliminate the Race Condition:** Remove the `wifi reload` command from the `PrepareForScan` function in `src/wireless_gateway_manager/connector.go`. This is the critical fix.
2.  **Improve Debugging:** Add a new log line to the `waitForInterface` function to print the raw JSON output from the `ubus` status call. This will give us direct insight into what the system is reporting if the timeout issue persists.

## 5. Unresolved Questions

*   **Will removing the `wifi reload` from `PrepareForScan` be sufficient to solve the timeout?** Our current diagnosis strongly suggests it will, but this is pending experimental validation.
*   **Are there other, more subtle race conditions or state management issues within the `wireless_gateway_manager`?** The improved logging will help us answer this if the primary fix is not completely effective.