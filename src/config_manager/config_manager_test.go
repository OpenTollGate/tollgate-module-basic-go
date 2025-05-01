package config_manager

import (
	"os"
	"testing"
)

func TestConfigManager(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	cm := NewConfigManager(tmpFile.Name())

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
		AcceptedMint:       "test_mint",
		PricePerMinute:     2,
		MinPayment:         2,
		MintFee:            2,
	}
	err = cm.SaveConfig(newConfig)
	if err != nil {
		t.Errorf("SaveConfig returned error: %v", err)
	}

	loadedConfig, err = cm.LoadConfig()
	if err != nil {
		t.Errorf("LoadConfig returned error after SaveConfig: %v", err)
	}
	if loadedConfig.TollgatePrivateKey != "test_key" {
		t.Errorf("Loaded config does not match saved config")
	}
}