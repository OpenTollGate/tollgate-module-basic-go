# Low-Level Design Document (LLDD): `crows_nest` Go Module - Gateway Manager

## 1. Introduction

This document provides a detailed low-level design for the `crows_nest` Go module, outlining the internal mechanisms, data structures, error handling, and performance considerations for implementing Wi-Fi gateway scanning, selection, and connection management in an OpenWRT environment.

## 2. Component-Specific Implementation Details

### 2.1. `GatewayManager`

The `GatewayManager` will be a central struct orchestrating the module's operations.

*   **Structure:**
    ```go
    type GatewayManager struct {
        scanner         *Scanner
        connector       *Connector
        vendorProcessor *VendorElementProcessor
        mu              sync.RWMutex // Protects availableGateways and currentHopCount
        availableGateways map[string]Gateway // Key: BSSID
        currentHopCount int
        scanInterval    time.Duration
        stopChan        chan struct{} // For graceful shutdown of scan goroutine
        log             *log.Logger   // Module-specific logger
    }
    ```
*   **Lifecycle:**
    *   `Init(ctx context.Context, logger *log.Logger) (*GatewayManager, error)`:
        *   Initializes `Scanner`, `Connector`, `VendorElementProcessor`.
        *   Sets `currentHopCount` to `math.MaxInt32` as the initial state (no connection).
        *   Sets up the `log` instance.
        *   Starts a background goroutine for periodic scanning using `time.NewTicker(gm.scanInterval)`. The `ctx` will be used to signal shutdown.
    *   `RunPeriodicScan(ctx context.Context)` (internal goroutine function):
        *   Loops, calling `scanner.ScanNetworks()` at `scanInterval`.
        *   Processes results, updates `availableGateways`, and triggers `vendorProcessor` for scoring.
        *   Filters the `availableGateways` list, removing any gateways with a hop count greater than or equal to the device's `currentHopCount`.
        *   Handles context cancellation for graceful shutdown.
*   **State Management:** `availableGateways` map will store `Gateway` objects. `currentHopCount` will store the device's hop count. Both are synchronized with an `sync.RWMutex` for concurrent access.
*   **Public Methods (as defined in HLDD):**
    *   `GetAvailableGateways() ([]Gateway, error)`: Reads from `availableGateways` under a read lock.
    *   `ConnectToGateway(bssid string, password string) error`: Calls `connector.Connect(...)`. On success, it calls `UpdateHopCountAndAPSSID()`.
    *   `UpdateHopCountAndAPSSID()` (internal method):
        *   Determines the new hop count. If connected to a non-TollGate (e.g., password-protected) network, hop count is 0. If connected to a TollGate, it's the gateway's hop count + 1.
        *   Updates `gm.currentHopCount`.
        *   Calls `connector.UpdateLocalAPSSID()` to advertise the new hop count.
    *   `SetLocalAPVendorElements(elements map[string]string) error`: Calls `vendorProcessor.SetLocalAPElements()`.
    *   `GetLocalAPVendorElements() (map[string]string, error)`: Calls `vendorProcessor.GetLocalAPElements()`.

### 2.2. `Scanner`

Handles Wi-Fi network scanning.

*   **Structure:**
    ```go
    type Scanner struct {
        log *log.Logger
    }
    ```
*   **Methods:**
    *   `ScanNetworks() ([]NetworkInfo, error)`:
        *   Executes `iw dev <interface> scan` using `os/exec.Command`. Determine the interface dynamically (e.g., `iw dev | awk '/Interface/ {print $2; exit}'`).
        *   Captures `stdout` and `stderr`.
        *   Parses `stdout` line by line using `bufio.Scanner`. Each BSS block will be processed to extract:
            *   BSSID (MAC address)
            *   SSID (`SSID:` field)
            *   Hop Count: If the SSID matches the format `TollGate-[ID]-[Frequency]-[HopCount]`, parse the hop count. Otherwise, default to 0 for non-TollGate SSIDs.
            *   Signal (`signal:` field, convert to `int` dBm)
            *   Encryption (`RSN:`, `WPA:`, `WPS:`, or infer "Open")
            *   **Vendor Elements:** Attempt to extract raw Information Elements if `iw scan` provides them in a parseable format (e.g., `iw -h -s scan` might be helpful). If not readily available, the `Scanner` will return `NetworkInfo` without parsed vendor elements, and `VendorElementProcessor` will need to use a fallback mechanism (see 2.4).
        *   Returns `[]NetworkInfo` on success, `error` on failure (e.g., `iw` command error, parsing error).
    *   `NetworkInfo` struct:
        ```go
        type NetworkInfo struct {
            BSSID      string
            SSID       string
            Signal     int // dBm
            Encryption string
            HopCount   int
            RawIEs     []byte // Raw Information Elements, if extractable from iw output
        }
        ```
