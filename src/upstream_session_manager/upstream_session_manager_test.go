package upstream_session_manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

func TestNew(t *testing.T) {
	usm := New("http://test.com", "test-device")
	if usm == nil {
		t.Fatal("Expected non-nil UpstreamSessionManager instance")
	}
	if usm.upstreamURL != "http://test.com" {
		t.Errorf("Expected upstream URL 'http://test.com', got %s", usm.upstreamURL)
	}
	if usm.deviceID != "test-device" {
		t.Errorf("Expected device ID 'test-device', got %s", usm.deviceID)
	}
}

func TestSetUpstreamURL(t *testing.T) {
	usm := New("", "test-device")
	testURL := "http://new-upstream.com"

	usm.SetUpstreamURL(testURL)

	if usm.upstreamURL != testURL {
		t.Errorf("Expected upstream URL %s, got %s", testURL, usm.upstreamURL)
	}
}

func TestGetUpstreamSessionInfoNoSession(t *testing.T) {
	usm := New("http://test.com", "test-device")

	_, err := usm.GetUpstreamSessionInfo()
	if err == nil {
		t.Error("Expected error when no session exists")
	}
}

func TestIsUpstreamActiveNoSession(t *testing.T) {
	usm := New("http://test.com", "test-device")

	if usm.IsUpstreamActive() {
		t.Error("Expected false when no session exists")
	}
}

func TestGetTimeUntilExpiryNoSession(t *testing.T) {
	usm := New("http://test.com", "test-device")

	_, err := usm.GetTimeUntilExpiry()
	if err == nil {
		t.Error("Expected error when no session exists")
	}
}

func TestGetBytesRemainingNoSession(t *testing.T) {
	usm := New("http://test.com", "test-device")

	_, err := usm.GetBytesRemaining()
	if err == nil {
		t.Error("Expected error when no session exists")
	}
}

func TestGetCurrentMetricNoSession(t *testing.T) {
	usm := New("http://test.com", "test-device")

	_, err := usm.GetCurrentMetric()
	if err == nil {
		t.Error("Expected error when no session exists")
	}
}

func TestUpdateSessionFromEvent(t *testing.T) {
	usm := New("http://test.com", "test-device")

	// Create test session event (time-based)
	sessionEvent := &nostr.Event{
		Kind:      1022,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"allotment", "60000"},
			{"metric", "milliseconds"},
			{"device-identifier", "mac", "test-device"},
		},
	}

	err := usm.updateSessionFromEvent(sessionEvent)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Check session was created
	session, err := usm.GetUpstreamSessionInfo()
	if err != nil {
		t.Errorf("Expected no error getting session info, got %v", err)
	}

	if session.Allotment != 60000 {
		t.Errorf("Expected allotment 60000, got %d", session.Allotment)
	}

	if session.Metric != "milliseconds" {
		t.Errorf("Expected metric 'milliseconds', got %s", session.Metric)
	}

	if session.DeviceID != "test-device" {
		t.Errorf("Expected device ID 'test-device', got %s", session.DeviceID)
	}
}

func TestUpdateSessionFromEventBytes(t *testing.T) {
	usm := New("http://test.com", "test-device")

	// Create test session event (byte-based)
	sessionEvent := &nostr.Event{
		Kind:      1022,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"allotment", "1000000"},
			{"metric", "bytes"},
			{"device-identifier", "mac", "test-device"},
		},
	}

	err := usm.updateSessionFromEvent(sessionEvent)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Check session was created
	session, err := usm.GetUpstreamSessionInfo()
	if err != nil {
		t.Errorf("Expected no error getting session info, got %v", err)
	}

	if session.Allotment != 1000000 {
		t.Errorf("Expected allotment 1000000, got %d", session.Allotment)
	}

	if session.Metric != "bytes" {
		t.Errorf("Expected metric 'bytes', got %s", session.Metric)
	}
}

func TestUpdateSessionFromEventErrors(t *testing.T) {
	usm := New("http://test.com", "test-device")

	// Test missing allotment
	sessionEvent1 := &nostr.Event{
		Kind: 1022,
		Tags: nostr.Tags{
			{"metric", "milliseconds"},
		},
	}
	err := usm.updateSessionFromEvent(sessionEvent1)
	if err == nil {
		t.Error("Expected error for missing allotment")
	}

	// Test missing metric
	sessionEvent2 := &nostr.Event{
		Kind: 1022,
		Tags: nostr.Tags{
			{"allotment", "60000"},
		},
	}
	err = usm.updateSessionFromEvent(sessionEvent2)
	if err == nil {
		t.Error("Expected error for missing metric")
	}
}

func TestIsUpstreamActiveWithTimeSession(t *testing.T) {
	usm := New("http://test.com", "test-device")

	// Create active time-based session
	sessionEvent := &nostr.Event{
		Kind:      1022,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"allotment", "60000"}, // 60 seconds
			{"metric", "milliseconds"},
			{"device-identifier", "mac", "test-device"},
		},
	}

	usm.updateSessionFromEvent(sessionEvent)

	// Should be active
	if !usm.IsUpstreamActive() {
		t.Error("Expected session to be active")
	}

	// Create expired time-based session
	pastTime := time.Now().Add(-2 * time.Hour)
	expiredEvent := &nostr.Event{
		Kind:      1022,
		CreatedAt: nostr.Timestamp(pastTime.Unix()),
		Tags: nostr.Tags{
			{"allotment", "60000"}, // 60 seconds
			{"metric", "milliseconds"},
			{"device-identifier", "mac", "test-device"},
		},
	}

	usm.updateSessionFromEvent(expiredEvent)

	// Should not be active
	if usm.IsUpstreamActive() {
		t.Error("Expected expired session to be inactive")
	}
}

