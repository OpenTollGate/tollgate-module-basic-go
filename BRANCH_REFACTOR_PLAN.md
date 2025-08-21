# Branch Refactor Plan

This document outlines the plan for refactoring the `feature/price-in-ssid` branch into smaller, more manageable feature branches.

## Proposed Feature Branches

1.  **`feature/price-based-gateway-selection`**
    *   **Goal:** Implement the core logic for selecting upstream gateways based on price.
    *   **Base Branch:** `main-with-plan`
    *   **Design Documents:**
        *   [HLDD](docs/price-based-gateway-selection/HLDD.md)
        *   [LLDD](docs/price-based-gateway-selection/LLDD.md)

2.  **`feature/wireless-improvements`**
    *   **Goal:** Enhance the wireless management system for more robust handling of Wi-Fi configurations.
    *   **Base Branch:** `main-with-plan`
    *   **Design Documents:**
        *   [HLDD](docs/wireless-improvements/HLDD.md)
        *   [LLDD](docs/wireless-improvements/LLDD.md)

3.  **`feature/gateway-reliability`**
    *   **Goal:** Introduce logic for tracking the reliability of upstream gateways.
    *   **Base Branch:** `main-with-plan`
    *   **Design Documents:**
        *   [HLDD](docs/gateway-reliability/HLDD.md)
        *   [LLDD](docs/gateway-reliability/LLDD.md)