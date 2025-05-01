package config_manager

import (
	"encoding/json"
	"os"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Create a temporary config file
	tmpFile, err := os.CreateTemp("", "config.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	// Write a sample config to the file
	sampleConfig := Config{
		// Add sample configuration parameters here
		// For example:
		// Parameter1: "value1",
		// Parameter2: 123,
	}
	data, err := json.Marshal(sampleConfig)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tmpFile.Write(data)
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	// Load the config from the file
	loadedConfig, err := LoadConfig(tmpFile.Name())
	if err != nil {
		t.Errorf("LoadConfig returned error: %v", err)
	}
	if loadedConfig == nil {
		t.Errorf("LoadConfig returned nil config")
	}
	// Add more checks here to verify the loaded config
}

func TestSaveConfig(t *testing.T) {
	// Create a temporary config file
	tmpFile, err := os.CreateTemp("", "config.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	// Create a sample config
	sampleConfig := &Config{
		// Add sample configuration parameters here
		// For example:
		// Parameter1: "value1",
		// Parameter2: 123,
	}

	// Save the config to the file
	err = SaveConfig(tmpFile.Name(), sampleConfig)
	if err != nil {
		t.Errorf("SaveConfig returned error: %v", err)
	}

	// Check if the file was created and has the correct content
	data, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Errorf("Failed to read saved config file: %v", err)
	}
	var loadedConfig Config
	err = json.Unmarshal(data, &loadedConfig)
	if err != nil {
		t.Errorf("Failed to unmarshal saved config: %v", err)
	}
	// Add more checks here to verify the saved config
}