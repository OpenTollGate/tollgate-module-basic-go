# Session Purchase Flow Diagram

This document illustrates the evolution of data-based session handling, from the flawed approach to the correct implementation.

## Original Flawed Flow (Fixed)

The original implementation had the `Merchant` opening the gate for a fixed 24-hour period for data-based sessions, completely ignoring the data allotment.

```mermaid
sequenceDiagram
    participant Client
    participant Server (main.go)
    participant Merchant
    participant Valve

    Client->>Server: POST / (Payment Event)
    Server->>Merchant: PurchaseSession(event)
    Merchant->>Merchant: calculateAllotment() -> 682MB
    Merchant->>Merchant: AddAllotment()
    Note over Merchant: if metric == "bytes":<br/>endTimestamp = now + 24h
    Merchant->>Valve: OpenGateUntil(mac, now + 24h)
    Valve-->>Merchant: Success
    Merchant-->>Server: Session Event
    Server-->>Client: Session Event
```

**Problem**: Data allotment was calculated but never enforced.

## Second Attempt (Also Flawed)

The second approach tried to fix this by using interface-level data tracking via `/proc/net/dev`.

```mermaid
sequenceDiagram
    participant Client
    participant Merchant
    participant Chandler
    participant DataUsageTracker
    participant Valve

    Client->>Merchant: Payment
    Merchant->>Chandler: StartSession(mac)
    Chandler->>DataUsageTracker: NewDataUsageTracker(lan_interface)
    Chandler->>Valve: OpenGate(mac)
    DataUsageTracker->>DataUsageTracker: monitor() /proc/net/dev
    Note over DataUsageTracker: Tracks ENTIRE interface<br/>not individual customer!
    DataUsageTracker->>Valve: CloseGate(mac)
```

**Problems**:
1. Tracked entire interface (`br-lan`) instead of individual customer
2. Multiple customers on same interface caused incorrect measurements
3. Unnecessary dependency: Merchant → Chandler

## Current Correct Implementation

The correct implementation uses per-customer tracking via `ndsctl` and proper separation of concerns.

```mermaid
sequenceDiagram
    participant Client
    participant Server (main.go)
    participant Merchant
    participant Valve
    participant ndsctl

    Client->>Server: POST / (Payment Event)
    Server->>Merchant: PurchaseSession(event)
    Merchant->>Merchant: calculateAllotment() -> 682MB
    Merchant->>Merchant: AddAllotment(mac, "bytes", 682MB)
    
    Merchant->>Valve: OpenGate(mac)
    Valve->>ndsctl: json <mac>
    ndsctl-->>Valve: {downloaded: X, uploaded: Y}
    Valve->>Valve: Store baseline (X, Y)
    Valve-->>Merchant: Success
    
    Merchant-->>Server: Session Event
    Server-->>Client: Session Event
    
    Note over Merchant: Periodically check usage
    loop Every N seconds
        Merchant->>Valve: GetDataUsageSinceBaseline(mac)
        Valve->>ndsctl: json <mac>
        ndsctl-->>Valve: {downloaded: X', uploaded: Y'}
        Valve->>Valve: Calculate: (X'-X) + (Y'-Y)
        Valve-->>Merchant: usage bytes
        
        alt usage >= 682MB
            Merchant->>Valve: CloseGate(mac)
            Valve->>Valve: ClearDataBaseline(mac)
        end
    end
```

**Key Improvements**:
1. **Per-customer tracking**: Uses `ndsctl json <mac>` for accurate individual customer data
2. **Baseline tracking**: Captures initial usage when gate opens, only counts new usage
3. **Proper separation**: Merchant handles session logic, Valve handles gate control
4. **No Chandler dependency**: Merchant works independently for downstream customers

This architecture ensures accurate data tracking and proper enforcement of data-based session limits.