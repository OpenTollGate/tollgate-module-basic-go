# LLDD: Price-Based Gateway Selection

## 1. Introduction

This document provides the low-level implementation details for the price-based gateway selection feature.

## 2. Component Implementation

### 2.1. Wireless Scanner

*   **SSID Parsing:** The `parseScanOutput` function in `wireless_scanner.go` will be modified to use a regular expression to parse SSIDs with the format `TollGate-<ID>-<price_per_step>-<step_size>`.
*   **Data Structure:** The `NetworkInfo` struct will be updated to include `PricePerStep` and `StepSize` fields.

### 2.2. Gateway Manager

*   **Gateway Scoring:** A new private method `calculateGatewayScore(gateway Gateway) float64` will be implemented. The scoring algorithm will be:
    *   `Score = (1 / Price) * SignalStrength`
    *   A higher score is better. This prioritizes lower-priced gateways, with signal strength as a secondary factor.
*   **Price Calculation:** The `updateAPSSID` function will be responsible for:
    1.  Getting the connected gateway's price from its vendor elements.
    2.  Retrieving the margin from the `ConfigManager`.
    3.  Calculating the new price: `ourPrice = gatewayPrice * (1 + margin)`.
    4.  Updating the local AP's SSID with the new price.
    5.  Calling `ConfigManager.UpdatePricing()` to save the new price to the configuration file.

### 2.3. Configuration Manager

*   **`UpdatePricing` function:** A new public method `UpdatePricing(pricePerStep, stepSize int) error` will be added to `config_manager.go`. This function will:
    1.  Load the current configuration.
    2.  Update the `PricePerStep` and `StepSize` values.
    3.  Save the updated configuration back to `config.json`.
*   **`Margin` field:** A new field `Margin float64` will be added to the `Config` struct in `config_manager_config.go`.

## 3. Commits to Cherry-Pick

The following commits will be cherry-picked from `feature/price-in-ssid` to `feature/price-based-gateway-selection`:

```
01f406e70054dd361dea24a29e5c7d982ea696b4
d8598d15ea78063fe650bdfc0286c1e80da49f28
a84262ea0b2a4756ba5da27371444720a5a19967
2e401d6dfc495716a4826875559f61c36a54bca4
ba9415c2790705175ec4ba2d5830bd816b09ff3e
```

## 4. Testing Plan

### 4.1. Unit Tests

*   **`TestParsePricingFromSSID`:** A table-driven test will be created to test the `parsePricingFromSSID` function with various valid and invalid SSID formats.
*   **`TestCalculateGatewayScore`:** A test will be created to verify the gateway scoring algorithm.
*   **`TestUpdatePricing`:** A test will be created to verify that the `UpdatePricing` function correctly updates the configuration file.

### 4.2. Manual Tests

1.  **Setup:**
    *   Set up two TollGate devices, A and B.
    *   Configure device A with a base price.
    *   Configure device B with a margin.
2.  **Test Case 1: Price-Based Selection**
    *   Power on both devices.
    *   Verify that device B scans and discovers device A.
    *   Verify that device B connects to device A.
    *   Verify that device B updates its own SSID to advertise a price that is the sum of device A's price and its own margin.
3.  **Test Case 2: Payment**
    *   Connect a client device to device B.
    *   Make a payment.
    *   Verify that the payment is successful and that device B forwards the correct payment amount to device A.