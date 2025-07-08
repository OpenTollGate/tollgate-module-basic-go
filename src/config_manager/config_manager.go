package config_manager

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/go-version"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

// CurrentConfigVersion is the latest version of the config.json format.
const CurrentConfigVersion = "v0.0.4"

// CurrentInstallVersion is the latest version of the install.json format.
const CurrentInstallVersion = "v0.0.2"

// CurrentIdentityVersion is the latest version of the identities.json format.
const CurrentIdentityVersion = "v0.0.1"

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

// MerchantConfig holds configuration specific to the merchant
type Identity struct {
	Name             string `json:"name"`
	Key              string `json:"key"`
	KeyFormat        string `json:"key_format"` // "nsec", "npub", or "hex_private"
	LightningAddress string `json:"lightning_address"`
}

type MerchantConfig struct {
	Identity string `json:"identity"`
}

// IdentityConfig holds the identities configuration parameters, including its version
type IdentityConfig struct {
	ConfigVersion string     `json:"config_version"`
	Identities    []Identity `json:"identities"`
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
	Factor   float64 `json:"factor"`
	Identity string  `json:"identity"`
}

// Config holds the configuration parameters
type PackageInfo struct {
	Version        string
	Timestamp      int64
	ReleaseChannel string
}

type Config struct {
	ConfigVersion         string              `json:"config_version"`
	// TollgatePrivateKey has been moved to identities.json
	AcceptedMints         []MintConfig        `json:"accepted_mints"`
	ProfitShare           []ProfitShareConfig `json:"profit_share"`
	StepSize              uint64              `json:"step_size"`
	Metric                string              `json:"metric"`
	Bragging              BraggingConfig      `json:"bragging"`
	Merchant              MerchantConfig      `json:"merchant"`
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
	ConfigVersion          string `json:"config_version"`
	PackagePath            string `json:"package_path"`
	IPAddressRandomized    bool   `json:"ip_address_randomized"`
	InstallTimestamp       int64  `json:"install_time"`
	DownloadTimestamp      int64  `json:"download_time"`
	ReleaseChannel         string `json:"release_channel"`
	EnsureDefaultTimestamp int64  `json:"ensure_default_timestamp"`
	InstalledVersion       string `json:"installed_version"` // Added this field
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
	_, err = cm.EnsureDefaultIdentities()
	if err != nil {
		return err
	}
	err = cm.UpdateCurrentInstallationID()
	if err != nil {
		return err
	}
	return nil
}

func (cm *ConfigManager) identitiesFilePath() string {
	return filepath.Join(filepath.Dir(cm.FilePath), "identities.json")
}

// GetIdentity retrieves a specific identity object by its name.
func (cm *ConfigManager) GetIdentity(name string) (*Identity, error) {
	identityConfig, err := cm.LoadIdentities()
	if err != nil {
		return nil, fmt.Errorf("failed to load identities: %w", err)
	}
	if identityConfig == nil {
		return nil, fmt.Errorf("identities config is nil")
	}

	for _, identity := range identityConfig.Identities {
		if identity.Name == name {
			return &identity, nil
		}
	}
	return nil, fmt.Errorf("identity not found: %s", name)
}

// GetPrivateKey retrieves the private key for a given identity as an nsec string.
// It handles conversion from hex if necessary.
func (cm *ConfigManager) GetPrivateKey(identityName string) (string, error) {
	identity, err := cm.GetIdentity(identityName)
	if err != nil {
		return "", err
	}

	switch identity.KeyFormat {
	case "nsec":
		return identity.Key, nil
	case "hex_private":
		nsec, err := nip19.EncodePrivateKey(identity.Key)
		if err != nil {
			return "", fmt.Errorf("failed to encode hex private key to nsec: %w", err)
		}
		return nsec, nil
	default:
		return "", fmt.Errorf("private key not available or in unsupported format for identity %s: %s", identityName, identity.KeyFormat)
	}
}

