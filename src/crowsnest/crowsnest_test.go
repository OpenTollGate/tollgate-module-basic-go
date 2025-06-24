package crowsnest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

func TestNew(t *testing.T) {
	c := New()
	if c == nil {
		t.Fatal("Expected non-nil Crowsnest instance")
	}
	if c.discoveryInterval != 30*time.Second {
		t.Errorf("Expected discovery interval of 30s, got %v", c.discoveryInterval)
	}
}

func TestSetUpstreamURL(t *testing.T) {
	c := New()
	testURL := "http://192.168.1.1:2121"

	c.SetUpstreamURL(testURL)

	if c.currentUpstreamURL != testURL {
		t.Errorf("Expected upstream URL %s, got %s", testURL, c.currentUpstreamURL)
	}

	if !c.IsUpstreamAvailable() {
		t.Error("Expected upstream to be available after setting URL")
	}
}

func TestDiscoverUpstreamRouter(t *testing.T) {
	c := New()

	// Test when no upstream is set
	_, err := c.DiscoverUpstreamRouter()
	if err == nil {
		t.Error("Expected error when no upstream is configured")
	}

	// Test when upstream is set
	testURL := "http://192.168.1.1:2121"
	c.SetUpstreamURL(testURL)

	url, err := c.DiscoverUpstreamRouter()
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if url != testURL {
		t.Errorf("Expected URL %s, got %s", testURL, url)
	}
}

func TestGetUpstreamAdvertisement(t *testing.T) {
	// Create a test event
	testEvent := nostr.Event{
		Kind:      10021,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"metric", "milliseconds"},
			{"step_size", "60000"},
			{"price_per_step", "cashu", "210", "sat", "https://mint.test.com", "1"},
		},
		Content: "",
	}

	// Sign the event with a test private key
	sk := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	err := testEvent.Sign(sk)
	if err != nil {
		t.Fatalf("Failed to sign test event: %v", err)
	}

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("Expected GET request, got %s", r.Method)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(testEvent)
	}))
	defer server.Close()

	c := New()

	// Test successful advertisement fetch
	event, err := c.GetUpstreamAdvertisement(server.URL)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if event.Kind != 10021 {
		t.Errorf("Expected kind 10021, got %d", event.Kind)
	}
}

func TestGetUpstreamAdvertisementErrors(t *testing.T) {
	c := New()

	// Test empty URL
	_, err := c.GetUpstreamAdvertisement("")
	if err == nil {
		t.Error("Expected error for empty URL")
	}

	// Test invalid URL
	_, err = c.GetUpstreamAdvertisement("http://invalid-url-that-does-not-exist:9999")
	if err == nil {
		t.Error("Expected error for invalid URL")
	}

	// Test server returning wrong status
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, err = c.GetUpstreamAdvertisement(server.URL)
	if err == nil {
		t.Error("Expected error for 404 status")
	}

	// Test server returning invalid JSON
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("invalid json"))
	}))
	defer server2.Close()

	_, err = c.GetUpstreamAdvertisement(server2.URL)
	if err == nil {
		t.Error("Expected error for invalid JSON")
	}

	// Test server returning wrong event kind
	wrongEvent := nostr.Event{
		Kind:      1000, // Wrong kind
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Content:   "",
	}
	sk := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	wrongEvent.Sign(sk)

	server3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(wrongEvent)
	}))
	defer server3.Close()

	_, err = c.GetUpstreamAdvertisement(server3.URL)
	if err == nil {
		t.Error("Expected error for wrong event kind")
	}
}

