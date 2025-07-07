package config_manager

import (
	"log"
	"os"
	"testing"

	"encoding/json"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
	"io/ioutil"
	"path/filepath"

	"github.com/stretchr/testify/assert"
)

// Helper functions for comparison
func compareBraggingConfig(a, b *BraggingConfig) bool {
	if a.Enabled != b.Enabled {
		return false
	}
	return compareStringSlices(a.Fields, b.Fields)
}

func compareStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func compareMintConfigs(a, b []MintConfig) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].URL != b[i].URL ||
			a[i].MinBalance != b[i].MinBalance ||
			a[i].BalanceTolerancePercent != b[i].BalanceTolerancePercent ||
			a[i].PayoutIntervalSeconds != b[i].PayoutIntervalSeconds ||
			a[i].MinPayoutAmount != b[i].MinPayoutAmount {
			return false
		}
	}
	return true
}

func TestConfigManager(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	cm, err := NewConfigManager(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Test EnsureInitializedConfig
	err = cm.EnsureInitializedConfig()
	assert.NoError(t, err)
	config, err := cm.LoadConfig()
	if err != nil {
		t.Errorf("EnsureDefaultConfig returned error: %v", err)
	}
	if config == nil {
		t.Errorf("EnsureDefaultConfig returned nil config")
	}

	// Test LoadConfig
	loadedConfig, err := cm.LoadConfig()
	if err != nil {
		t.Errorf("LoadConfig returned error: %v", err)
	}
	if loadedConfig == nil {
		t.Errorf("LoadConfig returned nil config")
	}

	// Test SaveConfig
	newConfig := &Config{
		AcceptedMints: []MintConfig{
			{
				URL:                     "test_mint",
				MinBalance:              100,
				BalanceTolerancePercent: 10,
				PayoutIntervalSeconds:   60,
				MinPayoutAmount:         1000,
				MinPurchaseSteps:        1,
				PricePerStep:            1,
			},
		},
		Metric:   "milliseconds",
		StepSize: 120000,
		Bragging: BraggingConfig{
			Enabled: true,
			Fields:  []string{"test_field"},
		},
		Relays:                []string{"test_relay"},
		ShowSetup:             true,
		CurrentInstallationID: "test_current_installation_id",
	}
	err = cm.SaveConfig(newConfig)
	if err != nil {
		t.Errorf("SaveConfig returned error: %v", err)
	}

	loadedConfig, err = cm.LoadConfig()
	if err != nil {
		t.Errorf("LoadConfig returned error after SaveConfig: %v", err)
	}
	// Verify all fields
	if !compareMintConfigs(loadedConfig.AcceptedMints, newConfig.AcceptedMints) ||
		loadedConfig.Metric != "milliseconds" ||
		loadedConfig.StepSize != 120000 ||
		!compareBraggingConfig(&loadedConfig.Bragging, &newConfig.Bragging) ||
		!compareStringSlices(loadedConfig.Relays, newConfig.Relays) ||
		loadedConfig.ShowSetup != newConfig.ShowSetup ||
		loadedConfig.CurrentInstallationID != newConfig.CurrentInstallationID {
		t.Errorf("Loaded config does not match saved config")
	}

	// Test LoadInstallConfig and SaveInstallConfig
	// Remove install.json file if it exists
	os.Remove(cm.installFilePath())
	installConfig, err := cm.LoadInstallConfig()
	if err != nil {
		t.Errorf("LoadInstallConfig returned error: %v", err)
	}
	if installConfig != nil {
		t.Errorf("LoadInstallConfig returned non-nil config")
	}

	newInstallConfig := &InstallConfig{
		ConfigVersion:       "v0.0.2", // New installs get v0.0.2
		PackagePath:         "/path/to/package",
		IPAddressRandomized: true, // Initialize as boolean true
	}
	err = cm.SaveInstallConfig(newInstallConfig)
	assert.NoError(t, err)

	loadedInstallConfig, err := cm.LoadInstallConfig()
	assert.NoError(t, err)
	assert.Equal(t, newInstallConfig.ConfigVersion, loadedInstallConfig.ConfigVersion)
	assert.Equal(t, newInstallConfig.PackagePath, loadedInstallConfig.PackagePath)
	assert.Equal(t, newInstallConfig.IPAddressRandomized, loadedInstallConfig.IPAddressRandomized)
	assert.Equal(t, newInstallConfig.InstallTimestamp, loadedInstallConfig.InstallTimestamp)
	assert.Equal(t, newInstallConfig.DownloadTimestamp, loadedInstallConfig.DownloadTimestamp)
	assert.Equal(t, newInstallConfig.ReleaseChannel, loadedInstallConfig.ReleaseChannel)
	assert.Equal(t, newInstallConfig.EnsureDefaultTimestamp, loadedInstallConfig.EnsureDefaultTimestamp)
	assert.Equal(t, newInstallConfig.InstalledVersion, loadedInstallConfig.InstalledVersion)
}

func TestEnsureDefaultConfig_MissingFields(t *testing.T) {
	// Create a temporary directory for test config files
	tempDir, err := ioutil.TempDir("", "test_ensure_default_config_missing_fields")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir) // Clean up the temporary directory

	configFile := filepath.Join(tempDir, "config.json")

	// Create a config.json file with some missing fields
	initalContent := `
{
	"config_version": "v0.0.4",
	"step_size": 100000,
	"metric": "seconds"
}
`
	err = ioutil.WriteFile(configFile, []byte(initalContent), 0644)
	assert.NoError(t, err)

	cm, err := NewConfigManager(configFile)
	assert.NoError(t, err)

	// Ensure default config should populate missing fields
	err = cm.EnsureInitializedConfig()
	assert.NoError(t, err)
	config, err := cm.LoadConfig()
	assert.NoError(t, err)
	assert.NotNil(t, config)

	// Verify missing fields are populated with defaults
	assert.Equal(t, CurrentConfigVersion, config.ConfigVersion)
	assert.NotEmpty(t, config.AcceptedMints)
	assert.NotEmpty(t, config.ProfitShare)
	assert.Equal(t, uint64(100000), config.StepSize)
	assert.Equal(t, "seconds", config.Metric)
	assert.True(t, config.Bragging.Enabled)
	assert.NotEmpty(t, config.Bragging.Fields)
	assert.NotEmpty(t, config.Relays)
	assert.True(t, config.ShowSetup) // Default is true
	assert.NotEmpty(t, config.Merchant.Identity)

	// Verify the file on disk is updated
	updatedContent, err := ioutil.ReadFile(configFile)
	assert.NoError(t, err)
	var loadedConfig Config
	err = json.Unmarshal(updatedContent, &loadedConfig)
	assert.NoError(t, err)

	assert.Equal(t, config.ConfigVersion, loadedConfig.ConfigVersion)
	assert.Equal(t, len(config.AcceptedMints), len(loadedConfig.AcceptedMints))
	assert.Equal(t, len(config.ProfitShare), len(loadedConfig.ProfitShare))
	assert.Equal(t, config.StepSize, loadedConfig.StepSize)
	assert.Equal(t, config.Metric, loadedConfig.Metric)
	assert.Equal(t, config.Bragging.Enabled, loadedConfig.Bragging.Enabled)
	assert.Equal(t, len(config.Bragging.Fields), len(loadedConfig.Bragging.Fields))
	assert.Equal(t, len(config.Relays), len(loadedConfig.Relays))
	assert.Equal(t, config.ShowSetup, loadedConfig.ShowSetup)
	assert.Equal(t, config.Merchant.Identity, loadedConfig.Merchant.Identity)
}

