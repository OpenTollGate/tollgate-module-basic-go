package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"encoding/hex"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/btcsuite/btcd/btcutil/bech32"
	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"log"
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

	// Create dummy janitor.json
	janitorFile := filepath.Join(tmpDir, "janitor.json")
	janitorConfig := config_manager.JanitorConfig{
		ConfigVersion:       "v0.0.2",
		PackagePath:         "false",
		IPAddressRandomized: false,
		InstallTimestamp:    0,
		DownloadTimestamp:   0,
		ReleaseChannel:      "stable",
		// EnsureDefaultTimestamp is set by time.Now().Unix() in NewDefaultInstallConfig()
		// We don't need to explicitly set it here for the test to pass,
		// as long as we're testing the loading of the *other* fields.
		InstalledVersion: "0.0.0",
	}
	janitorData, err := json.Marshal(janitorConfig)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(janitorFile, janitorData, 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Create dummy identities.json
	identitiesFile := filepath.Join(tmpDir, "identities.json")
	identitiesConfig := config_manager.IdentitiesConfig{
		OwnedIdentities: []config_manager.OwnedIdentity{
			{
				Name:       "merchant",
				PrivateKey: "e71fa3f07bea377a40ae2270aad2ab26c57b9929c46d16e76635e47cdbcba5da", // Default from NewDefaultIdentitiesConfig
			},
		},
		PublicIdentities: []config_manager.PublicIdentity{
			{
				Name:             "developer",
				LightningAddress: "tollgate@minibits.cash",
			},
			{
				Name:   "trusted_maintainer_1",
				PubKey: "5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a",
			},
			{
				Name:             "owner",
				PubKey:           "[on_setup]", // Default from NewDefaultIdentitiesConfig
				LightningAddress: "tollgate@minibits.cash",
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

	loadedJanitorConfig := configManager.GetJanitorConfig()
	if loadedJanitorConfig == nil {
		t.Fatalf("Global janitorConfig is nil after main.init()")
	}

	loadedIdentitiesConfig := configManager.GetIdentities()
	if loadedIdentitiesConfig == nil {
		t.Fatalf("Global identitiesConfig is nil after main.init()")
	}

	// Additional assertions can be added here to verify the content of the loaded configs.
	assert.Equal(t, config.AcceptedMints[0].URL, loadedMainConfig.AcceptedMints[0].URL)
	assert.Equal(t, config.Metric, loadedMainConfig.Metric)
	assert.Equal(t, janitorConfig.IPAddressRandomized, loadedJanitorConfig.IPAddressRandomized)
	assert.Equal(t, janitorConfig.DownloadTimestamp, loadedJanitorConfig.DownloadTimestamp)
	assert.Equal(t, identitiesConfig.OwnedIdentities[0].Name, loadedIdentitiesConfig.OwnedIdentities[0].Name)
	assert.Equal(t, identitiesConfig.PublicIdentities[1].Name, loadedIdentitiesConfig.PublicIdentities[1].Name) // Change index to 1 for "trusted_maintainer_1"
	assert.Equal(t, identitiesConfig.PublicIdentities[1].PubKey, loadedIdentitiesConfig.PublicIdentities[1].PubKey)
	assert.Equal(t, identitiesConfig.PublicIdentities[0].LightningAddress, loadedIdentitiesConfig.PublicIdentities[0].LightningAddress) // Check developer's lightning address
}

func TestHandleRoot(t *testing.T) {
	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(CorsMiddleware(HandleRoot))
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

	// Private key for testing (hex encoded)
	// e71fa3f07bea377a40ae2270aad2ab26c57b9929c46d16e76635e47cdbcba5da
	testPrivateKeyBech32 := "nsec1uu068urmagmh5s9wyfc2454tymzhhxffc3k3demxxhj8ek7t5hdqkfsm0w"
	_, data, err := bech32.Decode(testPrivateKeyBech32)
	if err != nil {
		log.Fatalf("Failed to decode test private key (bech32): %v", err)
	}
	converted, err := bech32.ConvertBits(data, 5, 8, false)
	if err != nil {
		log.Fatalf("Failed to convert bits for test private key: %v", err)
	}
	testPrivateKeyHex := hex.EncodeToString(converted)

	// Derive public key from private key
	testPublicKey, err := nostr.GetPublicKey(testPrivateKeyHex) // Handle error return
	if err != nil {
		log.Fatalf("Failed to get public key: %v", err)
	}
	event.PubKey = testPublicKey

	// Sign the event for testing
	err = event.Sign(testPrivateKeyHex)
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
	handler := http.HandlerFunc(HandleRootPost)
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

	// Private key for testing (hex encoded)
	// e71fa3f07bea377a40ae2270aad2ab26c57b9929c46d16e76635e47cdbcba5da
	testPrivateKeyBech32 := "nsec1uu068urmagmh5s9wyfc2454tymzhhxffc3k3demxxhj8ek7t5hdqkfsm0w"
	_, data, err := bech32.Decode(testPrivateKeyBech32)
	if err != nil {
		log.Fatalf("Failed to decode test private key (bech32): %v", err)
	}
	converted, err := bech32.ConvertBits(data, 5, 8, false)
	if err != nil {
		log.Fatalf("Failed to convert bits for test private key: %v", err)
	}
	testPrivateKeyHex := hex.EncodeToString(converted)

	// Derive public key from private key
	testPublicKey, err := nostr.GetPublicKey(testPrivateKeyHex) // Handle error return
	if err != nil {
		log.Fatalf("Failed to get public key: %v", err)
	}
	event.PubKey = testPublicKey

	// Sign the event
	err = event.Sign(testPrivateKeyHex)
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
	handler := http.HandlerFunc(HandleRootPost)
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
	handler := http.HandlerFunc(HandleRootPost)
	handler.ServeHTTP(rr, req)

	// Should return BadRequest due to invalid signature
	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}
}
