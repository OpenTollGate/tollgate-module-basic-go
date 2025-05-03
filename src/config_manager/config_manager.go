package config_manager

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nbd-wtf/go-nostr"
)

func (cm *ConfigManager) GetNIP94Event(eventID string) (*nostr.Event, error) {
	relayPool := nostr.NewSimplePool(context.Background())
	config, err := cm.LoadConfig()
	if err != nil {
		return nil, err
	}
	for _, relayURL := range config.Relays {
		relay, err := relayPool.EnsureRelay(relayURL)
		if err != nil {
			log.Printf("Failed to connect to relay %s: %v", relayURL, err)
			continue
		}
		filter := nostr.Filter{
			IDs: []string{eventID},
		}
		sub, err := relay.Subscribe(context.Background(), []nostr.Filter{filter})
		if err != nil {
			log.Printf("Failed to subscribe to NIP-94 events on relay %s: %v", relayURL, err)
			continue
		}
		for event := range sub.Events {
			return event, nil
		}
	}
	return nil, fmt.Errorf("NIP-94 event not found with ID %s", eventID)
}

// BraggingConfig holds the bragging configuration parameters
type BraggingConfig struct {
	Enabled bool     `json:"enabled"`
	Fields  []string `json:"fields"`
}

// Config holds the configuration parameters
type PackageInfo struct {
	Version   string
	Branch    string
	Timestamp int64
}

type Config struct {
	TollgatePrivateKey string         `json:"tollgate_private_key"`
	AcceptedMints       []string      `json:"accepted_mints"`
	PricePerMinute     int            `json:"price_per_minute"`
	Bragging           BraggingConfig `json:"bragging"`
	Relays             []string       `json:"relays"`
	TrustedMaintainers []string       `json:"trusted_maintainers"`
	FieldsToBeReviewed []string       `json:"fields_to_be_reviewed"`
	NIP94EventID       []string       `json:"EventID_currently_installed"`
}

func ExtractPackageInfo(event *nostr.Event) (*PackageInfo, error) {
	if event == nil {
		return nil, fmt.Errorf("event is nil")
	}

	var version string
	var branch string
	var timestamp int64

	for _, tag := range event.Tags {
		if len(tag) > 1 {
			switch tag[0] {
			case "version":
				version = tag[1]
			case "branch":
				branch = tag[1]
			}
		}
	}

	timestamp = int64(event.CreatedAt)

	if version == "" || branch == "" {
		return nil, fmt.Errorf("required information not found in NIP94 event")
	}

	return &PackageInfo{
		Version:   version,
		Branch:    branch,
		Timestamp: timestamp,
	}, nil
}



// InstallConfig holds the installation configuration parameters
type InstallConfig struct {
	PackagePath string `json:"package_path"`
	NIP94EventID string `json:"nip94_event_id"`
}

// NewInstallConfig creates a new InstallConfig instance
func NewInstallConfig(packagePath string) *InstallConfig {
	return &InstallConfig{PackagePath: packagePath}
}

// LoadInstallConfig reads the installation configuration from the managed file
func (cm *ConfigManager) LoadInstallConfig() (*InstallConfig, error) {
	data, err := os.ReadFile(cm.installFilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // Return nil config if file does not exist
		}
		return nil, err
	}
	var installConfig InstallConfig
	err = json.Unmarshal(data, &installConfig)
	if err != nil {
		return nil, err
	}
	return &installConfig, nil
}

// SaveInstallConfig writes the installation configuration to the managed file
func (cm *ConfigManager) SaveInstallConfig(installConfig *InstallConfig) error {
	data, err := json.Marshal(installConfig)
	if err != nil {
		return err
	}
	return os.WriteFile(cm.installFilePath(), data, 0644)
}

func (cm *ConfigManager) installFilePath() string {
	return filepath.Join(filepath.Dir(cm.filePath), "install.json")
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
// TODO: Run this every time rather than storing the information in a config file.
func GetMintFee(mintURL string) (int, error) {
	// Stub implementation: return a default mint fee
	return 0, nil
}

// calculateMinPayment calculates the minimum payment based on the mint fee
func CalculateMinPayment(mintFee int) int {
	// Stub implementation: return the mint fee as the minimum payment
	return 2 * mintFee + 1
}

// getInstalledVersion retrieves the installed version of the package
// TODO: run this every time rather than storing the ouptut in a config file. 
func GetInstalledVersion() (string, error) {
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
// TODO: run this every time rather than storing this information in a config file.
func GetArchitecture() (string, error) {
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

		defaultConfig := &Config{
			TollgatePrivateKey: privateKey,
			AcceptedMints:       []string{"https://mint.minibits.cash/Bitcoin", "https://mint2.nutmix.cash"},
			PricePerMinute:     1,
			Bragging: BraggingConfig{
				Enabled: true,
				Fields:  []string{"amount", "mint", "duration"},
			},
			Relays: []string{
				"wss://relay.damus.io",
				"wss://nos.lol",
				"wss://nostr.mom",
				"wss://relay.tollgate.me",
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