func TestEnsureDefaultInstall_MissingFields(t *testing.T) {
	// Create a temporary directory for test config files
	tempDir, err := ioutil.TempDir("", "test_ensure_default_install_missing_fields")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir) // Clean up the temporary directory

	installFile := filepath.Join(tempDir, "install.json")
	configFile := filepath.Join(tempDir, "config.json") // Need a dummy config.json for NewConfigManager

	// Create an install.json file with some missing fields
	initialContent := `
{
	"config_version": "v0.0.2",
	"package_path": "/existing/path",
	"ip_address_randomized": true
}
`
	err = ioutil.WriteFile(installFile, []byte(initialContent), 0644)
	assert.NoError(t, err)

	cm, err := NewConfigManager(configFile)
	assert.NoError(t, err)

	// Ensure default install should populate missing fields
	installConfig, err := cm.EnsureDefaultInstall()
	assert.NoError(t, err)
	assert.NotNil(t, installConfig)

	// Verify missing fields are populated with defaults
	assert.Equal(t, CurrentInstallVersion, installConfig.ConfigVersion)
	assert.Equal(t, "/existing/path", installConfig.PackagePath)
	assert.True(t, installConfig.IPAddressRandomized)
	assert.NotZero(t, installConfig.EnsureDefaultTimestamp) // Should be set to current timestamp
	assert.Equal(t, "stable", installConfig.ReleaseChannel)
	assert.Equal(t, "0.0.0", installConfig.InstalledVersion)

	// Verify InstallTimestamp and DownloadTimestamp remain 0
	assert.Zero(t, installConfig.InstallTimestamp)
	assert.Zero(t, installConfig.DownloadTimestamp)

	// Verify the file on disk is updated
	updatedContent, err := ioutil.ReadFile(installFile)
	assert.NoError(t, err)
	var loadedInstall InstallConfig
	err = json.Unmarshal(updatedContent, &loadedInstall)
	assert.NoError(t, err)

	assert.Equal(t, installConfig.ConfigVersion, loadedInstall.ConfigVersion)
	assert.Equal(t, installConfig.PackagePath, loadedInstall.PackagePath)
	assert.Equal(t, installConfig.IPAddressRandomized, loadedInstall.IPAddressRandomized)
	assert.Equal(t, installConfig.InstallTimestamp, loadedInstall.InstallTimestamp)
	assert.Equal(t, installConfig.DownloadTimestamp, loadedInstall.DownloadTimestamp)
	assert.Equal(t, installConfig.ReleaseChannel, loadedInstall.ReleaseChannel)
	assert.Equal(t, installConfig.EnsureDefaultTimestamp, loadedInstall.EnsureDefaultTimestamp)
	assert.Equal(t, installConfig.InstalledVersion, loadedInstall.InstalledVersion)
}

