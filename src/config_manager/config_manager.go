package config_manager

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// ConfigManager manages the configuration files.
type ConfigManager struct {
	ConfigFilePath     string
	InstallFilePath    string
	IdentitiesFilePath string
	config             *Config
	installConfig      *InstallConfig
	identitiesConfig   *IdentitiesConfig
}

// NewConfigManager creates a new ConfigManager instance and loads/ensures default configurations.
func NewConfigManager(configPath, installPath, identitiesPath string) (*ConfigManager, error) {
	// Check for a test configuration directory environment variable
	testConfigDir := os.Getenv("TOLLGATE_TEST_CONFIG_DIR")
	if testConfigDir != "" {
		if err := os.MkdirAll(testConfigDir, 0700); err != nil {
			return nil, fmt.Errorf("failed to create test config directory %s: %w", testConfigDir, err)
		}
		configPath = filepath.Join(testConfigDir, filepath.Base(configPath))
		installPath = filepath.Join(testConfigDir, filepath.Base(installPath))
		identitiesPath = filepath.Join(testConfigDir, filepath.Base(identitiesPath))
		log.Printf("Using config paths for testing: config=%s, install=%s, identities=%s", configPath, installPath, identitiesPath)
	} else {
		log.Printf("Using config paths: config=%s, install=%s, identities=%s", configPath, installPath, identitiesPath)
	}

	cm := &ConfigManager{
		ConfigFilePath:     configPath,
		InstallFilePath:    installPath,
		IdentitiesFilePath: identitiesPath,
	}

	var err error
	cm.config, err = EnsureDefaultConfig(cm.ConfigFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure default config: %w", err)
	}

	cm.installConfig, err = EnsureDefaultInstall(cm.InstallFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure default install config: %w", err)
	}

	cm.identitiesConfig, err = EnsureDefaultIdentities(cm.IdentitiesFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure default identities config: %w", err)
	}

	return cm, nil
}

// GetConfig returns the loaded main configuration.
func (cm *ConfigManager) GetConfig() *Config {
	return cm.config
}

// GetInstallConfig returns the loaded install configuration.
func (cm *ConfigManager) GetInstallConfig() *InstallConfig {
	return cm.installConfig
}

// GetIdentities returns the loaded identities configuration.
func (cm *ConfigManager) GetIdentities() *IdentitiesConfig {
	return cm.identitiesConfig
}

// GetIdentity retrieves a public identity by name.
func (cm *ConfigManager) GetIdentity(name string) (*PublicIdentity, error) {
	for _, identity := range cm.identitiesConfig.PublicIdentities {
		if identity.Name == name {
			return &identity, nil
		}
	}
	return nil, fmt.Errorf("public identity '%s' not found", name)
}

// UpdatePricing updates the pricing information in the config file if it has changed.
func (cm *ConfigManager) UpdatePricing(pricePerStep, stepSize int) error {
	config := cm.GetConfig()
	needsUpdate := false

	// Assuming the first mint is the one to update. This may need to be revisited.
	if len(config.AcceptedMints) > 0 {
		if config.AcceptedMints[0].PricePerStep != uint64(pricePerStep) {
			config.AcceptedMints[0].PricePerStep = uint64(pricePerStep)
			needsUpdate = true
		}
	}

	if config.StepSize != uint64(stepSize) {
		config.StepSize = uint64(stepSize)
		needsUpdate = true
	}

	if needsUpdate {
		log.Printf("Price changed. Udpating config file with price_per_step=%d, step_size=%d", pricePerStep, stepSize)
		return SaveConfig(cm.ConfigFilePath, config)
	}

	return nil
}

// GetOwnedIdentity retrieves an owned identity by name.
func (cm *ConfigManager) GetOwnedIdentity(name string) (*OwnedIdentity, error) {
	for _, identity := range cm.identitiesConfig.OwnedIdentities {
		if identity.Name == name {
			return &identity, nil
		}
	}
	return nil, fmt.Errorf("owned identity '%s' not found", name)
}

// backupAndLog backs up a specified file and logs the action.
func backupAndLog(filePath, backupDir, fileType, codeVersion string) error {
	// 1. Ensure backup directory exists
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("failed to create backup directory '%s': %w", backupDir, err)
	}

	// 2. Generate backup filename
	timestamp := time.Now().UTC().Format("2006-01-02T15-04-05Z")
	backupFilename := fmt.Sprintf("%s_%s_%s.json", fileType, timestamp, codeVersion)
	backupPath := filepath.Join(backupDir, backupFilename)

	// 3. Move the file
	if err := os.Rename(filePath, backupPath); err != nil {
		return fmt.Errorf("failed to move config '%s' to backup '%s': %w", filePath, backupPath, err)
	}

	// 4. Log the action
	log.Printf("WARNING: Invalid '%s' config file found. Backed up to %s", fileType, backupPath)
	return nil
}
