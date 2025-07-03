# Low-Level Design Document (LLDD) - Network Disconnection Fix

## 1. Context
This document details the implementation plan to resolve the network disconnection issue observed on OpenWRT routers configured in Wi-Fi client (STA) mode after installing the `tollgate-module-basic-go` package. The root cause has been identified as the `files/etc/uci-defaults/99-tollgate-setup` script's unconditional modification of wireless interfaces, which inadvertently disrupts existing STA connections.

## 2. Implementation Details for `files/etc/uci-defaults/99-tollgate-setup`

### 2.1. Identify the problematic section:
The relevant section in `files/etc/uci-defaults/99-tollgate-setup` is lines 160-205, which currently attempts to configure `wireless.default_radio0` and `wireless.default_radio1` and is causing the disruption.

### 2.2. Proposed Changes:

The current approach of modifying `wireless.default_radio0` and `wireless.default_radio1` directly will be replaced. Instead, the script will dynamically identify all physical Wi-Fi devices and create *new*, uniquely named `wifi-iface` sections for the TollGate APs. This ensures that existing `wifi-iface` sections (including STA interfaces) are preserved.

**Detailed Diff for `99-tollgate-setup`:**

```diff
--- a/files/etc/uci-defaults/99-tollgate-setup
+++ b/files/etc/uci-defaults/99-tollgate-setup
@@ -157,48 +157,46 @@
 log_message "dnsmasq configuration completed"
 
 # 4. Configure WiFi networks
-log_message "Configuring WiFi networks"
+# 4. Configure TollGate WiFi networks (AP mode)
+log_message "Configuring TollGate WiFi networks (AP mode)"
 # Generate a random 4-character suffix for the SSID
 RANDOM_SUFFIX=$(head /dev/urandom | tr -dc 'A-Z0-9' | head -c 4)
-# Check if SSID is already configured with TollGate prefix
-current_ssid=$(uci -q get wireless.default_radio0.ssid)
-if [[ "$current_ssid" == "TollGate-"* ]]; then
-    SSID_BASE="$current_ssid"
-    log_message "Using existing SSID: $SSID_BASE"
-else
-    SSID_BASE="TollGate-${RANDOM_SUFFIX}"
-    log_message "Generated new random SSID: $SSID_BASE"
-fi
+SSID_BASE="TollGate-${RANDOM_SUFFIX}"
+log_message "Generated new random SSID: $SSID_BASE"
 
-# Check if default radio0 exists before configuring
-if uci -q get wireless.default_radio0 >/dev/null; then
-    # Configure 2.4GHz WiFi with random suffix
-    uci_safe_set wireless default_radio0 name 'tollgate_2g_open'
-    uci_safe_set wireless default_radio0 ssid "${SSID_BASE}"
-    uci_safe_set wireless default_radio0 encryption 'none'
-    uci_safe_set wireless default_radio0 disabled '0'  # Ensure the interface is enabled
-    log_message "Configured 2.4GHz WiFi with SSID: ${SSID_BASE}"
-else
-    log_message "No 2.4GHz radio (default_radio0) found"
-fi
-
-# Check if default radio1 exists before configuring
-if uci -q get wireless.default_radio1 >/dev/null; then
-    # Configure 5GHz WiFi with the same random suffix
-    uci_safe_set wireless default_radio1 name 'tollgate_5g_open'
-    uci_safe_set wireless default_radio1 ssid "${SSID_BASE}"
-    uci_safe_set wireless default_radio1 encryption 'none'
-    uci_safe_set wireless default_radio1 disabled '0'  # Ensure the interface is enabled
-    log_message "Configured 5GHz WiFi with SSID: ${SSID_BASE}"
-else
-    log_message "No 5GHz radio (default_radio1) found"
-fi
-
-# Enable wireless interfaces if they exist
-if uci -q get wireless.radio0 >/dev/null; then
-    uci_safe_set wireless radio0 disabled '0'
-    log_message "Enabled radio0"
-fi
-if uci -q get wireless.radio1 >/dev/null; then
-    uci_safe_set wireless radio1 disabled '0'
-    log_message "Enabled radio1"
-fi
+# Get all wifi-device sections (e.g., radio0, radio1, mt798111, mt798112)
+# We use 'uci -q show wireless | grep "\.type='"' to get the section name
+# e.g., wireless.mt798111=wifi-device
+for device_section in $(uci -q show wireless | grep "\.type='" | cut -d'.' -f2 | cut -d'=' -f1); do
+    log_message "Processing wifi-device: $device_section"
+    
+    # Check if the device is already disabled
+    DEVICE_DISABLED=$(uci -q get wireless."$device_section".disabled)
+    if [ "$DEVICE_DISABLED" = "1" ]; then
+        log_message "Enabling wifi-device: $device_section"
+        uci_safe_set wireless "$device_section" disabled '0'
+    fi
+
+    # Create a new wifi-iface for the TollGate AP on this device
+    # Use a unique name for the new interface to avoid conflicts
+    NEW_IFACE_NAME="tollgate_ap_${device_section}"
+    
+    # Check if this specific TollGate AP interface already exists
+    if ! uci -q get wireless."$NEW_IFACE_NAME" >/dev/null 2>&1; then
+        uci add wireless wifi-iface >/dev/null 2>&1
+        uci rename wireless.@wifi-iface[-1]="$NEW_IFACE_NAME" >/dev/null 2>&1
+        log_message "Created new wifi-iface section: $NEW_IFACE_NAME"
+    else
+        log_message "TollGate AP interface $NEW_IFACE_NAME already exists."
+    fi
+
+    # Configure the new TollGate AP interface
+    uci_safe_set wireless "$NEW_IFACE_NAME" device "$device_section"
+    uci_safe_set wireless "$NEW_IFACE_NAME" mode 'ap'
+    uci_safe_set wireless "$NEW_IFACE_NAME" network 'lan'
+    uci_safe_set wireless "$NEW_IFACE_NAME" ssid "${SSID_BASE}"
+    uci_safe_set wireless "$NEW_IFACE_NAME" encryption 'none'
+    uci_safe_set wireless "$NEW_IFACE_NAME" disabled '0'
+    log_message "Configured $NEW_IFACE_NAME on device $device_section with SSID: ${SSID_BASE}"
+done
 
+# Commit wireless changes
+uci commit wireless
+log_message "Committed wireless configuration"
+
 # 5. Configure NoDogSplash
 log_message "Configuring NoDogSplash"
```