// GetPublicKey retrieves the public key for a given identity.
// It derives the public key from a private key if present, otherwise it returns the stored public key.
func (cm *ConfigManager) GetPublicKey(identityName string) (string, error) {
	identity, err := cm.GetIdentity(identityName)
	if err != nil {
		return "", err
	}

	switch identity.KeyFormat {
	case "nsec":
		_, data, err := nip19.Decode(identity.Key)
		if err != nil {
			return "", fmt.Errorf("failed to decode nsec: %w", err)
		}
		hexKey, ok := data.(string)
		if !ok {
			return "", fmt.Errorf("decoded nsec is not a string")
		}
		pubKey, err := nostr.GetPublicKey(hexKey)
		if err != nil {
			return "", fmt.Errorf("failed to derive public key from hex private key: %w", err)
		}
		return pubKey, nil
	case "hex_private":
		pubKey, err := nostr.GetPublicKey(identity.Key)
		if err != nil {
			return "", fmt.Errorf("failed to derive public key from hex private key: %w", err)
		}
		return pubKey, nil
	case "npub":
		_, data, err := nip19.Decode(identity.Key)
		if err != nil {
			return "", fmt.Errorf("failed to decode npub: %w", err)
		}
		hexPubKey, ok := data.(string)
		if !ok {
			return "", fmt.Errorf("decoded npub is not a string")
		}
		return hexPubKey, nil
	default:
		return "", fmt.Errorf("public key not available or in unsupported format for identity %s: %s", identityName, identity.KeyFormat)
	}
}

// LoadIdentities reads the identities from the managed file, handling both versioned and unversioned formats.
func (cm *ConfigManager) LoadIdentities() (*IdentityConfig, error) {
	data, err := os.ReadFile(cm.identitiesFilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // Return nil config if file does not exist
		}
		return nil, fmt.Errorf("error reading identities file: %w", err)
	}
	if len(data) == 0 {
		return nil, nil // Return nil config if file is empty
	}

	var identityConfig IdentityConfig
	err = json.Unmarshal(data, &identityConfig)
	if err == nil && identityConfig.ConfigVersion != "" {
		// Successfully unmarshalled into IdentityConfig with a version, return it.
		return &identityConfig, nil
	}

	// If unmarshalling into IdentityConfig failed or no version was present,
	// try unmarshalling into the old unversioned []Identity format.
	var oldIdentities []Identity
	err = json.Unmarshal(data, &oldIdentities)
	if err == nil {
		// Successfully unmarshalled into old format, wrap it in a new IdentityConfig
		// and mark it with the previous version for potential migration.
		log.Printf("Unversioned identities file found at %s. Migrating to versioned format.", cm.identitiesFilePath())
		return &IdentityConfig{
			ConfigVersion: CurrentIdentityVersion, // Assuming CurrentIdentityVersion is the version this unversioned config should be treated as.
			Identities:    oldIdentities,
		}, nil
	}

	// If both attempts failed, log and treat as empty/non-existent.
	log.Printf("Error unmarshalling identities file %s into either versioned or unversioned format: %v. Treating as empty/non-existent.", cm.identitiesFilePath(), err)
	return nil, nil
}

// SaveIdentities writes the IdentityConfig to the managed file with pretty formatting
func (cm *ConfigManager) SaveIdentities(identityConfig *IdentityConfig) error {
	data, err := json.MarshalIndent(identityConfig, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cm.identitiesFilePath(), data, 0644)
}

