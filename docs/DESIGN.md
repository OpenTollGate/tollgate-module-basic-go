# Reseller Mode Payment Logic Design

## 1. Problem

The reseller router connects to an upstream TollGate's Wi-Fi but fails to purchase an internet session, resulting in a connection loop.

## 2. Root Cause

The `wireless_gateway_manager` module, which handles the Wi-Fi connection, does not trigger the payment and discovery flow after connecting. The existing `crowsnest` and `chandler` modules, which handle discovery and payment, are not being invoked.

## 3. Proposed Solution

The solution is to integrate the `wireless_gateway_manager` with the `crowsnest` module. After a successful Wi-Fi connection, the `wireless_gateway_manager` will notify `crowsnest` to scan the new interface. This will trigger the existing discovery and payment logic, allowing the reseller router to purchase an internet session.

### 3.1. Architectural Changes

```mermaid
graph TD
    subgraph wireless_gateway_manager
        A[Connect to Wi-Fi]
    end

    subgraph crowsnest
        B[Scan Interface]
        C[Discover TollGate]
    end

    subgraph chandler
        D[Handle Payment]
    end

    A -- Notifies --> B
    B -- Finds TollGate --> C
    C -- Hands off to --> D
```

### 3.2. Implementation Steps

1.  **Add a `ScanInterface` function to the `Crowsnest` interface.** This new function will allow other modules to trigger a scan on a specific network interface.

2.  **Add a `crowsnest` field to the `GatewayManager` struct.** This will allow the `wireless_gateway_manager` to call the new `ScanInterface` function.

3.  **Update the `Init` function in `wireless_gateway_manager.go` to accept a `crowsnest` instance.** The `crowsnest` instance will be stored in the `GatewayManager` struct.

4.  **Modify the `ScanWirelessNetworks` function in `wireless_gateway_manager.go` to call `crowsnest.ScanInterface` after a successful connection.** This will trigger the discovery and payment flow.

5.  **Implement the `ScanInterface` function in `crowsnest.go`.** This function will perform a scan on the specified interface and hand off any discovered TollGates to the `chandler` module.