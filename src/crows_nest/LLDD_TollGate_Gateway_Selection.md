# Low-Level Design Document (LLDD): TollGate Wireless Gateway Selection and Connection

## 1. Introduction

This document provides a detailed low-level design for the implementation of the TollGate Wireless Gateway Selection and Connection feature within an OpenWRT environment. It elaborates on the individual components, their internal logic, data structures, error handling, and performance considerations.

## 2. Component-Specific Implementation Details

### 2.1. [`scan_wifi_networks.sh`](files/root/scan_wifi_networks.sh)

**Purpose:** Scans for Wi-Fi networks and enriches "TollGate" SSIDs with calculated scores based on vendor elements.

**Key Functions:**
*   `get_wifi_interface()`: Identifies the active managed Wi-Fi interface (e.g., `wlan0`).
*   `scan_wifi_networks_to_json()`:
    *   Executes `iw dev "$interface" scan` to get raw scan data.
    *   Uses `awk` to parse the raw output into a JSON array of network objects.
    *   **TollGate Specific Logic:**
        *   When `ssid` regex matches `/^TollGate_/`, it executes an external command:
            ```bash
            /root/get_vendor_elements.sh "$ssid" 12 | jq -r ".kb_allocation_decimal, .contribution_decimal"
            ```
            This retrieves `kb_allocation_decimal` and `contribution_decimal` from the 802.11u vendor elements (specifically, Information Element 12).
        *   It then calls [`/root/decibel.sh`](files/root/decibel.sh) for each of these decimal values to convert them to decibels:
            ```bash
            /root/decibel.sh $kb_allocation_decimal
            /root/decibel.sh $contribution_decimal
            ```
        *   The network `score` is calculated as `signal + kb_allocation_db + contribution_db`. This creates a weighted score favoring TollGate networks with strong signals and relevant Bitcoin/Nostr related data.
    *   For non-TollGate networks, `score` is simply the `signal` strength.
*   `scan_until_success()`: Implements retry logic, attempting `iw scan` up to 15 times with 3-second delays if the interface is busy or JSON parsing fails.

**Data Structure (JSON Output Example):**
```json
[
  {
    "mac": "00:11:22:33:44:55",
    "ssid": "MyHomeNetwork",
    "encryption": "wpa2",
    "signal": -70,
    "score": -70
  },
  {
    "mac": "AA:BB:CC:DD:EE:FF",
    "ssid": "TollGate_example",
    "encryption": "none",
    "signal": -50,
    "kb_allocation_dB": "10",
    "contribution_dB": "5",
    "score": -35
  }
]
```

**Error Handling:**
*   Checks for `jq` presence.
*   Handles "Resource busy" errors during `iw scan` by retrying.
*   Validates JSON output for correctness.

**Performance Considerations:**
*   `iw scan` can be time-consuming; retries add to delay.
*   External calls to `get_vendor_elements.sh` and `decibel.sh` add overhead for each TollGate network. Scalability might be affected by a very large number of TollGate SSIDs.

### 2.2. [`get_vendor_elements.sh`](files/root/get_vendor_elements.sh) (and `parse_beacon.sh`, `decibel.sh`)

**Purpose:** Extract and process specific Bitcoin/Nostr related data from 802.11u beacon frames.

**Assumed Interaction:** [`scan_wifi_networks.sh`](files/root/scan_wifi_networks.sh) calls this script. `parse_beacon.sh` is likely internally used or called by `get_vendor_elements.sh` to get vendor elements from the beacon. [`decibel.sh`](files/root/decibel.sh) performs a mathematical conversion.

**Key Logic (Conceptual):**
*   `get_vendor_elements.sh <SSID> <bytes>`: This script would leverage tools like `iw` or `tcpdump` to capture beacon frames for the given SSID, then parse the 802.11u Information Elements (IEs), specifically looking for vendor-specific IEs (Subtype 12). The `<bytes>` argument suggests it extracts a specific number of bytes from this IE which encode the `kb_allocation_decimal` and `contribution_decimal` values, then outputs them as JSON.
*   `decibel.sh <value>`: Likely performs a simple `log10` or similar calculation to convert a linear scale value to a decibel scale, perhaps `20 * log10(value)`.

**Error Handling:**
*   `get_vendor_elements.sh` likely includes basic argument validation.
*   Potential errors: Malformed beacon frames, missing vendor elements, invalid input to `decibel.sh`.

