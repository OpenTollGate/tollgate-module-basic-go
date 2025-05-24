# High-Level Design Document: main.go

## Overview

The `main.go` file is the entry point of the TollGate application. It handles HTTP requests, interacts with the Nostr protocol, and manages configuration.

## Responsibilities

- Initialize the configuration manager and load configuration
- Handle HTTP requests for different endpoints
- Interact with the Nostr protocol for Cashu operations
- Manage the janitor module for NIP-94 events
- Provide time-related information for authorized clients

## Interfaces

- `init()`: Initializes the configuration manager, loads configuration, and sets up the Nostr event
- `handleRoot()`: Handles HTTP requests to the root endpoint
- `handleRootPost()`: Handles POST requests to the root endpoint
- `announceSuccessfulPayment()`: Announces successful payments via Nostr
- `handleStatus()`: New endpoint that returns the remaining time for a client

## Dependencies

## Accepted Mints Tagging
- Each accepted mint will be represented as a separate tag in the Nostr event.
- The format for the tag will be `["mint", "mint_url", "min_payment"]`, where `mint_url` is the URL of the mint and `min_payment` is the minimum payment required.

- `config_manager`: Provides configuration management functionality
- `janitor`: Provides NIP-94 event handling functionality
- `nostr`: Provides Nostr protocol functionality
- `modules`: Provides functionality for valve control and time tracking

## Time-Related Features

### Payment Success Response Enhancement
- The 200 OK response now includes a structured JSON object with detailed time information when a payment is successful
- This provides clients with information about their authorized access period

### Status Endpoint
- A new `/status` endpoint allows clients to check their session expiry time
- The endpoint returns a Nostr event with the expiry time (Unix timestamp)
- The client is identified by their MAC address, which is automatically detected