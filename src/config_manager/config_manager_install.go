package config_manager

import (
	"encoding/json"
	"log"
	"os"
	"time"
)

// InstallConfig holds installation-specific parameters.
type JanitorConfig struct {
	ConfigVersion       string `json:"config_version"`
	PackagePath         string `json:"package_path"`
	Enabled             bool   `json:"enabled"`
	IPAddressRandomized bool   `json:"ip_address_randomized"`
	ReleaseChannel      string `json:"release_channel"`
}

// NewDefaultJanitorConfig creates a JanitorConfig with default values.
func NewDefaultJanitorConfig() *JanitorConfig {
	return &JanitorConfig{
		ConfigVersion:       "v0.0.2",
		PackagePath:         "",
		Enabled:             false,
		IPAddressRandomized: false,
		ReleaseChannel:      "stable",
	}
}

// LoadJanitorConfig loads and parses janitor.json.
func LoadJanitorConfig(filePath string) (*JanitorConfig, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var janitorConfig JanitorConfig
	err = json.Unmarshal(data, &janitorConfig)
	if err != nil {
		return nil, err
	}
	return &janitorConfig, nil
}

// SaveJanitorConfig saves janitor.json.
func SaveJanitorConfig(filePath string, janitorConfig *JanitorConfig) error {
	data, err := json.MarshalIndent(janitorConfig, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, data, 0644)
}

// EnsureDefaultJanitor ensures a default janitor.json exists, loading from file if present.
func EnsureDefaultJanitor(filePath string) (*JanitorConfig, error) {
	defaultJanitorConfig := NewDefaultJanitorConfig()

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultJanitorConfig, SaveJanitorConfig(filePath, defaultJanitorConfig)
		}
		return nil, err
	}

	var janitorConfig JanitorConfig
	if err := json.Unmarshal(data, &janitorConfig); err != nil || janitorConfig.ConfigVersion != defaultJanitorConfig.ConfigVersion {
		if backupErr := backupAndLog(filePath, "/etc/tollgate/config_backups", "janitor", defaultJanitorConfig.ConfigVersion); backupErr != nil {
			log.Printf("CRITICAL: Failed to backup and remove invalid janitor config: %v", backupErr)
			return nil, backupErr
		}
		return defaultJanitorConfig, SaveJanitorConfig(filePath, defaultJanitorConfig)
	}
	return &janitorConfig, nil
}

// Save saves the JanitorConfig to a specified file path.
func (jc *JanitorConfig) Save(filePath string) error {
	return SaveJanitorConfig(filePath, jc)
}