*   **Error Handling:** Check `cmd.Run()` errors, handle `io.EOF`, `io.ErrUnexpectedEOF` during parsing.

### 2.3. `Connector`

Manages OpenWRT network configurations via `uci` commands.

*   **Structure:**
    ```go
    type Connector struct {
        log *log.Logger
    }
    ```
*   **Methods:**
    *   `Connect(gateway Gateway) error`:
        *   Receives a `Gateway` struct (containing BSSID, SSID, encryption, HopCount etc.).
        *   Executes a series of `uci` commands via `os/exec.Command` to:
            *   Configure `network.wwan` (STA interface) with DHCP.
            *   Disable existing `wlan0` AP, configure `wireless.wifinetX` for STA mode on `radio0` (or appropriate radio).
            *   Set SSID, BSSID, encryption (`none`, `psk2`, `sae`), and `key` for the STA interface.
            *   Commit changes: `uci commit network`, `uci commit wireless`, `uci commit firewall`.
        *   Restarts network: `os/exec.Command("/etc/init.d/network", "restart")`.
        *   Performs internet connectivity check: `ping -c 1 8.8.8.8` in a loop with timeout.
        *   If TollGate network, updates `/etc/hosts` for `status.client` (read current `/etc/hosts`, `sed` functionality in Go, write back).
        *   Re-enables local AP after successful connection/internet check.
    *   `UpdateLocalAPSSID(hopCount int) error`:
        *   Retrieves the base SSID (e.g., `TollGate-ABCD-2.4GHz`) from UCI config.
        *   Constructs the new SSID: `[BaseSSID]-[hopCount]`.
        *   Executes `uci set wireless.default_radio0.ssid='<NEW_SSID>'`.
        *   Commits and restarts wireless.
    *   `ExecuteUCI(args ...string) (string, error)` (Helper): Generic function to run `uci` commands.
    *   `Ping(ip string) error`: Simple ping utility.
    *   `UpdateHostsEntry(hostname, ip string) error`: Adds or updates an entry in `/etc/hosts`.

### 2.4. `VendorElementProcessor`

Handles Bitcoin/Nostr related vendor elements.

*   **Structure:**
    ```go
    type VendorElementProcessor struct {
        log *log.Logger
    }
    ```
