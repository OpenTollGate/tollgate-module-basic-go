package config_manager

import (
	"context"
	"encoding/json" // Re-add json import
	"fmt"
	"log"
	"os"
	"os/exec"       // Re-add for GetInstalledVersion
	"path/filepath" // Add for backupAndLog
	"regexp"        // Re-add for GetArchitecture
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
	config := cm.GetConfig()
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
	// TODO: This state modification doesn't belong here. It modifies the in-memory config
	// but doesn't persist it. This needs to be re-evaluated.
	config.Relays = workingRelays
	return nil, fmt.Errorf("NIP-94 event not found with ID %s", eventID)
}

// PackageInfo holds information extracted from NIP-94 events.
type PackageInfo struct {
	Version        string
	Timestamp      int64
	ReleaseChannel string
}

// ExtractPackageInfo extracts package information from a NIP-94 event.
func ExtractPackageInfo(event *nostr.Event) (*PackageInfo, error) {
	// Dummy implementation for now, needs to be properly implemented based on NIP-94
	// For testing purposes, assume the content is a JSON string with version, timestamp, release_channel
	var pInfo struct {
		Version        string `json:"version"`
		Timestamp      int64  `json:"timestamp"`
		ReleaseChannel string `json:"release_channel"`
	}
	err := json.Unmarshal([]byte(event.Content), &pInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal NIP-94 event content: %w", err)
	}
	return &PackageInfo{
		Version:        pInfo.Version,
		Timestamp:      pInfo.Timestamp,
		ReleaseChannel: pInfo.ReleaseChannel,
	}, nil
}

// ConfigManager manages the configuration files.
type ConfigManager struct {
	ConfigFilePath     string
	InstallFilePath    string
	IdentitiesFilePath string
	config             *Config
	installConfig      *InstallConfig
	identitiesConfig   *IdentitiesConfig
	PublicPool         *nostr.SimplePool
	LocalPool          *nostr.SimplePool
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

	publicPool := nostr.NewSimplePool(context.Background())
	localPool := nostr.NewSimplePool(context.Background())

	cm := &ConfigManager{
		ConfigFilePath:     configPath,
		InstallFilePath:    installPath,
		IdentitiesFilePath: identitiesPath,
		PublicPool:         publicPool,
		LocalPool:          localPool,
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

// GetOwnedIdentity retrieves an owned identity by name.
func (cm *ConfigManager) GetOwnedIdentity(name string) (*OwnedIdentity, error) {
	for _, identity := range cm.identitiesConfig.OwnedIdentities {
		if identity.Name == name {
			return &identity, nil
		}
	}
	return nil, fmt.Errorf("owned identity '%s' not found", name)
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
	installConfig := cm.GetInstallConfig()
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

// generatePrivateKey generates a new Nostr private key.
func generatePrivateKey() (string, error) {
	return nostr.GeneratePrivateKey(), nil
}

// setUsername sets the username on the Nostr profile.
func (cm *ConfigManager) setUsername(privateKey string, username string) error {
	// No longer relying on config.Relays for this.
	// This function might need to be re-evaluated for its purpose given the new identity management.
	// For now, we'll just return nil to avoid breaking compilation.
	log.Printf("setUsername is deprecated and will not publish to relays.")
	return nil
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

func (cm *ConfigManager) GetReleaseChannel() (string, error) {
	installConfig := cm.GetInstallConfig()
	if installConfig == nil {
		return "", fmt.Errorf("install config not found")
	}
	return installConfig.ReleaseChannel, nil
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
