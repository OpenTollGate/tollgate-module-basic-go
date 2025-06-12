# High-Level Design Document: crowsnest

## Overview

The `crowsnest` module is responsible for finding and analyzing available network interfaces, identifying which ones are free networks and which are tollgates (paid network access points), and providing this information to the merchant module for decision making about network connections.

## Responsibilities

- Scan for available network interfaces
- Determine if interfaces are free networks or tollgates
- For tollgates, retrieve and provide pricing information
- Provide a consistent interface for the merchant module to access network information

## Interfaces

- `New(configManager *config_manager.ConfigManager) (*Crowsnest, error)`: Creates a new Crowsnest instance
- `GetConnected() ([]NetworkInterface, error)`: Returns a list of available network interfaces with their details
- `ScanInterfaces() error`: Scans for available interfaces and updates internal state
- `AnalyzeInterface(interface string) (NetworkInterface, error)`: Analyzes a specific interface to determine its type and pricing

## Dependencies

- `config_manager`: Provides configuration management functionality
- System networking utilities for interface scanning