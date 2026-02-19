# Router-to-Router Autopay Implementation

## Overview
This PR implements automatic payment handling for router-to-router (upstream gateway) connections in TollGate. The system now automatically detects when connected to an upstream TollGate gateway, monitors usage, and handles payment renewals without manual intervention.

## Key Features

### 🔄 Automatic Session Management
- **Unified Usage Tracking**: New `UpstreamUsageTracker` polls upstream gateway's `:2121/usage` endpoint every second
- **Automatic Initial Payment**: Detects `-1/-1` (no session) and triggers initial payment automatically
- **Smart Renewal**: Monitors usage and triggers renewal when approaching allotment threshold
- **Session State Detection**: Tracks session creation, expiration, and renewal completion

### 💳 Intelligent Payment Handling
- **Payment Throttling**: Prevents duplicate payments with 5-second minimum between attempts
- **Mutex Protection**: Thread-safe payment operations prevent race conditions
- **Token Recovery**: Automatic recovery of failed payment tokens via `merchant.Fund()`
- **Fallback Recovery**: Failed tokens saved to `/etc/tollgate/tokens-to-recover.txt` for manual recovery

### 🏗️ Architecture Improvements
- **Simplified Session Model**: `UpstreamSession` only tracks gateway IP (no customer identity needed)
- **Modular Design**: Clean separation between session management, usage tracking, and payment handling
- **Configurable Thresholds**: Renewal offsets and preferred increments configurable per metric type (bytes/milliseconds)

## Technical Changes

### New Files
- `src/chandler/session.go` - Upstream session lifecycle management
- `src/chandler/upstream_usage_tracker.go` - Unified usage polling and renewal triggering
- `src/chandler/token_recovery.go` - Payment token recovery utilities

### Removed Files
- `src/chandler/data_usage_tracker.go` - Replaced by unified upstream tracker
- `src/chandler/time_usage_tracker.go` - Replaced by unified upstream tracker

### Modified Files
- `src/chandler/chandler.go` - Refactored to use new session model
- `src/chandler/types.go` - Updated session types and status enums
- `src/config_manager/config_manager_config.go` - Updated default values for renewal thresholds

## Behavior

### Initial Connection Flow
1. Detect upstream gateway advertisement
2. Create `UpstreamSession` with selected pricing
3. Start `UpstreamUsageTracker` (polls every 1 second)
4. Tracker detects `-1/-1` → triggers initial payment
5. Payment sent → session created → tracking begins

### Renewal Flow
1. Tracker polls usage continuously
2. When `remaining ≤ renewalOffset` → triggers renewal
3. Payment throttling prevents duplicates
4. New allotment received → tracking continues
5. On failure → token recovery attempted

### Error Handling
- **Payment Failures**: Automatic token recovery via `merchant.Fund()`
- **Recovery Failures**: Token saved to file with timestamp and error details
- **Network Issues**: Graceful degradation with debug logging
- **Mutex Deadlocks**: Fixed with proper lock ordering

## Configuration

Updated default values in `config_manager_config.go`:

```go
Sessions: SessionConfig{
    PreferredSessionIncrementsMilliseconds: 60000,     // 1 minute
    PreferredSessionIncrementsBytes:        44040192,  // 42 MB
    MillisecondRenewalOffset:               10000,     // 10 seconds before expiry
    BytesRenewalOffset:                     31457280,  // 30 MB before limit
},
```

### Configuration Details
- **BytesRenewalOffset**: `30 MB` - Triggers renewal when 30MB remains in allotment
- **PreferredSessionIncrementsBytes**: `42 MB` - Default purchase size for data sessions
- **MillisecondRenewalOffset**: `10 seconds` - Triggers renewal 10 seconds before time expiry
- **PreferredSessionIncrementsMilliseconds**: `1 minute` - Default purchase size for time sessions

## Testing Recommendations
- [ ] Test initial payment on fresh connection
- [ ] Test renewal at threshold (30MB remaining)
- [ ] Test payment throttling (rapid renewals)
- [ ] Test token recovery on payment failure
- [ ] Test session expiration and recreation
- [ ] Test concurrent payment attempts (mutex protection)
- [ ] Verify 42MB purchase increments
- [ ] Verify 30MB renewal trigger point

## Breaking Changes
None - this is additive functionality for upstream gateway connections.

## Related Issues
Fixes router-to-router autopay functionality and resolves mutex deadlock issues in session management.

## Commit History
1. `ed7e609` - rename crowsnest -> Upstream Detector
2. `6d004b5` - session creation works
3. `c60b47d` - recovery works
4. `124311a` - better logs
5. `9356508` - fix gateway not detected when going in-and-out of reach
6. `7229692` - fix renewal on exhaustion
7. `80cbbe1` - refactor chandler, fully base off upstream usage
8. `cbce446` - fix mutex deadlock