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

*   **Refine IP Randomization Logic and Conditionalize Network Restart:** The existing IP check logic will be replaced with a more robust approach using the `is_default_ip` function and a `RANDOMIZE_IP` flag. The `RANDOMIZE_IP` flag will be determined as follows:
    *   Initialize `RANDOMIZE_IP` to `1` (randomize).
    *   Retrieve the `CURRENT_LAN_IP` from UCI.
    *   If `CURRENT_LAN_IP` is a common default vendor IP (e.g., `192.168.1.1`), `RANDOMIZE_IP` remains `1`.
    *   Otherwise (if `CURRENT_LAN_IP` is *not* a default IP):
        *   Check `install.json` for a `ip_address_randomized` value (`STORED_RANDOM_IP`).
        *   If `install.json` exists, and `STORED_RANDOM_IP` is a valid, non-default randomized IP, and `CURRENT_LAN_IP` matches `STORED_RANDOM_IP`, then set `RANDOMIZE_IP` to `0` (do not randomize).
        *   In all other cases (e.g., `install.json` missing, `ip_address_randomized` null/invalid, or `CURRENT_LAN_IP` doesn't match a valid stored random IP), `RANDOMIZE_IP` remains `1`.
    *   The `network restart` and `install.json` update will only occur if `RANDOMIZE_IP` is `1`.

**Detailed Diff for `95-random-lan-ip`:**

```diff
 b/files/etc/uci-defaults/95-random-lan-ip
@@ -1,22 +1,78 @@
 #!/bin/sh
 # This script randomizes the LAN IP address of the OpenWRT router.
 # It ensures that the router's LAN IP is not a common default (e.g., 192.168.1.1)
+# and avoids unnecessary re-randomization on subsequent boots if an IP has already been set.
 
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

# Determine if randomization is needed
RANDOMIZE_IP=1 # Assume we need to randomize by default

CURRENT_LAN_IP=$(uci -q get network.lan.ipaddr)
echo "DEBUG: Current LAN IP: $CURRENT_LAN_IP"

if is_default_ip "$CURRENT_LAN_IP"; then
    echo "Current LAN IP is a default vendor IP. Will randomize."
    RANDOMIZE_IP=1
else
    # Current LAN IP is NOT a default IP. Now check install.json.
    if [ -f "$INSTALL_JSON" ]; then
        STORED_RANDOM_IP=$(jq -r '.ip_address_randomized // "null"' $INSTALL_JSON")
        echo "DEBUG: Stored IP_RANDOMIZED in install.json: $STORED_RANDOM_IP"

        if [ -n "$STORED_RANDOM_IP" ] && [ "$STORED_RANDOM_IP" != "null" ]; then
            if echo "$STORED_RANDOM_IP" | grep -Eq '^[0-9]{1,3}(\.[0-9]{1,3}){3}$' && ! is_default_ip "$STORED_RANDOM_IP"; then
                if [ "$CURRENT_LAN_IP" = "$STORED_RANDOM_IP" ]; then
                    echo "Current LAN IP matches stored randomized IP. No randomization needed."
                    RANDOMIZE_IP=0
                else
                    echo "Current LAN IP '$CURRENT_LAN_IP' does not match stored randomized IP '$STORED_RANDOM_IP'. Will re-randomize."
                    RANDOMIZE_IP=1 # Re-randomize if current doesn't match stored valid random
                fi
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

if [ "$RANDOMIZE_IP" -eq 0 ]; then
    echo "IP is already randomized and set. Exiting 95-random-lan-ip."
    exit 0
fi
 
 # We don't need to check for a flag file since uci-defaults scripts 
 # are automatically run only once after installation or upgrade
   
    # Helper function to safely set UCI values with error handling
    uci_safe_set() {
@@ -90,10 +146,26 @@
    # Construct the random IP with last octet as 1
    RANDOM_IP="$OCTET1.$OCTET2.$OCTET3.1"
    echo "Setting random LAN IP to: $RANDOM_IP"


if [ "$RANDOMIZE_IP" -eq 1 ]; then
    # Update network config using UCI
    uci_safe_set network lan ipaddr "$RANDOM_IP"
    uci commit network
    
    # Update hosts file
    if grep -q "status.client" /etc/hosts; then
@@ -118,12 +190,12 @@
    BROADCAST="$OCTET1.$OCTET2.$OCTET3.255"
    uci_safe_set network lan broadcast "$BROADCAST"
    

    # Schedule network restart (safer than immediate restart during boot)
    # This restart is crucial for the new IP to take effect.
    (sleep 5 && /etc/init.d/network restart &&
     [ -f "/etc/init.d/nodogsplash" ] && /etc/init.d/nodogsplash restart) &
    
    # Update install.json with the new random IP
    jq '.ip_address_randomized = "'"$RANDOM_IP"'"' "$INSTALL_JSON" > "$INSTALL_JSON.tmp" && mv "$INSTALL_JSON.tmp" "$INSTALL_JSON"
fi

exit 0
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
*   The `ping 8.8.8.8` command succeeds after installation on a freshly configured system, even when config files are_initially absent.

## 4. Task Checklist (for Code Mode)

*   [ ] Implement the changes in `files/etc/uci-defaults/95-random-lan-ip` as per the detailed diff.
*   [ ] Create a git commit with a meaningful message.
*   [ ] Request Architect mode review.