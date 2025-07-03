# Low-Level Design Document (LLDD) - IP Randomization Enhancement

## 1. Context
This document details the implementation plan to simplify the IP address randomization logic in `files/etc/uci-defaults/95-random-lan-ip`. The goal is to ensure IP randomization occurs only once and to clearly indicate its status.

## 2. Implementation Details for `files/etc/uci-defaults/95-random-lan-ip`

### 2.1. Identify the problematic section:
The IP check logic in `files/etc/uci-defaults/95-random-lan-ip` needs to be simplified. The network restart will be conditionalized.

### 2.2. Proposed Changes:

*   **Remove `is_default_ip` function:** This function is no longer needed as we are simplifying the randomization trigger.

*   **Simplify IP Randomization Logic and Conditionalize Network Restart:** The existing IP check logic will be replaced with a simpler approach based solely on the `ip_address_randomized` flag in `install.json`.

    *   Retrieve the `ip_address_randomized` flag from `install.json`.

    *   **Decision Logic:**
        *   **If `install.json` does not exist, or `ip_address_randomized` is `false` or absent:**
            *   Generate a new random LAN IP.
            *   Update network config with the new IP in `/etc/config/network`.
            *   Update `/etc/hosts` with the new IP.
            *   Set `ip_address_randomized` to `true` in `install.json`.
            *   Schedule `network restart`.
        *   **Else (if `ip_address_randomized` is `true`):**
            *   Exit the script. No action needed.

**Detailed Diff for `95-random-lan-ip`:**

