package config_manager

import (
	"encoding/json"
	"log"
	"os"
	"time"
)

// Config represents the main configuration for the Tollgate service.
type Config struct {
	ConfigVersion string              `json:"config_version"`
	LogLevel      string              `json:"log_level"`
	AcceptedMints []MintConfig        `json:"accepted_mints"`
	ProfitShare   []ProfitShareConfig `json:"profit_share"`
	StepSize      uint64              `json:"step_size"`
	Margin        float64             `json:"margin,omitempty"`
	Metric        string              `json:"metric"`
	Relays        []string            `json:"relays"`
	ShowSetup     bool                `json:"show_setup"`
	ResellerMode  bool                `json:"reseller_mode"`
	Crowsnest     CrowsnestConfig     `json:"crowsnest"`
	Chandler      ChandlerConfig      `json:"chandler"`
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

// CrowsnestConfig holds configuration for the crowsnest module
type CrowsnestConfig struct {
	// Probing settings
	ProbeTimeout    time.Duration `json:"probe_timeout"`
	ProbeRetryCount int           `json:"probe_retry_count"`
	ProbeRetryDelay time.Duration `json:"probe_retry_delay"`

	// Validation settings
	RequireValidSignature bool `json:"require_valid_signature"`

	// Interface filtering
	IgnoreInterfaces []string `json:"ignore_interfaces"`
	OnlyInterfaces   []string `json:"only_interfaces"`

	// Discovery deduplication
	DiscoveryTimeout time.Duration `json:"discovery_timeout"`
}

// ChandlerConfig holds configuration for the chandler module
type ChandlerConfig struct {
	// Simple budget settings
	MaxPricePerMillisecond float64 `json:"max_price_per_millisecond"` // Max sats per ms (can be fractional)
	MaxPricePerByte        float64 `json:"max_price_per_byte"`        // Max sats per byte (can be fractional)

	// Trust settings
	Trust TrustConfig `json:"trust"`

	// Session settings
	Sessions SessionConfig `json:"sessions"`

	// Usage tracking settings
	UsageTracking UsageTrackingConfig `json:"usage_tracking"`
}

// TrustConfig holds trust policy configuration
type TrustConfig struct {
	DefaultPolicy string   `json:"default_policy"` // "trust_all", "trust_none"
	Allowlist     []string `json:"allowlist"`      // Trusted pubkeys
	Blocklist     []string `json:"blocklist"`      // Blocked pubkeys
}

// SessionConfig holds session management configuration
type SessionConfig struct {
	DefaultRenewalThresholds               []float64 `json:"default_renewal_thresholds"`                // [0.8]
	PreferredSessionIncrementsMilliseconds uint64    `json:"preferred_session_increments_milliseconds"` // Preferred increment for time sessions
	PreferredSessionIncrementsBytes        uint64    `json:"preferred_session_increments_bytes"`        // Preferred increment for data sessions
}

// UsageTrackingConfig holds usage tracking configuration
type UsageTrackingConfig struct {
	DataMonitoringInterval time.Duration `json:"data_monitoring_interval"` // How often to check data usage
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
		ConfigVersion: "v0.0.6",
		LogLevel:      "info",
		AcceptedMints: []MintConfig{
			{
				URL:                     "https://nofees.testnut.cashu.space",
				MinBalance:              9446744073709551615,
				BalanceTolerancePercent: 10,
				PayoutIntervalSeconds:   60,
				MinPayoutAmount:         9446744073709551615,
				PricePerStep:            1,
				PriceUnit:               "sats",
				MinPurchaseSteps:        0,
			},
			{
			 	URL:                     "https://mint.coinos.io/",
			 	MinBalance:              64,
			 	BalanceTolerancePercent: 10,
			 	PayoutIntervalSeconds:   60,
			 	MinPayoutAmount:         128,
			 	PricePerStep:            1,
			 	PriceUnit:               "sats",
			 	MinPurchaseSteps:        0,
			},
			{
			 	URL:                     "https://mint.minibits.cash/Bitcoin",
			 	MinBalance:              64,
			 	BalanceTolerancePercent: 10,
			 	PayoutIntervalSeconds:   60,
			 	MinPayoutAmount:         128,
			 	PricePerStep:            1,
			 	PriceUnit:               "sats",
			 	MinPurchaseSteps:        0,
			},
		},
		ProfitShare: []ProfitShareConfig{
			{
				Factor:   0.79,
				Identity: "owner",
			},
			{
				Factor:   0.21,
				Identity: "developer",
			},
		},
		StepSize: 20000,
		Margin:   0.1,
		Metric:   "milliseconds",
		Relays: []string{
			"wss://relay.damus.io",
			"wss://nos.lol",
			"wss://nostr.mom",
		},
		ShowSetup:    true,
		ResellerMode: false,
		Crowsnest: CrowsnestConfig{
			ProbeTimeout:          10 * time.Second,
			ProbeRetryCount:       3,
			ProbeRetryDelay:       2 * time.Second,
			RequireValidSignature: true,
			IgnoreInterfaces:      []string{"lo", "docker0", "br-lan", "phy1-ap0", "phy0-ap0", "wlan0-ap", "wlan1-ap", "hostap0"},
			OnlyInterfaces:        []string{},
			DiscoveryTimeout:      300 * time.Second,
		},
		Chandler: ChandlerConfig{
			MaxPricePerMillisecond: 0.002777777778,   // 10k sats/hr
			MaxPricePerByte:        0.00003725782414, // 5k sats/gbit
			Trust: TrustConfig{
				DefaultPolicy: "trust_all",
				Allowlist:     []string{},
				Blocklist:     []string{},
			},
			Sessions: SessionConfig{
				DefaultRenewalThresholds:               []float64{0.8},
				PreferredSessionIncrementsMilliseconds: 20000,   // 1 minute
				PreferredSessionIncrementsBytes:        1048576, // 1 MB
			},
			UsageTracking: UsageTrackingConfig{
				DataMonitoringInterval: 500 * time.Millisecond,
			},
		},
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
