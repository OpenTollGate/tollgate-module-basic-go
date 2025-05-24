# High-Level Design Document: Valve Module

## Overview

The Valve Module is responsible for controlling network access for clients. It manages the authorization and deauthorization of MAC addresses and tracks the remaining time for each authorized client.

## Responsibilities

- Authorize MAC addresses for network access
- Deauthorize MAC addresses when their time expires
- Track the remaining time for authorized MAC addresses
- Provide an interface for checking the remaining time for MAC addresses

## Interfaces

- `OpenGate(macAddress string, durationSeconds int64) error`: Authorizes a MAC address for network access for a specified duration
- `GetRemainingTime(macAddress string) (int64, time.Time, bool)`: New function to get the remaining time for a MAC address
- `GetActiveTimers() int`: Returns the number of active timers for debugging

## Dependencies

- `ndsctl`: Command-line tool for authorizing and deauthorizing MAC addresses
- `time`: Go package for managing timers and durations

## Implementation Details

- The module maintains a map of active timers for each MAC address
- When a client is authorized, a timer is set to automatically deauthorize the client after the specified duration
- If a client is already authorized and makes a new payment, their existing timer is canceled and a new one is created with the extended duration
- The module provides a thread-safe implementation using mutex locks to manage concurrent access to the timer map