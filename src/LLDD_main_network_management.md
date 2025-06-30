# Low-Level Design Document (LLDD): TollGate Core Network Connectivity Management

## 1. Introduction

This document details the low-level implementation for managing network connectivity within the TollGate main application (`src/main.go`). It specifies the concrete steps, code constructs, error handling, and timing considerations for ensuring the device remains connected to the internet, leveraging the `crows_nest.GatewayManager` module.

## 2. Implementation Details

The network connectivity management will primarily reside within `src/main.go`, involving the initialization of `GatewayManager` and a dedicated goroutine for the monitoring and reconnection loop.

### 2.1. `init` Function Modifications

The `init` function will be responsible for initializing the `crows_nest.GatewayManager`.

*   **`src/main.go:init()`:**
    ```go
    import (
        // ...
        "github.com/OpenTollGate/tollgate-module-basic-go/src/crows_nest" // Re-import if not already present
        // ...
    )

    // Global variable declaration
    var gatewayManager *crows_nest.GatewayManager // Already present in main.go diff

    func init() {
        var err error
        // ... existing init code ...

        // Initialize GatewayManager
        gatewayManager, err = crows_nest.Init(context.Background(), log.Default())
        if err != nil {
            log.Fatalf("Failed to initialize GatewayManager: %v", err)
        }
        log.Println("GatewayManager initialized.")
    }
    ```
    *   **Rationale:** Ensures that the `GatewayManager` is ready for use as soon as the application starts, before `main()` is executed. The `context.Background()` is used initially, but it could be replaced with a context tailored for application lifecycle management later if needed for more complex shutdowns. `log.Default()` provides a standard logger.

### 2.2. Connectivity Monitoring Goroutine

A new goroutine will be started in the `main` function to handle periodic internet checks and gateway management.

*   **`src/main.go:main()`:**
    ```go
    import (
        // ...
        "os/exec" // For ping
        "time"    // For ticker
        "strings" // For SSID check
        // ...
    )

    func main() {
        // ... existing main function code ...

        // Start network connectivity monitoring goroutine
        go func() {
            ticker := time.NewTicker(30 * time.Second) // Check every 30 seconds
            defer ticker.Stop()

            for range ticker.C {
                if !isOnline() {
                    log.Println("Device is offline. Initiating gateway scan...")
                    gateways, err := gatewayManager.GetAvailableGateways()
                    if err != nil {
                        log.Printf("ERROR: Failed to retrieve available gateways: %v", err)
                        continue
                    }

                    foundTollGate := false
                    for _, gateway := range gateways {
                        if strings.Contains(gateway.SSID, "TollGate") {
                            log.Printf("Attempting to connect to TollGate gateway: %s (BSSID: %s)", gateway.SSID, gateway.BSSID)
                            // Assuming password is not required for TollGate gateways or retrieved differently
                            // For now, an empty string is passed. This should be refined based on actual TollGate connection requirements.
                            err := gatewayManager.ConnectToGateway(gateway.BSSID, "")
                            if err != nil {
                                log.Printf("ERROR: Failed to connect to gateway %s (%s): %v", gateway.SSID, gateway.BSSID, err)
                            } else {
                                log.Printf("SUCCESS: Connected to TollGate gateway: %s", gateway.SSID)
                                foundTollGate = true
                                break // Connected to one, no need to try others
                            }
                        }
                    }
                    if !foundTollGate {
                        log.Println("No suitable TollGate gateways found or could connect to. Retrying in next interval.")
                    }
                } else {
                    log.Println("Device is online.")
                }
                // Optional: Short sleep to prevent busy-looping if ticker is very short and ping returns immediately
                // time.Sleep(5 * time.Second)
            }
        }()

        // ... rest of main function ...
    }
    ```
    *   **Rationale:** A goroutine ensures non-blocking execution of the monitoring process. A `time.Ticker` provides a simple way to schedule periodic checks. The `isOnline()` function abstracts the connectivity check.
    *   **Offline Logic:** If `isOnline()` returns `false`, the `GatewayManager` is used to get available networks. It iterates through them, looking for SSIDs containing "TollGate" and attempts connection.
    *   **Password:** Currently, `ConnectToGateway` is called with an empty string for the password. This assumes "TollGate" networks are open or handle authentication differently. This is a point of refinement.

### 2.3. Helper Function: `isOnline()`

A simple function to perform a network connectivity check.

*   **`src/main.go:isOnline()`:**
    ```go
    func isOnline() bool {
        // Ping Google's DNS or another reliable public server
        cmd := exec.Command("ping", "-c", "1", "8.8.8.8")
        err := cmd.Run()
        if err != nil {
            log.Printf("Connectivity check failed: %v", err)
            return false
        }
        return true
    }
    ```
    *   **Rationale:** Uses a simple `ping` command as a quick check. This is typical in OpenWRT environments. More sophisticated checks (e.g., HTTP requests to known services) could be added if `ping` alone is deemed insufficient.
    *   **Error Handling for `ping`:** The `cmd.Run()` method returns an error if the command fails (e.g., host unreachable, no network access).

## 3. Error Handling and Logging

*   **Logging:** All new log messages will use `log.Printf` and include contextual information (e.g., "Device is offline", "Attempting to connect"). It's crucial for debugging on OpenWRT.
*   **Dependency Management:** Ensure `os/exec`, `time`, and `strings` imports are present in `src/main.go`.

## 4. Performance Considerations

*   **Scan Interval:** The 30-second interval for `time.NewTicker` is a starting point. It should be tuned based on observed performance and desired responsiveness. Too frequent checks might consume excessive resources on low-power OpenWRT devices.
*   **`ping` overhead:** A single `ping -c 1` is lightweight.
*   **`strings.Contains`:** Efficient for SSID substring matching.
*   **Connection blocking:** The `ConnectToGateway` call is assumed to be blocking. If it's long-running, it might need to run in its own goroutine to avoid blocking the main connectivity loop, though for a single connection attempt, it's often acceptable.

## 5. Open Questions / Assumptions

*   **TollGate Network Password:** The current implementation assumes "TollGate" networks either don't require a password or the password is pre-configured/retrieved by a different mechanism (e.g., from `config_manager`). This needs explicit clarification and a plan if passwords are required.
*   **Prioritization of TollGate SSIDs:** The current logic connects to the *first* "TollGate" SSID found in the list returned by `GetAvailableGateways()`. If multiple exist, a scoring mechanism (which `GatewayManager` already provides) should be used to select the *best* one. This needs to be integrated from the `crows_nest.Gateway` score.