package config_manager

import (
	"encoding/json"
	"os"
	"time"
)

// InstallConfig holds installation-specific parameters.
type InstallConfig struct {
	ConfigVersion          string `json:"config_version"`
	PackagePath            string `json:"package_path"`
	IPAddressRandomized    bool   `json:"ip_address_randomized"`
	InstallTimestamp       int64  `json:"install_time"`
	DownloadTimestamp      int64  `json:"download_time"`
	ReleaseChannel         string `json:"release_channel"`
	EnsureDefaultTimestamp int64  `json:"ensure_default_timestamp"`
	InstalledVersion       string `json:"installed_version"`
}

// NewDefaultInstallConfig creates an InstallConfig with default values.
func NewDefaultInstallConfig() *InstallConfig {
	return &InstallConfig{
		ConfigVersion:          "v0.0.2",
		PackagePath:            "false",
		IPAddressRandomized:    false,
		InstallTimestamp:       0,
		DownloadTimestamp:      0,
		ReleaseChannel:         "stable",
		EnsureDefaultTimestamp: time.Now().Unix(),
		InstalledVersion:       "0.0.0",
	}
}

// LoadInstallConfig loads and parses install.json.
func LoadInstallConfig(filePath string) (*InstallConfig, error) {
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
	var installConfig InstallConfig
	err = json.Unmarshal(data, &installConfig)
	if err != nil {
		return nil, err
	}
	return &installConfig, nil
}

// SaveInstallConfig saves install.json.
func SaveInstallConfig(filePath string, installConfig *InstallConfig) error {
	data, err := json.MarshalIndent(installConfig, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, data, 0644)
}

// EnsureDefaultInstall ensures a default install.json exists, loading from file if present.
func EnsureDefaultInstall(filePath string) (*InstallConfig, error) {
	installConfig := NewDefaultInstallConfig()

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			err = SaveInstallConfig(filePath, installConfig)
			if err != nil {
				return nil, err
			}
			return installConfig, nil
		}
		return nil, err
	}

	err = json.Unmarshal(data, installConfig)
	if err != nil {
		return nil, err
	}
	return installConfig, nil
}
// Save saves the InstallConfig to a specified file path.
func (ic *InstallConfig) Save(filePath string) error {
	return SaveInstallConfig(filePath, ic)
}