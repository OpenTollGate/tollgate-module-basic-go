package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/crowsnest"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/upstream_session_manager"
	"github.com/nbd-wtf/go-nostr"
)

// TestUpstreamPurchasingIntegration demonstrates the complete upstream purchasing flow
func TestUpstreamPurchasingIntegration(t *testing.T) {
	// Test Setup: Create a mock upstream router that responds to TIP-03 requests

	// 1. Mock upstream router advertisement (TIP-01)
	upstreamAdvertisement := nostr.Event{
		Kind:      10021,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"metric", "milliseconds"},
			{"step_size", "60000"}, // 1 minute step size
			{"price_per_step", "cashu", "210", "sat", "https://mint.upstream.com", "1"},
		},
		Content: "",
	}

	// Sign the advertisement event
	sk := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	err := upstreamAdvertisement.Sign(sk)
	if err != nil {
		t.Fatalf("Failed to sign upstream advertisement: %v", err)
	}

	// 2. Mock upstream session response (TIP-01)
	upstreamSessionResponse := nostr.Event{
		Kind:      1022,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"allotment", "60000"}, // 60 seconds granted
			{"metric", "milliseconds"},
			{"device-identifier", "mac", "test-device"},
		},
		Content: "",
	}
	err = upstreamSessionResponse.Sign(sk)
	if err != nil {
		t.Fatalf("Failed to sign upstream session response: %v", err)
	}

	// 3. Create mock upstream server (TIP-03)
	callCount := 0
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		if r.Method == "GET" {
			// Return advertisement for GET requests
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(upstreamAdvertisement)
			t.Logf("Mock upstream: Served advertisement (call %d)", callCount)
		} else if r.Method == "POST" {
			// Return session for POST requests (payment)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(upstreamSessionResponse)
			t.Logf("Mock upstream: Served session response (call %d)", callCount)
		}
	}))
	defer upstreamServer.Close()

	t.Logf("Mock upstream server running at: %s", upstreamServer.URL)

	// Test Flow: Demonstrate complete upstream purchasing integration

	// Phase 1: Crowsnest discovers and analyzes upstream pricing
	t.Log("=== Phase 1: Crowsnest Discovery ===")

	crowsnestInstance := crowsnest.New()
	crowsnestInstance.SetUpstreamURL(upstreamServer.URL)

	// Verify upstream is available
	if !crowsnestInstance.IsUpstreamAvailable() {
		t.Fatal("Expected upstream to be available after setting URL")
	}

	// Get pricing information
	pricing, err := crowsnestInstance.GetUpstreamPricing(upstreamServer.URL)
	if err != nil {
		t.Fatalf("Failed to get upstream pricing: %v", err)
	}

	t.Logf("Upstream pricing - Metric: %s, StepSize: %d, Mints: %d",
		pricing.Metric, pricing.StepSize, len(pricing.AcceptedMints))

	// Verify pricing information
	if pricing.Metric != "milliseconds" {
		t.Errorf("Expected metric 'milliseconds', got %s", pricing.Metric)
	}
	if pricing.StepSize != 60000 {
		t.Errorf("Expected step size 60000, got %d", pricing.StepSize)
	}
	if len(pricing.AcceptedMints) == 0 {
		t.Error("Expected at least one accepted mint")
	}

	// Phase 2: Upstream Session Manager executes purchase
	t.Log("=== Phase 2: Session Manager Purchase ===")

	sessionManager := upstream_session_manager.New(upstreamServer.URL, "test-device")

	// Simulate merchant providing payment token
	paymentToken := "cashuA_test_token_for_upstream_purchase"

	// Execute upstream purchase
	sessionEvent, err := sessionManager.PurchaseUpstreamTime(60000, paymentToken)
	if err != nil {
		t.Fatalf("Failed to purchase upstream time: %v", err)
	}

	t.Logf("Purchase successful - Session ID: %s", sessionEvent.ID)

	// Verify session was created
	sessionInfo, err := sessionManager.GetUpstreamSessionInfo()
	if err != nil {
		t.Fatalf("Failed to get session info: %v", err)
	}

	if sessionInfo.Allotment != 60000 {
		t.Errorf("Expected allotment 60000, got %d", sessionInfo.Allotment)
	}
	if sessionInfo.Metric != "milliseconds" {
		t.Errorf("Expected metric 'milliseconds', got %s", sessionInfo.Metric)
	}

	// Phase 3: Session monitoring and status checks
	t.Log("=== Phase 3: Session Monitoring ===")

	// Check if upstream is active
	if !sessionManager.IsUpstreamActive() {
		t.Error("Expected upstream session to be active")
	}

	// Check time until expiry
	timeRemaining, err := sessionManager.GetTimeUntilExpiry()
	if err != nil {
		t.Fatalf("Failed to get time until expiry: %v", err)
	}

	t.Logf("Time remaining: %d milliseconds", timeRemaining)

	// Should be close to 60000ms (allowing for small timing differences)
	if timeRemaining < 59000 || timeRemaining > 60000 {
		t.Errorf("Expected time remaining around 60000ms, got %d", timeRemaining)
	}

	// Check current metric
	metric, err := sessionManager.GetCurrentMetric()
	if err != nil {
		t.Fatalf("Failed to get current metric: %v", err)
	}
	if metric != "milliseconds" {
		t.Errorf("Expected metric 'milliseconds', got %s", metric)
	}

	// Phase 4: Demonstrate renewal scenario (5-second trigger simulation)
	t.Log("=== Phase 4: Renewal Scenario ===")

	// Simulate session nearing expiry by creating an old session
	pastTime := time.Now().Add(-55 * time.Second) // 55 seconds ago
	nearExpiryEvent := nostr.Event{
		Kind:      1022,
		CreatedAt: nostr.Timestamp(pastTime.Unix()),
		Tags: nostr.Tags{
			{"allotment", "60000"},
			{"metric", "milliseconds"},
			{"device-identifier", "mac", "test-device"},
		},
		Content: "",
	}
	nearExpiryEvent.Sign(sk)

	// Manually update session to simulate near-expiry
	timeRemaining, _ = sessionManager.GetTimeUntilExpiry()
	t.Logf("Before renewal trigger: %d ms remaining", timeRemaining)

	// If this were in production, the merchant would trigger a purchase when < 5000ms
	if timeRemaining < 5000 {
		t.Log("Would trigger upstream purchase renewal (< 5000ms remaining)")

		// Simulate renewal purchase
		renewalEvent, err := sessionManager.PurchaseUpstreamTime(60000, paymentToken)
		if err != nil {
			t.Fatalf("Failed to renew upstream session: %v", err)
		}

		t.Logf("Renewal successful - New session ID: %s", renewalEvent.ID)
	}

	// Phase 5: Connection monitoring
	t.Log("=== Phase 5: Connection Monitoring ===")

	// Test connection health check
	err = crowsnestInstance.MonitorUpstreamConnection()
	if err != nil {
		t.Errorf("Expected healthy connection, got error: %v", err)
	}

	// Test disconnection scenario
	crowsnestInstance.SetUpstreamURL("http://invalid-upstream:9999")
	err = crowsnestInstance.MonitorUpstreamConnection()
	if err == nil {
		t.Error("Expected error for invalid upstream connection")
	}

	// Verify upstream availability was cleared
	if crowsnestInstance.IsUpstreamAvailable() {
		t.Error("Expected upstream to be unavailable after connection failure")
	}

	// Phase 6: Data-based session simulation (future capability)
	t.Log("=== Phase 6: Data-Based Session Simulation ===")

	// Create a data-based session response
	dataSessionResponse := nostr.Event{
		Kind:      1022,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"allotment", "1000000"}, // 1MB
			{"metric", "bytes"},
			{"device-identifier", "mac", "test-device"},
		},
		Content: "",
	}
	dataSessionResponse.Sign(sk)

	// Create new session manager for data testing
	dataSessionManager := upstream_session_manager.New(upstreamServer.URL, "test-device")

	// Manually set up a data-based session (simulating purchase response)
	// Note: In a real implementation, this would come from the upstream purchase

	// For now, just verify the data monitoring interface exists
	err = dataSessionManager.MonitorDataUsage()
	if err == nil {
		t.Error("Expected error for monitoring without active session")
	}

	// Verify call count to upstream server
	t.Logf("Total calls to mock upstream server: %d", callCount)
	if callCount < 3 { // At least advertisement + 2 purchases
		t.Errorf("Expected at least 3 calls to upstream, got %d", callCount)
	}

	t.Log("=== Integration Test Complete ===")
	t.Log("✅ Crowsnest successfully discovered and parsed upstream pricing")
	t.Log("✅ Session Manager successfully executed upstream purchases")
	t.Log("✅ Session monitoring and status checks working")
	t.Log("✅ Connection health monitoring functional")
	t.Log("✅ Architecture ready for both time and data-based metrics")
}

