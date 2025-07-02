# Low-Level Design Document (LLDD) - IP Randomization Enhancement

## 1. Context
This document details the implementation plan to enhance the IP address randomization logic in `files/etc/uci-defaults/95-random-lan-ip`. The current script is not robust enough to identify common vendor default IP addresses as "not randomized" and can trigger unnecessary IP changes and transient network disconnections.

## 2. Implementation Details for `files/etc/uci-defaults/95-random-lan-ip`

### 2.1. Identify the problematic section:
The IP check logic in `files/etc/uci-defaults/95-random-lan-ip` (lines 5-12 in the original file) needs to be enhanced. The network restart at lines 130-132 (in the original file) will be conditionalized.

### 2.2. Proposed Changes:

*   **Add `is_default_ip` function:** This function will be added at the beginning of the script, after the initial `INSTALL_JSON` check.

    ```bash
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
    ```

*   **Modify the initial check block and conditionalize network restart:** The existing IP check logic will be replaced with a more comprehensive one that utilizes the `is_default_ip` function and a `RANDOMIZE_IP` flag. The `network restart` will be moved inside the conditional block that executes only when a new IP is generated.

**Detailed Diff for `95-random-lan-ip`:**

```diff
--- a/files/etc/uci-defaults/95-random-lan-ip
+++ b/files/etc/uci-defaults/95-random-lan-ip
@@ -3,18 +3,41 @@
      
      INSTALL_JSON="/etc/tollgate/install.json"
      if [ -f "$INSTALL_JSON" ]; then
    -    IP_RANDOMIZED=$(jq -r '.ip_address_randomized' "$INSTALL_JSON")
    -    echo "DEBUG: IP_RANDOMIZED value: $IP_RANDOMIZED"
    -    valid_ip=$(echo "$IP_RANDOMIZED" | grep -E '^[0-9]{1,3}(\.[0-9]{1,3}){3}$')
    -    if [ -n "$valid_ip" ]; then
    -        echo "IP is already randomized to $IP_RANDOMIZED. Exiting."
    +    STORED_RANDOM_IP=$(jq -r '.ip_address_randomized // "null"' "$INSTALL_JSON")
+        echo "DEBUG: Stored IP_RANDOMIZED in install.json: $STORED_RANDOM_IP"
+    fi
+    
    +# Function to check if an IP address is a common default vendor IP
    +is_default_ip() {
    +    local ip="$1"
    +    # Common default IPs: 192.168.1.1, 192.168.8.1, 192.168.X.1
    +    if echo "$ip" | grep -Eq "^192\.168\.(1|8|([0-9]{1,3}))\.1$"; then
    +        return 0 # It is a default IP
    +    else
    +        return 1 # It is not a default IP
    +    fi
    +}
    +
    +# Determine if randomization is needed
    +RANDOMIZE_IP=1 # Assume we need to randomize by default
    +
    +if [ -n "$STORED_RANDOM_IP" ] && [ "$STORED_RANDOM_IP" != "null" ]; then
    +    # If there's a stored randomized IP, check if it's a valid non-default IP
    +    if echo "$STORED_RANDOM_IP" | grep -Eq '^[0-9]{1,3}(\.[0-9]{1,3}){3}$' && ! is_default_ip "$STORED_RANDOM_IP"; then
    +        echo "Stored IP '$STORED_RANDOM_IP' is a valid, non-default randomized IP. Checking current LAN IP."
    +        CURRENT_LAN_IP=$(uci -q get network.lan.ipaddr)
    +        if [ "$CURRENT_LAN_IP" = "$STORED_RANDOM_IP" ]; then
    +            echo "Current LAN IP matches stored randomized IP. No randomization needed."
    +            RANDOMIZE_IP=0
    +        else
    +            echo "Current LAN IP '$CURRENT_LAN_IP' does not match stored randomized IP. Will re-randomize."
+        fi
+    else
+        echo "Stored IP '$STORED_RANDOM_IP' is null, invalid, or a default IP. Will randomize."
+    fi
+else
+    echo "install.json not found or ip_address_randomized is missing/null. Will randomize."
+fi
+
+if [ "$RANDOMIZE_IP" -eq 0 ]; then
+    echo "IP is already randomized and set. Exiting 95-random-lan-ip."
     exit 0
- fi
-else
-    echo "install.json not found. Exiting."
-    exit 1
- fi
- 
- # We don't need to check for a flag file since uci-defaults scripts 
- # are automatically run only once after installation or upgrade
  
   # Helper function to safely set UCI values with error handling
   uci_safe_set() {
@@ -90,10 +113,12 @@
   # Construct the random IP with last octet as 1
   RANDOM_IP="$OCTET1.$OCTET2.$OCTET3.1"
   echo "Setting random LAN IP to: $RANDOM_IP"
+fi # End of RANDOMIZE_IP block
   
- # Update network config using UCI
- uci_safe_set network lan ipaddr "$RANDOM_IP"
- uci commit network
+if [ "$RANDOMIZE_IP" -eq 1 ]; then
+    # Update network config using UCI
+    uci_safe_set network lan ipaddr "$RANDOM_IP"
+    uci commit network
   
   # Update hosts file
   if grep -q "status.client" /etc/hosts; then
@@ -118,12 +143,12 @@
   BROADCAST="$OCTET1.$OCTET2.$OCTET3.255"
   uci_safe_set network lan broadcast "$BROADCAST"
   
- # No need for a flag file - uci-defaults handles this automatically
- 
- # Schedule network restart (safer than immediate restart during boot)
- (sleep 5 && /etc/init.d/network restart &&
-  [ -f "/etc/init.d/nodogsplash" ] && /etc/init.d/nodogsplash restart) &
- 
- # Update install.json with the new random IP
- jq '.ip_address_randomized = "'"$RANDOM_IP"'"' "$INSTALL_JSON" > "$INSTALL_JSON.tmp" && mv "$INSTALL_JSON.tmp" "$INSTALL_JSON"
- 
- 
- exit 0
+    # Schedule network restart (safer than immediate restart during boot)
+    # This restart is crucial for the new IP to take effect.
+    (sleep 5 && /etc/init.d/network restart &&
+     [ -f "/etc/init.d/nodogsplash" ] && /etc/init.d/nodogsplash restart) &
+    
+    # Update install.json with the new random IP
+    jq '.ip_address_randomized = "'"$RANDOM_IP"'"' "$INSTALL_JSON" > "$INSTALL_JSON.tmp" && mv "$INSTALL_JSON.tmp" "$INSTALL_JSON"
+fi
+
+exit 0
```

