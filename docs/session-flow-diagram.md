# Session Purchase Flow Diagram

This document illustrates the incorrect and correct flows for handling a data-based session purchase.

## Current Flawed Flow

The current implementation has the `Merchant` module directly calling the `Valve` to open the gate for a fixed 24-hour period when it sees a data-based (`bytes`) metric. It completely ignores the calculated data allotment for the purpose of gate control.

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

## Corrected Flow

The correct implementation involves the `Merchant` notifying the `Chandler` after a successful payment. The `Chandler` is responsible for creating the correct session tracker (`DataUsageTracker`), opening the gate indefinitely, and then closing the gate only when the data allotment is consumed.

```mermaid
sequenceDiagram
    participant Client
    participant Server (main.go)
    participant Merchant
    participant Chandler
    participant DataUsageTracker
    participant Valve

    Client->>Server: POST / (Payment Event)
    Server->>Merchant: PurchaseSession(event)
    Merchant->>Merchant: calculateAllotment() -> 682MB
    Merchant->>Merchant: AddAllotment()
    Merchant->>Chandler: StartSession(mac)
    Chandler->>Merchant: GetSession(mac)
    Merchant-->>Chandler: sessionData (682MB)
    Chandler->>DataUsageTracker: NewDataUsageTracker(lan_interface)
    Chandler->>Valve: OpenGate(mac)
    Chandler->>DataUsageTracker: Start(682MB)
    DataUsageTracker->>DataUsageTracker: monitor() /proc/net/dev
    Note over DataUsageTracker: When allotment is used...
    DataUsageTracker->>Chandler: done signal
    Chandler->>Valve: CloseGate(mac)
    Chandler-->>Merchant: 
    Merchant-->>Server: Session Event
    Server-->>Client: Session Event
```

The core problem is that the `Merchant` is performing session management tasks that belong to the `Chandler`. I will now prepare the fix to correct this architectural flaw.