func TestEnsureDefaultInstall_UnversionedConfig(t *testing.T) {
	// Create a temporary directory for test config files
	tempDir, err := ioutil.TempDir("", "test_ensure_default_install_unversioned")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir) // Clean up the temporary directory

	installFile := filepath.Join(tempDir, "install.json")
	configFile := filepath.Join(tempDir, "config.json") // Need a dummy config.json for NewConfigManager

	// Create an unversioned install.json file (simulating v0.0.1)
	unversionedContent := `{"package_path":"/old/path","ip_address_randomized":false}`
	err = ioutil.WriteFile(installFile, []byte(unversionedContent), 0644)
	assert.NoError(t, err)

	cm, err := NewConfigManager(configFile)
	assert.NoError(t, err)

	// Ensure default install should load the unversioned and add the version
	installConfig, err := cm.EnsureDefaultInstall()
	assert.NoError(t, err)
	assert.NotNil(t, installConfig)

	// Verify the config_version is set to v0.0.1
	assert.Equal(t, CurrentInstallVersion, installConfig.ConfigVersion, "Unversioned install.json should be marked as CurrentInstallVersion")

	// Verify other fields are preserved
	assert.Equal(t, "/old/path", installConfig.PackagePath)
	assert.Equal(t, false, installConfig.IPAddressRandomized)

	// Verify the file on disk is updated
	updatedContent, err := ioutil.ReadFile(installFile)
	assert.NoError(t, err)
	var loadedInstall InstallConfig
	err = json.Unmarshal(updatedContent, &loadedInstall)
	assert.NoError(t, err)
	assert.Equal(t, CurrentInstallVersion, loadedInstall.ConfigVersion)
	assert.Equal(t, "/old/path", loadedInstall.PackagePath)
	assert.Equal(t, false, loadedInstall.IPAddressRandomized)
}

