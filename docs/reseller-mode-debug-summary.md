# Tollgate-WRT Control Flow Diagram

This diagram illustrates the different logic paths for handling network events in `tollgate-wrt`, contrasting the standard (gateway) mode with the reseller (client) mode.

```mermaid
graph TD
    subgraph "System Events"
        A[Interface Event e.g., ifup/ifdown wwan]
    end

    A --> B{OpenWrt Hotplug};

    subgraph "Hotplug Scripts"
        B --> C[95-tollgate-restart];
        B --> D[96-tollgate-scan];
    end

    subgraph "tollgate-wrt Daemon"
        E[Wireless Gateway Manager]
        F[Network Monitor]
        G[Crowsnest Prober/Scanner]
        H[Chandler Session Manager]
    end

    C --> I{Reseller Mode Enabled?};
    I -- No --> J[Check Internet, Restart Service];
    I -- Yes --> K[Exit Script];

    D --> L{Reseller Mode Enabled?};
    L -- No --> M[Trigger Crowsnest Scan];
    L -- Yes --> N[Exit Script];

    A -- Notifies Kernel --> F;
    F -- Connectivity Lost --> E;
    E -- Start Scan --> G;
    G -- Finds Gateways --> E;
    E -- Selects & Connects --> G;
    G -- Probes & Auth Captive Portal --> H;
    H -- Starts Payment Session --> E;
    E -- Confirms Connection --> F;

    style J fill:#f9f,stroke:#333,stroke-width:2px
    style M fill:#f9f,stroke:#333,stroke-width:2px
    style K fill:#9cf,stroke:#333,stroke-width:2px
    style N fill:#9cf,stroke:#333,stroke-width:2px
```

## Analysis of Current Issue

The logs show a race condition:
1.  The `wwan` interface comes up (`ifup`).
2.  The `tollgate-wrt` daemon's `Wireless Gateway Manager` begins its complex process: connect, get IP, get route, probe gateway, **trigger captive portal**, and start payment.
3.  Simultaneously, the `95-tollgate-restart` hotplug script is triggered. It does **not** currently check for reseller mode. It waits 5 seconds and runs its own simple `ping 8.8.8.8` check.
4.  This ping check happens *before* the daemon has completed the captive portal authorization. The ping fails, and the script logs `WAN up but no connectivity...`.
5.  The daemon, unaware of the script's failure, successfully completes its process and establishes a payment session. However, other services that might depend on the hotplug script are not correctly notified/restarted.

The core issue is that `95-tollgate-restart` is interfering with the `Wireless Gateway Manager`, which should have sole authority over the `wwan` interface in reseller mode.