```diff
 b/files/etc/uci-defaults/95-random-lan-ip
@@ -1,22 +1,78 @@
 #!/bin/sh
 # This script randomizes the LAN IP address of the OpenWRT router.
 # It ensures that the router's LAN IP is not a common default (e.g., 192.168.1.1)
-# and avoids unnecessary re-randomization on subsequent boots if an IP has already been set.
+# and avoids unnecessary re-randomization on subsequent boots if an IP has already been set,
+# or persists the current non-default IP if install.json is not properly populated.
 
 INSTALL_JSON="/etc/tollgate/install.json"
 
 
 # Function to check if an IP address is a common default vendor IP
 is_default_ip() {
     local ip="$1"
     # Common default IPs: 192.168.1.1, 192.168.8.1, 192.168.X.1
     if echo "$ip" | grep -Eq "^192\.168\.(1|8|([0-9]{1,3}))\.1$"; then
         return 0 # It is a default IP
     else
         return 1 # It is not a default IP
     fi
 }
 
-# Determine if randomization is needed
-RANDOMIZE_IP=1 # Assume we need to randomize by default
+# Determine if randomization or persistence is needed
+RANDOMIZE_IP=0 # Assume no randomization by default
+PERSIST_CURRENT_IP=0 # Assume no persistence by default
+EXIT_EARLY=0 # Assume we don't exit early by default
 
 CURRENT_LAN_IP=$(uci -q get network.lan.ipaddr)
 echo "DEBUG: Current LAN IP: $CURRENT_LAN_IP"
 
+STORED_RANDOM_IP="null"
+if [ -f "$INSTALL_JSON" ]; then
+    STORED_RANDOM_IP=$(jq -r '.ip_address_randomized // "null"' "$INSTALL_JSON")
+fi
+echo "DEBUG: Stored IP_RANDOMIZED in install.json: $STORED_RANDOM_IP"
+
 if is_default_ip "$CURRENT_LAN_IP"; then
     echo "Current LAN IP is a default vendor IP. Will randomize."
     RANDOMIZE_IP=1
 else
-    # Current LAN IP is NOT a default IP. Now check install.json.
-    if [ -f "$INSTALL_JSON" ]; then
-        STORED_RANDOM_IP=$(jq -r '.ip_address_randomized // "null"' $INSTALL_JSON")
-        echo "DEBUG: Stored IP_RANDOMIZED in install.json: $STORED_RANDOM_IP"
-
-        if [ -n "$STORED_RANDOM_IP" ] && [ "$STORED_RANDOM_IP" != "null" ]; then
-            if echo "$STORED_RANDOM_IP" | grep -Eq '^[0-9]{1,3}(\.[0-9]{1,3}){3}$' && ! is_default_ip "$STORED_RANDOM_IP"; then
-                if [ "$CURRENT_LAN_IP" = "$STORED_RANDOM_IP" ]; then
-                    echo "Current LAN IP matches stored randomized IP. No randomization needed."
-                    RANDOMIZE_IP=0
-                else
-                    echo "Current LAN IP '$CURRENT_LAN_IP' does not match stored randomized IP '$STORED_RANDOM_IP'. Will re-randomize."
-                    RANDOMIZE_IP=1 # Re-randomize if current doesn't match stored valid random
-                fi
+    # Current LAN IP is NOT a default IP.
+    if [ -n "$STORED_RANDOM_IP" ] && [ "$STORED_RANDOM_IP" != "null" ]; then
+        if echo "$STORED_RANDOM_IP" | grep -Eq '^[0-9]{1,3}(\.[0-9]{1,3}){3}$' && ! is_default_ip "$STORED_RANDOM_IP"; then
+            if [ "$CURRENT_LAN_IP" = "$STORED_RANDOM_IP" ]; then
+                echo "Current LAN IP matches stored randomized IP. No action needed."
+                EXIT_EARLY=1
             else
                 echo "Stored IP '$STORED_RANDOM_IP' is invalid or a default IP. Will randomize."
                 RANDOMIZE_IP=1
             fi
         else
             echo "install.json found, but ip_address_randomized is missing/null. Will randomize."
             RANDOMIZE_IP=1
         fi
     else
         echo "install.json not found. Will randomize."
         RANDOMIZE_IP=1
     fi
 fi
 
-if [ "$RANDOMIZE_IP" -eq 0 ]; then
-    echo "IP is already randomized and set. Exiting 95-random-lan-ip."
-    exit 0
+if [ "$RANDOMIZE_IP" -eq 1 ]; then
+    # ... (existing randomization logic) ...
+    # Construct the random IP with last octet as 1
+    RANDOM_IP="$OCTET1.$OCTET2.$OCTET3.1"
+    echo "Setting random LAN IP to: $RANDOM_IP"
+
+    # Update network config using UCI
+    uci_safe_set network lan ipaddr "$RANDOM_IP"
+    uci commit network
+
+    # Update hosts file
+    if grep -q "status.client" /etc/hosts; then
+        # ... (hosts file update logic) ...
+    fi
+
+    BROADCAST="$OCTET1.$OCTET2.$OCTET3.255"
+    uci_safe_set network lan broadcast "$BROADCAST"
+
+    # Schedule network restart (safer than immediate restart during boot)
+    # This restart is crucial for the new IP to take effect.
+    (sleep 5 && /etc/init.d/network restart &&
+     [ -f "/etc/init.d/nodogsplash" ] && /etc/init.d/nodogsplash restart) &
+
+    # Update install.json with the new random IP
+    jq '.ip_address_randomized = "'"$RANDOM_IP"'"' "$INSTALL_JSON" > "$INSTALL_JSON.tmp" && mv "$INSTALL_JSON.tmp" "$INSTALL_JSON"
+elif [ "$PERSIST_CURRENT_IP" -eq 1 ]; then
+    echo "Persisting current LAN IP '$CURRENT_LAN_IP' to install.json."
+    jq '.ip_address_randomized = "'"$CURRENT_LAN_IP"'"' "$INSTALL_JSON" > "$INSTALL_JSON.tmp" && mv "$INSTALL_JSON.tmp" "$INSTALL_JSON"
+elif [ "$EXIT_EARLY" -eq 1 ]; then
+    echo "IP is already randomized and set. Exiting 95-random-lan-ip."
+    exit 0
 fi
  
  # We don't need to check for a flag file since uci-defaults scripts
  # are automatically run only once after installation or upgrade
    
     # Helper function to safely set UCI values with error handling
     uci_safe_set() {
-@@ -90,10 +146,26 @@
-     # Construct the random IP with last octet as 1
-     RANDOM_IP="$OCTET1.$OCTET2.$OCTET3.1"
-     echo "Setting random LAN IP to: $RANDOM_IP"
-
-
-if [ "$RANDOMIZE_IP" -eq 1 ]; then
-     # Update network config using UCI
-     uci_safe_set network lan ipaddr "$RANDOM_IP"
-     uci commit network
-
-     # Update hosts file
-     if grep -q "status.client" /etc/hosts; then
-@@ -118,12 +190,12 @@
-     BROADCAST="$OCTET1.$OCTET2.$OCTET3.255"
-     uci_safe_set network lan broadcast "$BROADCAST"
-
-
-     # Schedule network restart (safer than immediate restart during boot)
-     # This restart is crucial for the new IP to take effect.
-     (sleep 5 && /etc/init.d/network restart &&
-      [ -f "/etc/init.d/nodogsplash" ] && /etc/init.d/nodogsplash restart) &
-
-     # Update install.json with the new random IP
-     jq '.ip_address_randomized = "'"$RANDOM_IP"'"' "$INSTALL_JSON" > "$INSTALL_JSON.tmp" && mv "$INSTALL_JSON.tmp" "$INSTALL_JSON"
- fi
-
- exit 0
- ```
```

### 2.3. Data Structures and Algorithms:

*   **`RANDOMIZE_IP_FLAG`:** A boolean-like variable (`"true"` or `"false"`) read from `install.json` to control whether IP randomization should occur.
*   **Random IP Generation:** Generates a `10.X.Y.1` IP address.
*   **Conditional Logic:** The script's flow is controlled by an `if` statement that evaluates the `RANDOMIZE_IP_FLAG`.

### 2.4. Error Handling and Edge Cases:

*   **`install.json` absence/null:** The script gracefully handles cases where `install.json` is missing or the `ip_address_randomized` field is null/missing, correctly defaulting to randomization.
*   **`network restart` conditionalization:** The network restart and `install.json` update only occur if a new random IP is actually generated and set, preventing unnecessary disruptions.

### 2.5. Performance Considerations for OpenWRT Environments:
*   The added logic involves basic shell commands and string/regex matching, which are efficient operations on OpenWRT devices.
*   The primary performance improvement comes from avoiding unnecessary `network restart` calls, which can be resource-intensive and disruptive.

## 3. Acceptance Criteria

*   The router's LAN IP address is randomized if and only if the `ip_address_randomized` flag in `/etc/tollgate/install.json` is `false` or absent.
*   The randomized IP address is stored *only* in `/etc/config/network`.
*   The `/etc/tollgate/install.json` file contains *only* a boolean flag `ip_address_randomized` set to `true` after randomization.
*   The `ping 8.8.8.8` command succeeds after installation on a freshly configured system.

## 4. Task Checklist (for Code Mode)

*   [ ] Implement the changes in `files/etc/uci-defaults/95-random-lan-ip` as per the detailed diff.
*   [ ] Update `src/config_manager/config_manager.go` to reflect the new `install.json` format (boolean `ip_address_randomized` flag).
*   [ ] Update `src/config_manager/config_manager_test.go` to test the new `install.json` format.
*   [ ] Create a git commit with a meaningful message.
*   [ ] Request Architect mode review.