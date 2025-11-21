# TollGate WRT Initial Restart Solution

## Problem
The `tollgate-wrt` service has a bug that manifests on its first run when the router has an upstream internet connection. Restarting the service once connectivity to `8.8.8.8` is established resolves the issue, and no further restarts are required.

## Solution Implemented
An event-driven approach using an OpenWrt hotplug script was chosen to handle this one-time restart. This is more efficient and idiomatic than polling.

### How it works
1.  A new script, `95-tollgate-restart`, is placed in `/etc/hotplug.d/iface/`. Scripts in this directory are executed in response to network interface events.
2.  The script listens specifically for `ifup` (interface up) events. It identifies the correct WAN interface by checking the device name associated with the `wan` logical interface in the UCI configuration, making it robust for both wired and wireless WAN setups (e.g., where the WAN device might be `wwan`).
3.  It checks for the existence of a flag file (`/tmp/tollgate_initial_restart_done`). If the file exists, the script exits, ensuring the restart happens only once per boot cycle.
4.  If the flag file is not present, the script waits 5 seconds for the interface to stabilize, then attempts to ping `8.8.8.8`.
5.  If the ping is successful, it executes `/etc/init.d/tollgate-wrt restart`.
6.  Finally, it creates the flag file `/tmp/tollgate_initial_restart_done` to prevent further restarts until the next reboot.

### Files Created
*   `files/etc/hotplug.d/iface/95-tollgate-restart`: The hotplug script.
*   `docs/tollgate-wrt-initial-restart.md`: This documentation file.

This solution ensures the `tollgate-wrt` service is reliably restarted after the router establishes a stable internet connection, resolving the initial startup bug without unnecessary overhead. The improved interface detection makes it compatible with various WAN configurations.