### Draft Release Notes for v0.0.4

#### General Release Notes (Non-Technical Users)

The v0.0.4 release introduces:

1. **Flexible Pricing Metrics**: Support for different pricing units (time, data) with configurable step sizes
2. **Mint-Specific Pricing**: Each mint can have individual pricing and minimum purchase requirements
3. **Enhanced Error Messages**: More detailed feedback when payments fail with specific error codes
4. **Human-Readable Configuration**: Config files are now pretty-printed for easier editing

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

5. **Mint Fee Integration**:
   - `GetMintFee()` implementation using gonuts library
   - Active keyset fee retrieval with fallback handling
   - Integration with advertisement generation

6. **Migration & Deployment**:
   - Added `99-tollgate-config-migration-v0.0.2-to-v0.0.3-migration` script
   - Converts global `price_per_minute` to mint-specific `price_per_step`
   - Sets default metric to "milliseconds" with 60000ms step size
   - Version-specific migration guards (only runs on v0.0.2)
   - Integrated into Makefile for package deployment

7. **Code Quality**:
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