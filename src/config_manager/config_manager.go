package config_manager

import (
	"encoding/json"
	"os"
)

// Config holds the configuration parameters
type Config struct {
	TollgatePrivateKey string `json:"tollgate_private_key"`
	AcceptedMint       string `json:"accepted_mint"`
	PricePerMinute     int    `json:"price_per_minute"`
	MinPayment         int    `json:"min_payment"`
	MintFee            int    `json:"mint_fee"`
}

// ConfigManager manages the configuration file
type ConfigManager struct {
	filePath string
}

// NewConfigManager creates a new ConfigManager instance
func NewConfigManager(filePath string) (*ConfigManager, error) {
	cm := &ConfigManager{filePath: filePath}
	_, err := cm.EnsureDefaultConfig()
	if err != nil {
		return nil, err
	}
	return cm, nil
}

// LoadConfig reads the configuration from the managed file
func (cm *ConfigManager) LoadConfig() (*Config, error) {
	data, err := os.ReadFile(cm.filePath)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil // Return nil config if file is empty
	}
	var config Config
	err = json.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}
	return &config, nil
}

// SaveConfig writes the configuration to the managed file
func (cm *ConfigManager) SaveConfig(config *Config) error {
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return os.WriteFile(cm.filePath, data, 0644)
}

// EnsureDefaultConfig ensures a default configuration exists, creating it if necessary
func (cm *ConfigManager) EnsureDefaultConfig() (*Config, error) {
	config, err := cm.LoadConfig()
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if config == nil {
		defaultConfig := &Config{
			TollgatePrivateKey: "8a45d0add1c7ddf668f9818df550edfa907ae8ea59d6581a4ca07473d468d663",
			AcceptedMint:       "",
			PricePerMinute:     1,
			MinPayment:         1,
			MintFee:            1,
		}
		err = cm.SaveConfig(defaultConfig)
		if err != nil {
			return nil, err
		}
		return defaultConfig, nil
	}
	return config, nil
}