### 2.3. Data Structures and Algorithms:

*   **`SSID_BASE` Generation:** A random 4-character suffix is generated using `head /dev/urandom | tr -dc 'A-Z0-9' | head -c 4` to create a unique SSID for the TollGate AP.
*   **`wifi-device` Iteration:** The script uses `uci -q show wireless | grep "\.type='" | cut -d'.' -f2 | cut -d'=' -f1` to programmatically extract the names of all physical Wi-Fi devices (e.g., `mt798111`, `mt798112`). This makes the script more robust to different hardware configurations.
*   **Conditional Device Enabling:** Before creating a new `wifi-iface`, the script checks if the corresponding `wifi-device` is disabled and enables it if necessary.
*   **Unique `wifi-iface` Creation:** For each `wifi-device`, a new `wifi-iface` section is added with a unique name (`tollgate_ap_DEVICE_NAME`) to prevent conflicts with existing interfaces.
*   **`uci_safe_set` and `uci_safe_add_list`:** These helper functions ensure that UCI configurations are applied safely, handling cases where sections or options might not yet exist.

### 2.4. Error Handling and Edge Cases:

*   **Log Messages:** Extensive `log_message` calls are included to provide detailed debugging information in `/tmp/tollgate-setup.log`.
*   **Existing TollGate AP:** The script checks if a `tollgate_ap_DEVICE_NAME` interface already exists for a given device to prevent duplicate creation.
*   **`uci_safe_set` and `uci_safe_add_list`:** These functions handle cases where UCI configurations or sections might not exist, attempting to create them if necessary.
*   **Network Restart:** A `network restart` and `nodogsplash restart` are scheduled at the end of the script to apply changes, with `2>/dev/null || true` for `nodogsplash restart` to prevent script failure if NoDogSplash is not installed or running.

### 2.5. Performance Considerations for OpenWRT Environments:
*   The script uses standard `uci` commands and basic shell scripting, which are generally lightweight for OpenWRT devices.
*   The iteration over `wifi-device` sections is efficient as the number of such devices is typically small (1-3).
*   The script avoids heavy computations or prolonged operations that could strain limited resources.

## 3. Acceptance Criteria

*   The router successfully maintains its Wi-Fi client (STA) connection to the upstream gateway after package installation.
*   New Open/unencrypted Access Points with the "TollGate-XXXX" SSID are created on each available Wi-Fi radio device.
*   Existing Wi-Fi configurations (including other APs and the STA interface) are preserved and not overwritten.
*   The `ping 8.8.8.8` command succeeds after installation on a freshly configured system.

## 4. Task Checklist (for Code Mode)

*   [ ] Modify `files/etc/uci-defaults/99-tollgate-setup` as per the detailed diff.
*   [ ] Create a git commit with a meaningful message.
*   [ ] Inform the user of the completion and readiness for testing.