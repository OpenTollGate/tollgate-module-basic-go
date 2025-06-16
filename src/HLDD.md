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

- **Merchant Integration**: Payment processing logic moved to merchant module
- **Metric-Agnostic**: Support for flexible pricing metrics (milliseconds, bytes, etc.)
- **Error Handling**: Enhanced with granular error codes and notice events
- **Session Management**: Dynamic metric support in session events

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