### 2.3. [`sort_wifi_networks.sh`](files/root/sort_wifi_networks.sh)

**Purpose:** Processes, filters, sorts, and enables user selection of Wi-Fi networks.

**Key Functions:**
*   `sort_and_display_full_json()`:
    *   Calls `./scan_wifi_networks.sh` to get the raw JSON output.
    *   Pipes the output through `jq` for various transformations:
        *   `remove_empty_ssids()`: `map(select(.ssid != ""))`
        *   `sort_networks_by_signal_desc()`: `map(.signal |= tonumber) | sort_by(-.signal)` (This sort is applied before `remove_duplicate_ssids`, ensuring the strongest signal for a given SSID is kept).
        *   `remove_duplicate_ssids()`: Uses `jq`'s `reduce` to create an object where keys are SSIDs, effectively keeping only the first (strongest) entry, then converts back to an array.
*   `filter_tollgate_ssids()`: Uses `jq -r 'map(select(.ssid | startswith("TollGate_")))'` to extract only TollGate networks.
*   `select_ssid()`:
    *   Calls `sort_and_display_full_json` to get the processed list.
    *   Stores the full sorted JSON in `/tmp/networks.json`.
    *   Presents a numbered list of SSIDs to the user.
    *   Prompts the user for selection and validates input.
    *   Saves the selected network's JSON object to `/tmp/selected_ssid.md`.

**Data Structures:**
*   Input/Output: JSON array of network objects (same format as `scan_wifi_networks.sh` output, but processed).
*   Temporary Files: `/tmp/networks.json`, `/tmp/selected_ssid.md` (JSON objects).

**Error Handling:**
*   Checks if `scan_wifi_networks.sh` successfully provides valid JSON.
*   Validates user input for SSID selection.

**Performance Considerations:**
*   Extensive `jq` operations on potentially large JSON arrays. For very large numbers of networks, this could be CPU/memory intensive on resource-constrained OpenWRT devices.

### 2.4. [`select_gateway.sh`](files/root/select_gateway.sh)

**Purpose:** Orchestrates network selection, configures OpenWRT, and manages post-connection actions.

**Key Functions/Logic:**
*   `select_network()`: Calls `./sort_wifi_networks.sh --select-ssid` to get user input.
*   Reads selected network details from `/tmp/selected_ssid.md`.
*   Parses `NEW_SSID` and `ENCRYPTION_TYPE` using `jq`.
*   **UCI Configuration (Standard):**
    *   Sets firewall zone to include `wwan`.
    *   Configures `network.wwan` as a `dhcp` interface.
    *   Ensures `wireless.radio0.disabled='0'`.
    *   Deletes any existing `wireless.wifinet1` and creates a new `wifi-iface` for `radio0` in `sta` mode, linked to `wwan`.
    *   Sets `wireless.wifinet1.ssid`.
*   **TollGate Specific Logic:**
    *   If `echo "$NEW_SSID" | grep -q "^TollGate_"` is true:
        *   `uci set wireless.wifinet1.encryption='none'` is explicitly set. This designs for an unencrypted Wi-Fi connection, implying authentication will occur at a higher layer (e.g., a captive portal).
    *   Else (non-TollGate):
        *   Prompts for `NEW_PASSWORD`.
        *   Uses a `case` statement to set `wireless.wifinet1.encryption` based on `ENCRYPTION_TYPE` (sae, wpa2, none) and sets `wireless.wifinet1.key` if a password is provided.
*   `uci commit firewall`, `uci commit network`, `uci commit wireless`: Persists changes.
*   `/etc/init.d/network restart`: Applies network configuration changes.
*   `check_internet()`: Pings `8.8.8.8` to verify outbound connectivity.
*   **Post-Connection Actions:**
    *   If internet is confirmed:
        *   Runs `/root/./get_moscow_time.sh` (likely for time synchronization needed for Bitcoin/Nostr protocols).
        *   Enables the local Access Point (`uci set wireless.default_radio0.disabled='0'`) and reloads Wi-Fi.
    *   **TollGate Specific `/etc/hosts` Update:** If `NEW_SSID` is a "TollGate" network and a gateway IP is detected:
        *   `sed -i '/status.client/d' /etc/hosts`: Removes any prior `status.client` entry.
        *   `echo "$GATEWAY_IP status.client" >> /etc/hosts`: Maps the connected gateway's IP to `status.client`. This facilitates local resolution of a captive portal's status page or API endpoint, crucial for the captive portal flow.

