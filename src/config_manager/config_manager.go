package config_manager

import (
	"encoding/json"
	"os"
)

// Config holds the configuration parameters
type Config struct {
	// Add configuration parameters here as needed
	// For example:
	// Parameter1 string `json:"parameter1"`
	// Parameter2 int    `json:"parameter2"`
}

// LoadConfig reads the configuration from a specified file
func LoadConfig(filename string) (*Config, error) {
	var config Config
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}
	return &config, nil
}

// SaveConfig writes the configuration to a specified file
func SaveConfig(filename string, config *Config) error {
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0644)
}