package config_manager

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/go-version"
	"github.com/nbd-wtf/go-nostr"
)

var relayRequestSemaphore = make(chan struct{}, 5) // Allow up to 5 concurrent requests

func rateLimitedRelayRequest(relay *nostr.Relay, event nostr.Event) error {
	relayRequestSemaphore <- struct{}{}
	defer func() { <-relayRequestSemaphore }()

	return relay.Publish(context.Background(), event)
}

func (cm *ConfigManager) GetNIP94Event(eventID string) (*nostr.Event, error) {
	config, err := cm.LoadConfig()
	if err != nil {
		return nil, err
	}
	workingRelays := []string{}
	for _, relayURL := range config.Relays {
		relay, err := cm.PublicPool.EnsureRelay(relayURL)
		if err != nil {
			log.Printf("Failed to connect to relay %s: %v", relayURL, err)
			continue
		}
		workingRelays = append(workingRelays, relayURL)
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
	config.Relays = workingRelays // TODO: use a separate file to store program state. This doesn't belong in the config file..
	cm.SaveConfig(config)
	return nil, fmt.Errorf("NIP-94 event not found with ID %s", eventID)
}

// BraggingConfig holds the bragging configuration parameters
type BraggingConfig struct {
	Enabled bool     `json:"enabled"`
	Fields  []string `json:"fields"`
}

// MintConfig holds configuration for a specific mint including payout settings
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

type ProfitShareConfig struct {
	Factor           float64 `json:"factor"`
	LightningAddress string  `json:"lightning_address"`
}

// Config holds the configuration parameters
type PackageInfo struct {
	Version        string
	Timestamp      int64
	ReleaseChannel string
}

type Config struct {
	ConfigVersion         string              `json:"config_version"`
	TollgatePrivateKey    string              `json:"tollgate_private_key"`
	AcceptedMints         []MintConfig        `json:"accepted_mints"`
	ProfitShare           []ProfitShareConfig `json:"profit_share"`
	StepSize              uint64              `json:"step_size"`
	Metric                string              `json:"metric"`
	Bragging              BraggingConfig      `json:"bragging"`
	Relays                []string            `json:"relays"`
	TrustedMaintainers    []string            `json:"trusted_maintainers"`
	ShowSetup             bool                `json:"show_setup"`
	CurrentInstallationID string              `json:"current_installation_id"`
}

func ExtractPackageInfo(event *nostr.Event) (*PackageInfo, error) {
	if event == nil {
		return nil, fmt.Errorf("event is nil")
	}

	var version string
	var releaseChannel string
	var timestamp int64

	for _, tag := range event.Tags {
		if len(tag) > 1 {
			switch tag[0] {
			case "version":
				version = tag[1]
			case "release_channel":
				releaseChannel = tag[1]
			}
		}
	}

	timestamp = int64(event.CreatedAt)

	if version == "" {
		return nil, fmt.Errorf("required information 'version' not found in NIP94 event")
	}

	return &PackageInfo{
		Version:        version,
		Timestamp:      timestamp,
		ReleaseChannel: releaseChannel,
	}, nil
}

// InstallConfig holds the installation configuration parameters
// The difference between config.json and install.json is that the install config is modified by other programs while config.json is only modified by this program.
type InstallConfig struct {
	PackagePath            string `json:"package_path"`
	IPAddressRandomized    string `json:"ip_address_randomized"`
	InstallTimestamp       int64  `json:"install_time"`
	DownloadTimestamp      int64  `json:"download_time"`
	ReleaseChannel         string `json:"release_channel"`
	EnsureDefaultTimestamp int64  `json:"ensure_default_timestamp"`
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
		return nil, fmt.Errorf("error reading install config file: %w", err)
	}
	if len(data) == 0 {
		return nil, nil // Return nil config if file is empty
	}
	var installConfig InstallConfig
	err = json.Unmarshal(data, &installConfig)
	if err != nil {
		// Treat unmarshalling errors (e.g., malformed JSON) as if the file was empty/non-existent
		// to trigger default install config creation.
		log.Printf("Error unmarshalling install config file %s: %v. Treating as empty/non-existent.", cm.installFilePath(), err)
		return nil, nil
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
	return filepath.Join(filepath.Dir(cm.FilePath), "install.json")
}

// ConfigManager manages the configuration file
type ConfigManager struct {
	FilePath   string
	PublicPool *nostr.SimplePool
	LocalPool  *nostr.SimplePool
}

// NewConfigManager creates a new ConfigManager instance
func NewConfigManager(filePath string) (*ConfigManager, error) {
	publicPool := nostr.NewSimplePool(context.Background())
	localPool := nostr.NewSimplePool(context.Background())
	cm := &ConfigManager{
		FilePath:   filePath,
		PublicPool: publicPool,
		LocalPool:  localPool,
	}
	return cm, nil
}

// EnsureInitializedConfig ensures a default configuration and install configuration exist.
// This function will be called explicitly where needed, not during NewConfigManager if possible in test code.
func (cm *ConfigManager) EnsureInitializedConfig() error {
	_, err := cm.EnsureDefaultConfig()
	if err != nil {
		return err
	}
	_, err = cm.EnsureDefaultInstall()
	if err != nil {
		return err
	}
	err = cm.UpdateCurrentInstallationID()
	if err != nil {
		return err
	}
	return nil
}

func getIPAddress() {
	// Gets the IP address of
	// root@OpenWrt:/tmp# ifconfig br-lan | grep 'inet addr:' | cut -d: -f2 | awk '{ print $1}'
	// 172.20.203.1
	// Use commands like the above or the go net package to get the IP address this device's LAN interface.
}

func (cm *ConfigManager) EnsureDefaultInstall() (*InstallConfig, error) {
	CURRENT_TIMESTAMP := time.Now().Unix()
	installConfig, err := cm.LoadInstallConfig()
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if installConfig == nil {
		installConfig = &InstallConfig{}
	}

	// Ensure all fields have default values
	if installConfig.PackagePath == "" {
		installConfig.PackagePath = "false"
	}
	if installConfig.IPAddressRandomized == "" {
		installConfig.IPAddressRandomized = "false"
	}
	if installConfig.InstallTimestamp == 0 {
		installConfig.InstallTimestamp = 0 // unknown
	}
	if installConfig.DownloadTimestamp == 0 {
		installConfig.DownloadTimestamp = 0 // unknown
	}
	if installConfig.ReleaseChannel == "" {
		installConfig.ReleaseChannel = "stable"
	}
	if installConfig.EnsureDefaultTimestamp == 0 {
		installConfig.EnsureDefaultTimestamp = CURRENT_TIMESTAMP
	}

	err = cm.SaveInstallConfig(installConfig)
	if err != nil {
		return nil, err
	}

	return installConfig, nil
}

// LoadConfig reads the configuration from the managed file
func (cm *ConfigManager) LoadConfig() (*Config, error) {
	data, err := os.ReadFile(cm.FilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // Return nil config if file does not exist
		}
		return nil, fmt.Errorf("error reading config file: %w", err)
	}
	if len(data) == 0 {
		return nil, nil // Return nil config if file is empty
	}
	var config Config

	err = json.Unmarshal(data, &config)
	if err != nil {
		// Treat unmarshalling errors (e.g., malformed JSON) as if the file was empty/non-existent
		// to trigger default config creation.
		log.Printf("Error unmarshalling config file %s: %v. Treating as empty/non-existent.", cm.FilePath, err)
		return nil, nil
	}
	return &config, nil
}

