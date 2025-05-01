package config_manager

import (
	"os"
	"testing"
)

// Helper functions for comparison
func compareBraggingConfig(a, b *BraggingConfig) bool {
	if a.Enabled != b.Enabled {
		return false
	}
	return compareStringSlices(a.Fields, b.Fields)
}

func comparePackageInfo(a, b *PackageInfo) bool {
	return a.Timestamp == b.Timestamp &&
		a.Version == b.Version &&
		a.Branch == b.Branch &&
		a.Arch == b.Arch
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
		AcceptedMint:       "test_mint",
		PricePerMinute:     2,
		MinPayment:         2,
		MintFee:            2,
		Bragging: BraggingConfig{
			Enabled: true,
			Fields:  []string{"test_field"},
		},
		PackageInfo: PackageInfo{
			Timestamp: 1234567890,
			Version:   "test_version",
			Branch:    "test_branch",
			Arch:      "test_arch",
		},
		Relays:             []string{"test_relay"},
		TrustedMaintainers: []string{"test_maintainer"},
		FieldsToBeReviewed: []string{"test_field_to_review"},
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
		loadedConfig.AcceptedMint != "test_mint" ||
		loadedConfig.PricePerMinute != 2 ||
		loadedConfig.MinPayment != 2 ||
		loadedConfig.MintFee != 2 ||
		!compareBraggingConfig(&loadedConfig.Bragging, &newConfig.Bragging) ||
		!comparePackageInfo(&loadedConfig.PackageInfo, &newConfig.PackageInfo) ||
		!compareStringSlices(loadedConfig.Relays, newConfig.Relays) ||
		!compareStringSlices(loadedConfig.TrustedMaintainers, newConfig.TrustedMaintainers) ||
		!compareStringSlices(loadedConfig.FieldsToBeReviewed, newConfig.FieldsToBeReviewed) {
		t.Errorf("Loaded config does not match saved config")
	}
}
