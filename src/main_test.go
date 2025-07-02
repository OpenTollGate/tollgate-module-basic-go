package main

import (
	"bytes"         // Added import for bytes
	"encoding/json"
	"net/http"      // Added import for net/http
	"net/http/httptest" // Added import for net/http/httptest
	"os"
	"path/filepath"
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/mock"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant" // Import the merchant package
)

// Define the nsec1 secret key for testing

// MockMerchant is a mock implementation of merchant.MerchantService
type MockMerchant struct {
	mock.Mock
}

// Ensure MockMerchant implements merchant.MerchantService
var _ merchant.MerchantService = (*MockMerchant)(nil)

func (m *MockMerchant) PurchaseSession(event nostr.Event) (*nostr.Event, error) {
	args := m.Called(event)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nostr.Event), args.Error(1)
}

func (m *MockMerchant) CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error) {
	args := m.Called(level, code, message, customerPubkey)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nostr.Event), args.Error(1)
}

func (m *MockMerchant) GetAdvertisement() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockMerchant) StartPayoutRoutine() {
	m.Called()
}

func TestLoadConfig(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "testconfig")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	configFile := filepath.Join(tmpDir, "config.json")
	config := config_manager.Config{
		TollgatePrivateKey: "test_private_key",
		AcceptedMints: []config_manager.MintConfig{
			{
				URL:                     "https://mint.minibits.cash/Bitcoin",
				MinBalance:              100,
				BalanceTolerancePercent: 10,
				PayoutIntervalSeconds:   60,
				MinPayoutAmount:         1000,
				PricePerStep:            1,
				MinPurchaseSteps:        0,
			},
			{
				URL:                     "https://mint2.nutmix.cash",
				MinBalance:              100,
				BalanceTolerancePercent: 10,
				PayoutIntervalSeconds:   60,
				MinPayoutAmount:         1000,
				PricePerStep:            1,
				MinPurchaseSteps:        0,
			},
		},
		Metric:   "milliseconds",
		StepSize: 60000,
	}

	configData, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(configFile, configData, 0644)
	if err != nil {
		t.Fatal(err)
	}

	oldConfigFile := configFile
	configFile = configFile
	defer func() { configFile = oldConfigFile }()

	configManager, err := config_manager.NewConfigManager(configFile)
	if err != nil {
		t.Errorf("Failed to create config manager: %v", err)
	}

	_, err2 := configManager.LoadConfig()
	if err2 != nil {
		t.Errorf("loadConfig failed: %v", err2)
	}
}

func TestHandleRoot(t *testing.T) {
	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	mockMerchant := new(MockMerchant)
	expectedAdvertisement := "test_advertisement_json"
	mockMerchant.On("GetAdvertisement").Return(expectedAdvertisement)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleRoot(mockMerchant, w, r)
	})
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	if rr.Body.String() != expectedAdvertisement {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expectedAdvertisement)
	}

	mockMerchant.AssertExpectations(t)
}

func TestHandleRootPost(t *testing.T) {
	// Test with correct payment event (kind 21000)
	event := nostr.Event{
		Kind: 21000, // Payment event kind
		Tags: nostr.Tags{
			nostr.Tag{"device-identifier", "mac", "00:11:22:33:44:55"}, // Added "mac" identifier
			nostr.Tag{"payment", "test_token"},
		},
		PubKey: testPublicKeyHex,
	}

	// Sign the event for testing
	err := event.Sign(testPrivateKeyHex)
	if err != nil {
		t.Fatal("Failed to sign event:", err)
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest("POST", "/", bytes.NewBuffer(eventJSON))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	mockMerchant := new(MockMerchant)
	// Expect PurchaseSession to be called and return a successful session event
	mockMerchant.On("PurchaseSession", mock.AnythingOfType("nostr.Event")).Return(&nostr.Event{Kind: 1022}, nil)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleRootPost(mockMerchant, w, r)
	})
	handler.ServeHTTP(rr, req)

	// Should return OK since merchant is mocked to return a successful session
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	mockMerchant.AssertExpectations(t)
}

// TestHandleRootPostInvalidKind tests rejection of non-payment events
func TestHandleRootPostInvalidKind(t *testing.T) {
	event := nostr.Event{
		Kind: 1022, // Session event kind (invalid for payment endpoint)
		Tags: nostr.Tags{
			nostr.Tag{"device-identifier", "mac", "00:11:22:33:44:55"},
			nostr.Tag{"payment", "test_token"},
		},
		PubKey: testPublicKeyHex,
	}

	// Sign the event
	err := event.Sign(testPrivateKeyHex)
	if err != nil {
		t.Fatal("Failed to sign event:", err)
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest("POST", "/", bytes.NewBuffer(eventJSON))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	mockMerchant := new(MockMerchant)
	// For invalid kind, PurchaseSession should NOT be called.
	// Expect CreateNoticeEvent to be called when an invalid event kind is processed
	mockMerchant.On("CreateNoticeEvent", "error", "invalid-event", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(&nostr.Event{Kind: 21023}, nil)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleRootPost(mockMerchant, w, r)
	})
	handler.ServeHTTP(rr, req)

	// Should return BadRequest due to invalid kind
	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	// Check that the response contains error about invalid kind
	var response map[string]interface{}
	err = json.Unmarshal(rr.Body.Bytes(), &response)
	if err != nil {
		t.Fatal("Failed to parse response:", err)
	}

	if response["kind"] != float64(21023) { // Notice event
		t.Errorf("Expected notice event in response")
	}

	mockMerchant.AssertExpectations(t)
}

// // TestHandleRootPostInvalidSignature tests rejection of events with invalid signatures
// func TestHandleRootPostInvalidSignature(t *testing.T) {
// 	event := nostr.Event{
// 		Kind: 21000,
// 		Tags: nostr.Tags{
// 			nostr.Tag{"device-identifier", "mac", "00:11:22:33:44:55"},
// 			nostr.Tag{"payment", "test_token"},
// 		},
// 		PubKey: testPublicKeyHex,
// 		Sig:    "invalid_signature",
// 	}

// 	eventJSON, err := json.Marshal(event)
// 	if err != nil {
// 		t.Fatal(err)
// 	}

// 	req, err := http.NewRequest("POST", "/", bytes.NewBuffer(eventJSON))
// 	if err != nil {
// 		t.Fatal(err)
// 	}
// 	req.Header.Set("Content-Type", "application/json")

// 	rr := httptest.NewRecorder()
// 	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		mockMerchant := new(MockMerchant)
// 		mockMerchant.On("PurchaseSession", mock.Anything).Return(&nostr.Event{}, nil) // This specific mock might not be strictly needed for this test, but it's good practice for consistency
// 		handleRootPost(mockMerchant, w, r)
// 	})
// 	handler.ServeHTTP(rr, req)

// 	// Should return BadRequest due to invalid signature
// 	if status := rr.Code; status != http.StatusBadRequest {
// 		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
// 	}
// }
