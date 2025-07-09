package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/nbd-wtf/go-nostr"
)

var testConfigDir string

func TestMain(m *testing.M) {
	// Create a temporary directory for test configuration files
	tmpDir, err := os.MkdirTemp("", "tollgate_test_config")
	if err != nil {
		panic(err)
	}
	testConfigDir = tmpDir
	// Set the environment variable that main.go's init() will read
	os.Setenv("TOLLGATE_TEST_CONFIG_DIR", testConfigDir)

	// Run all tests
	code := m.Run()

	// Clean up the temporary directory
	os.RemoveAll(tmpDir)
	os.Unsetenv("TOLLGATE_TEST_CONFIG_DIR")

	os.Exit(code)
}

func TestLoadConfig(t *testing.T) {
	// Use the temporary directory created by TestMain
	tmpDir := testConfigDir

	configFile := filepath.Join(tmpDir, "config.json")
	config := config_manager.Config{
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

	// Create dummy install.json
	installFile := filepath.Join(tmpDir, "install.json")
	installConfig := config_manager.InstallConfig{
		IPAddressRandomized: true,
		DownloadTimestamp:   123456789,
	}
	installData, err := json.Marshal(installConfig)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(installFile, installData, 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Create dummy identities.json
	identitiesFile := filepath.Join(tmpDir, "identities.json")
	identitiesConfig := config_manager.IdentitiesConfig{
		OwnedIdentities: []config_manager.OwnedIdentity{
			{
				Name:       "merchant",
				PrivateKey: "test-merchant-private-key",
			},
		},
		PublicIdentities: []config_manager.PublicIdentity{
			{
				Name:   "trusted_maintainer_1",
				PubKey: "test-trusted-maintainer-pubkey",
			},
		},
	}
	identitiesData, err := json.Marshal(identitiesConfig)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(identitiesFile, identitiesData, 0644)
	if err != nil {
		t.Fatal(err)
	}

	// The rest of the test logic remains the same, as the init() function in main.go
	// will now correctly pick up the temporary paths via TOLLGATE_TEST_CONFIG_DIR.
	// No need to explicitly call NewConfigManager with these paths here,
	// as the global configManager will be initialized by main's init()
	// using the temporary paths.

	// Since we are now testing the full init() process, we need to ensure
	// that the global configManager is correctly initialized.
	// The TestLoadConfig is likely testing the behavior of the main application's
	// config loading, so we rely on main.init() to set up the global configManager.

	// We can add assertions here to check the state of the globally initialized
	// configManager after main.init() has run.
	if configManager == nil {
		t.Fatalf("Global configManager is nil after main.init()")
	}

	loadedMainConfig := configManager.GetConfig()
	if loadedMainConfig == nil {
		t.Fatalf("Global mainConfig is nil after main.init()")
	}

	loadedInstallConfig := configManager.GetInstallConfig()
	if loadedInstallConfig == nil {
		t.Fatalf("Global installConfig is nil after main.init()")
	}

	loadedIdentitiesConfig := configManager.GetIdentities()
	if loadedIdentitiesConfig == nil {
		t.Fatalf("Global identitiesConfig is nil after main.init()")
	}

	// Additional assertions can be added here to verify the content of the loaded configs.
	assert.Equal(t, config.AcceptedMints[0].URL, loadedMainConfig.AcceptedMints[0].URL)
	assert.Equal(t, config.Metric, loadedMainConfig.Metric)
	assert.Equal(t, installConfig.IPAddressRandomized, loadedInstallConfig.IPAddressRandomized)
	assert.Equal(t, installConfig.DownloadTimestamp, loadedInstallConfig.DownloadTimestamp)
	assert.Equal(t, identitiesConfig.OwnedIdentities[0].Name, loadedIdentitiesConfig.OwnedIdentities[0].Name)
	assert.Equal(t, identitiesConfig.PublicIdentities[0].Name, loadedIdentitiesConfig.PublicIdentities[0].Name)
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
