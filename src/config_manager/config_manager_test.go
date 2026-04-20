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

func TestValidateProfitShare(t *testing.T) {
	tests := []struct {
		name    string
		shares  []ProfitShareConfig
		wantErr bool
	}{
		{
			name:    "default config sums to 1",
			shares:  NewDefaultConfig().ProfitShare,
			wantErr: false,
		},
		{
			name: "single share of 1.0",
			shares: []ProfitShareConfig{
				{Factor: 1.0, Identity: "owner"},
			},
			wantErr: false,
		},
		{
			name: "sum below 1",
			shares: []ProfitShareConfig{
				{Factor: 0.5, Identity: "owner"},
				{Factor: 0.4, Identity: "developer"},
			},
			wantErr: true,
		},
		{
			name: "sum above 1",
			shares: []ProfitShareConfig{
				{Factor: 0.6, Identity: "owner"},
				{Factor: 0.6, Identity: "developer"},
			},
			wantErr: true,
		},
		{
			name: "negative factor",
			shares: []ProfitShareConfig{
				{Factor: 1.2, Identity: "owner"},
				{Factor: -0.2, Identity: "developer"},
			},
			wantErr: true,
		},
		{
			name:    "empty list",
			shares:  []ProfitShareConfig{},
			wantErr: true,
		},
		{
			name: "within epsilon of 1",
			shares: []ProfitShareConfig{
				{Factor: 1.0 / 3, Identity: "a"},
				{Factor: 1.0 / 3, Identity: "b"},
				{Factor: 1.0 / 3, Identity: "c"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{ProfitShare: tt.shares}
			err := c.ValidateProfitShare()
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateProfitShare() err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}
