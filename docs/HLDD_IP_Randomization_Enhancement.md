# High-Level Design Document (HLDD) - IP Randomization Enhancement

## 1. Problem Statement
The current IP address randomization logic in `files/etc/uci-defaults/95-random-lan-ip` is not sufficiently robust. It currently only checks `install.json` and fails to identify common vendor default IP addresses (e.g., `192.168.1.1`, `192.168.8.1`, and `192.168.X.1` where `X` is any number) as "not randomized," leading to unnecessary or incorrect IP randomization. This can also contribute to transient network disconnections if an IP change is triggered unnecessarily.

## 2. Goal
Enhance the IP address randomization check in `files/etc/uci-defaults/95-random-lan-ip` to:
*   Check the currently assigned `network.lan.ipaddr`.
*   Accurately identify and treat common vendor default IP addresses (`192.168.8.1`, `192.168.1.1`, and `192.168.X.1` where `X` is any number) as "not randomized", triggering a new randomization if found.
*   Ensure randomization only occurs if no valid, non-default randomized IP is already set and active.
*   Mitigate transient network disconnections by only triggering a `network restart` when a new IP address is actually set.

## 3. Current Architecture Overview (Relevant to IP Randomization)

*   **`95-random-lan-ip`:**
    *   Reads `ip_address_randomized` from `install.json`.
    *   If `install.json` is missing or `ip_address_randomized` is invalid/null, it proceeds to randomize.
    *   Sets `network.lan.ipaddr`, `netmask`, `broadcast`.
    *   Schedules `network restart` (sleep 5) and `nodogsplash restart` at the end of its execution.

## 4. Proposed Solution Architecture
The `95-random-lan-ip` script will be modified to include a robust check for existing randomized IPs and common default IPs.

*   A new shell function `is_default_ip()` will be introduced. This function will use regex to check if a given IP address matches common vendor default patterns (`^192\.168\.(1|8|([0-9]{1,3}))\.1$`).
*   The main logic in `95-random-lan-ip` will be modified to:
    1.  Attempt to retrieve `ip_address_randomized` from `install.json`.
    2.  Get the *current* `network.lan.ipaddr` from UCI.
    3.  A `RANDOMIZE_IP` flag will be initialized to `1` (true).
    4.  If a `STORED_RANDOM_IP` exists in `install.json` and is a valid, non-default IP (checked using `is_default_ip`), and if this `STORED_RANDOM_IP` matches the `CURRENT_LAN_IP`, then `RANDOMIZE_IP` will be set to `0` (false).
    5.  If `RANDOMIZE_IP` is `0`, the script will exit early.
    6.  Otherwise, if `RANDOMIZE_IP` is `1`, the script will proceed with generating a new random IP, applying it, updating `/etc/hosts`, and then updating `install.json` with the new randomized IP. The `network restart` will only be triggered within this `if` block.

## 5. Data Flow Diagram (Mermaid)

```mermaid
graph TD
    A[Start 95-random-lan-ip] --> B{Read ip_address_randomized from install.json}
    B --> C{Get current network.lan.ipaddr}
    C --> D{Define is_default_ip() function}
    D --> E{Determine RANDOMIZE_IP flag}
    E -- RANDOMIZE_IP == 0 (no randomization needed) --> F[Exit]
    E -- RANDOMIZE_IP == 1 (randomize) --> G[Generate new random LAN IP]
    G --> H[Update network config with new IP]
    H --> I[Update hosts file]
    I --> J[Update install.json with new IP]
    J --> K[Schedule network restart]
    K --> F
```

## 6. Future Extensibility Considerations

*   **Configurable Default IPs:** Allow the list of "default" IPs to be configurable, perhaps via a separate UCI option, to adapt to different vendor environments.
*   **More Sophisticated IP Conflict Detection:** Implement more advanced checks to detect potential IP conflicts on the network *before* applying a new randomized IP.