func TestUpdateCurrentInstallationID(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	cm, err := NewConfigManager(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Ensure config is initialized before testing UpdateCurrentInstallationID
	err = cm.EnsureInitializedConfig()
	if err != nil {
		t.Fatalf("Failed to ensure initialized config: %v", err)
	}

	// Test UpdateCurrentInstallationID
	log.Println("Testing UpdateCurrentInstallationID")
	err = cm.UpdateCurrentInstallationID()
	if err != nil {
		t.Errorf("Error updating CurrentInstallationID: %v", err)
	} else {
		log.Println("Successfully updated CurrentInstallationID")
	}
	config, err := cm.LoadConfig()
	if err != nil {
		t.Errorf("Error loading config after update: %v", err)
	} else {
		log.Printf("CurrentInstallationID after update: %s", config.CurrentInstallationID)
	}
}

func TestSetUsername(t *testing.T) {
	tempDir, err := ioutil.TempDir("", "test_set_username")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	configFile := filepath.Join(tempDir, "config.json")
	cm, err := NewConfigManager(configFile)
	assert.NoError(t, err)

	_, err = cm.EnsureDefaultIdentities() // Ensure operator identity exists
	assert.NoError(t, err)

	// Get the private key for the "operator" identity
	operatorPrivateKey, err := cm.GetPrivateKey("operator")
	assert.NoError(t, err)
	assert.NotEmpty(t, operatorPrivateKey)

	err = cm.setUsername(operatorPrivateKey, "test_c03rad0r")
	assert.NoError(t, err)
	// Additional checks can be added here to verify the username is set correctly on relays
}

func TestEnsureInitializedConfig(t *testing.T) {
	// Create a temporary directory for test config files
	tempDir, err := ioutil.TempDir("", "test_config_manager")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir) // Clean up the temporary directory

	configFile := filepath.Join(tempDir, "config.json")
	installFile := filepath.Join(tempDir, "install.json")

	cm, err := NewConfigManager(configFile)
	assert.NoError(t, err)

	// Ensure the config files are initialized
	err = cm.EnsureInitializedConfig()
	assert.NoError(t, err)

	// Verify config.json was created
	_, err = os.Stat(configFile)
	assert.NoError(t, err, "config.json should exist")

	// Verify install.json was created
	_, err = os.Stat(installFile)
	assert.NoError(t, err, "install.json should exist")

	// Read and verify content of config.json
	configContent, err := ioutil.ReadFile(configFile)
	assert.NoError(t, err)
	var config Config
	err = json.Unmarshal(configContent, &config)
	assert.NoError(t, err, "config.json should be valid JSON")
	assert.NotNil(t, config.Bragging, "config.json should contain 'Bragging' section")
	assert.NotNil(t, config.Merchant, "config.json should contain 'Merchant' section")

	// Read and verify content of install.json
	installContent, err := ioutil.ReadFile(installFile)
	assert.NoError(t, err)
	var install InstallConfig
	err = json.Unmarshal(installContent, &install)
	assert.NoError(t, err, "install.json should be valid JSON")
	assert.NotNil(t, install.InstalledVersion, "install.json should contain 'InstalledVersion'")
}

