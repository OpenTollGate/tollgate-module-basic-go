package config_manager

import (
	"os"
	"reflect"
	"testing"
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
		AcceptedMints:      []string{"test_mint"},
		PricePerMinute:     2,
		Bragging: BraggingConfig{
			Enabled: true,
			Fields:  []string{"test_field"},
		},
		Relays:             []string{"test_relay"},
		TrustedMaintainers: []string{"test_maintainer"},
		FieldsToBeReviewed: []string{"test_field_to_review"},
		NIP94EventID:       "test_nip94_event_id",
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
		!compareStringSlices(loadedConfig.AcceptedMints, newConfig.AcceptedMints) ||
		loadedConfig.PricePerMinute != 2 ||
		!compareBraggingConfig(&loadedConfig.Bragging, &newConfig.Bragging) ||
		!compareStringSlices(loadedConfig.Relays, newConfig.Relays) ||
		!compareStringSlices(loadedConfig.TrustedMaintainers, newConfig.TrustedMaintainers) ||
		!compareStringSlices(loadedConfig.FieldsToBeReviewed, newConfig.FieldsToBeReviewed) ||
		loadedConfig.NIP94EventID != newConfig.NIP94EventID {
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
		PackagePath: "/path/to/package"
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
func TestUpdateNIP94EventID(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	cm, err := NewConfigManager(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Test UpdateNIP94EventID
	log.Println("Testing UpdateNIP94EventID")
	err = cm.UpdateNIP94EventID()
	if err != nil {
		t.Errorf("Error updating NIP94EventID: %v", err)
	} else {
		log.Println("Successfully updated NIP94EventID")
	}
	config, err := cm.LoadConfig()
	if err != nil {
		t.Errorf("Error loading config after update: %v", err)
	} else {
		log.Printf("NIP94EventID after update: %s", config.NIP94EventID)
	}
}
