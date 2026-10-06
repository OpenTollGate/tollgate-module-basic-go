package config_manager

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
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
	RedirectURL            string                       `json:"redirect_url,omitempty"`
	AuthDelaySeconds       int                          `json:"auth_delay_seconds,omitempty"`
	UpstreamDetector       UpstreamDetectorConfig       `json:"upstream_detector"`
	UpstreamSessionManager UpstreamSessionManagerConfig `json:"upstream_session_manager"`
	UpstreamWifi           UpstreamWifiConfig           `json:"upstream_wifi"`
	// Operator-settable network settings. Declared intent: nothing reads these
	// fields directly. The applier in src/cli/operator_settings.go converges
	// them onto the router (UCI wireless + the admin-access nft fragment) on
	// every config set/save, on `tollgate config apply`, and at daemon start.
	// See docs/architecture/lan-port-management-bridge-decision.md (D9-D12).
	//
	// PrivateSSID/PrivateKey are empty by default, which means "keep whatever
	// the router already has" (99-tollgate-setup mints the first values): an
	// empty field is never written over a value the operator or the minter put
	// on the box, so an upgrade cannot revert a router's private network.
	// PrivateKey is a SECRET: it is redacted on every read path (see
	// cli.redactSecretFields) and a wholesale save that omits or blanks it
	// preserves the stored value rather than clearing it.
	PrivateSSID       string `json:"private_ssid,omitempty"`
	PrivateKey        string `json:"private_key,omitempty"`
	PrivateEncryption string `json:"private_encryption"`
	AdminAccess       string `json:"admin_access"`
}

type UpstreamWifiConfig struct {
	ScanIntervalSeconds    int  `json:"scan_interval_seconds"`
	FastCheckSeconds       int  `json:"fast_check_seconds"`
	LostThreshold          int  `json:"lost_threshold"`
	HysteresisDB           int  `json:"hysteresis_db"`
	SignalFloor            int  `json:"signal_floor"`
	BlacklistTTLMinutes    int  `json:"blacklist_ttl_minutes"`
	EmergencyPenalty       int  `json:"emergency_penalty"`
	MaxConsecutiveFailures int  `json:"max_consecutive_failures"`
	SwitchCooldownMinutes  int  `json:"switch_cooldown_minutes"`
	StartupGraceSeconds    int  `json:"startup_grace_seconds"`
	PostSwitchWaitSeconds  int  `json:"post_switch_wait_seconds"`
	DHCPTimeoutSeconds     int  `json:"dhcp_timeout_seconds"`
	ManualPauseSeconds     int  `json:"manual_pause_seconds"`
	VendorIEDiscovery      bool `json:"vendor_ie_discovery"`
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
// SaveConfig persists the config atomically: a plain os.WriteFile killed
// mid-write (power loss, procd respawn in the write window — the #402
// incident class) leaves a truncated file, which the loader then routes into
// the backup-and-defaults path: the operator's accepted mints silently become
// the factory set. Temp file + rename in the same directory means a reader
// always sees either the whole previous file or the whole new one.
func SaveConfig(filePath string, config *Config) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(filePath), ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := renameConfigIntoPlace(tmpName, filePath); err != nil {
		// A pinned inode (single-file bind mount) cannot be renamed over.
		// Fall back to the durable in-place write rather than refusing to
		// save; the temp file is removed by the deferred cleanup.
		if inErr := writeConfigInPlaceDurably(filePath, data); inErr != nil {
			return fmt.Errorf("rename temp config into place: %v (in-place fallback also failed: %v)", err, inErr)
		}
		return nil
	}
	cleanup = false
	return nil
}

// renameConfigIntoPlace is os.Rename, overridable by tests to model the
// environments where a rename onto the config path is impossible.
var renameConfigIntoPlace = os.Rename