func TestEnsureInitializedConfig_FilesAlreadyExist(t *testing.T) {
	// Create a temporary directory for test config files
	tempDir, err := ioutil.TempDir("", "test_config_manager_existing")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir) // Clean up the temporary directory

	configFile := filepath.Join(tempDir, "config.json")
	installFile := filepath.Join(tempDir, "install.json")

	// Create dummy existing files with valid JSON structure for the types
	err = ioutil.WriteFile(configFile, []byte(`{"config_version":"v0.0.0","bragging":{"enabled":false,"fields":[]},"merchant":{"identity":"operator"}}`), 0644)
	assert.NoError(t, err)
	// For install.json, we'll create a v0.0.2 version to test existing
	err = ioutil.WriteFile(installFile, []byte(`{"config_version":"v0.0.2","installed_version":"1.0.0","package_path":"existing_path","ip_address_randomized":true}`), 0644)
	assert.NoError(t, err)

	cm, err := NewConfigManager(configFile)
	assert.NoError(t, err)

	// Ensure the config files are initialized - should not overwrite existing
	err = cm.EnsureInitializedConfig()
	assert.NoError(t, err)

	// Verify config.json content is largely unchanged (only missing fields should be added)
	configContent, err := ioutil.ReadFile(configFile)
	assert.NoError(t, err)
	var loadedConfig Config
	err = json.Unmarshal(configContent, &loadedConfig)
	assert.NoError(t, err)
	assert.Equal(t, "operator", loadedConfig.Merchant.Identity, "config.json merchant identity should not be overwritten")
	// The other fields like Relays, TrustedMaintainers, etc., should be populated by EnsureDefaultConfig

	// Verify install.json content is largely unchanged (only missing fields should be added)
	installContent, err := ioutil.ReadFile(installFile)
	assert.NoError(t, err)
	var loadedInstall InstallConfig
	err = json.Unmarshal(installContent, &loadedInstall)
	assert.NoError(t, err)
	assert.Equal(t, "1.0.0", loadedInstall.InstalledVersion, "install.json InstalledVersion should not be overwritten")
	assert.Equal(t, "existing_path", loadedInstall.PackagePath, "install.json PackagePath should not be overwritten")
	assert.Equal(t, true, loadedInstall.IPAddressRandomized, "install.json IPAddressRandomized should be populated")
	assert.Equal(t, "v0.0.2", loadedInstall.ConfigVersion, "install.json ConfigVersion should be v0.0.2")
}

func TestEnsureDefaultIdentities_MissingFields(t *testing.T) {
	// Create a temporary directory for test config files
	tempDir, err := ioutil.TempDir("", "test_ensure_default_identities_missing_fields")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir) // Clean up the temporary directory

	identitiesFile := filepath.Join(tempDir, "identities.json")
	configFile := filepath.Join(tempDir, "config.json") // Need a dummy config.json for NewConfigManager

	// Create an identities.json file with some missing fields (e.g., lightning_address)
	initialContent := `
{
	"config_version": "v0.0.1",
	"identities": [
		{
			"name": "Test Identity 1",
			"npub": "npub1..."
		}
	]
}
`
	err = ioutil.WriteFile(identitiesFile, []byte(initialContent), 0644)
	assert.NoError(t, err)

	cm, err := NewConfigManager(configFile)
	assert.NoError(t, err)

	// Ensure default identities should populate missing fields
	identitiesConfig, err := cm.EnsureDefaultIdentities()
	assert.NoError(t, err)
	assert.NotNil(t, identitiesConfig)

	// Verify missing fields are populated with defaults
	assert.Equal(t, CurrentIdentityVersion, identitiesConfig.ConfigVersion)
	assert.Len(t, identitiesConfig.Identities, 4) // Test Identity 1, operator, developer, trusted_maintainer_1
	assert.Equal(t, "Test Identity 1", identitiesConfig.Identities[0].Name)
	assert.Equal(t, "tollgate@minibits.cash", identitiesConfig.Identities[0].LightningAddress) // Should be defaulted

	// Verify that trusted_maintainer_1 is present and has a generated key
	foundTrustedMaintainer := false
	var trustedMaintainerKey string
	for _, identity := range identitiesConfig.Identities {
		if identity.Name == "trusted_maintainer_1" {
			foundTrustedMaintainer = true
			trustedMaintainerKey = identity.Key
			assert.NotEmpty(t, identity.Key)
			assert.Equal(t, "tollgate@minibits.cash", identity.LightningAddress)
			break
		}
	}
	assert.True(t, foundTrustedMaintainer, "trusted_maintainer_1 identity not found")

	// Verify the file on disk is updated
	updatedContent, err := ioutil.ReadFile(identitiesFile)
	assert.NoError(t, err)
	var loadedIdentities IdentityConfig
	err = json.Unmarshal(updatedContent, &loadedIdentities)
	assert.NoError(t, err)

	assert.Equal(t, identitiesConfig.ConfigVersion, loadedIdentities.ConfigVersion)
	assert.Len(t, loadedIdentities.Identities, 4)
	assert.Equal(t, identitiesConfig.Identities[0].Name, loadedIdentities.Identities[0].Name)
	assert.Equal(t, identitiesConfig.Identities[0].LightningAddress, loadedIdentities.Identities[0].LightningAddress)
	// Also verify the trusted_maintainer_1 key in the loaded identities
	foundTrustedMaintainerLoaded := false
	for _, identity := range loadedIdentities.Identities {
		if identity.Name == "trusted_maintainer_1" {
			foundTrustedMaintainerLoaded = true
			assert.Equal(t, trustedMaintainerKey, identity.Key)
			break
		}
	}
	assert.True(t, foundTrustedMaintainerLoaded, "trusted_maintainer_1 identity not found in loaded config")

	// Verify that the "operator" identity's npub is correctly derived and stored
	operatorIdentity, err := cm.GetIdentity("operator")
	assert.NoError(t, err)
	assert.NotNil(t, operatorIdentity)

	derivedNpub, err := cm.GetPublicKey("operator")
	assert.NoError(t, err)
	assert.NotEmpty(t, derivedNpub)
}