// SaveConfig writes the configuration to the managed file with pretty formatting
func (cm *ConfigManager) SaveConfig(config *Config) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cm.FilePath, data, 0644)
}

// calculateMinPayment calculates the minimum payment based on the mint fee
func CalculateMinPayment(mintFee uint64) uint64 {
	// Stub implementation: return the mint fee as the minimum payment
	return 2*mintFee + 1
}

// getInstalledVersion retrieves the installed version of the package
// TODO: run this every time rather than storing the ouptut in a config file.
func GetInstalledVersion() (string, error) {
	_, err := exec.LookPath("opkg")
	if err != nil {
		// opkg not found, return a default version or skip this check
		return "0.0.1+1cac608", nil
	}

	maxAttempts := 5
	delay := 100 * time.Millisecond

	for attempt := 0; attempt < maxAttempts; attempt++ {
		cmd := exec.Command("sh", "-c", "opkg list-installed | grep tollgate")
		output, err := cmd.CombinedOutput()
		if err != nil {
			outputStr := strings.TrimSpace(string(output))
			if strings.Contains(outputStr, "Could not lock /var/lock/opkg.lock: Resource temporarily unavailable") {
				log.Printf("Opkg output: %s", output)
				log.Printf("Attempt %d failed: %v. Retrying in %v...", attempt+1, err, delay)
				time.Sleep(delay)
				delay *= 2 // Exponential backoff
				continue
			}
			log.Printf("Opkg output: %s", output)
			return "", fmt.Errorf("failed to get installed version: %w", err)
		}

		outputStr := strings.TrimSpace(string(output))
		parts := strings.Split(outputStr, " - ")
		if len(parts) > 1 {
			return parts[1], nil
		}
		return "", fmt.Errorf("tollgate package not found or invalid output format")
	}

	return "", fmt.Errorf("failed to get installed version after %d attempts", maxAttempts)
}

