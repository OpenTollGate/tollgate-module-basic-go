# TollGate Reliability Fixes - Implementation Plan

## Overview

This document outlines architectural improvements and bug fixes to address reliability issues in the upstream gateway connection flow. These fixes are based on analysis of the current codebase and documentation review.

## Refactoring Tasks

### 1. Rename `crowsnest` → `upstream_detector`

**Rationale**:
- More descriptive name that clearly indicates purpose
- "crowsnest" is nautical metaphor that's not immediately clear
- "upstream_detector" is domain-specific and self-documenting
- Easier for new developers to understand

**Scope**:
- Rename package: `src/crowsnest/` → `src/upstream_detector/`
- Rename all files in package
- Update all imports across codebase
- Update configuration keys in `config.json`
- Update documentation references
- Update CLI commands if any

**Files to Update**:
- `src/crowsnest/*.go` → `src/upstream_detector/*.go`
- `src/main.go` (imports and initialization)
- `src/chandler/chandler.go` (imports)
- `go.mod` files
- Configuration files
- All documentation

**Configuration Changes**:
```json
// Before
{
  "crowsnest": {
    "monitoring_interval": "5s",
    ...
  }
}

// After
{
  "upstream_detector": {
    "monitoring_interval": "5s",
    ...
  }
}
```

### 2. Move Advertisement Detection from `upstream_detector` to `chandler`

**Rationale**:
- Cleaner separation of concerns
- `upstream_detector` becomes purely network event reporter
- `chandler` owns all upstream connection decisions
- Enables better retry logic in chandler
- Supports advertisement polling naturally in chandler
- Simplifies session recovery (check usage + fetch ad in one place)

**Current Flow**:
```
upstream_detector:
  1. Detect network event
  2. Probe gateway :2121/
  3. Validate advertisement
  4. Call chandler.HandleUpstreamTollgate(upstream)

chandler:
  5. Receive validated upstream
  6. Make session decisions
  7. Create payment
```

**New Flow**:
```
upstream_detector:
  1. Detect network event
  2. Extract gateway IP
  3. Call upstream_session_manager.HandleGatewayDiscovered(interface, gateway_ip)

upstream_session_manager:
  4. Check :2121/usage (session recovery)
  5. If no session, probe :2121/ (advertisement)
  6. Validate advertisement (is it a TollGate?)
  7. Check trust policy (is it whitelisted?)
  8. Check wallet balance (do we have money?)
  9. Check pricing compatibility (matching mints?)
  10. If ANY check fails: ignore gateway, retry after 60s
  11. If all checks pass: create payment and session
```

**Key Behavior Change**:
- Failed validations don't permanently reject gateway
- All checks re-evaluated every 60 seconds via advertisement polling
- Enables recovery from temporary conditions (low balance, trust changes, etc.)

**Changes Required**:

#### In `upstream_detector`:
- Remove `TollGateProber` component
- Remove advertisement validation logic
- Remove `UpstreamTollgate` creation
- Remove `DiscoveryTracker` (no longer needed)
- Simplify to just gateway IP detection and reporting
- New interface: `upstream_session_manager.HandleGatewayDiscovered(interfaceName, macAddress, gatewayIP string)`

#### In `upstream_session_manager` (renamed from chandler):
- Add `TollGateProber` component (move from upstream_detector)
- Add advertisement validation logic
- Add `HandleGatewayDiscovered()` method (main entry point)
- Implement session recovery check (`:2121/usage` before creating session)
- Implement advertisement polling (60s for all known gateways)
- Implement gateway re-evaluation on poll (check all requirements again)
- Update `HandleUpstreamTollgate()` → internal method or remove
- Remove `HandleDisconnect()` complexity (just mark expired and cleanup)

#### New Gateway Tracking:
```go
type KnownGateway struct {
    InterfaceName  string
    MacAddress     string
    GatewayIP      string
    LastChecked    time.Time
    LastCheckError error
}

// Track all gateways we've seen
knownGateways map[string]*KnownGateway  // keyed by gateway IP
```

#### Advertisement Polling Logic:
```go
// Every 60 seconds, for each known gateway:
func (u *UpstreamSessionManager) pollGateways() {
    for gatewayIP, gateway := range u.knownGateways {
        // 1. Check :2121/usage (session recovery)
        // 2. Probe :2121/ (advertisement)
        // 3. Validate advertisement (is TollGate?)
        // 4. Check trust policy (whitelisted?)
        // 5. Check balance (have money?)
        // 6. Check pricing (matching mints?)
        // 7. If all pass and no session: create session
        // 8. If any fail: log and retry next cycle
    }
}
```