func TestGetSetIdentity(t *testing.T) {
	tempDir, err := ioutil.TempDir("", "test_get_set_identity")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	configFile := filepath.Join(tempDir, "config.json")
	cm, err := NewConfigManager(configFile)
	assert.NoError(t, err)

	// Ensure default identities are created
	_, err = cm.EnsureDefaultIdentities()
	assert.NoError(t, err)

	// Test GetIdentity for "operator"
	operatorIdentity, err := cm.GetIdentity("operator")
	assert.NoError(t, err)
	assert.NotNil(t, operatorIdentity)
	assert.Equal(t, "operator", operatorIdentity.Name)
	assert.NotEmpty(t, operatorIdentity.Key) // Key should be populated

	// Test GetPublicKey for "operator"
	operatorPublicKey, err := cm.GetPublicKey("operator")
	assert.NoError(t, err)
	hexPubKey, err := nostr.GetPublicKey(operatorIdentity.Key[4:]) // Assuming nsec prefix
	assert.NoError(t, err)
	derivedPublicKeyFromKey, err := nip19.EncodePublicKey(hexPubKey)
	assert.NoError(t, err)
	assert.Equal(t, derivedPublicKeyFromKey, operatorPublicKey)

	// Test GetPrivateKey for "operator"
	operatorPrivateKey, err := cm.GetPrivateKey("operator")
	assert.NoError(t, err)
	assert.Equal(t, operatorIdentity.Key[4:], operatorPrivateKey) // Assuming nsec prefix

	// Test setting a new identity
	newIdentityName := "test_identity"
	newPrivateKey := nostr.GeneratePrivateKey()
	newPublicKey, err := nostr.GetPublicKey(newPrivateKey)
	assert.NoError(t, err)
	newNpub, err := nip19.EncodePublicKey(newPublicKey)
	assert.NoError(t, err)

	err = cm.SetIdentity(newIdentityName, newPrivateKey, "new@address.com")
	assert.NoError(t, err)

	// Verify the new identity can be retrieved
	retrievedIdentity, err := cm.GetIdentity(newIdentityName)
	assert.NoError(t, err)
	assert.NotNil(t, retrievedIdentity)
	assert.Equal(t, newIdentityName, retrievedIdentity.Name)
	derivedRetrievedNpub, err := cm.GetPublicKey(newIdentityName)
	assert.NoError(t, err)
	assert.Equal(t, newNpub, derivedRetrievedNpub)
	assert.Equal(t, newPrivateKey, retrievedIdentity.Key[4:]) // Assuming nsec prefix
	assert.Equal(t, "new@address.com", retrievedIdentity.LightningAddress)

	// Verify the new identity is saved to disk
	identitiesConfig, err := cm.LoadIdentities()
	assert.NoError(t, err)
	found := false
	for _, identity := range identitiesConfig.Identities {
		if identity.Name == newIdentityName {
			hexPubKeySaved, err := nostr.GetPublicKey(identity.Key[4:]) // Assuming nsec prefix
			assert.NoError(t, err)
			derivedSavedNpub, err := nip19.EncodePublicKey(hexPubKeySaved)
			assert.NoError(t, err)
			assert.Equal(t, newNpub, derivedSavedNpub)
			assert.Equal(t, newPrivateKey, identity.Key[4:]) // Assuming nsec prefix
			assert.Equal(t, "new@address.com", identity.LightningAddress)
			found = true
			break
		}
	}
	assert.True(t, found, "New identity not found in identities.json")

	// Test updating an existing identity
	updatedPrivateKey := nostr.GeneratePrivateKey()
	updatedPublicKey, err := nostr.GetPublicKey(updatedPrivateKey)
	assert.NoError(t, err)
	updatedNpub, err := nip19.EncodePublicKey(updatedPublicKey)
	assert.NoError(t, err)

	err = cm.SetIdentity(newIdentityName, updatedPrivateKey, "updated@address.com")
	assert.NoError(t, err)

	// Verify the updated identity
	retrievedUpdatedIdentity, err := cm.GetIdentity(newIdentityName)
	assert.NoError(t, err)
	assert.NotNil(t, retrievedUpdatedIdentity)
	assert.Equal(t, newIdentityName, retrievedUpdatedIdentity.Name)
	derivedUpdatedRetrievedNpub, err := cm.GetPublicKey(newIdentityName)
	assert.NoError(t, err)
	assert.Equal(t, updatedNpub, derivedUpdatedRetrievedNpub)
	assert.Equal(t, updatedPrivateKey, retrievedUpdatedIdentity.Key[4:]) // Assuming nsec prefix
	assert.Equal(t, "updated@address.com", retrievedUpdatedIdentity.LightningAddress)
}