func (cm *ConfigManager) GetArchitecture() (string, error) {
	data, err := os.ReadFile("/etc/openwrt_release")
	if err != nil {
		return "", fmt.Errorf("failed to read /etc/openwrt_release: %w", err)
	}

	re := regexp.MustCompile(`DISTRIB_ARCH='([^']+)'`)
	match := re.FindStringSubmatch(string(data))
	if len(match) < 2 {
		return "", fmt.Errorf("DISTRIB_ARCH not found in /etc/openwrt_release")
	}

	// TODO: Use ExtractPackageInfo to determine architecture from NIP94 event and throw an error if it is different from the architecture that we found on the filesystem. Don't do this check if CurrentInstallationID is set to `unknown`
	return match[1], nil
}

func (cm *ConfigManager) GetTimestamp() (int64, error) {
	config, err := cm.LoadConfig()
	if err != nil {
		return 0, err
	}

	if config.CurrentInstallationID != "" {
		event, err := cm.GetNIP94Event(config.CurrentInstallationID)
		if err != nil {
			return 0, err
		}
		packageInfo, err := ExtractPackageInfo(event)
		if err != nil {
			return 0, err
		}
		// Compare the timestamp from the NIP94 event with the timestamp from the filesystem.
		// For now, we'll just return the NIP94 event timestamp.
		return packageInfo.Timestamp, nil
	} else {
		installConfig, err := cm.LoadInstallConfig()
		if err != nil {
			return 0, err
		}
		if installConfig == nil {
			return 0, fmt.Errorf("install config not found")
		}

		var timestamp int64
		switch {
		case installConfig.DownloadTimestamp != 0 && installConfig.InstallTimestamp != 0:
			timestamp = min(installConfig.DownloadTimestamp, installConfig.InstallTimestamp)
		case installConfig.DownloadTimestamp != 0:
			timestamp = installConfig.DownloadTimestamp
		case installConfig.InstallTimestamp != 0:
			timestamp = installConfig.InstallTimestamp
		case installConfig.EnsureDefaultTimestamp != 0:
			timestamp = installConfig.EnsureDefaultTimestamp
		default:
			return 0, fmt.Errorf("neither download, install, nor ensure default timestamp found in install.json")
		}
		return timestamp, nil
	}
	return 0, fmt.Errorf("Unexpected state")
}

func (cm *ConfigManager) GetVersion() (string, error) {
	releaseChannel, err := cm.GetReleaseChannel()
	if err != nil {
		return "", err
	}

	installedVersion, err := GetInstalledVersion()
	if err != nil {
		return "", err
	}

	if releaseChannel == "stable" {
		_, err := version.NewVersion(installedVersion)
		if err != nil {
			log.Printf("Warning: Invalid installed version format for stable release channel: %v", err)
			return installedVersion, nil // Return the version despite the format issue
		}
		return installedVersion, nil
	} else {
		// For dev channel, return the installed version as a string
		return installedVersion, nil
	}
}

