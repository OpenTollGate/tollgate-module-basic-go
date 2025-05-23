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
				Url:                     "https://mint.minibits.cash/Bitcoin",
				MinBalance:              100,
				BalanceTolerancePercent: 10,
				PayoutIntervalSeconds:   60,
				MinPayoutAmount:         1000,
			},
			{
				Url:                     "https://mint2.nutmix.cash",
				MinBalance:              100,
				BalanceTolerancePercent: 10,
				PayoutIntervalSeconds:   60,
				MinPayoutAmount:         1000,
			},
		},
		PricePerMinute: 1,
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
	event := nostr.Event{
		Kind: 21022,
		Tags: nostr.Tags{
			nostr.Tag{"device-identifier", "", "00:11:22:33:44:55"},
			nostr.Tag{"payment", "test_token"},
		},
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

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}
}