func TestGetIdentity_NotFound(t *testing.T) {
	tempDir, err := ioutil.TempDir("", "test_get_identity_not_found")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	configFile := filepath.Join(tempDir, "config.json")
	cm, err := NewConfigManager(configFile)
	assert.NoError(t, err)

	// Ensure default identities are created (which will not include "nonexistent")
	_, err = cm.EnsureDefaultIdentities()
	assert.NoError(t, err)

	// Attempt to get a nonexistent identity
	identity, err := cm.GetIdentity("nonexistent")
	assert.Error(t, err)
	assert.Nil(t, identity)
	assert.Contains(t, err.Error(), "identity 'nonexistent' not found")
}

func TestGetPublicKey_NotFound(t *testing.T) {
	tempDir, err := ioutil.TempDir("", "test_get_public_key_not_found")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	configFile := filepath.Join(tempDir, "config.json")
	cm, err := NewConfigManager(configFile)
	assert.NoError(t, err)

	_, err = cm.EnsureDefaultIdentities()
	assert.NoError(t, err)

	publicKey, err := cm.GetPublicKey("nonexistent")
	assert.Error(t, err)
	assert.Empty(t, publicKey)
	assert.Contains(t, err.Error(), "identity 'nonexistent' not found")
}

func TestGetPrivateKey_NotFound(t *testing.T) {
	tempDir, err := ioutil.TempDir("", "test_get_private_key_not_found")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	configFile := filepath.Join(tempDir, "config.json")
	cm, err := NewConfigManager(configFile)
	assert.NoError(t, err)

	_, err = cm.EnsureDefaultIdentities()
	assert.NoError(t, err)

	privateKey, err := cm.GetPrivateKey("nonexistent")
	assert.Error(t, err)
	assert.Empty(t, privateKey)
	assert.Contains(t, err.Error(), "identity 'nonexistent' not found")
}