func (cm *ConfigManager) generatePrivateKey() (string, error) {
	privateKey := nostr.GeneratePrivateKey()
	err := cm.setUsername(privateKey, "c03rad0r")
	if err != nil {
		log.Printf("Failed to set username: %v", err)
	}
	return privateKey, nil
}

func (cm *ConfigManager) setUsername(privateKey string, username string) error {
	config, err := cm.LoadConfig()
	if err != nil {
		return err
	}

	if config == nil {
		return fmt.Errorf("config is nil")
	}

	event := nostr.Event{
		Kind: nostr.KindProfileMetadata,
		Tags: nostr.Tags{{
			"d",
			username,
		}},
		Content:   `{"name":"` + username + `"}`,
		CreatedAt: nostr.Now(),
	}

	event.ID = event.GetID()

	event.Sign(privateKey)

	for _, relayURL := range config.Relays {
		relay, err := cm.PublicPool.EnsureRelay(relayURL)
		if err != nil {
			log.Printf("Failed to connect to relay %s: %v", relayURL, err)
			continue
		}
		err = rateLimitedRelayRequest(relay, event)
		if err != nil {
			log.Printf("Failed to publish event to relay %s: %v", relayURL, err)
		}
	}

	return nil
}

// EnsureDefaultConfig ensures a default configuration exists, creating it if necessary
func (cm *ConfigManager) EnsureDefaultConfig() (*Config, error) {
	config, err := cm.LoadConfig()
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if config == nil {
		privateKey, err := cm.generatePrivateKey()
		if err != nil {
			return nil, err
		}

		defaultConfig := &Config{
			ConfigVersion:      "v0.0.3",
			TollgatePrivateKey: privateKey,
			AcceptedMints: []MintConfig{
				{
					URL:                     "https://mint.minibits.cash/Bitcoin",
					MinBalance:              8,
					BalanceTolerancePercent: 10,
					PayoutIntervalSeconds:   60,
					MinPayoutAmount:         16,
					PricePerStep:            1,
					MinPurchaseSteps:        0,
				},
				{
					URL:                     "https://mint2.nutmix.cash",
					MinBalance:              8,
					BalanceTolerancePercent: 10,
					PayoutIntervalSeconds:   60,
					MinPayoutAmount:         16,
					PricePerStep:            1,
					MinPurchaseSteps:        0,
				},
			},
			ProfitShare: []ProfitShareConfig{
				{0.70, "tollgate@minibits.cash"}, // User should change this
				{0.30, "tollgate@minibits.cash"},
			},
			Metric:   "milliseconds",
			StepSize: 600000,
			Bragging: BraggingConfig{
				Enabled: true,
				Fields:  []string{"amount", "mint", "duration"},
			},
			Relays: []string{
				"wss://relay.damus.io",
				"wss://nos.lol",
				"wss://nostr.mom",
				//"wss://relay.tollgate.me", // TODO: make it more resillient to broken relays..
			},
			TrustedMaintainers: []string{
				"5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a",
			},
			ShowSetup:             true,
			CurrentInstallationID: "",
		} // TODO: update the default EventID when we merge to main.
		// TODO: consider using separate files to track state and user configurations in future. One file is intended only for the user to write to and config_manager to read from. The other file is intended only for config_manager.go to write to.
		err = cm.SaveConfig(defaultConfig)
		if err != nil {
			return nil, err
		}
		return defaultConfig, nil
	}
	return config, nil
}

func (cm *ConfigManager) GetReleaseChannel() (string, error) {
	config, err := cm.LoadConfig()
	if err != nil {
		return "", err
	}

	if config.CurrentInstallationID == "" {
		installConfig, err := cm.LoadInstallConfig()
		if err != nil {
			return "", err
		}
		if installConfig != nil {
			// log.Printf("Returning release channel from install config: %s", installConfig.ReleaseChannel)
			return installConfig.ReleaseChannel, nil
		}
		return "", fmt.Errorf("CurrentInstallationID is unknown and install config is nil")
	}

	event, err := cm.GetNIP94Event(config.CurrentInstallationID)
	if err != nil {
		fmt.Println("Failed to get NIP94Event")
		return "noevent", err
	}

	packageInfo, err := ExtractPackageInfo(event)
	if err != nil {
		fmt.Println("Failed to extract from NIP94Event")
		return "noextract", err
	}

	return packageInfo.ReleaseChannel, nil
}