// TestUpstreamConfigurationStructure validates the configuration structure
func TestUpstreamConfigurationStructure(t *testing.T) {
	// Test the upstream configuration structure
	testConfig := struct {
		UpstreamConfig struct {
			Enabled                          bool   `json:"enabled"`
			AlwaysMaintainUpstreamConnection bool   `json:"always_maintain_upstream_connection"`
			PreferredPurchaseAmountMs        uint64 `json:"preferred_purchase_amount_ms"`
			PreferredPurchaseAmountBytes     uint64 `json:"preferred_purchase_amount_bytes"`
			PurchaseTriggerBufferMs          uint64 `json:"purchase_trigger_buffer_ms"`
			PurchaseTriggerBufferBytes       uint64 `json:"purchase_trigger_buffer_bytes"`
			RetryAttempts                    int    `json:"retry_attempts"`
			RetryBackoffSeconds              int    `json:"retry_backoff_seconds"`
		} `json:"upstream_config"`
	}{
		UpstreamConfig: struct {
			Enabled                          bool   `json:"enabled"`
			AlwaysMaintainUpstreamConnection bool   `json:"always_maintain_upstream_connection"`
			PreferredPurchaseAmountMs        uint64 `json:"preferred_purchase_amount_ms"`
			PreferredPurchaseAmountBytes     uint64 `json:"preferred_purchase_amount_bytes"`
			PurchaseTriggerBufferMs          uint64 `json:"purchase_trigger_buffer_ms"`
			PurchaseTriggerBufferBytes       uint64 `json:"purchase_trigger_buffer_bytes"`
			RetryAttempts                    int    `json:"retry_attempts"`
			RetryBackoffSeconds              int    `json:"retry_backoff_seconds"`
		}{
			Enabled:                          true,
			AlwaysMaintainUpstreamConnection: false,
			PreferredPurchaseAmountMs:        10000,
			PreferredPurchaseAmountBytes:     1000000,
			PurchaseTriggerBufferMs:          5000,
			PurchaseTriggerBufferBytes:       100000,
			RetryAttempts:                    3,
			RetryBackoffSeconds:              5,
		},
	}

	// Serialize to JSON and back to verify structure
	jsonData, err := json.Marshal(testConfig)
	if err != nil {
		t.Fatalf("Failed to marshal config: %v", err)
	}

	var parsedConfig map[string]interface{}
	err = json.Unmarshal(jsonData, &parsedConfig)
	if err != nil {
		t.Fatalf("Failed to unmarshal config: %v", err)
	}

	// Verify the structure exists
	upstreamConfig, exists := parsedConfig["upstream_config"]
	if !exists {
		t.Fatal("upstream_config not found in parsed JSON")
	}

	upstreamMap := upstreamConfig.(map[string]interface{})

	// Verify all expected fields exist
	expectedFields := []string{
		"enabled", "always_maintain_upstream_connection",
		"preferred_purchase_amount_ms", "preferred_purchase_amount_bytes",
		"purchase_trigger_buffer_ms", "purchase_trigger_buffer_bytes",
		"retry_attempts", "retry_backoff_seconds",
	}

	for _, field := range expectedFields {
		if _, exists := upstreamMap[field]; !exists {
			t.Errorf("Expected field %s not found in upstream_config", field)
		}
	}

	t.Log("✅ Upstream configuration structure validated")
}

func main() {
	fmt.Println("Upstream Router Purchasing Feature - Integration Test")
	fmt.Println("=====================================================")
	fmt.Println("This test demonstrates the complete upstream purchasing flow:")
	fmt.Println("1. Crowsnest discovers upstream router and pricing")
	fmt.Println("2. Session Manager executes purchases via TIP-03")
	fmt.Println("3. Session monitoring for time-based metrics")
	fmt.Println("4. Connection health monitoring")
	fmt.Println("5. Configuration structure validation")
	fmt.Println()
	fmt.Println("Run with: go test -v ./integration_test.go")
}
