# HLDD: Gateway Reliability

## 1. Overview

This document outlines the high-level design for a gateway reliability tracking mechanism in the TollGate system. The goal is to improve the stability of the mesh network by avoiding connections to gateways that are known to be unreliable.

## 2. Key Features

*   **Reliability Tracking:** The system will track gateways that have been connected to but failed to provide a working internet connection.
*   **Gateway Blacklisting:** Unreliable gateways will be temporarily blacklisted to prevent the device from reconnecting to them.
*   **Blacklist Expiration:** The blacklist will have a timeout, after which the gateway will be considered for connection again.

## 3. System Architecture

These changes primarily affect the `wireless_gateway_manager` component.

*   **Gateway Manager:** Will be updated to maintain a list of unreliable gateways and to filter them out during the gateway selection process.

## 4. Data Flow

1.  When the device connects to a gateway, it will perform a connectivity check.
2.  If the connectivity check fails, the gateway's BSSID will be added to a blacklist in the `GatewayManager`.
3.  During the gateway selection process, the `GatewayManager` will filter out any gateways that are in the blacklist.
4.  Each blacklisted gateway will have a timestamp. After a certain period of time, the gateway will be removed from the blacklist and will be considered for connection again.

## 5. Testing Strategy

*   **Unit Tests:** Unit tests will be added for the new functionality.
*   **Manual Tests:** A manual testing plan will be developed to test the new features on a real device.