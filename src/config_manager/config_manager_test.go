package config_manager

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

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
	tempDir := t.TempDir()
	configFilePath := filepath.Join(tempDir, "test_config.json")
	installFilePath := filepath.Join(tempDir, "test_install.json")
	identitiesFilePath := filepath.Join(tempDir, "test_identities.json")

	cm, err := NewConfigManager(configFilePath, installFilePath, identitiesFilePath)
	if err != nil {
		t.Fatalf("Failed to create ConfigManager: %v", err)
	}

	// Test EnsureDefaultConfig - this is implicitly handled by NewConfigManager
	config := cm.GetConfig()
	if config == nil {
		t.Errorf("GetConfig returned nil config")
	}

	// Test LoadConfig
	loadedConfig, err := LoadConfig(configFilePath)
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
		Metric:    "milliseconds",
		StepSize:  120000,
		ShowSetup: true,
	}
	err = SaveConfig(configFilePath, newConfig)
	if err != nil {
		t.Errorf("SaveConfig returned error: %v", err)
	}

	loadedConfig, err = LoadConfig(configFilePath)
	if err != nil {
		t.Errorf("LoadConfig returned error after SaveConfig: %v", err)
	}
	// Verify all fields
	if !compareMintConfigs(loadedConfig.AcceptedMints, newConfig.AcceptedMints) ||
		loadedConfig.Metric != "milliseconds" ||
		loadedConfig.StepSize != 120000 ||
		loadedConfig.ShowSetup != newConfig.ShowSetup {
		t.Errorf("Loaded config does not match saved config")
	}

	// Test LoadInstallConfig and SaveInstallConfig
	// Remove install.json file if it exists
	os.Remove(cm.InstallFilePath)
	installConfig, err := LoadInstallConfig(cm.InstallFilePath)
	if err != nil {
		t.Errorf("LoadInstallConfig returned error: %v", err)
	}
	if installConfig != nil {
		t.Errorf("LoadInstallConfig returned non-nil config")
	}

	newInstallConfig := &InstallConfig{
		PackagePath: "/path/to/package",
	}
	err = SaveInstallConfig(cm.InstallFilePath, newInstallConfig)
	if err != nil {
		t.Errorf("SaveInstallConfig returned error: %v", err)
	}

	loadedInstallConfig, err := LoadInstallConfig(cm.InstallFilePath)
	if err != nil {
		t.Errorf("LoadInstallConfig returned error after SaveInstallConfig: %v", err)
	}
	if !reflect.DeepEqual(loadedInstallConfig, newInstallConfig) {
		t.Errorf("Loaded install config does not match saved config")
	}
}

func TestUpstreamWifiConfig_Defaults(t *testing.T) {
	config := NewDefaultConfig()
	cfg := config.UpstreamWifi
	if cfg.ScanIntervalSeconds != 300 {
		t.Errorf("expected ScanIntervalSeconds=300, got %d", cfg.ScanIntervalSeconds)
	}
	if cfg.FastCheckSeconds != 30 {
		t.Errorf("expected FastCheckSeconds=30, got %d", cfg.FastCheckSeconds)
	}
	if cfg.LostThreshold != 2 {
		t.Errorf("expected LostThreshold=2, got %d", cfg.LostThreshold)
	}
	if cfg.HysteresisDB != 12 {
		t.Errorf("expected HysteresisDB=12, got %d", cfg.HysteresisDB)
	}
	if cfg.SignalFloor != -85 {
		t.Errorf("expected SignalFloor=-85, got %d", cfg.SignalFloor)
	}
	if cfg.BlacklistTTLMinutes != 60 {
		t.Errorf("expected BlacklistTTLMinutes=60, got %d", cfg.BlacklistTTLMinutes)
	}
	if cfg.EmergencyPenalty != 20 {
		t.Errorf("expected EmergencyPenalty=20, got %d", cfg.EmergencyPenalty)
	}
	if cfg.MaxConsecutiveFailures != 3 {
		t.Errorf("expected MaxConsecutiveFailures=3, got %d", cfg.MaxConsecutiveFailures)
	}
	if cfg.SwitchCooldownMinutes != 10 {
		t.Errorf("expected SwitchCooldownMinutes=10, got %d", cfg.SwitchCooldownMinutes)
	}
	if cfg.StartupGraceSeconds != 90 {
		t.Errorf("expected StartupGraceSeconds=90, got %d", cfg.StartupGraceSeconds)
	}
	if cfg.PostSwitchWaitSeconds != 5 {
		t.Errorf("expected PostSwitchWaitSeconds=5, got %d", cfg.PostSwitchWaitSeconds)
	}
	if cfg.DHCPTimeoutSeconds != 180 {
		t.Errorf("expected DHCPTimeoutSeconds=180, got %d", cfg.DHCPTimeoutSeconds)
	}
	if cfg.ManualPauseSeconds != 120 {
		t.Errorf("expected ManualPauseSeconds=120, got %d", cfg.ManualPauseSeconds)
	}
}

func TestUpstreamWifiConfig_RoundTrip(t *testing.T) {
	tempDir := t.TempDir()
	configFilePath := filepath.Join(tempDir, "config.json")

	original := NewDefaultConfig()
	original.UpstreamWifi.ScanIntervalSeconds = 600
	original.UpstreamWifi.SignalFloor = -70

	err := SaveConfig(configFilePath, original)
	if err != nil {
		t.Fatalf("SaveConfig error: %v", err)
	}

	loaded, err := LoadConfig(configFilePath)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if loaded.UpstreamWifi.ScanIntervalSeconds != 600 {
		t.Errorf("expected ScanIntervalSeconds=600, got %d", loaded.UpstreamWifi.ScanIntervalSeconds)
	}
	if loaded.UpstreamWifi.SignalFloor != -70 {
		t.Errorf("expected SignalFloor=-70, got %d", loaded.UpstreamWifi.SignalFloor)
	}
}