**Error Handling:**
*   Checks for success of `sort_wifi_networks.sh --select-ssid`.
*   Validates reading and parsing of JSON from `/tmp/selected_ssid.md`.
*   Checks `uci commit` and `network restart` return codes.
*   Monitors for network connectivity (max 30 seconds).

**Performance Considerations:**
*   `uci commit` and `network restart` can cause brief network interruptions.
*   The `check_internet` loop with `sleep 1` introduces a potential delay of up to 30 seconds before AP is enabled or `/etc/hosts` updated.

## 3. Interaction Flow Examples

### 3.1. Connecting to a TollGate Network

1.  User executes `select_gateway.sh`.
2.  `select_gateway.sh` calls `sort_wifi_networks.sh --select-ssid`.
3.  `sort_wifi_networks.sh` calls `scan_wifi_networks.sh`.
4.  `scan_wifi_networks.sh` performs `iw scan`, identifies "TollGate_XYZ", calls `get_vendor_elements.sh` and `decibel.sh` to calculate `score`.
5.  `scan_wifi_networks.sh` returns JSON list to `sort_wifi_networks.sh`.
6.  `sort_wifi_networks.sh` sorts & filters, presents list including "TollGate_XYZ" (potentially highly ranked due to score).
7.  User selects "TollGate_XYZ". `sort_wifi_networks.sh` saves its JSON to `/tmp/selected_ssid.md`.
8.  `select_gateway.sh` reads `/tmp/selected_ssid.md`, extracts `NEW_SSID="TollGate_XYZ"`, `ENCRYPTION_TYPE="none"`.
9.  `select_gateway.sh` configures UCI: `wireless.wifinet1.ssid="TollGate_XYZ"`, `wireless.wifinet1.encryption='none'`. No password prompted.
10. `uci commit` and `/etc/init.d/network restart`.
11. `select_gateway.sh` waits for IP, finds gateway IP, checks internet.
12. If internet OK, calls `get_moscow_time.sh`, enables local AP, and adds `GATEWAY_IP status.client` to `/etc/hosts`.

### 3.2. Connecting to a Regular Encrypted Network

1.  User executes `select_gateway.sh`.
2.  (`scan_wifi_networks.sh` and `sort_wifi_networks.sh` steps similar to above, but scores are just signal strength).
3.  User selects "MyHomeNetwork" with `encryption="wpa2"`. `sort_wifi_networks.sh` saves its JSON to `/tmp/selected_ssid.md`.
4.  `select_gateway.sh` reads `/tmp/selected_ssid.md`, extracts `NEW_SSID="MyHomeNetwork"`, `ENCRYPTION_TYPE="wpa2"`.
5.  `select_gateway.sh` prompts for password.
6.  `select_gateway.sh` configures UCI: `wireless.wifinet1.ssid="MyHomeNetwork"`, `wireless.wifinet1.encryption='psk2'`, `wireless.wifinet1.key="<PASSWORD>"`.
7.  `uci commit` and `/etc/init.d/network restart`.
8.  `select_gateway.sh` waits for IP, finds gateway IP, checks internet.
9.  If internet OK, calls `get_moscow_time.sh`, enables local AP. (No `/etc/hosts` update for non-TollGate).

## 4. Pending Tasks / Future Improvements (from HLDD perspective)

*   **Implement `get_vendor_elements.sh` (or confirm its existence and functionality):** This script's precise implementation is key to the "TollGate" scoring. Further investigation or development might be needed here.
*   **Investigate `parse_beacon.sh`:** Its role and interaction with `get_vendor_elements.sh` should be clarified and documented.
*   **Detailed `decibel.sh` specification:** Verify the exact mathematical formula used.
*   **Automated TollGate Selection:** Add a mode to `select_gateway.sh` or `sort_wifi_networks.sh` to automatically select the highest-scoring TollGate network without user prompt.
*   **Robustness:** Enhance error messages for user clarity. Add checks for specific `uci` command failures.
*   **Security:** Explore options for encrypted TollGate networks, potentially using Nostr key-based authentication post-connection instead of an entirely open network.
*   **Logging:** Implement structured logging for better diagnostics.