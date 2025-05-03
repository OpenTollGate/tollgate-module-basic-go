# High-Level Design Document: main.go

## Overview

The `main.go` file is the entry point of the TollGate application. It handles HTTP requests, interacts with the Nostr protocol, and manages configuration.

## Responsibilities

- Initialize the configuration manager and load configuration
- Handle HTTP requests for different endpoints
- Interact with the Nostr protocol for Cashu operations
- Manage the janitor module for NIP-94 events

## Interfaces

- `init()`: Initializes the configuration manager, loads configuration, and sets up the Nostr event
- `handleRoot()`: Handles HTTP requests to the root endpoint
- `handleRootPost()`: Handles POST requests to the root endpoint
- `announceSuccessfulPayment()`: Announces successful payments via Nostr

## Dependencies

## Accepted Mints Tagging
- Each accepted mint will be represented as a separate tag in the Nostr event.
- The format for the tag will be `["mint", "mint_url", "min_payment"]`, where `mint_url` is the URL of the mint and `min_payment` is the minimum payment required.

- `config_manager`: Provides configuration management functionality
- `janitor`: Provides NIP-94 event handling functionality
- `nostr`: Provides Nostr protocol functionality