# Low-Level Design Document (LLDD) - Router Identity Management

## 1. Context
This document provides the detailed implementation plan for the Identity Management scripts outlined in the HLDD. These scripts will provide the functionality to get, set, and use the router's complete network identity (all MAC addresses) to generate a deterministic SSID.

## 2. Implementation Details

### 2.1. Helper Script: `get-identity.sh`
This script will be an internal, non-executable helper located in `/usr/lib/tollgate/` to signify it's for use by other scripts.

*   **Location:** `files/usr/lib/tollgate/get-identity.sh`
*   **Permissions:** `644` (not executable)
*   **Logic:**
    1.  Find all network interfaces in `/sys/class/net/`.
    2.  Filter out non-physical or irrelevant interfaces (like `lo`, `docker*`, `veth*`).
    3.  Sort the remaining interface names alphabetically.
    4.  For each interface, read its MAC address from `/sys/class/net/INTERFACE_NAME/address`.
    5.  Print each MAC address to standard output, one per line.

```sh
#!/bin/sh
# /usr/lib/tollgate/get-identity.sh
# Outputs a sorted, deterministic list of all relevant MAC addresses.

find /sys/class/net/* -maxdepth 0 -type d | \
    cut -d'/' -f5 | \
    grep -vE '^lo$|^docker|^veth' | \
    sort | \
    while read -r iface; do
        if [ -f "/sys/class/net/$iface/address" ]; then
            cat "/sys/class/net/$iface/address"
        fi
    done
```

### 2.2. Helper Script: `set-identity.sh`
This script will be a user-facing, executable tool.

*   **Location:** `files/usr/bin/tollgate-rotate-identity`
*   **Permissions:** `755` (executable)
*   **Logic:**
    1.  Define a function to generate a valid, random, locally-administered MAC address. The prefix `02:` is a standard for this.
    2.  Iterate through all physical network interfaces (`eth*`, `wlan*`, etc., which can be found via `uci show network` and `uci show wireless`).
    3.  For each `network.interface` section that has a `macaddr` option, generate a new MAC and use `uci set`.
    4.  For each `wireless.wifi-device` section, generate a new MAC and use `uci set`.
    5.  After all changes are made, run `uci commit network` and `uci commit wireless`.
    6.  Inform the user that a reboot or network restart is required to apply the new MAC addresses.

```sh
#!/bin/sh
# /usr/bin/tollgate-rotate-identity
# Rotates all MAC addresses on the device to new random ones.

# Function to generate a random, locally-administered MAC address
generate_mac() {
    printf '02:%02x:%02x:%02x:%02x:%02x\n' "$(random_byte)" "$(random_byte)" "$(random_byte)" "$(random_byte)" "$(random_byte)"
}

random_byte() {
    head -c1 /dev/urandom | od -An -t x1 | tr -d ' '
}

log_message() {
    echo "tollgate-rotate-identity: $1"
}

log_message "Starting MAC address rotation..."

# Rotate MACs in /etc/config/network
for section in $(uci -q show network | grep 'network\..*\.macaddr' | cut -d'.' -f2); do
    NEW_MAC=$(generate_mac)
    log_message "Setting network.$section.macaddr to $NEW_MAC"
    uci set "network.$section.macaddr=$NEW_MAC"
done

# Rotate MACs in /etc/config/wireless
for section in $(uci -q show wireless | grep 'wireless\..*\.macaddr' | cut -d'.' -f2); do
    NEW_MAC=$(generate_mac)
    log_message "Setting wireless.$section.macaddr to $NEW_MAC"
    uci set "wireless.$section.macaddr=$NEW_MAC"
done

log_message "Committing changes..."
uci commit network
uci commit wireless

log_message "MAC address rotation complete."
log_message "Please REBOOT your router for all changes to take effect."
```

### 2.3. Helper Script: `generate-tollgate-ssid.sh`
This script will also be an internal helper.

*   **Location:** `files/usr/lib/tollgate/generate-tollgate-ssid.sh`
*   **Permissions:** `644`
*   **Logic:**
    1.  Call `/usr/lib/tollgate/get-identity.sh`.
    2.  Pipe the output to `sha256sum`.
    3.  Take the first 4 characters of the resulting hash.
    4.  Convert to uppercase.
    5.  Print the final SSID string `TollGate-XXXX`.

```sh
#!/bin/sh
# /usr/lib/tollgate/generate-tollgate-ssid.sh
# Generates the deterministic TollGate SSID.

IDENTITY_HASH=$(/usr/lib/tollgate/get-identity.sh | sha256sum)
SUFFIX=$(echo "$IDENTITY_HASH" | head -c 4 | tr '[:lower:]' '[:upper:]')

echo "TollGate-${SUFFIX}"
```

## 3. Error Handling and Edge Cases
*   **`get-identity.sh`:** The `grep` filter is designed to be robust, but new virtual interface types could appear. The script is simple enough that this is a low risk.
*   **`set-identity.sh`:** The script relies on `uci` correctly identifying sections with `macaddr` options. This is a standard and reliable method. It explicitly tells the user to reboot, which is crucial for the changes to apply correctly.
*   **`generate-tollgate-ssid.sh`:** If `get-identity.sh` produces no output (e.g., a very unusual system with no identifiable interfaces), the hash will still be computed on an empty string, resulting in a consistent (but default) SSID. This is acceptable fallback behavior.