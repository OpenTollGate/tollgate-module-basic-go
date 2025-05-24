package config_manager

import (
	"bytes"
	"log"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

// Helper functions for comparison
func compareBraggingConfig(a, b *BraggingConfig) bool {
	if a.Enabled != b.Enabled {
		return false
	}
	return compareStringSlices(a.Fields, b.Fields)
}

func compareStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func compareMintConfigs(a, b []MintConfig) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].URL != b[i].URL ||
			a[i].MinBalance != b[i].MinBalance ||
			a[i].BalanceTolerancePercent != b[i].BalanceTolerancePercent ||
			a[i].PayoutIntervalSeconds != b[i].PayoutIntervalSeconds ||
			a[i].MinPayoutAmount != b[i].MinPayoutAmount {
			return false
		}
	}
	return true
}

func TestConfigManager(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	cm, err := NewConfigManager(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Test EnsureDefaultConfig
	config, err := cm.EnsureDefaultConfig()
	if err != nil {
		t.Errorf("EnsureDefaultConfig returned error: %v", err)
	}
	if config == nil {
		t.Errorf("EnsureDefaultConfig returned nil config")
	}

	// Test LoadConfig
	loadedConfig, err := cm.LoadConfig()
	if err != nil {
		t.Errorf("LoadConfig returned error: %v", err)
	}
	if loadedConfig == nil {
		t.Errorf("LoadConfig returned nil config")
	}

	// Test SaveConfig
	newConfig := &Config{
		TollgatePrivateKey: "test_key",
		AcceptedMints: []MintConfig{
			{
				URL:                     "test_mint",
				MinBalance:              100,
				BalanceTolerancePercent: 10,
				PayoutIntervalSeconds:   60,
				MinPayoutAmount:         1000,
			},
		},
		PricePerMinute: 2,
		Bragging: BraggingConfig{
			Enabled: true,
			Fields:  []string{"test_field"},
		},
		Relays:                []string{"test_relay"},
		TrustedMaintainers:    []string{"test_maintainer"},
		ShowSetup:             true,
		CurrentInstallationID: "test_current_installation_id",
	}
	err = cm.SaveConfig(newConfig)
	if err != nil {
		t.Errorf("SaveConfig returned error: %v", err)
	}

	loadedConfig, err = cm.LoadConfig()
	if err != nil {
		t.Errorf("LoadConfig returned error after SaveConfig: %v", err)
	}
	// Verify all fields
	if loadedConfig.TollgatePrivateKey != "test_key" ||
		!compareMintConfigs(loadedConfig.AcceptedMints, newConfig.AcceptedMints) ||
		loadedConfig.PricePerMinute != 2 ||
		!compareBraggingConfig(&loadedConfig.Bragging, &newConfig.Bragging) ||
		!compareStringSlices(loadedConfig.Relays, newConfig.Relays) ||
		!compareStringSlices(loadedConfig.TrustedMaintainers, newConfig.TrustedMaintainers) ||
		loadedConfig.ShowSetup != newConfig.ShowSetup ||
		loadedConfig.CurrentInstallationID != newConfig.CurrentInstallationID {
		t.Errorf("Loaded config does not match saved config")
	}

	// Test LoadInstallConfig and SaveInstallConfig
	// Remove install.json file if it exists
	os.Remove(cm.installFilePath())
	installConfig, err := cm.LoadInstallConfig()
	if err != nil {
		t.Errorf("LoadInstallConfig returned error: %v", err)
	}
	if installConfig != nil {
		t.Errorf("LoadInstallConfig returned non-nil config")
	}

	newInstallConfig := &InstallConfig{
		PackagePath: "/path/to/package",
	}
	err = cm.SaveInstallConfig(newInstallConfig)
	if err != nil {
		t.Errorf("SaveInstallConfig returned error: %v", err)
	}

	loadedInstallConfig, err := cm.LoadInstallConfig()
	if err != nil {
		t.Errorf("LoadInstallConfig returned error after SaveInstallConfig: %v", err)
	}
	if !reflect.DeepEqual(loadedInstallConfig, newInstallConfig) {
		t.Errorf("Loaded install config does not match saved config")
	}
}

func TestUpdateCurrentInstallationID(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	cm, err := NewConfigManager(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Test UpdateCurrentInstallationID
	log.Println("Testing UpdateCurrentInstallationID")
	err = cm.UpdateCurrentInstallationID()
	if err != nil {
		t.Errorf("Error updating CurrentInstallationID: %v", err)
	} else {
		log.Println("Successfully updated CurrentInstallationID")
	}
	config, err := cm.LoadConfig()
	if err != nil {
		t.Errorf("Error loading config after update: %v", err)
	} else {
		log.Printf("CurrentInstallationID after update: %s", config.CurrentInstallationID)
	}
}

func TestGeneratePrivateKey(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	cm, err := NewConfigManager(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	_, err = cm.EnsureDefaultConfig()
	if err != nil {
		t.Errorf("EnsureDefaultConfig returned error: %v", err)
	}
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	privateKey, err := cm.generatePrivateKey()
	if err != nil {
		t.Errorf("generatePrivateKey returned error: %v", err)
	}
	if privateKey == "" {
		t.Errorf("generatePrivateKey returned empty private key")
	} else {
		log.Printf("Generated private key: %s", privateKey)
	}
	logOutput := buf.String()
	if strings.Contains(logOutput, "Failed to publish event to relay") {
		t.Errorf("Event publication failed during private key generation: %s", logOutput)
	}
}

func TestSetUsername(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	cm, err := NewConfigManager(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	privateKey := nostr.GeneratePrivateKey()
	_, err = cm.EnsureDefaultConfig()
	if err != nil {
		t.Errorf("EnsureDefaultConfig returned error: %v", err)
	}
	err = cm.setUsername(privateKey, "test_c03rad0r")
	if err != nil {
		t.Errorf("setUsername returned error: %v", err)
	}
	// Additional checks can be added here to verify the username is set correctly on relays
}
