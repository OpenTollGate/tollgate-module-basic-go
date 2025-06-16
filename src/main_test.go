package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/nbd-wtf/go-nostr"
)

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
	handler := http.HandlerFunc(corsMiddleware(handleRoot))
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}
}

func TestHandleRootPost(t *testing.T) {
	// Test with correct payment event (kind 21000) but without merchant dependency
	event := nostr.Event{
		Kind: 21000, // Payment event kind
		Tags: nostr.Tags{
			nostr.Tag{"device-identifier", "mac", "00:11:22:33:44:55"}, // Added "mac" identifier
			nostr.Tag{"payment", "test_token"},
		},
		PubKey: "02a7451395735369f2ecdfc829c0f774e88ef1303dfe5b2f04dbaab30a535dfdd6",
	}

	// Sign the event for testing
	err := event.Sign("nsec1j8ee8lzkjre3tm6sn9gc4w0v24vy0k5fkw3c2xpn9vpy8vygm9yq2a0zqz")
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
	handler := http.HandlerFunc(handleRootPost)
	handler.ServeHTTP(rr, req)

	// Should return BadRequest due to missing merchant instance (but signature should be valid)
	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}
}

// TestHandleRootPostInvalidKind tests rejection of non-payment events
func TestHandleRootPostInvalidKind(t *testing.T) {
	event := nostr.Event{
		Kind: 1022, // Session event kind (invalid for payment endpoint)
		Tags: nostr.Tags{
			nostr.Tag{"device-identifier", "mac", "00:11:22:33:44:55"},
			nostr.Tag{"payment", "test_token"},
		},
		PubKey: "02a7451395735369f2ecdfc829c0f774e88ef1303dfe5b2f04dbaab30a535dfdd6",
	}

	// Sign the event
	err := event.Sign("nsec1j8ee8lzkjre3tm6sn9gc4w0v24vy0k5fkw3c2xpn9vpy8vygm9yq2a0zqz")
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
	handler := http.HandlerFunc(handleRootPost)
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
}

// TestHandleRootPostInvalidSignature tests rejection of events with invalid signatures
func TestHandleRootPostInvalidSignature(t *testing.T) {
	event := nostr.Event{
		Kind: 21000,
		Tags: nostr.Tags{
			nostr.Tag{"device-identifier", "mac", "00:11:22:33:44:55"},
			nostr.Tag{"payment", "test_token"},
		},
		PubKey: "02a7451395735369f2ecdfc829c0f774e88ef1303dfe5b2f04dbaab30a535dfdd6",
		Sig:    "invalid_signature",
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
	handler := http.HandlerFunc(handleRootPost)
	handler.ServeHTTP(rr, req)

	// Should return BadRequest due to invalid signature
	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}
}
