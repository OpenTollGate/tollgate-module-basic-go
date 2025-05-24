# Low-Level Design Document: Valve Module

## Overview

The Valve Module provides functionality for controlling network access for clients. It manages timers for automatic deauthorization of MAC addresses after their allocated time expires.

## Data Structures

### activeTimers

```go
var (
    activeTimers = make(map[string]*time.Timer)
    timerMutex   = &sync.Mutex{}
)
```

The `activeTimers` map stores active timers for each MAC address. The `timerMutex` provides thread-safe access to the map.

## Functions

### OpenGate

```go
func OpenGate(macAddress string, durationSeconds int64) error
```

- Authorizes a MAC address for network access for a specified duration
- If the MAC address is already authorized, it cancels the existing timer and creates a new one with the extended duration
- Uses `ndsctl` to authorize the MAC address

### GetRemainingTime

```go
func GetRemainingTime(macAddress string) (int64, time.Time, bool)
```

- Returns the remaining time in seconds for a MAC address
- Returns the expiry timestamp
- Returns a boolean indicating whether the MAC address has an active timer
- Implementation details:
  - Acquires the timer mutex lock to safely access the active timers map
  - Checks if the MAC address has an active timer
  - If no active timer exists, returns (0, time.Time{}, false)
  - Otherwise, calculates the remaining time and expiry timestamp, and returns them along with true

### cancelExistingTimer

```go
func cancelExistingTimer(macAddress string)
```

- Cancels any existing timer for the given MAC address
- Removes the timer from the active timers map

### authorizeMAC

```go
func authorizeMAC(macAddress string) error
```

- Authorizes a MAC address using `ndsctl`
- Executes the command `ndsctl auth <macAddress>`

### deauthorizeMAC

```go
func deauthorizeMAC(macAddress string) error
```

- Deauthorizes a MAC address using `ndsctl`
- Executes the command `ndsctl deauth <macAddress>`

### GetActiveTimers

```go
func GetActiveTimers() int
```

- Returns the number of active timers for debugging purposes

## Error Handling

- The module handles errors from `ndsctl` commands
- If an error occurs during deauthorization, it is logged but the timer is still removed from the map
- Thread safety is ensured using mutex locks

## Testing

- Unit tests should be written to ensure the correct functionality of the valve module
- Mock commands should be used to simulate `ndsctl` behavior

## Implementation Tasks

1. Add the `GetRemainingTime` function to retrieve remaining time for a MAC address
2. Modify the timer setup to store the expiry timestamp for each MAC address
3. Ensure thread safety when accessing timer information
4. Update documentation to reflect the new functionality