package config_manager

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// BraggingConfig holds the bragging configuration parameters
type BraggingConfig struct {
	Enabled bool     `json:"enabled"`
	Fields  []string `json:"fields"`
}

// PackageInfo holds information about the package
type PackageInfo struct {
	Timestamp int64  `json:"timestamp"`
	Version   string `json:"version"`
	Branch    string `json:"branch"`
	Arch      string `json:"json"`
}

// Config holds the configuration parameters
type Config struct {
	TollgatePrivateKey string         `json:"tollgate_private_key"`
	AcceptedMint       string         `json:"accepted_mint"`
	PricePerMinute     int            `json:"price_per_minute"`
	MinPayment         int            `json:"min_payment"`
	MintFee            int            `json:"mint_fee"`
	Bragging           BraggingConfig `json:"bragging"`
	PackageInfo        PackageInfo    `json:"package_info"`
	Relays             []string       `json:"relays"`
	TrustedMaintainers []string       `json:"trusted_maintainers"`
	FieldsToBeReviewed []string       `json:"fields_to_be_reviewed"`
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

// getMintFee retrieves the mint fee for a given mint URL
func getMintFee(mintURL string) (int, error) {
	// Stub implementation: return a default mint fee
	return 0, nil
}

// calculateMinPayment calculates the minimum payment based on the mint fee
func calculateMinPayment(mintFee int) int {
	// Stub implementation: return the mint fee as the minimum payment
	return mintFee
}

// getInstalledVersion retrieves the installed version of the package
func getInstalledVersion() (string, error) {
	_, err := exec.LookPath("opkg")
	if err != nil {
		// opkg not found, return a default version or skip this check
		return "0.0.1+1cac608", nil
	}
	cmd := exec.Command("opkg", "list-installed", "tollgate-basic")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get installed version: %w", err)
	}
	outputStr := strings.TrimSpace(string(output))
	parts := strings.Split(outputStr, " - ")
	if len(parts) != 2 {
		return "", fmt.Errorf("unexpected output format: %s", outputStr)
	}
	return parts[1], nil
}

// getArchitecture retrieves the device architecture
func getArchitecture() (string, error) {
	_, err := exec.LookPath("uci")
	if err != nil {
		// uci not found, return a default architecture
		return "aarch64_cortex-a53", nil
	}
	cmd := exec.Command("uci", "get", "system.@system[0].DISTRIB_ARCH")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get architecture: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func generatePrivateKey() (string, error) {
	// TODO: Implement proper private key generation or management
	return "8a45d0add1c7ddf668f9818df550edfa907ae8ea59d6581a4ca07473d468d663", nil
}

// EnsureDefaultConfig ensures a default configuration exists, creating it if necessary
func (cm *ConfigManager) EnsureDefaultConfig() (*Config, error) {
	config, err := cm.LoadConfig()
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if config == nil {
		privateKey, err := generatePrivateKey()
		if err != nil {
			return nil, err
		}

		// Get the installed version and architecture
		version, err := getInstalledVersion()
		if err != nil {
			return nil, err
		}
		arch, err := getArchitecture()
		if err != nil {
			return nil, err
		}

		mintFee, err := getMintFee("https://mint.minibits.cash/Bitcoin")
		if err != nil {
			return nil, err
		}

		defaultConfig := &Config{
			TollgatePrivateKey: privateKey,
			AcceptedMint:       "https://mint.minibits.cash/Bitcoin",
			PricePerMinute:     1,
			MinPayment:         calculateMinPayment(mintFee),
			MintFee:            mintFee,
			Bragging: BraggingConfig{
				Enabled: true,
				Fields:  []string{"amount", "mint", "duration"},
			},
			PackageInfo: PackageInfo{
				Timestamp: time.Now().Unix(),
				Version:   version,
				Branch:    "main",
				Arch:      arch,
			},
			Relays: []string{
				"wss://relay.damus.io",
				"wss://nos.lol",
				"wss://nostr.mom",
			},
			TrustedMaintainers: []string{
				"5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a",
			},
			FieldsToBeReviewed: []string{
				"price_per_minute",
				"relays",
				"tollgate_private_key",
				"trusted_maintainers",
			},
		}
		err = cm.SaveConfig(defaultConfig)
		if err != nil {
			return nil, err
		}
		return defaultConfig, nil
	}
	return config, nil
}
