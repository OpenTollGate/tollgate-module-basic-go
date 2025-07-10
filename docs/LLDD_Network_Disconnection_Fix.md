# Low-Level Design Document (LLDD) - Network Disconnection Fix and AP Configuration

## 1. Context
This document details the simplified implementation plan to resolve the network disconnection issue and stabilize the TollGate SSID. To avoid scope creep, this fix focuses on using the `br-lan` MAC address as a stable identifier and modifying existing Access Points.

The more complex "Identity Management" feature is documented in `docs/LLDD_Identity_Management.md` and is deferred to a future pull request.

## 2. Implementation Details

### 2.1. Helper Script: `generate-tollgate-ssid.sh`
A single, simple helper script will be created to encapsulate the SSID generation logic.

*   **Location:** `files/usr/bin/generate-tollgate-ssid`
*   **Permissions:** `755` (executable)
*   **Logic:**
    1.  Get the MAC address of the `br-lan` interface. This is the most reliable and stable identifier for the router's LAN.
    2.  If `br-lan` or its MAC cannot be found, fall back to `eth0` as a secondary choice.
    3.  If no MAC can be found, log an error and exit gracefully.
    4.  Use the last two bytes (4 hex characters) of the MAC address as the deterministic suffix for the SSID.
    5.  Print the final SSID string `TollGate-XXXX`.

```sh
#!/bin/sh
# /usr/bin/generate-tollgate-ssid
# Generates a deterministic TollGate SSID based on the LAN MAC address.

# First, try to get the MAC from the br-lan interface address file
MAC_ADDR=$(cat /sys/class/net/br-lan/address 2>/dev/null)

# If that fails, try the eth0 interface, which is usually the base for br-lan
if [ -z "$MAC_ADDR" ]; then
    MAC_ADDR=$(cat /sys/class/net/eth0/address 2>/dev/null)
fi

# If we still can't get a MAC, we have a problem.
if [ -z "$MAC_ADDR" ]; then
    echo "TollGate-ERROR"
    exit 1
fi

# Generate a short, deterministic suffix from the last 2 bytes (4 hex chars) of the MAC address.
SUFFIX=$(echo "$MAC_ADDR" | awk -F: '{print $5$6}' | tr '[:lower:]' '[:upper:]')

echo "TollGate-${SUFFIX}"
```

### 2.2. Modified `99-tollgate-setup` WiFi Section
This is the new implementation for the Wi-Fi configuration block in `files/etc/uci-defaults/99-tollgate-setup`. It is now simpler and more focused.

```sh
# 4. Configure TollGate WiFi networks (AP mode)
log_message "Configuring TollGate WiFi networks (AP mode)"

# Ensure the new helper script is available and executable
[ -f /usr/bin/generate-tollgate-ssid ] && chmod 755 /usr/bin/generate-tollgate-ssid

# Generate the single, deterministic SSID for all APs
TOLLGATE_SSID=$(/usr/bin/generate-tollgate-ssid)
if [ "$TOLLGATE_SSID" = "TollGate-ERROR" ] || [ -z "$TOLLGATE_SSID" ]; then
    log_message "CRITICAL: Could not generate TollGate SSID. Using random fallback."
    RANDOM_SUFFIX=$(head /dev/urandom | tr -dc 'A-Z0-9' | head -c 4)
    TOLLGATE_SSID="TollGate-${RANDOM_SUFFIX}"
fi
log_message "Using SSID for all APs: $TOLLGATE_SSID"

# Get all wifi-device sections (e.g., radio0, radio1)
for device_section in $(uci -q show wireless | grep "\.type='wifi-device'" | cut -d'.' -f2 | sort); do
    log_message "Processing wifi-device: $device_section"

    # Enable the radio if it's disabled
    if [ "$(uci -q get wireless."$device_section".disabled)" = "1" ]; then
        log_message "Enabling wifi-device: $device_section"
        uci_safe_set wireless "$device_section" disabled '0'
    fi

    # Find the first existing AP interface for this radio to modify.
    ap_iface=$(uci -q show wireless | grep "\.device='$device_section'" | grep "\.mode='ap'" | cut -d'.' -f2 | head -n 1)

    if [ -n "$ap_iface" ]; then
        log_message "Found existing AP interface '$ap_iface' for device '$device_section'. Reconfiguring for TollGate."

        # Configure the existing AP interface for TollGate
        uci_safe_set wireless "$ap_iface" network 'lan'
        uci_safe_set wireless "$ap_iface" ssid "$TOLLGATE_SSID"
        uci_safe_set wireless "$ap_iface" encryption 'none'
        uci_safe_set wireless "$ap_iface" disabled '0'
        log_message "Configured $ap_iface with SSID: $TOLLGATE_SSID"
    else
        log_message "Warning: No existing AP interface found for device '$device_section'. A TollGate AP will not be available on this radio."
    fi
done

# Commit wireless changes
uci commit wireless
log_message "Committed wireless configuration"
```

## 3. Acceptance Criteria
*   The router maintains its Wi-Fi client (STA) connection after installation.
*   Existing Access Points are **reconfigured** with the "TollGate-XXXX" SSID.
*   All TollGate APs on all radios share the **same** deterministic SSID, derived from the `br-lan` MAC address.
*   The system does **not** implement the full identity rotation feature in this PR.