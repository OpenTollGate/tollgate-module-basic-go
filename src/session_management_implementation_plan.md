# Session Management Implementation Plan

## Overview
This document outlines the step-by-step implementation plan for adding session tracking and extension functionality to the existing TollGate system using a distributed architecture approach.

## Phase 1: Merchant Module Enhancements

### 1.1 Update Merchant Structure
- Add session-related fields to Merchant struct
- Add relay pool access for session storage
- Add session querying capabilities

### 1.2 Modify PurchaseSession Method
- **Current**: `PurchaseSession(paymentToken string, macAddress string) (PurchaseSessionResult, error)`
- **New**: `PurchaseSession(paymentEvent nostr.Event) (*nostr.Event, error)`
- Implement session lookup by customer pubkey
- Add session creation/extension logic
- Return session event instead of status result

### 1.3 Add Session Management Methods
```go
func (m *Merchant) getLatestSession(customerPubkey string) (*nostr.Event, error)
func (m *Merchant) createSessionEvent(paymentEvent nostr.Event, purchasedSteps uint64) (*nostr.Event, error)
func (m *Merchant) extendSessionEvent(existing *nostr.Event, additional uint64) (*nostr.Event, error)
func (m *Merchant) publishEvent(event *nostr.Event) error
func (m *Merchant) createNoticeEvent(level, code, message string, customerPubkey string) (*nostr.Event, error)
```

## Phase 2: Valve Module Enhancements

### 2.1 Update Valve Data Structures
- Extend activeTimers to include session information
- Add customer pubkey tracking to timer entries
- Add session event references to timers

### 2.2 Add Session-Based Methods
```go
func OpenGateForSession(sessionEvent nostr.Event) error
func ExtendSessionForCustomer(customerPubkey string, sessionEvent nostr.Event) error
func CleanupExpiredSessions() error
func GetActiveSessionTimers() map[string]*SessionTimer
```

### 2.3 Enhanced Timer Management
- Link timers to session events
- Support session extension without re-authorization
- Cleanup expired sessions from relay pool

## Phase 3: HTTP Handler Updates

### 3.1 Modify handleRootPost Function
- Parse full payment event from request body
- Validate signature
- Pass complete payment event to merchant
- Return session event as JSON response (TIP-03 compliance)
- Update HTTP status code handling

### 3.2 Protocol Compliance
- Ensure response format matches TIP-03 specification
- Return kind=21022 session events on successful payments
- Maintain proper error status codes (402, 500, etc.)

## Phase 4: Integration and Testing

### 4.1 Module Integration
- Update go.mod dependencies between modules
- Ensure proper relay pool sharing
- Test inter-module communication

### 4.2 Session Flow Testing
- Test new customer payment flow
- Test existing customer session extension
- Test session cleanup and expiration
- Test concurrent session operations

## Error Communication Protocol

### Notice Events (kind=21023)
For communicating issues to customers, we define a new event type:

```json
{
    "kind": 21023,
    "pubkey": "tollgate_pubkey",
    "tags": [
        ["p", "customer_pubkey"], // Optional
        ["level", "error|warning|info|debug"],
        ["code", "payment-err|invalid-event|insufficient-funds|session-err"]
    ],
    "content": "Human-readable error message",
    "created_at": timestamp,
    "sig": "signature"
}
```

**Common Error Codes:**
- `payment-err`: Payment processing failed
- `invalid-event`: Malformed or invalid payment event
- `insufficient-funds`: Payment amount insufficient for minimum session
- `session-err`: Session creation or management error
- `mint-not-accepted`: Payment from unsupported mint

**Levels:**
- `error`: Critical errors preventing session creation
- `warning`: Non-critical issues (e.g., partial payment)
- `info`: Informational messages
- `debug`: Debug information for troubleshooting

## Implementation Details

### Session Event Creation (Merchant)
```go
func (m *Merchant) createSessionEvent(paymentEvent nostr.Event, purchasedSteps uint64) (*nostr.Event, error) {
    // Extract customer info from payment event
    customerPubkey := paymentEvent.PubKey
    deviceIdentifier := extractDeviceIdentifier(paymentEvent)
    
    // Create session event (kind=21022)
    sessionEvent := &nostr.Event{
        Kind:      21022,
        PubKey:    m.config.TollgatePrivateKey,
        CreatedAt: nostr.Now(),
        Tags: nostr.Tags{
            {"p", customerPubkey},
            {"device-identifier", "mac", deviceIdentifier},
            {"purchased_steps", fmt.Sprintf("%d", purchasedSteps)},
        },
        Content: "",
    }
    
    // Sign with tollgate private key
    err := sessionEvent.Sign(m.config.TollgatePrivateKey)
    if err != nil {
        return nil, fmt.Errorf("failed to sign session event: %w", err)
    }
    
    return sessionEvent, nil
}
```

### Session Extension Logic (Merchant)
```go
func (m *Merchant) PurchaseSession(paymentEvent nostr.Event) (*nostr.Event, error) {
    // Process payment first
    paymentToken := extractPaymentToken(paymentEvent)
    amountAfterSwap, err := m.tollwallet.Receive(paymentToken)
    if err != nil {
        return nil, fmt.Errorf("payment processing failed: %w", err)
    }
    
    // Calculate purchased steps
    purchasedSteps := amountAfterSwap * 60000 / m.config.PricePerMinute // milliseconds
    
    // Check for existing session
    customerPubkey := paymentEvent.PubKey
    existingSession, err := m.getLatestSession(customerPubkey)
    
    var sessionEvent *nostr.Event
    if existingSession != nil {
        // Extend existing session
        sessionEvent, err = m.extendSessionEvent(existingSession, purchasedSteps)
    } else {
        // Create new session
        sessionEvent, err = m.createSessionEvent(paymentEvent, purchasedSteps)
    }
    
    if err != nil {
        return nil, fmt.Errorf("session management failed: %w", err)
    }
    
    // Publish session event to relay pool
    err = m.publishEvent(sessionEvent)
    if err != nil {
        log.Printf("Warning: failed to publish session event: %v", err)
    }
    
    // Update valve with session information
    err = valve.OpenGateForSession(*sessionEvent)
    if err != nil {
        return nil, fmt.Errorf("failed to open gate for session: %w", err)
    }
    
    return sessionEvent, nil
}
```