// EnsureDefaultIdentities ensures a default identities file exists, creating it if necessary
func (cm *ConfigManager) EnsureDefaultIdentities() (*IdentityConfig, error) {
	identityConfig, err := cm.LoadIdentities()
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	changed := false
	if identityConfig == nil {
		log.Printf("No identities file found. Creating new default identities.")
		identityConfig = &IdentityConfig{
			ConfigVersion: CurrentIdentityVersion,
			Identities:    []Identity{},
		}
		changed = true
	}

	// Ensure default identities "operator" and "developer" exist
	identitiesMap := make(map[string]*Identity)
	for i := range identityConfig.Identities {
		identitiesMap[identityConfig.Identities[i].Name] = &identityConfig.Identities[i]
	}

	// Operator Identity
	operatorIdentity, foundOperator := identitiesMap["operator"]
	if !foundOperator {
		log.Printf("Default identity 'operator' missing. Adding.")
		operatorIdentity = &Identity{Name: "operator", LightningAddress: "tollgate@minibits.cash"}
		identityConfig.Identities = append(identityConfig.Identities, *operatorIdentity)
		identitiesMap["operator"] = operatorIdentity // Update map with pointer to newly appended element
		changed = true
	}

	// Developer Identity
	developerIdentity, foundDeveloper := identitiesMap["developer"]
	if !foundDeveloper {
		log.Printf("Default identity 'developer' missing. Adding.")
		developerIdentity = &Identity{Name: "developer", LightningAddress: "tollgate@minibits.cash"}
		identityConfig.Identities = append(identityConfig.Identities, *developerIdentity)
		identitiesMap["developer"] = developerIdentity // Update map with pointer to newly appended element
		changed = true
	}

	// Populate missing fields for existing identities
	for i := range identityConfig.Identities {
		identity := &identityConfig.Identities[i]
		if identity.Name == "operator" {
			// If operator's key is empty, generate a new one
			if identity.Key == "" {
				log.Printf("Operator key missing. Generating new key.")
				privateKey := nostr.GeneratePrivateKey()
				nsec, err := nip19.EncodePrivateKey(privateKey)
				if err != nil {
					return nil, fmt.Errorf("failed to encode private key to nsec: %w", err)
				}
				identity.Key = nsec
				identity.KeyFormat = "nsec"
				changed = true
			}
			// Ensure KeyFormat is set if Key is present
			if identity.Key != "" && identity.KeyFormat == "" {
				// Attempt to determine format. Assume nsec if it decodes.
				_, _, err := nip19.Decode(identity.Key) // Use nip19.Decode for nsec
				if err == nil {
					identity.KeyFormat = "nsec"
				} else {
					// Fallback: if it's not nsec, assume hex_private for now for migration purposes
					identity.KeyFormat = "hex_private"
				}
				changed = true
			}
		}

		if identity.LightningAddress == "" {
			log.Printf("Identity '%s' missing LightningAddress. Setting to default.", identity.Name)
			identity.LightningAddress = "tollgate@minibits.cash"
			changed = true
		}
	}

	if identityConfig.ConfigVersion != CurrentIdentityVersion {
		log.Printf("Updating identities config version from '%s' to '%s'", identityConfig.ConfigVersion, CurrentIdentityVersion)
		identityConfig.ConfigVersion = CurrentIdentityVersion
		changed = true
	}

	if changed {
		log.Printf("Saving updated identities configuration to %s", cm.identitiesFilePath())
		if err = cm.SaveIdentities(identityConfig); err != nil {
			return nil, err
		}
	}

	return identityConfig, nil
}