### 2.3. Data Structures and Algorithms:

*   **`is_default_ip()` function:** Uses a regex pattern to match common default IP addresses (e.g., `192.168.1.1`, `192.168.8.1`, `192.168.X.1`).
*   **`RANDOMIZE_IP` flag:** A boolean-like variable (0 or 1) to control whether IP randomization and network restart should occur.
*   **Conditional Logic:** The script's flow is controlled by `if` statements that evaluate the `RANDOMIZE_IP` flag and the results of `is_default_ip()`.

### 2.4. Error Handling and Edge Cases:

*   **`install.json` absence/null:** The script gracefully handles cases where `install.json` is missing or the `ip_address_randomized` field is null/missing, correctly defaulting to randomization.
*   **Invalid `ip_address_randomized`:** The script checks if `STORED_RANDOM_IP` is a valid IP format before using it for comparison.
*   **`network restart` conditionalization:** The network restart and `install.json` update only occur if a new random IP is actually generated and set, preventing unnecessary disruptions.

### 2.5. Performance Considerations for OpenWRT Environments:
*   The added logic involves basic shell commands and string/regex matching, which are efficient operations on OpenWRT devices.
*   The primary performance improvement comes from avoiding unnecessary `network restart` calls, which can be resource-intensive and disruptive.

## 3. Acceptance Criteria

*   The router's LAN IP address is randomized only if:
    *   `install.json` does not contain a valid, non-default randomized IP.
    *   OR, the currently configured `network.lan.ipaddr` is a common default vendor IP (`192.168.1.1`, `192.168.8.1`, `192.168.X.1`).
*   The transient network disconnection during fresh installs is mitigated by conditionalizing the `network restart` within `95-random-lan-ip`.
*   The `ping 8.8.8.8` command succeeds after installation on a freshly configured system, even when config files are initially absent.

## 4. Task Checklist (for Code Mode)

*   [ ] Implement the changes in `files/etc/uci-defaults/95-random-lan-ip` as per the detailed diff.
*   [ ] Create a git commit with a meaningful message.
*   [ ] Request Architect mode review.