#### Benefits:
- Simpler upstream_detector (just network events)
- upstream_session_manager owns complete upstream connection lifecycle
- Better error handling and retry in one place
- Natural place for advertisement polling
- Session recovery integrated with advertisement fetch
- Automatic retry for all failure conditions (trust, budget, balance, etc.)
- No permanent rejection of gateways

### 3. Rename `chandler` → `upstream_session_manager`

**Rationale**:
- More descriptive name indicating purpose
- "chandler" is metaphorical (ship's supplier) but not immediately clear
- "upstream_session_manager" is self-documenting
- Clearly indicates it manages sessions with upstream TollGates
- Distinguishes from downstream session management (merchant)

**Scope**:
- Rename package: `src/chandler/` → `src/upstream_session_manager/`
- Rename all files in package
- Update all imports across codebase
- Update interface names
- Update documentation references
- Update CLI commands if any

**Files to Update**:
- `src/chandler/*.go` → `src/upstream_session_manager/*.go`
- `src/main.go` (imports and initialization)
- `src/upstream_detector/*.go` (imports and interface)
- `go.mod` files
- All documentation

**Interface Rename**:
```go
// Before
type ChandlerInterface interface { ... }
type ChandlerSession struct { ... }

// After
type UpstreamSessionManagerInterface interface { ... }
type UpstreamSession struct { ... }
```

### 4. Remove "Paused" Session Status

**Rationale**:
- Paused sessions create ambiguous state
- Unclear when/how sessions resume
- Complicates session lifecycle
- Better to terminate and recreate sessions

**Current Session States**:
```go
const (
    SessionActive SessionStatus = iota
    SessionPaused   // REMOVE THIS
    SessionExpired
    SessionError
)
```

**New Session States**:
```go
const (
    SessionActive SessionStatus = iota
    SessionExpired
    SessionError
)
```

**Changes Required**:

1. **Remove Pause/Resume Methods**:
   - Delete `PauseSession(pubkey string) error`
   - Delete `ResumeSession(pubkey string) error`

2. **Update Renewal Failure Handling**:
```go
// Current behavior on renewal failure
if err := validateBudget(); err != nil {
    session.Status = SessionPaused  // REMOVE
    session.UsageTracker.Stop()
    return err
}

// New behavior
if err := validateBudget(); err != nil {
    session.Status = SessionExpired  // Terminate instead
    session.UsageTracker.Stop()
    delete(c.sessions, pubkey)  // Remove from map
    return err
}
```

3. **Update Periodic Check Logic**:
```go
// Current
activeSessions := cs.chandler.GetActiveSessions()
if len(activeSessions) > 0 {
    return // Includes paused sessions
}

// New (already correct if we remove paused)
activeSessions := cs.chandler.GetActiveSessions()
if len(activeSessions) > 0 {
    return // Only active sessions
}
```

4. **Simplify Session Lifecycle**:
   - Active → Expired (on exhaustion or failure)
   - Active → Expired (on disconnect)
   - Active → Error (on unrecoverable error)
   - No pause/resume transitions

**Benefits**:
- Simpler state machine
- Clearer session lifecycle
- Expired sessions automatically cleaned up
- Periodic check works correctly
- No ambiguous "paused" state

**Migration**:
- Any existing paused sessions become expired
- Remove pause/resume from CLI if present
- Update documentation

### 5. Unify Initial Purchase and Renewal Flow

**Rationale**:
- Initial purchase and renewal are essentially the same operation
- Both check requirements and create payment
- Only difference is what triggers the attempt
- Simplifies code and reduces duplication

**Current Implementation**:
- `HandleUpstreamTollgate()` - initial purchase with full validation
- `HandleUpcomingRenewal()` - renewal with partial validation
- Duplicate validation logic

**New Implementation**:
```go
// Single unified method
func (u *UpstreamSessionManager) attemptPurchase(gateway *KnownGateway, reason string) error {
    // 1. Check :2121/usage (session recovery)
    // 2. Probe :2121/ (advertisement)
    // 3. Validate advertisement (is TollGate?)
    // 4. Check trust policy (whitelisted?)
    // 5. Check balance (have money?)
    // 6. Check pricing compatibility (matching mints?)
    // 7. If ANY fails: return error, retry later
    // 8. Create payment
    // 9. Send to upstream
    // 10. Create/update session
    // 11. Start/update usage tracker
}

// Triggered by:
// - HandleGatewayDiscovered() → attemptPurchase(gateway, "initial")
// - HandleUpcomingRenewal() → attemptPurchase(gateway, "renewal")
// - Advertisement poll → attemptPurchase(gateway, "retry")
```

**Benefits**:
- Single code path for all purchases
- Consistent validation every time
- Easier to maintain and test
- Natural retry mechanism
- Session recovery integrated

## Implementation Order

**Phase 1: Naming Refactors** (Low Risk)
1. Rename `crowsnest` → `upstream_detector`
2. Rename `chandler` → `upstream_session_manager`
3. Update all references and documentation

**Phase 2: Session State Simplification** (Medium Risk)
1. Remove `SessionPaused` status
2. Remove pause/resume methods
3. Update renewal failure handling to mark as expired
4. Test session lifecycle

**Phase 3: Unify Purchase Flow** (Medium Risk)
1. Create unified `attemptPurchase()` method
2. Refactor initial purchase to use it
3. Refactor renewal to use it
4. Remove duplicate validation code
5. Test both initial and renewal flows

**Phase 4: Advertisement Detection Refactor** (High Risk)
1. Move `TollGateProber` to `upstream_session_manager`
2. Add `HandleGatewayDiscovered()` to `upstream_session_manager`
3. Implement session recovery check (`:2121/usage`)
4. Implement advertisement polling (60s)
5. Add gateway tracking (known gateways map)
6. Simplify `upstream_detector` to just gateway detection
7. Update integration in `main.go`
8. Comprehensive testing

## Additional Notes

### Payment Rejection HTTP Status

**Correction**: Payment rejections return **HTTP 400** (Bad Request), not 402 (Payment Required)

**Response Types from Upstream**:
- **200 OK**: Payment accepted, returns session event (kind 1022)
- **400 Bad Request**: Payment rejected, returns notice event (kind 21023)
- **500 Internal Server Error**: Server error processing payment

**Documentation Update Needed**:
- Update chandler.md to show HTTP 400 for rejections
- Update sequence diagrams showing payment rejection

### Session Recovery on Startup

**Implementation Detail**:

When `upstream_session_manager` receives `HandleGatewayDiscovered()`:

```go
func (u *UpstreamSessionManager) HandleGatewayDiscovered(interfaceName, macAddress, gatewayIP string) error {
    // Step 1: Check if session already exists on upstream
    usage, allotment, err := u.checkUpstreamUsage(gatewayIP)
    
    if err == nil && usage != -1 && allotment != -1 {
        // Session exists! Recover it
        logger.Info("Existing session found on upstream, recovering")
        return u.recoverSession(interfaceName, macAddress, gatewayIP, usage, allotment)
    }
    
    // Step 2: No session, proceed with new purchase
    return u.attemptPurchase(gateway, "initial")
}
```

**Response Handling**:
- `-1/-1`: No session, create new one
- `usage/allotment`: Session exists, recover it
- Error: Can't determine, proceed with new (may duplicate)

### Advertisement Polling Behavior

**Every 60 seconds**:
```go
func (u *UpstreamSessionManager) pollKnownGateways() {
    for gatewayIP, gateway := range u.knownGateways {
        // Attempt purchase (includes all validations)
        err := u.attemptPurchase(gateway, "poll")
        
        if err != nil {
            // Log specific failure reason
            // Will retry next cycle (60s)
            logger.WithError(err).Debug("Gateway not suitable this cycle")
        }
    }
}
```

**Failure Handling**:
- Not a TollGate: Ignore, retry next cycle
- Not whitelisted: Ignore, retry next cycle (trust policy may change)
- No money: Ignore, retry next cycle (balance may increase)
- No matching mints: Ignore, retry next cycle (config may change)
- Already have session: Skip, continue monitoring

**This enables automatic recovery from**:
- Temporary network issues
- Temporary balance issues
- Trust policy changes
- Configuration changes
- Advertisement changes

## Testing Requirements

### For Each Phase:

**Unit Tests**:
- Test renamed components work correctly
- Test new interfaces
- Test state transitions

**Integration Tests**:
- Test complete flow: network detection → session creation
- Test session recovery on startup
- Test advertisement polling
- Test renewal flows

**Manual Tests**:
- Connect to upstream WiFi
- Verify session created
- Restart TollGate
- Verify session recovered (not double payment)
- Change upstream advertisement
- Verify chandler detects change

## Rollback Plan

- Keep old code in separate branch
- Test thoroughly before merging
- Document breaking changes
- Provide migration guide for configuration

## Next Steps

After review and approval:
1. Create detailed implementation tasks for each refactor
2. Implement in phases with testing between each
3. Update all documentation to reflect new architecture
4. Create migration guide for users