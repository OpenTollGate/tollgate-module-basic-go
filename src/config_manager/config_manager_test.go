package config_manager

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

// Helper functions for comparison

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
	tempDir := t.TempDir()
	configFilePath := filepath.Join(tempDir, "test_config.json")
	installFilePath := filepath.Join(tempDir, "test_install.json")
	identitiesFilePath := filepath.Join(tempDir, "test_identities.json")

	cm, err := NewConfigManager(configFilePath, installFilePath, identitiesFilePath)
	if err != nil {
		t.Fatalf("Failed to create ConfigManager: %v", err)
	}

	// Test EnsureDefaultConfig - this is implicitly handled by NewConfigManager
	config := cm.GetConfig()
	if config == nil {
		t.Errorf("GetConfig returned nil config")
	}

	// Test LoadConfig
	loadedConfig, err := LoadConfig(configFilePath)
	if err != nil {
		t.Errorf("LoadConfig returned error: %v", err)
	}
	if loadedConfig == nil {
		t.Errorf("LoadConfig returned nil config")
	}

	// Test SaveConfig
	newConfig := &Config{
		AcceptedMints: []MintConfig{
			{
				URL:                     "test_mint",
				MinBalance:              100,
				BalanceTolerancePercent: 10,
				PayoutIntervalSeconds:   60,
				MinPayoutAmount:         1000,
				MinPurchaseSteps:        1,
				PricePerStep:            1,
			},
		},
		Metric:    "milliseconds",
		StepSize:  120000,
		Relays:    []string{"test_relay"},
		ShowSetup: true,
	}
	err = SaveConfig(configFilePath, newConfig)
	if err != nil {
		t.Errorf("SaveConfig returned error: %v", err)
	}

	loadedConfig, err = LoadConfig(configFilePath)
	if err != nil {
		t.Errorf("LoadConfig returned error after SaveConfig: %v", err)
	}
	// Verify all fields
	if !compareMintConfigs(loadedConfig.AcceptedMints, newConfig.AcceptedMints) ||
		loadedConfig.Metric != "milliseconds" ||
		loadedConfig.StepSize != 120000 ||
		!compareStringSlices(loadedConfig.Relays, newConfig.Relays) ||
		loadedConfig.ShowSetup != newConfig.ShowSetup {
		t.Errorf("Loaded config does not match saved config")
	}

	// Test LoadInstallConfig and SaveInstallConfig
	// Remove install.json file if it exists
	os.Remove(cm.InstallFilePath)
	installConfig, err := LoadInstallConfig(cm.InstallFilePath)
	if err != nil {
		t.Errorf("LoadInstallConfig returned error: %v", err)
	}
	if installConfig != nil {
		t.Errorf("LoadInstallConfig returned non-nil config")
	}

	newInstallConfig := &InstallConfig{
		PackagePath: "/path/to/package",
	}
	err = SaveInstallConfig(cm.InstallFilePath, newInstallConfig)
	if err != nil {
		t.Errorf("SaveInstallConfig returned error: %v", err)
	}

	loadedInstallConfig, err := LoadInstallConfig(cm.InstallFilePath)
	if err != nil {
		t.Errorf("LoadInstallConfig returned error after SaveInstallConfig: %v", err)
	}
	if !reflect.DeepEqual(loadedInstallConfig, newInstallConfig) {
		t.Errorf("Loaded install config does not match saved config")
	}
}

func TestGeneratePrivateKey(t *testing.T) {
	// No file paths are directly used or created by generatePrivateKey,
	// so no temporary directory setup is needed here.
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr) // Defer resetting output to ensure it happens even if test fails

	privateKey, err := generatePrivateKey()
	if err != nil {
		t.Errorf("generatePrivateKey returned error: %v", err)
	}
	if privateKey == "" {
		t.Errorf("generatePrivateKey returned empty private key")
	}
	// Note: The original test checked for "Failed to publish event to relay"
	// but setUsername is now a no-op that logs "setUsername is deprecated".
	// This test should probably be updated or removed depending on future plans for generatePrivateKey.
}

func TestSetUsername(t *testing.T) {
	tempDir := t.TempDir()
	configFilePath := filepath.Join(tempDir, "test_config.json")
	installFilePath := filepath.Join(tempDir, "test_install.json")
	identitiesFilePath := filepath.Join(tempDir, "test_identities.json")

	cm, err := NewConfigManager(configFilePath, installFilePath, identitiesFilePath)
	if err != nil {
		t.Fatalf("Failed to create ConfigManager: %v", err)
	}

	privateKey := nostr.GeneratePrivateKey()
	err = cm.setUsername(privateKey, "test_c03rad0r")
	if err != nil {
		t.Errorf("setUsername returned error: %v", err)
	}
	// Additional checks can be added here to verify the username is set correctly on relays
}
