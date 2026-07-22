package config_manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigMigration_v007_to_v008(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	oldConfigJSON := `{
		"config_version": "v0.0.7",
		"log_level": "debug",
		"metric": "milliseconds",
		"step_size": 60000,
		"margin": 0.15,
		"show_setup": false,
		"reseller_mode": true,
		"accepted_mints": [
			{
				"url": "https://custom-mint.example.com",
				"min_balance": 100,
				"balance_tolerance_percent": 5,
				"payout_interval_seconds": 120,
				"min_payout_amount": 50,
				"price_per_step": 2,
				"price_unit": "sat",
				"purchase_min_steps": 1
			}
		],
		"profit_share": [
			{"factor": 0.9, "identity": "owner"},
			{"factor": 0.1, "identity": "developer"}
		]
	}`

	if err := os.WriteFile(configPath, []byte(oldConfigJSON), 0644); err != nil {
		t.Fatalf("failed to write old config: %v", err)
	}

	migrated, err := EnsureDefaultConfig(configPath)
	if err != nil {
		t.Fatalf("EnsureDefaultConfig failed: %v", err)
	}

	if migrated.ConfigVersion != "v0.0.8" {
		t.Errorf("config_version: got %s, want v0.0.8", migrated.ConfigVersion)
	}

	if migrated.UpstreamWifi.ScanIntervalSeconds != 300 {
		t.Errorf("UpstreamWifi.ScanIntervalSeconds: got %d, want 300 (default)", migrated.UpstreamWifi.ScanIntervalSeconds)
	}

	if migrated.UpstreamWifi.SignalFloor != -85 {
		t.Errorf("UpstreamWifi.SignalFloor: got %d, want -85 (default)", migrated.UpstreamWifi.SignalFloor)
	}

	if migrated.Metric != "milliseconds" {
		t.Errorf("Metric: got %s, want milliseconds (preserved)", migrated.Metric)
	}

	if migrated.StepSize != 60000 {
		t.Errorf("StepSize: got %d, want 60000 (preserved)", migrated.StepSize)
	}

	if migrated.LogLevel != "debug" {
		t.Errorf("LogLevel: got %s, want debug (preserved)", migrated.LogLevel)
	}

	if !migrated.ResellerMode {
		t.Errorf("ResellerMode: got false, want true (preserved)")
	}

	if len(migrated.AcceptedMints) != 1 {
		t.Fatalf("AcceptedMints: got %d, want 1 (preserved)", len(migrated.AcceptedMints))
	}

	if migrated.AcceptedMints[0].URL != "https://custom-mint.example.com" {
		t.Errorf("AcceptedMints[0].URL: got %s, want custom-mint.example.com", migrated.AcceptedMints[0].URL)
	}

	if len(migrated.ProfitShare) != 2 {
		t.Fatalf("ProfitShare: got %d entries, want 2", len(migrated.ProfitShare))
	}

	if migrated.ProfitShare[0].Factor != 0.9 {
		t.Errorf("ProfitShare[0].Factor: got %f, want 0.9", migrated.ProfitShare[0].Factor)
	}

	savedData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read saved config: %v", err)
	}
	var saved Config
	if err := json.Unmarshal(savedData, &saved); err != nil {
		t.Fatalf("saved config is invalid JSON: %v", err)
	}
	if saved.ConfigVersion != "v0.0.8" {
		t.Errorf("saved config_version: got %s, want v0.0.8", saved.ConfigVersion)
	}
}

func TestConfigMigration_alreadyCurrent(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	defaults := NewDefaultConfig()
	data, _ := json.Marshal(defaults)
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	loaded, err := EnsureDefaultConfig(configPath)
	if err != nil {
		t.Fatalf("EnsureDefaultConfig failed: %v", err)
	}

	if loaded.ConfigVersion != defaults.ConfigVersion {
		t.Errorf("config_version changed: got %s, want %s", loaded.ConfigVersion, defaults.ConfigVersion)
	}
}