func TestIsUpstreamActiveWithByteSession(t *testing.T) {
	usm := New("http://test.com", "test-device")

	// Create byte-based session
	sessionEvent := &nostr.Event{
		Kind:      1022,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"allotment", "1000000"}, // 1MB
			{"metric", "bytes"},
			{"device-identifier", "mac", "test-device"},
		},
	}

	usm.updateSessionFromEvent(sessionEvent)

	// Should be active (no bytes consumed yet)
	if !usm.IsUpstreamActive() {
		t.Error("Expected session to be active")
	}

	// Simulate full consumption
	usm.currentSession.ConsumedBytes = 1000000

	// Should not be active
	if usm.IsUpstreamActive() {
		t.Error("Expected fully consumed session to be inactive")
	}
}

func TestGetTimeUntilExpiry(t *testing.T) {
	usm := New("http://test.com", "test-device")

	// Create active time-based session
	sessionEvent := &nostr.Event{
		Kind:      1022,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"allotment", "60000"}, // 60 seconds
			{"metric", "milliseconds"},
			{"device-identifier", "mac", "test-device"},
		},
	}

	usm.updateSessionFromEvent(sessionEvent)

	remaining, err := usm.GetTimeUntilExpiry()
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Should be close to 60000ms (allowing for small timing differences)
	if remaining < 59000 || remaining > 60000 {
		t.Errorf("Expected remaining time around 60000ms, got %d", remaining)
	}

	// Test with byte-based session (should error)
	byteEvent := &nostr.Event{
		Kind:      1022,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"allotment", "1000000"},
			{"metric", "bytes"},
			{"device-identifier", "mac", "test-device"},
		},
	}

	usm.updateSessionFromEvent(byteEvent)

	_, err = usm.GetTimeUntilExpiry()
	if err == nil {
		t.Error("Expected error for byte-based session")
	}
}

func TestGetBytesRemaining(t *testing.T) {
	usm := New("http://test.com", "test-device")

	// Create byte-based session
	sessionEvent := &nostr.Event{
		Kind:      1022,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"allotment", "1000000"}, // 1MB
			{"metric", "bytes"},
			{"device-identifier", "mac", "test-device"},
		},
	}

	usm.updateSessionFromEvent(sessionEvent)

	remaining, err := usm.GetBytesRemaining()
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if remaining != 1000000 {
		t.Errorf("Expected 1000000 bytes remaining, got %d", remaining)
	}

	// Simulate some consumption
	usm.currentSession.ConsumedBytes = 300000

	remaining, err = usm.GetBytesRemaining()
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if remaining != 700000 {
		t.Errorf("Expected 700000 bytes remaining, got %d", remaining)
	}

	// Test with time-based session (should error)
	timeEvent := &nostr.Event{
		Kind:      1022,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"allotment", "60000"},
			{"metric", "milliseconds"},
			{"device-identifier", "mac", "test-device"},
		},
	}

	usm.updateSessionFromEvent(timeEvent)

	_, err = usm.GetBytesRemaining()
	if err == nil {
		t.Error("Expected error for time-based session")
	}
}

func TestClearSession(t *testing.T) {
	usm := New("http://test.com", "test-device")

	// Create session
	sessionEvent := &nostr.Event{
		Kind:      1022,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"allotment", "60000"},
			{"metric", "milliseconds"},
			{"device-identifier", "mac", "test-device"},
		},
	}

	usm.updateSessionFromEvent(sessionEvent)

	// Verify session exists
	if !usm.IsUpstreamActive() {
		t.Error("Expected session to be active before clearing")
	}

	// Clear session
	usm.ClearSession()

	// Verify session is cleared
	if usm.IsUpstreamActive() {
		t.Error("Expected session to be inactive after clearing")
	}

	_, err := usm.GetUpstreamSessionInfo()
	if err == nil {
		t.Error("Expected error when getting info for cleared session")
	}
}

func TestSendPaymentToUpstream(t *testing.T) {
	// Create test session event response
	testSessionEvent := nostr.Event{
		Kind:      1022,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"allotment", "60000"},
			{"metric", "milliseconds"},
			{"device-identifier", "mac", "test-device"},
		},
		Content: "",
	}

	// Sign the event with a test private key
	sk := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	err := testSessionEvent.Sign(sk)
	if err != nil {
		t.Fatalf("Failed to sign test event: %v", err)
	}

	// Create test server that returns session event
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(testSessionEvent)
	}))
	defer server.Close()

	usm := New(server.URL, "test-device")

	// Create test payment event
	paymentEvent := nostr.Event{
		Kind:      21000,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"device-identifier", "mac", "test-device"},
			{"payment", "test-token"},
		},
		Content: "",
	}

	// Test successful payment
	sessionEvent, err := usm.sendPaymentToUpstream(paymentEvent)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if sessionEvent.Kind != 1022 {
		t.Errorf("Expected session event kind 1022, got %d", sessionEvent.Kind)
	}
}

func TestSendPaymentToUpstreamErrors(t *testing.T) {
	// Test server returning error status
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Payment failed"))
	}))
	defer server.Close()

	usm := New(server.URL, "test-device")

	paymentEvent := nostr.Event{
		Kind:      21000,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"device-identifier", "mac", "test-device"},
			{"payment", "test-token"},
		},
		Content: "",
	}

	_, err := usm.sendPaymentToUpstream(paymentEvent)
	if err == nil {
		t.Error("Expected error for failed payment")
	}

	// Test server returning invalid JSON
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("invalid json"))
	}))
	defer server2.Close()

	usm.SetUpstreamURL(server2.URL)

	_, err = usm.sendPaymentToUpstream(paymentEvent)
	if err == nil {
		t.Error("Expected error for invalid JSON response")
	}
}