### Session-Based Valve Operations
```go
func OpenGateForSession(sessionEvent nostr.Event, merchantConfig *config_manager.Config) error {
    // Extract information from session event
    customerPubkey := sessionEvent.PubKey
    macAddress := extractMACFromSession(sessionEvent)
    purchasedSteps := extractPurchasedSteps(sessionEvent)
    
    // Get step_size from merchant's advertisement/discovery event
    stepSizeMs, err := getStepSizeFromConfig(merchantConfig)
    if err != nil {
        return fmt.Errorf("failed to get step size: %w", err)
    }
    
    // Convert steps to duration using actual step_size
    durationMs := purchasedSteps * stepSizeMs  // total milliseconds
    durationSeconds := int64(durationMs / 1000)
    
    // Check for existing timer for this customer
    timerMutex.Lock()
    existingTimer, exists := activeTimers[customerPubkey]
    timerMutex.Unlock()
    
    if exists {
        // Extend existing session
        return ExtendSessionForCustomer(customerPubkey, sessionEvent, durationSeconds)
    } else {
        // Create new session timer
        return openGateForNewSession(customerPubkey, macAddress, durationSeconds, sessionEvent)
    }
}

// Helper function to extract step_size from merchant configuration
func getStepSizeFromConfig(config *config_manager.Config) (uint64, error) {
    // Parse the advertisement event to get step_size
    // This should match the step_size from the discovery event (kind=21021)
    // Default to 60000ms (1 minute) if not found
    return 60000, nil // TODO: Parse from actual advertisement event
}
```

### HTTP Handler Updates
```go
func handleRootPost(w http.ResponseWriter, r *http.Request) {
    // Parse payment event from request body
    body, err := io.ReadAll(r.Body)
    if err != nil {
        log.Println("Error reading request body:", err)
        w.WriteHeader(http.StatusInternalServerError)
        return
    }
    
    var paymentEvent nostr.Event
    err = json.Unmarshal(body, &paymentEvent)
    if err != nil {
        log.Println("Error parsing payment event:", err)
        // Send notice event to customer about invalid event
        noticeEvent, _ := merchantInstance.CreateNoticeEvent("error", "invalid-event", "Failed to parse payment event", "")
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusBadRequest)
        json.NewEncoder(w).Encode(noticeEvent)
        return
    }
    
    // Verify event signature
    ok, err := paymentEvent.CheckSignature()
    if err != nil || !ok {
        log.Println("Invalid signature for payment event:", err)
        // Send notice event about signature failure
        noticeEvent, _ := merchantInstance.CreateNoticeEvent("error", "invalid-event", "Invalid event signature", paymentEvent.PubKey)
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusBadRequest)
        json.NewEncoder(w).Encode(noticeEvent)
        return
    }
    
    // Process payment and get session
    sessionEvent, err := merchantInstance.PurchaseSession(paymentEvent)
    if err != nil {
        log.Printf("Payment processing failed: %v", err)
        // Send notice event about payment failure
        noticeEvent, _ := merchantInstance.CreateNoticeEvent("error", "payment-err", fmt.Sprintf("Payment processing failed: %v", err), paymentEvent.PubKey)
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusPaymentRequired)
        json.NewEncoder(w).Encode(noticeEvent)
        return
    }
    
    // Return session event as JSON (TIP-03 compliance)
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    
    err = json.NewEncoder(w).Encode(sessionEvent)
    if err != nil {
        log.Printf("Error encoding session response: %v", err)
    }
}
```

### Notice Event Creation (Merchant)
```go
func (m *Merchant) createNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error) {
    noticeEvent := &nostr.Event{
        Kind:      21023,
        PubKey:    m.config.TollgatePrivateKey,
        CreatedAt: nostr.Now(),
        Tags: nostr.Tags{
            {"level", level},
            {"code", code},
        },
        Content: message,
    }
    
    // Add customer pubkey if provided
    if customerPubkey != "" {
        noticeEvent.Tags = append(noticeEvent.Tags, nostr.Tag{"p", customerPubkey})
    }
    
    // Sign with tollgate private key
    err := noticeEvent.Sign(m.config.TollgatePrivateKey)
    if err != nil {
        return nil, fmt.Errorf("failed to sign notice event: %w", err)
    }
    
    return noticeEvent, nil
}
```

## Testing Strategy

### Unit Tests
- Merchant session creation and extension
- Valve timer management for sessions
- Session event parsing and validation
- Relay pool operations for session storage

### Integration Tests
- Complete payment-to-session flow
- Session extension scenarios
- Cleanup and expiration handling
- Concurrent session operations

### Protocol Compliance Tests
- Verify session event format matches TIP-01
- Verify HTTP responses match TIP-03
- Test various payment scenarios and error conditions

## Breaking Changes

### API Changes
- `Merchant.PurchaseSession()` signature changed
- HTTP response format changed from status object to session event
- Internal timer management now session-based instead of MAC-based only

### Migration Strategy
- No backwards compatibility maintained (as approved)
- Update any existing integrations to expect session event responses
- Update client implementations to handle new response format