### Release Notes for v0.0.4-beta1

> **📦 Download this release**: Visit [releases.tollgate.me](https://releases.tollgate.me) to download v0.0.4-beta1 and explore all available TollGate releases.

#### General Release Notes (Non-Technical Users)

The v0.0.4-beta1 release introduces:

1. **Flexible Pricing Metrics**: Support for different pricing units (time, data) with configurable step sizes
2. **Mint-Specific Pricing**: Each mint can have individual pricing and minimum purchase requirements
3. **Enhanced Error Messages**: More detailed feedback when payments fail with specific error codes
4. **Human-Readable Configuration**: Config files are now pretty-printed for easier editing
5. **Internal Relay Integration**: Built-in relay functionality for enhanced nostr event handling
6. **Session Extensions**: Improved session management with extension capabilities
7. **Release Channels**: Introduction of alpha and beta release channels for better version management

#### Technical Release Notes (Technical Contributors)

Key changes:

1. **Config Structure Changes**:
   - Removed global `PricePerMinute`
   - Added `Metric` and `StepSize` to main config
   - Added `PricePerStep`, `PriceUnit`, `MinPurchaseSteps` to mint config
   - Pretty-printed JSON output using `json.MarshalIndent()`

2. **Merchant Refactoring**:
   - Generic `calculateAllotment()` with metric-specific handlers (`calculateAllotmentMs`, future `calculateAllotmentBytes`)
   - Mint-specific pricing logic with validation
   - Removed legacy `PurchaseSessionLegacy()` method
   - Steps calculation moved to shared logic level

3. **Session Management**:
   - `createSessionEvent()` uses dynamic metric from config
   - `extendSessionEvent()` handles time-based vs non-time metrics appropriately
   - Metric-agnostic variable naming (removed "Ms" suffixes)
   - Session events include dynamic metric tags

4. **Error Handling**:
   - Granular error codes (`payment-error-token-spent`, `invalid-mac-address`, `payment-processing-failed`, etc.)
   - Notice events generated within merchant for better separation of concerns
   - Specific token already spent detection

5. **Internal Relay Module**:
   - Added dedicated relay module for nostr event processing
   - Enhanced bragging module integration with relay functionality
   - Improved event routing and handling capabilities

6. **Session Extension Features**:
   - Enhanced session lifecycle management
   - Support for extending active sessions with additional payments
   - Improved session validation and timeout handling

7. **Mint Fee Integration**:
   - `GetMintFee()` implementation using gonuts library
   - Active keyset fee retrieval with fallback handling
   - Integration with advertisement generation

8. **Migration & Deployment**:
   - Added `99-tollgate-config-migration-v0.0.2-to-v0.0.3-migration` script
   - Converts global `price_per_minute` to mint-specific `price_per_step`
   - Sets default metric to "milliseconds" with 60000ms step size
   - Version-specific migration guards (only runs on v0.0.2)
   - Integrated into Makefile for package deployment

9. **Release Channel Management**:
   - Implemented alpha and beta release channel support
   - Version tagging system for pre-release versions
   - Improved deployment pipeline for different release stages

10. **Code Quality**:
    - Updated all test files for new config structure
    - Fixed variable naming consistency across codebase
    - Improved logging with metric-aware messages

#### Breaking Changes

- Configuration format updated from v0.0.2 (automatic migration provided)

#### Migration Notes

The migration script automatically:
- Converts `price_per_minute` to `price_per_step` for each mint
- Adds `metric: "milliseconds"` and `step_size: 60000` to main config
- Preserves all existing mint configurations
- Creates timestamped backups before migration

#### Beta Release Notes

This is the first beta release of v0.0.4. Key beta features include:

- **Stability Testing**: Core functionality has been tested but may contain minor issues
- **Feature Completeness**: All planned v0.0.4 features are implemented
- **Migration Safety**: Automatic config migration with backup protection
- **Feedback Welcome**: Please report any issues or unexpected behavior

#### Known Issues

- Session extension timing may need fine-tuning based on usage patterns
- Internal relay performance optimization ongoing
- Beta channel deployment scripts are still being refined

#### Next Steps

- Collect feedback from beta users
- Performance optimization based on real-world usage
- Final testing and validation before stable v0.0.4 release