func TestParseAdvertisementToPricing(t *testing.T) {
	c := New()

	// Create test event with pricing information
	event := &nostr.Event{
		Kind: 10021,
		Tags: nostr.Tags{
			{"metric", "milliseconds"},
			{"step_size", "60000"},
			{"price_per_step", "cashu", "210", "sat", "https://mint1.test.com", "1"},
			{"price_per_step", "cashu", "210", "sat", "https://mint2.test.com", "2"},
		},
	}

	pricing, err := c.parseAdvertisementToPricing(event)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Check metric
	if pricing.Metric != "milliseconds" {
		t.Errorf("Expected metric 'milliseconds', got %s", pricing.Metric)
	}

	// Check step size
	if pricing.StepSize != 60000 {
		t.Errorf("Expected step size 60000, got %d", pricing.StepSize)
	}

	// Check accepted mints
	if len(pricing.AcceptedMints) != 2 {
		t.Errorf("Expected 2 accepted mints, got %d", len(pricing.AcceptedMints))
	}

	// Check price per step
	if pricing.PricePerStep["https://mint1.test.com"] != 210 {
		t.Errorf("Expected price 210 for mint1, got %d", pricing.PricePerStep["https://mint1.test.com"])
	}

	// Check price unit
	if pricing.PriceUnit["https://mint1.test.com"] != "sat" {
		t.Errorf("Expected unit 'sat' for mint1, got %s", pricing.PriceUnit["https://mint1.test.com"])
	}

	// Check min purchase steps
	if pricing.MinPurchaseSteps["https://mint1.test.com"] != 1 {
		t.Errorf("Expected min steps 1 for mint1, got %d", pricing.MinPurchaseSteps["https://mint1.test.com"])
	}
	if pricing.MinPurchaseSteps["https://mint2.test.com"] != 2 {
		t.Errorf("Expected min steps 2 for mint2, got %d", pricing.MinPurchaseSteps["https://mint2.test.com"])
	}
}

func TestParseAdvertisementToPricingErrors(t *testing.T) {
	c := New()

	// Test missing metric
	event1 := &nostr.Event{
		Kind: 10021,
		Tags: nostr.Tags{
			{"step_size", "60000"},
		},
	}
	_, err := c.parseAdvertisementToPricing(event1)
	if err == nil {
		t.Error("Expected error for missing metric")
	}

	// Test missing step_size
	event2 := &nostr.Event{
		Kind: 10021,
		Tags: nostr.Tags{
			{"metric", "milliseconds"},
		},
	}
	_, err = c.parseAdvertisementToPricing(event2)
	if err == nil {
		t.Error("Expected error for missing step_size")
	}

	// Test missing price_per_step
	event3 := &nostr.Event{
		Kind: 10021,
		Tags: nostr.Tags{
			{"metric", "milliseconds"},
			{"step_size", "60000"},
		},
	}
	_, err = c.parseAdvertisementToPricing(event3)
	if err == nil {
		t.Error("Expected error for missing price_per_step")
	}
}

func TestGetUpstreamPricing(t *testing.T) {
	// Create a test event with valid pricing
	testEvent := nostr.Event{
		Kind:      10021,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"metric", "milliseconds"},
			{"step_size", "60000"},
			{"price_per_step", "cashu", "210", "sat", "https://mint.test.com", "1"},
		},
		Content: "",
	}

	// Sign the event
	sk := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	err := testEvent.Sign(sk)
	if err != nil {
		t.Fatalf("Failed to sign test event: %v", err)
	}

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(testEvent)
	}))
	defer server.Close()

	c := New()

	// Test successful pricing fetch
	pricing, err := c.GetUpstreamPricing(server.URL)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if pricing.Metric != "milliseconds" {
		t.Errorf("Expected metric 'milliseconds', got %s", pricing.Metric)
	}

	if len(pricing.AcceptedMints) != 1 {
		t.Errorf("Expected 1 accepted mint, got %d", len(pricing.AcceptedMints))
	}
}

func TestMonitorUpstreamConnection(t *testing.T) {
	c := New()

	// Test with no upstream URL
	err := c.MonitorUpstreamConnection()
	if err == nil {
		t.Error("Expected error when no upstream URL is configured")
	}

	// Test with valid upstream
	testEvent := nostr.Event{
		Kind:      10021,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"metric", "milliseconds"},
			{"step_size", "60000"},
		},
		Content: "",
	}

	sk := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	testEvent.Sign(sk)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(testEvent)
	}))
	defer server.Close()

	c.SetUpstreamURL(server.URL)

	err = c.MonitorUpstreamConnection()
	if err != nil {
		t.Errorf("Expected no error for healthy connection, got %v", err)
	}

	// Test with failing upstream
	c.SetUpstreamURL("http://invalid-url:9999")
	err = c.MonitorUpstreamConnection()
	if err == nil {
		t.Error("Expected error for failed connection")
	}

	// Check that upstream URL was cleared
	if c.IsUpstreamAvailable() {
		t.Error("Expected upstream to be unavailable after connection failure")
	}
}