*   **Methods:**
    *   `ExtractAndScore(ni NetworkInfo) (map[string]interface{}, int, error)`:
        *   **Vendor Element Extraction & Parsing (Crucial - Assumption for now):**
            *   **Initial Approach (ideal if `iw scan` output allows):** Parse `ni.RawIEs` directly. This requires understanding 802.11 Information Element (IE) structure, particularly Vendor Specific IEs (IE Number 221). The OUI (Organizationally Unique Identifier) for Bitcoin/Nostr related elements needs to be identified. Within the vendor-specific IE, proprietary data fields (e.g., `kb_allocation_decimal`, `contribution_decimal`) will be extracted.
            *   **Fallback/Alternative Approach (if raw IE not easily accessible from `iw scan`):** The Go module might need to execute a lightweight, optimized external tool (e.g., a slim Go binary compiled for OpenWRT, or a highly optimized shell script like `get_vendor_elements.sh` if it's already well-performing) that can extract raw IEs from beacon frames. The output of this tool would then be parsed in Go. This is a key assumption requiring detailed investigation during coding.
        *   **Decibel Conversion:** Implement a Go function for decibel conversion: `decibel(value float64) float64`. Based on typical signal conversions, it might be `20 * log10(value)` or `10 * log10(value)`. This needs to align with `decibel.sh`'s exact calculation.
        *   Calculates the score based on extracted vendor elements and signal strength as per `HLDD_TollGate_Gateway_Selection.md` (`score = signal + kb_allocation_dB + contribution_dB`).
        *   Returns parsed elements, calculated score, and error.
    *   `SetLocalAPElements(elements map[string]string) error`:
        *   Converts the `elements` map into a byte slice representing the vendor-specific IE.
        *   Encodes this byte slice into a hex string.
        *   Executes `uci set wireless.default_radio0.ie='<HEX_STRING>'` (or `default_radio1` etc.) via `connector.ExecuteUCI()`. Commits and restarts wireless if necessary.
        *   This requires mapping `elements` (e.g., "kb_allocation" -> 0xXX, "contribution" -> 0xYY) to the byte structure expected in the vendor IE.
    *   `GetLocalAPElements() (map[string]string, error)`:
        *   Retrieves the current `ie` value from `uci wireless.default_radio0.ie`.
        *   Decodes the hex string back to a byte slice.
        *   Parses the byte slice to reconstruct the `map[string]string`.

## 3. Data Structures

*   `Gateway` struct:
    ```go
    type Gateway struct {
        BSSID          string `json:"bssid"` // MAC address of the AP
        SSID           string `json:"ssid"`
        Signal         int    `json:"signal"`    // Signal strength in dBm
        Encryption     string `json:"encryption"`// e.g., "none", "psk2", "sae"
        HopCount       int    `json:"hop_count"` // Hop count parsed from SSID
        Score          int    `json:"score"`     // Calculated score for prioritization
        VendorElements map[string]string `json:"vendor_elements"` // Map of parsed vendor-specific data
    }
    ```
*   `NetworkInfo` (as defined in `Scanner` section).

## 4. Error Handling and Logging

*   **Error Handling:** All functions will return `error` types explicitly. Use `fmt.Errorf("component: action failed: %w", err)` for error wrapping to preserve context.
*   **Logging:** The Go standard `log` package will be used. The `GatewayManager` will hold a `*log.Logger` instance. This logger should be configured during `Init` to output to `os.Stderr`, which OpenWRT typically redirects to `syslog` for `logread` visibility.
    *   Log levels will be implicitly handled by using different `log.Printf` statements:
        *   `log.Printf("[crows_nest] INFO: ...")` for general operation.
        *   `log.Printf("[crows_nest] WARN: ...")` for non-critical issues.
        *   `log.Printf("[crows_nest] ERROR: ...")` for critical failures.
    *   All log messages will be prefixed with `[crows_nest]` to facilitate `logread | grep "tollgate"` as requested.

## 5. Performance Considerations within OpenWRT

*   **`os/exec` Overhead:** Minimize calls to `os/exec.Command`. Batch `uci` commands where possible (e.g., `uci set X=Y; uci set A=B; uci commit`).
*   **Parsing Efficiency:** Optimize string parsing from `iw scan` output. Avoid complex regex if simpler `strings.Contains` or `strings.Cut` suffice.
*   **Concurrency:** Use goroutines for background tasks (e.g., periodic scanning) but ensure `sync.Mutex` or `sync.RWMutex` adequately protect shared data (`availableGateways`). Ensure graceful termination of goroutines.
*   **Memory Footprint:** Design data structures to be lightweight. Be mindful of large scan results.

## 6. Testing and Verification

*   **Unit Tests:** Develop comprehensive unit tests for `Scanner`, `Connector` (mocking `os/exec.Command` responses), and `VendorElementProcessor` (mocking raw IE data).
*   **Integration Tests:** Requires an OpenWRT test environment (e.g., VM, actual device) to verify end-to-end functionality, including `uci` interactions and network connectivity.
*   **Manual Testing:** Verify log output using `logread`.

## 7. Known Limitations/Assumptions

*   **Vendor Element Parsing (Critical):** The precise structure and ease of extraction of Bitcoin/Nostr specific vendor elements from `iw scan` output remains an assumption. If `iw scan` does not provide raw IEs easily parsable in Go, a separate, minimal Go binary (or shell script proxy) specifically for raw IE extraction will be required. This would be called by the `VendorElementProcessor`.
*   **`iw` and `uci` Availability:** Assumes `iw` and `uci` utilities are present and functional on the target OpenWRT device.
*   **Interface Naming:** Assumes a standard Wi-Fi interface naming convention (e.g., `wlan0`, `radio0`). Robustness might require dynamically determining interface names.
*   **UCI Configuration Specifics:** Detailed `uci` configurations for various encryption types and network setups will be confirmed during implementation.