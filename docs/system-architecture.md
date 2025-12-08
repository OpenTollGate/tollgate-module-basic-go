# Tollgate System Architecture

This document provides a high-level overview of the Tollgate system architecture.

## Component Interaction Diagram

```mermaid
graph TD
    subgraph OS/System Level
        A[Hotplug Scripts e.g., ifup/ifdown]
    end

    subgraph Tollgate Application
        B[WirelessGatewayManager]
        C[NetworkMonitor]
        D[Crowsnest]
        E[Chandler]
        F[TollWallet]
        G[Main App]
    end

    subgraph External
        H[Upstream TollGate Gateway]
    end

    G --> B
    G --> D
    G --> E
    G --> F

    A -- Triggers --> D

    B -- Contains --> C
    C -- Triggers Scan --> B

    B -- Scans & Connects to --> H
    D -- Discovers --> H
    D -- Reports Discovery to --> E

    E -- Manages Session & Payments with --> H
    E -- Uses --> F
    E -.-> C: Signals Reset
```

## Detailed Sequence Diagram

```mermaid
sequenceDiagram
    participant Client as Tollgate Client Device
    participant OS as Operating System
    participant WGM as WirelessGatewayManager
    participant NM as NetworkMonitor
    participant Crowsnest
    participant Chandler
    participant Gateway as Upstream TollGate Gateway

    OS->>+Crowsnest: Hotplug event (e.g., ifup)
    Crowsnest->>Crowsnest: Start Scan for TollGate advertisements
    Crowsnest->>Gateway: Probe for TollGate advertisement (HTTP request)
    Gateway-->>Crowsnest: Return TollGate advertisement
    Crowsnest->>Chandler: Forward advertisement

    Chandler->>Gateway: Send payment (Cashu tokens)
    Gateway-->>Chandler: Return session token (proof of payment)
    Chandler->>Chandler: Start session monitoring
    Chandler-->>NM: Send reset signal on resetChan

    NM->>NM: Reset consecutiveFailures counter

    loop Connectivity Check
        NM->>Gateway: Check internet connectivity (e.g., ping 8.8.8.8)
        alt Connection OK
            Gateway-->>NM: Ping reply
        else Connection Failed
            Gateway-->>NM: No reply
            NM->>WGM: Trigger force scan after threshold
        end
    end
```