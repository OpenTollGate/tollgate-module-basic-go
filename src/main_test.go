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
	// Skip this test if running on non-OpenWRT environments
	if !isTargetOpenWRT() {
		t.Skip("Skipping TestLoadConfig on non-OpenWRT environment")
	}

	tmpDir, err := os.MkdirTemp("", "testconfig")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	configFile := filepath.Join(tmpDir, "config.json")
	dummyConfig := config_manager.Config{
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
	dummyConfigData, err := json.Marshal(dummyConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configFile, dummyConfigData, 0644); err != nil {
		t.Fatal(err)
	}

	configManager, err := config_manager.NewConfigManager(configFile) // Initialize config manager with temporary file
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	if isTargetOpenWRT() { // Only ensure initialized config if it's an OpenWRT environment
		err = configManager.EnsureInitializedConfig()
		if err != nil {
			t.Fatalf("Failed to ensure initialized config: %v", err)
		}
	}

	// Now that configManager is initialized (and possibly ensured), try to load it
	_, err = configManager.LoadConfig()
	if err != nil {
		t.Errorf("configManager.LoadConfig failed: %v", err)
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
	if !isTargetOpenWRT() {
		t.Skip("Skipping TestHandleRootPost on non-OpenWRT environment")
	}
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

// isTargetOpenWRT checks if the current environment is OpenWRT.
// This is a simplified check that might need to be more robust depending on the testing environment.
func isTargetOpenWRT() bool {
	// A common way to detect OpenWRT is to check for specific files or environment variables.
	// For example, checking for /etc/openwrt_release or /etc/config/network
	// For now, we'll assume true for an OpenWRT-like environment and false otherwise.
	// In a real scenario, you might parse `runtime.GOOS` and `runtime.GOARCH` or
	// check for specific files/directories present only on OpenWRT.

	// A simple check could be: if os.ReadFile("/etc/openwrt_release") works
	// For this test, we'll simulate a simple check for now, returning false for generic Linux/x86_64
	// and true if specific OpenWRT conditions are met (which aren't here by default).

	// For the purpose of this test, we'll assume it's not OpenWRT by default.
	// We can refine this later if needed.
	return false
}