func (cm *ConfigManager) UpdateCurrentInstallationID() error {
	config, err := cm.LoadConfig()
	if err != nil {
		return err
	}

	if config.CurrentInstallationID != "" {
		event, err := cm.GetNIP94Event(config.CurrentInstallationID)
		if err != nil {
			return err
		}

		packageInfo, err := ExtractPackageInfo(event)
		if err != nil {
			return err
		}

		installedVersion, err := GetInstalledVersion()
		if err != nil {
			return err
		}

		if installedVersion != packageInfo.Version {
			config.CurrentInstallationID = ""
			err = cm.SaveConfig(config)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (cm *ConfigManager) GetPublicPool() *nostr.SimplePool {
	return cm.PublicPool
}

// GetLocalPool returns the local pool that connects to the local relay
func (cm *ConfigManager) GetLocalPool() *nostr.SimplePool {
	return cm.LocalPool
}

// PublishToLocalPool publishes an event to the local relay pool
func (cm *ConfigManager) PublishToLocalPool(event nostr.Event) error {
	localRelayURL := "ws://localhost:4242"

	relay, err := cm.LocalPool.EnsureRelay(localRelayURL)
	if err != nil {
		log.Printf("Failed to connect to local relay %s: %v", localRelayURL, err)
		return err
	}

	err = relay.Publish(context.Background(), event)
	if err != nil {
		log.Printf("Failed to publish event to local relay %s: %v", localRelayURL, err)
		return err
	}

	log.Printf("Successfully published event %s to local relay", event.ID)
	return nil
}

// QueryLocalPool queries events from the local relay pool
func (cm *ConfigManager) QueryLocalPool(filters []nostr.Filter) (chan *nostr.Event, error) {
	localRelayURL := "ws://localhost:4242"

	relay, err := cm.LocalPool.EnsureRelay(localRelayURL)
	if err != nil {
		log.Printf("Failed to connect to local relay %s: %v", localRelayURL, err)
		return nil, err
	}

	sub, err := relay.Subscribe(context.Background(), filters)
	if err != nil {
		log.Printf("Failed to subscribe to local relay %s: %v", localRelayURL, err)
		return nil, err
	}

	log.Printf("Successfully subscribed to local relay for %d filters", len(filters))
	return sub.Events, nil
}

// GetLocalPoolEvents retrieves all events from the local pool matching filters
func (cm *ConfigManager) GetLocalPoolEvents(filters []nostr.Filter) ([]*nostr.Event, error) {
	localRelayURL := "ws://localhost:4242"

	relay, err := cm.LocalPool.EnsureRelay(localRelayURL)
	if err != nil {
		log.Printf("Failed to connect to local relay %s: %v", localRelayURL, err)
		return nil, err
	}

	sub, err := relay.Subscribe(context.Background(), filters)
	if err != nil {
		log.Printf("Failed to subscribe to local relay %s: %v", localRelayURL, err)
		return nil, err
	}

	var events []*nostr.Event
	timeout := time.NewTimer(5 * time.Second) // Fallback timeout in case EOSE is never received
	defer timeout.Stop()

	for {
		select {
		case event, ok := <-sub.Events:
			if !ok {
				// Channel closed, return what we have
				return events, nil
			}
			events = append(events, event)
		case <-sub.EndOfStoredEvents:
			// End of stored events received, return immediately
			log.Printf("EOSE received, returning %d events", len(events))
			return events, nil
		case <-timeout.C:
			// Fallback timeout in case EOSE is never received
			log.Printf("Timeout waiting for EOSE, returning %d events", len(events))
			return events, nil
		}
	}
}
