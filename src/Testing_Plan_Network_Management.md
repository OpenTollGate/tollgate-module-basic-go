# Testing Plan for TollGate Core Network Connectivity Management

## Objective
To verify that the TollGate main application (`src/main.go`) can autonomously detect loss of internet connectivity, scan for "TollGate" SSIDs, and successfully connect to a suitable "TollGate" gateway on OpenWRT devices.

## Devices
*   **Router A (TollGate Gateway):** This router will be configured to act as a "TollGate" gateway, broadcasting a "TollGate"-prefixed SSID and potentially specific vendor elements.
*   **Router B (TollGate Client):** This router will run the modified `tollgate-module-basic-go` application (`src/main.go`) and attempt to connect to Router A when offline.

## Test Setup

1.  **Preparation of Router A (TollGate Gateway):**
    *   Flash Router A with an OpenWRT image that includes the `tollgate-module-basic-go` package (from the current branch).
    *   Configure Router A to broadcast a Wi-Fi network with a "TollGate-" prefix, for example, `TollGate-ABCD-2.4GHz`. The `99-tollgate-setup` script will handle the dynamic generation of this SSID.
    *   Ensure Router A has internet connectivity through its WAN interface.
    *   Verify that `src/main.go` running on Router A is publishing the necessary TollGate vendor elements (if applicable).

2.  **Preparation of Router B (TollGate Client):**
    *   Flash Router B with an OpenWRT image that includes the `tollgate-module-basic-go` package (from the current branch, specifically with changes to `src/main.go` for network management).
    *   Ensure Router B's WAN interface is *not* connected to the internet initially, or can be easily disconnected to simulate "offline" conditions.
    *   Access Router B's OpenWRT CLI (via SSH or serial).

## Test Cases

### Test Case 1: Initial Offline State and Connection

*   **Preconditions:**
    *   Router A is broadcasting its "TollGate-" prefixed SSID with internet access.
    *   Router B is running the `tollgate-module-basic-go` application.
    *   Router B has no internet connectivity (e.g., WAN cable disconnected).
*   **Steps:**
    1.  On Router B, start the `tollgate-module-basic-go` application.
    2.  Monitor the application logs (`logread -f`) on Router B.
*   **Expected Results:**
    *   Router B's application logs should show:
        *   "Device is offline. Initiating gateway scan..."
        *   Messages indicating it's scanning for and detecting the "TollGate-" prefixed SSID.
        *   "Attempting to connect to TollGate gateway: TollGate-ABCD-2.4GHz"
        *   "SUCCESS: Connected to TollGate gateway: TollGate-ABCD-2.4GHz" (or similar success message).
    *   Router B should establish a Wi-Fi connection to Router A's "TollGate-" prefixed SSID.
    *   Router B should gain internet connectivity (verify by pinging 8.8.8.8 from Router B's CLI).

### Test Case 2: Internet Disruption and Reconnection

*   **Preconditions:**
    *   Router B is successfully connected to Router A's "TollGate-" prefixed SSID and has internet access.
    *   The `tollgate-module-basic-go` application is running on Router B.
*   **Steps:**
    1.  On Router A, temporarily disable the "TollGate-" prefixed SSID (or simulate an internet outage on Router A's WAN).
    2.  Monitor the application logs on Router B (`logread -f`).
    3.  Re-enable the "TollGate-" prefixed SSID on Router A (or restore Router A's internet).
*   **Expected Results:**
    *   Router B's application logs should show:
        *   "Device is offline. Initiating gateway scan..." (after initial `8.8.8.8` ping failures).
        *   Messages indicating loss of connection and re-scanning.
        *   Upon Router A's network becoming available again, Router B should successfully reconnect: "SUCCESS: Connected to TollGate gateway: TollGate-ABCD-2.4GHz".
    *   Router B should regain internet connectivity.

### Test Case 3: No TollGate SSID Available

*   **Preconditions:**
    *   Router B is running the `tollgate-module-basic-go` application.
    *   Router B has no internet connectivity.
    *   Router A (or any "TollGate" gateway) is *not* broadcasting its SSID.
*   **Steps:**
    1.  On Router B, start the `tollgate-module-basic-go` application.
    2.  Monitor the application logs (`logread -f`) on Router B.
*   **Expected Results:**
    *   Router B's application logs should continuously show:
        *   "Device is offline. Initiating gateway scan..."
        *   "No suitable TollGate gateways found or could connect to. Retrying in next interval."
    *   Router B should remain offline.

## Verification Methods

*   **Log Analysis:** Use `logread -f` on Router B to observe the application's behavior and connection attempts.
*   **Network Status:** Use OpenWRT UCI commands or `ifconfig`/`iwinfo` to check Wi-Fi association status and IP address assignments on Router B.
*   **Ping Tests:** From Router B's CLI, `ping 8.8.8.8` to verify internet connectivity.

## Open Questions / Considerations for Testing

*   **TollGate Network Security:** If "TollGate" networks require a password, how will this be handled during the connection attempt? Current design assumes unencrypted or pre-configured. This needs to be addressed for complete testing.
*   **Vendor Element Verification:** How will the scoring based on vendor elements be visually or programmatically verified during testing? This might require deeper analysis of `iw scan` output or `crows_nest` internal logs.
*   **Conflicting Networks:** Testing in an environment with other Wi-Fi networks (both "TollGate" and non-"TollGate") to ensure correct filtering and prioritization.