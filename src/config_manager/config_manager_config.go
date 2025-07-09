package config_manager

import (
	"encoding/json"
	"log"
	"os"
)

// Config represents the main configuration for the Tollgate service.
type Config struct {
	ConfigVersion string              `json:"config_version"`
	AcceptedMints []MintConfig        `json:"accepted_mints"`
	ProfitShare   []ProfitShareConfig `json:"profit_share"`
	StepSize      uint64              `json:"step_size"`
	Metric        string              `json:"metric"`
	Relays        []string            `json:"relays"`
	ShowSetup     bool                `json:"show_setup"`
}

// MintConfig holds configuration for a specific mint.
type MintConfig struct {
	URL                     string `json:"url"`
	MinBalance              uint64 `json:"min_balance"`
	BalanceTolerancePercent uint64 `json:"balance_tolerance_percent"`
	PayoutIntervalSeconds   uint64 `json:"payout_interval_seconds"`
	MinPayoutAmount         uint64 `json:"min_payout_amount"`
	PricePerStep            uint64 `json:"price_per_step"`
	PriceUnit               string `json:"price_unit"`
	MinPurchaseSteps        uint64 `json:"purchase_min_steps"`
}

// ProfitShareConfig defines how profits are shared.
type ProfitShareConfig struct {
	Factor   float64 `json:"factor"`
	Identity string  `json:"identity"`
}

// LoadConfig loads and parses config.json.
func LoadConfig(filePath string) (*Config, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // Return nil config if file does not exist
		}
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

// SaveConfig saves config.json.
func SaveConfig(filePath string, config *Config) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, data, 0644)
}

// NewDefaultConfig creates a Config with default values.
func NewDefaultConfig() *Config {
	return &Config{
		ConfigVersion: "v0.0.4",
		AcceptedMints: []MintConfig{
			{
				URL:                     "https://mint.minibits.cash/Bitcoin",
				MinBalance:              8,
				BalanceTolerancePercent: 10,
				PayoutIntervalSeconds:   60,
				MinPayoutAmount:         16,
				PricePerStep:            1,
				PriceUnit:               "sats",
				MinPurchaseSteps:        0,
			},
			{
				URL:                     "https://mint2.nutmix.cash",
				MinBalance:              8,
				BalanceTolerancePercent: 10,
				PayoutIntervalSeconds:   60,
				MinPayoutAmount:         16,
				PricePerStep:            1,
				PriceUnit:               "sats",
				MinPurchaseSteps:        0,
			},
		},
		ProfitShare: []ProfitShareConfig{
			{
				Factor:   0.7,
				Identity: "owner",
			},
			{
				Factor:   0.3,
				Identity: "developer",
			},
		},
		StepSize: 600000,
		Metric:   "milliseconds",
		Relays: []string{
			"wss://relay.damus.io",
			"wss://nos.lol",
			"wss://nostr.mom",
		},
		ShowSetup: true,
	}
}

// EnsureDefaultConfig ensures a default config.json exists, loading from file if present.
func EnsureDefaultConfig(filePath string) (*Config, error) {
	defaultConfig := NewDefaultConfig()
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// File does not exist, save the default config
			return defaultConfig, SaveConfig(filePath, defaultConfig)
		}
		return nil, err // Other read error
	}

	// File exists, attempt to unmarshal
	var config Config
	if err := json.Unmarshal(data, &config); err != nil || config.ConfigVersion != defaultConfig.ConfigVersion {
		// Unmarshal failed or version mismatch, trigger backup and recreate
		if backupErr := backupAndLog(filePath, "/etc/tollgate/config_backups", "config", defaultConfig.ConfigVersion); backupErr != nil {
			log.Printf("CRITICAL: Failed to backup and remove invalid config: %v", backupErr)
			// Depending on desired behavior, we might return an error or proceed with default
			return nil, backupErr
		}
		// Save new default config
		return defaultConfig, SaveConfig(filePath, defaultConfig)
	}

	return &config, nil
}