// writeConfigInPlaceDurably is the fallback for environments a rename cannot
// serve: a single-file bind mount (the cloud-lab lane mounts
// runtime-config.json at /etc/tollgate/config.json; containerized deploys do
// the same) has its inode pinned, so rename(2) answers EBUSY no matter how
// the temp file is prepared. There, truncating and rewriting the mounted file
// is the best atomicity available — the same guarantee the pre-#402 writer
// gave — and strictly better than refusing to save the config at all.
func writeConfigInPlaceDurably(filePath string, data []byte) error {
	f, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func defaultProductionMints() []MintConfig {
	return []MintConfig{
		{
			URL:                     "https://mint.coinos.io",
			MinBalance:              64,
			BalanceTolerancePercent: 10,
			PayoutIntervalSeconds:   60,
			MinPayoutAmount:         128,
			PricePerStep:            1,
			PriceUnit:               "sat",
			MinPurchaseSteps:        0,
		},
		{
			URL:                     "https://mint.minibits.cash/Bitcoin",
			MinBalance:              64,
			BalanceTolerancePercent: 10,
			PayoutIntervalSeconds:   60,
			MinPayoutAmount:         128,
			PricePerStep:            1,
			PriceUnit:               "sat",
			MinPurchaseSteps:        0,
		},
		{
			URL:                     "https://mint.lnserver.com",
			MinBalance:              64,
			BalanceTolerancePercent: 10,
			PayoutIntervalSeconds:   60,
			MinPayoutAmount:         128,
			PricePerStep:            1,
			PriceUnit:               "sat",
			MinPurchaseSteps:        0,
		},
		{
			URL:                     "https://mint.macadamia.cash",
			MinBalance:              64,
			BalanceTolerancePercent: 10,
			PayoutIntervalSeconds:   60,
			MinPayoutAmount:         128,
			PricePerStep:            1,
			PriceUnit:               "sat",
			MinPurchaseSteps:        0,
		},
		{
			URL:                     "https://mint.westernbtc.com",
			MinBalance:              64,
			BalanceTolerancePercent: 10,
			PayoutIntervalSeconds:   60,
			MinPayoutAmount:         128,
			PricePerStep:            1,
			PriceUnit:               "sat",
			MinPurchaseSteps:        0,
		},
		{
			URL:                     "https://kashu.me",
			MinBalance:              64,
			BalanceTolerancePercent: 10,
			PayoutIntervalSeconds:   60,
			MinPayoutAmount:         128,
			PricePerStep:            1,
			PriceUnit:               "sat",
			MinPurchaseSteps:        0,
		},
		{
			URL:                     "https://mint.cubabitcoin.org",
			MinBalance:              64,
			BalanceTolerancePercent: 10,
			PayoutIntervalSeconds:   60,
			MinPayoutAmount:         128,
			PricePerStep:            1,
			PriceUnit:               "sat",
			MinPurchaseSteps:        0,
		},
	}
}

func defaultTestMint() MintConfig {
	return MintConfig{
		URL:                     "https://testnut.cashu.exchange",
		MinBalance:              0,
		BalanceTolerancePercent: 0,
		PayoutIntervalSeconds:   999999,
		MinPayoutAmount:         999999,
		PricePerStep:            1,
		PriceUnit:               "sat",
		MinPurchaseSteps:        0,
	}
}

// IsDevBuild returns true when the binary was built from a non-main branch.
func IsDevBuild() bool {
	if GitBranch == "main" || GitBranch == "unknown" || GitBranch == "" {
		return false
	}
	return true
}

// NewDefaultConfig creates a Config with default values.
func NewDefaultConfig() *Config {
	mints := defaultProductionMints()
	if IsDevBuild() {
		testMint := defaultTestMint()
		log.Printf("WARN: dev build detected (branch=%s), injecting test mint: %s", GitBranch, testMint.URL)
		mints = append(mints, testMint)
	}

	return &Config{
		ConfigVersion: "v0.0.9",
		LogLevel:      "info",
		AcceptedMints: mints,
		ProfitShare: []ProfitShareConfig{
			{
				Factor:   0.79,
				Identity: "owner",
			},
			{
				Factor:   0.07,
				Identity: "c08r4d0r",
			},
			{
				Factor:   0.07,
				Identity: "amperstrand",
			},
			{
				Factor:   0.07,
				Identity: "origami74",
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
				PreferredSessionIncrementsMilliseconds: 60000,      // 1 minute
				PreferredSessionIncrementsBytes:        2500000000, // ~2.5 GB prepay per renewal
				MillisecondRenewalOffset:               10000,      // 10 seconds before expiry
				BytesRenewalOffset:                     1225000000, // 49% of the preferred bytes increment
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
		// Empty means "keep what the router already has" — see the field
		// comments on Config. These two are the literals the schema declares,
		// so defaults_parity_test.go pins them together.
		PrivateSSID:       "",
		PrivateKey:        "",
		PrivateEncryption: "psk2+ccmp",
		AdminAccess:       "both",
	}
}

// EnsureDefaultConfig ensures a default config.json exists, loading from file if present.
func EnsureDefaultConfig(filePath string) (*Config, error) {
	defaultConfig := NewDefaultConfig()
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// The #402 forensics flagged this as the one default-write path
			// with zero forensics: a config that vanishes between the read
			// and here silently reverts the router to factory mints. Say it
			// loudly so an operator reading the log can restore a backup.
			log.Printf("WARNING: %s does not exist — writing factory defaults; if this router was configured, restore from config_backups and investigate what removed the file", filePath)
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
	}
	if config.ConfigVersion != defaultConfig.ConfigVersion {
		log.Printf("INFO: Config version %s → %s, migrating (preserving user settings)", config.ConfigVersion, defaultConfig.ConfigVersion)
		if backupErr := backupAndLog(filePath, "/etc/tollgate/config_backups", "config", config.ConfigVersion); backupErr != nil {
			log.Printf("WARN: Failed to backup config before migration (continuing): %v", backupErr)
		}
		migrateConfig(&config, defaultConfig)
		return &config, SaveConfig(filePath, &config)
	}
	if profitShareErr != nil {
		return &config, SaveConfig(filePath, &config)
	}

	return &config, nil
}

func migrateConfig(config *Config, defaults *Config) {
	if config.UpstreamWifi.ScanIntervalSeconds == 0 {
		config.UpstreamWifi = defaults.UpstreamWifi
		log.Printf("INFO: Populated UpstreamWifi defaults (was missing in v%s)", config.ConfigVersion)
	}
	// v0.0.9 added the operator-settable network settings. An upgraded router
	// has neither key, so both land as empty strings: fill in the two that must
	// never be empty (the schema's enum values), and leave the two blank-means-
	// keep fields blank so the applier does not touch an existing private
	// network. A router that HAS declared them keeps its values.
	if config.PrivateEncryption == "" {
		config.PrivateEncryption = defaults.PrivateEncryption
		log.Printf("INFO: Populated private_encryption default (%s) on upgrade", defaults.PrivateEncryption)
	}
	if config.AdminAccess == "" {
		config.AdminAccess = defaults.AdminAccess
		log.Printf("INFO: Populated admin_access default (%s) on upgrade", defaults.AdminAccess)
	}
	config.ConfigVersion = defaults.ConfigVersion
	for i := range config.AcceptedMints {
		if config.AcceptedMints[i].PriceUnit == "sats" {
			config.AcceptedMints[i].PriceUnit = "sat"
			log.Printf("INFO: Migrated price_unit sats to sat for mint %s", config.AcceptedMints[i].URL)
		}
	}
}
