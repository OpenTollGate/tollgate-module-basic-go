# High-Level Design Document: main.go

## Overview

The `main.go` file is the entry point of the TollGate application. It handles HTTP requests, processes Nostr payment events, and coordinates between merchant and other modules for payment processing.

## Responsibilities

- Initialize the configuration manager and merchant service
- Handle HTTP requests for different endpoints
- Process Nostr payment events (kind 21000)
- Route responses (session events or notice events) back to clients
- Manage the janitor module for NIP-94 events

## Architecture Changes (v0.0.4)

This release focuses on enhancing the robustness of configuration management and improving the testability and modularity of the merchant functionality.

- **Configuration Management Robustness**: Improved handling of missing or malformed configuration files, ensuring the application can always start with a valid default configuration. Migration scripts now include more rigorous validation checks.
- **Merchant Module Refactoring for Testability**: The core payment processing logic has been refactored to introduce a `MerchantService` interface and a mock implementation, enabling isolated unit testing of components that depend on merchant functionality. This significantly improves the maintainability and testability of the application.
- **Metric-Agnostic Payment Processing**: Support for flexible pricing metrics (milliseconds, bytes, etc.) has been integrated, allowing for diverse payment models.
- **Enhanced Error Handling**: Error handling is now more granular, with specific error codes and the use of Nostr notice events (Kind 21023) for clear communication of issues.
- **Dynamic Session Management**: Session events (Kind 1022) now dynamically support various metrics, providing flexibility in how internet access time or data usage is managed.

## Interfaces

- `init()`: Initializes the configuration manager, merchant, and sets up services
- `handleRoot()`: Handles HTTP requests to the root endpoint (serves advertisement)
- `handleRootPost()`: Handles POST requests with Nostr payment events
- `sendNoticeResponse()`: Creates and sends notice event responses for errors

## Payment Flow

1. Receive Nostr payment event (kind 21000)
2. Validate event signature and structure
3. Delegate payment processing to merchant
4. Return session event (success) or notice event (error)

## Dependencies

- `config_manager`: Configuration management with migration support
- `merchant`: Financial decision making and payment processing
- `janitor`: NIP-94 event handling and auto-updates
- `tollwallet`: Cashu token processing and mint interactions
- `valve`: Network access control
- `nostr`: Nostr protocol functionality

## Mint Advertisement Structure

- Dynamic metric and step_size from configuration
- Mint-specific pricing with `price_per_step` tags
- Format: `["price_per_step", "cashu", price, unit, mint_url, min_steps]`
- Minimum purchase steps included for each mint