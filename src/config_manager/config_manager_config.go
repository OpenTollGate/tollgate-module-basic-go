package config_manager

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"time"
)

const profitShareSumTolerance = 1e-6

// Config represents the main configuration for the Tollgate service.
type Config struct {
	ConfigVersion          string                       `json:"config_version"`
	LogLevel               string                       `json:"log_level"`
	AcceptedMints          []MintConfig                 `json:"accepted_mints"`
	ProfitShare            []ProfitShareConfig          `json:"profit_share"`
	StepSize               uint64                       `json:"step_size"`
	Margin                 float64                      `json:"margin,omitempty"`
	Metric                 string                       `json:"metric"`
	ShowSetup              bool                         `json:"show_setup"`
	ResellerMode           bool                         `json:"reseller_mode"`
	UpstreamDetector       UpstreamDetectorConfig       `json:"upstream_detector"`
	UpstreamSessionManager UpstreamSessionManagerConfig `json:"upstream_session_manager"`
	UpstreamWifi           UpstreamWifiConfig           `json:"upstream_wifi"`
}

type UpstreamWifiConfig struct {
	ScanIntervalSeconds     int `json:"scan_interval_seconds"`
	FastCheckSeconds        int `json:"fast_check_seconds"`
	LostThreshold           int `json:"lost_threshold"`
	HysteresisDB            int `json:"hysteresis_db"`
	SignalFloor             int `json:"signal_floor"`
	BlacklistTTLMinutes     int `json:"blacklist_ttl_minutes"`
	EmergencyPenalty        int `json:"emergency_penalty"`
	MaxConsecutiveFailures  int `json:"max_consecutive_failures"`
	SwitchCooldownMinutes   int `json:"switch_cooldown_minutes"`
	StartupGraceSeconds     int `json:"startup_grace_seconds"`
	PostSwitchWaitSeconds   int `json:"post_switch_wait_seconds"`
	DHCPTimeoutSeconds      int `json:"dhcp_timeout_seconds"`
	ManualPauseSeconds      int `json:"manual_pause_seconds"`
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

// UpstreamDetectorConfig holds configuration for the upstream_detector module
type UpstreamDetectorConfig struct {
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

// UpstreamSessionManagerConfig holds configuration for the upstream_session_manager module
type UpstreamSessionManagerConfig struct {
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
	PreferredSessionIncrementsMilliseconds uint64 `json:"preferred_session_increments_milliseconds"` // Preferred increment for time sessions
	PreferredSessionIncrementsBytes        uint64 `json:"preferred_session_increments_bytes"`        // Preferred increment for data sessions
	MillisecondRenewalOffset               uint64 `json:"millisecond_renewal_offset"`                // Milliseconds before expiry to trigger renewal (e.g., 5000 = 5 seconds)
	BytesRenewalOffset                     uint64 `json:"bytes_renewal_offset"`                      // Bytes before limit to trigger renewal (e.g., 5242880 = 5 MB)
}

// UsageTrackingConfig holds usage tracking configuration
type UsageTrackingConfig struct {
	DataMonitoringInterval time.Duration `json:"data_monitoring_interval"`
}

func (c *Config) ValidateProfitShare() error {
	if len(c.ProfitShare) == 0 {
		return fmt.Errorf("profit_share is empty: at least one entry required")
	}
	var sum float64
	for i, ps := range c.ProfitShare {
		if ps.Factor < 0 {
			return fmt.Errorf("profit_share[%d] (%q) has negative factor %v", i, ps.Identity, ps.Factor)
		}
		if ps.Factor > 1.0 {
			return fmt.Errorf("profit_share[%d] (%q) has factor %v > 1.0 (use decimal ratio, not percentage)", i, ps.Identity, ps.Factor)
		}
		sum += ps.Factor
	}
	if math.Abs(sum-1.0) > profitShareSumTolerance {
		return fmt.Errorf("profit_share factors must sum to 1.0, got %v (%.1f%% will remain in wallet each payout cycle)", sum, (1.0-sum)*100)
	}
	return nil
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
		ConfigVersion: "v0.0.7",
		LogLevel:      "info",
		AcceptedMints: []MintConfig{
			{
				URL:                     "https://mint.coinos.io",
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
		StepSize:     22020096, // 21 MiB
		Margin:       0.1,
		Metric:       "bytes",
		ShowSetup:    true,
		ResellerMode: false,
		UpstreamDetector: UpstreamDetectorConfig{
			ProbeTimeout:          10 * time.Second,
			ProbeRetryCount:       3,
			ProbeRetryDelay:       2 * time.Second,
			RequireValidSignature: true,
			IgnoreInterfaces:      []string{"lo", "docker0", "br-lan", "hostap0"},
			OnlyInterfaces:        []string{},
			DiscoveryTimeout:      300 * time.Second,
		},
		UpstreamSessionManager: UpstreamSessionManagerConfig{
			MaxPricePerMillisecond: 0.002777777778,   // 10k sats/hr
			MaxPricePerByte:        0.00003725782414, // 5k sats/gbit
			Trust: TrustConfig{
				DefaultPolicy: "trust_all",
				Allowlist:     []string{},
				Blocklist:     []string{},
			},
			Sessions: SessionConfig{
				PreferredSessionIncrementsMilliseconds: 60000,     // 1 minute
				PreferredSessionIncrementsBytes:        131100000, // 1000 MB
				MillisecondRenewalOffset:               10000,     // 10 seconds before expiry
				BytesRenewalOffset:                     131100000, // 1000 MB before limit
			},
			UsageTracking: UsageTrackingConfig{
				DataMonitoringInterval: 500 * time.Millisecond,
			},
		},
		UpstreamWifi: UpstreamWifiConfig{
			ScanIntervalSeconds:    300,
			FastCheckSeconds:       30,
			LostThreshold:          2,
			HysteresisDB:           12,
			SignalFloor:            -85,
			BlacklistTTLMinutes:    60,
			EmergencyPenalty:       20,
			MaxConsecutiveFailures: 3,
			SwitchCooldownMinutes:  10,
			StartupGraceSeconds:    90,
			PostSwitchWaitSeconds:  5,
			DHCPTimeoutSeconds:     180,
			ManualPauseSeconds:     120,
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
	unmarshalErr := json.Unmarshal(data, &config)
	profitShareErr := error(nil)
	if unmarshalErr == nil {
		profitShareErr = config.ValidateProfitShare()
	}
	if unmarshalErr != nil {
		log.Printf("WARNING: Invalid config JSON, backing up and recreating: %v", unmarshalErr)
		if backupErr := backupAndLog(filePath, "/etc/tollgate/config_backups", "config", defaultConfig.ConfigVersion); backupErr != nil {
			log.Printf("CRITICAL: Failed to backup invalid config: %v", backupErr)
			return nil, backupErr
		}
		return defaultConfig, SaveConfig(filePath, defaultConfig)
	}
	if profitShareErr != nil {
		log.Printf("WARNING: Invalid profit_share, resetting to defaults: %v", profitShareErr)
		config.ProfitShare = defaultConfig.ProfitShare
		return &config, SaveConfig(filePath, &config)
	}
	if config.ConfigVersion != defaultConfig.ConfigVersion {
		log.Printf("WARNING: Config version mismatch, backing up and recreating")
		if backupErr := backupAndLog(filePath, "/etc/tollgate/config_backups", "config", defaultConfig.ConfigVersion); backupErr != nil {
			log.Printf("CRITICAL: Failed to backup config: %v", backupErr)
			return nil, backupErr
		}
		return defaultConfig, SaveConfig(filePath, defaultConfig)
	}

	return &config, nil
}