func (cm *ConfigManager) EnsureDefaultInstall() (*InstallConfig, error) {
	CURRENT_TIMESTAMP := time.Now().Unix()
	installConfig, err := cm.LoadInstallConfig()
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	changed := false
	if installConfig == nil {
		log.Printf("No install file found. Creating new default install config.")
		installConfig = &InstallConfig{}
		changed = true
	}

	// Use a map to check for the actual presence of keys, to distinguish missing vs. zero-value
	rawMap := make(map[string]interface{})
	rawBytes, err := os.ReadFile(cm.installFilePath())
	if err == nil { // Only unmarshal if file exists and is readable
		json.Unmarshal(rawBytes, &rawMap)
	}

	if installConfig.ConfigVersion == "" || installConfig.ConfigVersion != CurrentInstallVersion {
		if installConfig.ConfigVersion != "" {
			log.Printf("Updating install config version from '%s' to '%s'", installConfig.ConfigVersion, CurrentInstallVersion)
		} else {
			log.Printf("Setting install config version to '%s'", CurrentInstallVersion)
		}
		installConfig.ConfigVersion = CurrentInstallVersion
		changed = true
	}

	if installConfig.PackagePath == "" {
		log.Printf("PackagePath missing. Setting to default.")
		installConfig.PackagePath = "/usr/bin/tollgate" // Default path
		changed = true
	}

	if installConfig.InstallTimestamp == 0 {
		log.Printf("InstallTimestamp missing. Can only be set during first install.")
	}

	if installConfig.DownloadTimestamp == 0 {
		log.Printf("DownloadTimestamp missing. Can only be set when downloading.")

	}

	if installConfig.EnsureDefaultTimestamp == 0 {
		log.Printf("EnsureDefaultTimestamp missing. Setting to current time.")
		installConfig.EnsureDefaultTimestamp = CURRENT_TIMESTAMP
		changed = true
	}

	if installConfig.InstalledVersion == "" {
		log.Printf("InstalledVersion missing. Setting to current time.")
		installedVersion, err := GetInstalledVersion()
		if err != nil {
			return nil, fmt.Errorf("error getting installed version: %w", err)
		}
		installConfig.InstalledVersion = installedVersion
		changed = true
	}

	if _, ok := rawMap["ip_address_randomized"]; !ok {
		log.Printf("IPAddressRandomized missing. Setting to default (false).")
		installConfig.IPAddressRandomized = false
		changed = true
	}

	if changed {
		log.Printf("Saving updated install configuration to %s", cm.installFilePath())
		if err = cm.SaveInstallConfig(installConfig); err != nil {
			return nil, err
		}
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

// CalculateMinPayment calculates the minimum payment amount for a given mint fee.
func CalculateMinPayment(mintFee uint64) uint64 {
	// Stub implementation: return the mint fee as the minimum payment
	// The actual fee depends on the keyset and stuff..
	return 2*mintFee + 1
}

// GetInstalledVersion retrieves the installed version of the tollgate application.
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
	// The setUsername function requires a loaded config. For initial generation,
	// we'll attempt to set the username after the config is saved.
	// This might still log "Failed to set username: config is nil" if called before save,
	// but the private key generation itself is independent.
	// The actual setting of username will happen when EnsureDefaultConfig saves the config.
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

// EnsureDefaultConfig ensures a default config file exists, creating it if necessary
func (cm *ConfigManager) EnsureDefaultConfig() (*Config, error) {
	config, err := cm.LoadConfig()
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	changed := false
	if config == nil {
		log.Printf("No config file found. Creating new default config.")
		config = &Config{
			ConfigVersion: CurrentConfigVersion,
			AcceptedMints: []MintConfig{
				{URL: "https://mint.minibits.cash/mint", MinBalance: 1000, BalanceTolerancePercent: 5, PayoutIntervalSeconds: 3600, MinPayoutAmount: 1000, PricePerStep: 1, PriceUnit: "sat", MinPurchaseSteps: 1000},
			},
			ProfitShare:        []ProfitShareConfig{},
			StepSize:           1000,
			Metric:             "bytes",
			Bragging:           BraggingConfig{Enabled: false},
			Merchant:           MerchantConfig{Identity: "operator"},
			Relays:             []string{"wss://relay.minibits.cash/", "wss://relay.getalby.com/v1"},
			ShowSetup:          true,
		}
		changed = true
	}

	// Ensure default values for fields that might be missing in older configs
	if config.ConfigVersion == "" {
		log.Printf("ConfigVersion missing. Setting to default.")
		config.ConfigVersion = CurrentConfigVersion
		changed = true
	}

	if config.AcceptedMints == nil || len(config.AcceptedMints) == 0 {
		log.Printf("AcceptedMints missing. Setting to default.")
		config.AcceptedMints = []MintConfig{
			{URL: "https://mint.minibits.cash/Bitcoin", MinBalance: 1000, BalanceTolerancePercent: 5, PayoutIntervalSeconds: 3600, MinPayoutAmount: 1000, PricePerStep: 1, PriceUnit: "sat", MinPurchaseSteps: 1000},
		}
		changed = true
	}

	if config.ProfitShare == nil {
		log.Printf("ProfitShare missing. Populating with default profit share.")
		config.ProfitShare = []ProfitShareConfig{
			{Factor: 0.70, Identity: "operator"},
			{Factor: 0.30, Identity: "developer"},
		}
		changed = true
	}

	if config.StepSize == 0 {
		log.Printf("StepSize missing. Setting to default.")
		config.StepSize = 600000
		changed = true
	}

	if config.Metric == "" {
		log.Printf("Metric missing. Setting to default.")
		config.Metric = "milliseconds"
		changed = true
	}

	if (Config{}) == config.Bragging {
		log.Printf("Bragging config missing. Setting to default (disabled).")
		config.Bragging = BraggingConfig{
			Enabled: false,
			Fields:  []string{"amount", "mint", "duration"},
		}		
		changed = true
	}

	if (Config{}) == config.Merchant {
		log.Printf("Merchant config missing. Setting to default (operator).")
		config.Merchant = MerchantConfig{Identity: "operator"}
		changed = true
	}

	if config.Relays == nil || len(config.Relays) == 0 {
		log.Printf("Relays missing. Setting to default.")
		config.Relays = []string{"wss://relay.minibits.cash/", "wss://relay.getalby.com/v1"}
		changed = true
	}

	// Check if ShowSetup exists in the original JSON to avoid overwriting if explicitly set to false
	rawMap := make(map[string]interface{})
	rawBytes, err := os.ReadFile(cm.FilePath)
	if err == nil { // Only unmarshal if file exists and is readable
		json.Unmarshal(rawBytes, &rawMap)
	}
	if _, ok := rawMap["show_setup"]; !ok {
		log.Printf("ShowSetup missing. Setting to default (true).")
		config.ShowSetup = true
		changed = true
	}

	if config.CurrentInstallationID == "" {
		log.Printf("CurrentInstallationID missing. Generating new ID.")
		config.CurrentInstallationID = generateInstallationID() // Ensure this function is defined elsewhere
		changed = true
	}

	// Update config version if it's older
	currentVer, err := version.NewVersion(CurrentConfigVersion)
	if err != nil {
		return nil, fmt.Errorf("error parsing current config version: %w", err)
	}
	configVer, err := version.NewVersion(config.ConfigVersion)
	if err != nil {
		log.Printf("Warning: error parsing existing config version '%s': %v. Assuming older version.", config.ConfigVersion, err)
		configVer = version.Must(version.NewVersion("v0.0.0")) // Treat as very old
	}

	if configVer.LessThan(currentVer) {
		log.Printf("Updating config version from '%s' to '%s'", config.ConfigVersion, CurrentConfigVersion)
		config.ConfigVersion = CurrentConfigVersion
		changed = true
	}

	if changed {
		log.Printf("Saving updated configuration to %s", cm.FilePath)
		if err = cm.SaveConfig(config); err != nil {
			return nil, err
		}
		// The private key is now managed by EnsureDefaultIdentities,
		// and setUsername will be called from there if needed.
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
			return "", fmt.Errorf("error loading install config: %w", err)
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

func getIPAddress() {
	// Gets the IP address of
	// root@OpenWrt:/tmp# ifconfig br-lan | grep 'inet addr:' | cut -d: -f2 | awk '{ print $1}'
	// 172.20.203.1
	// Use commands like the above or the go net package to get the IP address this device's LAN interface.
}
