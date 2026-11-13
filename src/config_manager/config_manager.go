package config_manager

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ConfigManager manages the configuration files.
type ConfigManager struct {
	ConfigFilePath     string
	InstallFilePath    string
	IdentitiesFilePath string
	mu                 sync.RWMutex
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
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	cp := *cm.config
	cp.AcceptedMints = make([]MintConfig, len(cm.config.AcceptedMints))
	copy(cp.AcceptedMints, cm.config.AcceptedMints)
	cp.ProfitShare = make([]ProfitShareConfig, len(cm.config.ProfitShare))
	copy(cp.ProfitShare, cm.config.ProfitShare)
	return &cp
}

func (cm *ConfigManager) ReloadConfig() error {
	loaded, err := LoadConfig(cm.ConfigFilePath)
	if err != nil {
		return err
	}
	if loaded == nil {
		return fmt.Errorf("config file %s is empty or missing", cm.ConfigFilePath)
	}
	cm.mu.Lock()
	cm.config = loaded
	cm.mu.Unlock()
	return nil
}

func (cm *ConfigManager) ReloadIdentities() error {
	loaded, err := LoadIdentities(cm.IdentitiesFilePath)
	if err != nil {
		return err
	}
	if loaded == nil {
		return fmt.Errorf("identities file %s is empty or missing", cm.IdentitiesFilePath)
	}
	cm.mu.Lock()
	cm.identitiesConfig = loaded
	cm.mu.Unlock()
	return nil
}

// GetInstallConfig returns the loaded install configuration.
func (cm *ConfigManager) GetInstallConfig() *InstallConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	cp := *cm.installConfig
	return &cp
}

func (cm *ConfigManager) GetIdentities() *IdentitiesConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	cp := *cm.identitiesConfig
	cp.PublicIdentities = make([]PublicIdentity, len(cm.identitiesConfig.PublicIdentities))
	copy(cp.PublicIdentities, cm.identitiesConfig.PublicIdentities)
	cp.OwnedIdentities = make([]OwnedIdentity, len(cm.identitiesConfig.OwnedIdentities))
	copy(cp.OwnedIdentities, cm.identitiesConfig.OwnedIdentities)
	return &cp
}

// GetIdentity retrieves a public identity by name.
func (cm *ConfigManager) GetIdentity(name string) (*PublicIdentity, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	for _, identity := range cm.identitiesConfig.PublicIdentities {
		if identity.Name == name {
			return &identity, nil
		}
	}
	return nil, fmt.Errorf("public identity '%s' not found", name)
}

// UpdatePricing updates the pricing information in the config file if it has changed.
func (cm *ConfigManager) UpdatePricing(pricePerStep, stepSize int) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	needsUpdate := false

	if len(cm.config.AcceptedMints) > 0 {
		if cm.config.AcceptedMints[0].PricePerStep != uint64(pricePerStep) {
			cm.config.AcceptedMints[0].PricePerStep = uint64(pricePerStep)
			needsUpdate = true
		}
	}

	if cm.config.StepSize != uint64(stepSize) {
		cm.config.StepSize = uint64(stepSize)
		needsUpdate = true
	}

	if needsUpdate {
		log.Printf("Price changed. Udpating config file with price_per_step=%d, step_size=%d", pricePerStep, stepSize)
		return SaveConfig(cm.ConfigFilePath, cm.config)
	}

	return nil
}

// GetOwnedIdentity retrieves an owned identity by name.
func (cm *ConfigManager) GetOwnedIdentity(name string) (*OwnedIdentity, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	for _, identity := range cm.identitiesConfig.OwnedIdentities {
		if identity.Name == name {
			return &identity, nil
		}
	}
	return nil, fmt.Errorf("owned identity '%s' not found", name)
}

// backupAndLog backs up a specified file and logs the action.
func backupAndLog(filePath, backupDir, fileType, codeVersion string) error {
	return backupAndLogAt(filePath, backupDir, fileType, codeVersion, time.Now().UTC())
}

func backupAndLogAt(filePath, backupDir, fileType, codeVersion string, timestamp time.Time) error {
	// 1. Ensure backup directory exists
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("failed to create backup directory '%s': %w", backupDir, err)
	}

	// 2. Reserve a unique backup path before moving anything. Renaming
	// directly to a timestamped name silently overwrites a backup made in
	// the same second; O_EXCL makes the collision visible and adds a suffix.
	stem := fmt.Sprintf("%s_%s_%s", fileType, timestamp.Format("2006-01-02T15-04-05.000000000Z"), codeVersion)
	var backupPath string
	for suffix := 0; suffix < 1000; suffix++ {
		name := stem + ".json"
		if suffix > 0 {
			name = fmt.Sprintf("%s-%d.json", stem, suffix)
		}
		candidate := filepath.Join(backupDir, name)
		f, err := os.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return fmt.Errorf("failed to reserve backup path '%s': %w", candidate, err)
		}
		if err = f.Close(); err != nil {
			_ = os.Remove(candidate)
			return fmt.Errorf("failed to close backup reservation '%s': %w", candidate, err)
		}
		backupPath = candidate
		break
	}
	if backupPath == "" {
		return fmt.Errorf("failed to find a unique backup path for %s in '%s'", fileType, backupDir)
	}

	// 3. Move the file. The reserved placeholder prevents another writer
	// from choosing the same name; rename atomically replaces that placeholder.
	if err := os.Rename(filePath, backupPath); err != nil {
		_ = os.Remove(backupPath)
		return fmt.Errorf("failed to move config '%s' to backup '%s': %w", filePath, backupPath, err)
	}

	// 4. Log the action
	log.Printf("WARNING: Invalid '%s' config file found. Backed up to %s", fileType, backupPath)
	return nil
}
